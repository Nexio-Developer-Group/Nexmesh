package wiring_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
)

type rig struct {
	t    *testing.T
	ctx  context.Context
	st   *store.Memory
	svc  *wiring.Service
	comp *component.Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	aud := audit.New(st, log)
	comp := component.NewService(st, cat, bus, aud)
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady, CPU: 4, RAMGB: 8, StorageGB: 40})
	return &rig{t, ctx, st, wiring.New(st, cat, comp, bus, aud), comp}
}

// card adds a card in the given mode and observed state.
func (r *rig) card(id, mode, observed string) *domain.Component {
	r.t.Helper()
	c := &domain.Component{ID: store.NewID("cmp"), WorkspaceID: "ws1", CatalogID: id, Mode: mode, Observed: observed, Desired: observed, Settings: map[string]any{}}
	if err := r.st.CreateComponent(r.ctx, c); err != nil {
		r.t.Fatal(err)
	}
	return c
}

func code(err error) string {
	var ve *runtime.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func (r *rig) wire(from, to *domain.Component, name string) (*wiring.View, error) {
	return r.svc.Create(r.ctx, domain.ActorUser, "u1", "u1", "ws1", wiring.Input{FromID: from.ID, ToID: to.ID, Name: name, Confirm: true})
}

func TestCatalogRulesDecideValidPairs(t *testing.T) {
	r := newRig(t)
	nest := r.card("nestjs", "installed", "ready")
	mongo := r.card("mongodb", "installed", "running")
	react := r.card("react", "installed", "ready")
	redis := r.card("redis", "installed", "running")

	cases := []struct {
		name     string
		from, to *domain.Component
		want     string // "" means valid
	}{
		{"backend to database", nest, mongo, ""},
		{"backend to queue", nest, redis, ""},
		{"frontend to backend", react, nest, ""},
		{"database cannot consume", mongo, nest, "cannot_consume"},
		{"frontend cannot reach a database", react, mongo, "invalid_pair"},
		{"nothing connects to a frontend", nest, react, "cannot_provide"},
		{"self", nest, nest, "self_connection"},
	}
	for _, c := range cases {
		_, err := r.svc.Preview(r.ctx, "u1", "ws1", wiring.Input{FromID: c.from.ID, ToID: c.to.ID})
		if got := code(err); got != c.want {
			t.Errorf("%s: code %q, want %q (%v)", c.name, got, c.want, err)
		}
	}
	// The refusal explains itself in plain words.
	_, err := r.svc.Preview(r.ctx, "u1", "ws1", wiring.Input{FromID: react.ID, ToID: mongo.ID})
	if !strings.Contains(err.Error(), "backend") {
		t.Fatalf("explanation should name what React connects to: %v", err)
	}
}

func TestPreviewListsNamesNotValuesAndConfirmIsRequired(t *testing.T) {
	r := newRig(t)
	nest, mongo := r.card("nestjs", "installed", "ready"), r.card("mongodb", "installed", "running")
	pv, err := r.svc.Preview(r.ctx, "u1", "ws1", wiring.Input{FromID: nest.ID, ToID: mongo.ID, Name: "orders-db"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range pv.Variables {
		names[v.Name] = true
	}
	for _, want := range []string{"ORDERS_DB_URI", "ORDERS_DB_HOST", "ORDERS_DB_PORT"} {
		if !names[want] {
			t.Errorf("preview missing %s: %+v", want, pv.Variables)
		}
	}
	if list, _ := r.svc.List(r.ctx, "u1", "ws1"); len(list) != 0 {
		t.Fatal("a preview must not create a line")
	}
	_, err = r.svc.Create(r.ctx, domain.ActorUser, "u1", "u1", "ws1", wiring.Input{FromID: nest.ID, ToID: mongo.ID})
	if code(err) != "confirmation_required" {
		t.Fatalf("create without confirm: %v", err)
	}
}

func TestLineVariablesAppearAndDisappearWithTheLine(t *testing.T) {
	r := newRig(t)
	nest, mongo := r.card("nestjs", "installed", "ready"), r.card("mongodb", "installed", "running")
	if env, _ := r.svc.Env(r.ctx, "ws1"); len(env) != 0 {
		t.Fatalf("no lines, no variables: %v", env)
	}
	v, err := r.wire(nest, mongo, "")
	if err != nil {
		t.Fatal(err)
	}
	env, _ := r.svc.Env(r.ctx, "ws1")
	if env["MONGODB_URI"] != "mongodb://mongodb:27017/" || env["MONGODB_HOST"] != "mongodb" || env["MONGODB_PORT"] != "27017" {
		t.Fatalf("installed provider variables: %v", env)
	}
	if got := r.svc.ExposedFor(r.ctx, "ws1", nest.ID); len(got) == 0 {
		t.Fatal("consumer should list the names it receives")
	}
	if err := r.svc.Delete(r.ctx, domain.ActorUser, "u1", "u1", "ws1", v.ID); err != nil {
		t.Fatal(err)
	}
	if env, _ := r.svc.Env(r.ctx, "ws1"); len(env) != 0 {
		t.Fatalf("variables must disappear with the line: %v", env)
	}
	if got := r.svc.ExposedFor(r.ctx, "ws1", nest.ID); len(got) != 0 {
		t.Fatal("exposed names must disappear too")
	}
}

func TestBackendProviderGivesAURL(t *testing.T) {
	r := newRig(t)
	react, nest := r.card("react", "installed", "ready"), r.card("nestjs", "installed", "ready")
	if _, err := r.wire(react, nest, "api"); err != nil {
		t.Fatal(err)
	}
	env, _ := r.svc.Env(r.ctx, "ws1")
	if env["API_URL"] != "http://localhost:3000" || env["API_PORT"] != "3000" {
		t.Fatalf("%v", env)
	}
}

func TestLineStateFollowsTheProvider(t *testing.T) {
	r := newRig(t)
	nest := r.card("nestjs", "installed", "ready")
	draft := r.card("mongodb", "", "draft")
	v, err := r.wire(nest, draft, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != component.WirePending {
		t.Fatalf("draft provider: %s", v.State)
	}
	if env, _ := r.svc.Env(r.ctx, "ws1"); len(env) != 0 {
		t.Fatal("a draft provider has no values yet")
	}
	state := func() string {
		l, _ := r.svc.List(r.ctx, "u1", "ws1")
		return l[0].State
	}
	set := func(mode, observed string) {
		_, _ = r.st.UpdateComponent(r.ctx, draft.ID, func(c *domain.Component) error { c.Mode, c.Observed = mode, observed; return nil })
	}
	set("installed", "running")
	if state() != component.WireHealthy {
		t.Fatal("running provider should be healthy")
	}
	set("installed", "stopped")
	if state() != component.WireUnreachable {
		t.Fatal("stopped provider should be unreachable")
	}
	set("connected", "degraded")
	if state() != component.WireDegraded {
		t.Fatal("degraded connected provider should be degraded")
	}
	set("connected", "test_failed")
	if state() != component.WireUnreachable {
		t.Fatal("failed provider should be unreachable")
	}
}

func TestDuplicatesAndNameClashes(t *testing.T) {
	r := newRig(t)
	nest := r.card("nestjs", "installed", "ready")
	a, b := r.card("mysql", "installed", "running"), r.card("mysql", "installed", "running")
	if _, err := r.wire(nest, a, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.wire(nest, a, "other"); code(err) != "duplicate" {
		t.Fatalf("same pair twice: %v", err)
	}
	if _, err := r.wire(nest, b, ""); code(err) != "name_in_use" {
		t.Fatalf("two MySQL lines with the default name must clash: %v", err)
	}
	if _, err := r.wire(nest, b, "reporting-db"); err != nil {
		t.Fatalf("a distinct name should work: %v", err)
	}
	if _, err := r.wire(nest, r.card("redis", "installed", "running"), "Bad Name!"); code(err) != "invalid_name" {
		t.Fatalf("invalid name: %v", err)
	}
}

func TestUserVariableConflictIsWarnedAndRemovingACardRemovesItsLines(t *testing.T) {
	r := newRig(t)
	nest, mongo := r.card("nestjs", "installed", "ready"), r.card("mongodb", "installed", "running")
	_, _ = r.st.UpdateWorkspace(r.ctx, "ws1", func(w *domain.Workspace) error {
		w.Variables = append(w.Variables, domain.Variable{Name: "MONGODB_URI", Value: "mine"})
		return nil
	})
	pv, _ := r.svc.Preview(r.ctx, "u1", "ws1", wiring.Input{FromID: nest.ID, ToID: mongo.ID})
	if len(pv.Warnings) == 0 {
		t.Fatal("a clash with a user variable should be warned about")
	}
	if _, err := r.wire(nest, mongo, ""); err != nil {
		t.Fatal(err)
	}
	// Removing the provider (a draft-state card can be deleted) drops the line.
	_, _ = r.st.UpdateComponent(r.ctx, mongo.ID, func(c *domain.Component) error { c.Mode, c.Observed = "", "draft"; return nil })
	if err := r.comp.Remove(r.ctx, "u1", "ws1", mongo.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.svc.List(r.ctx, "u1", "ws1"); len(l) != 0 {
		t.Fatalf("lines should go with their card: %v", l)
	}
	evs, _ := r.st.ListAudit(r.ctx, "ws1", 20)
	var created, deleted bool
	for _, e := range evs {
		created = created || e.Action == "wire.create"
		deleted = deleted || e.Action == "wire.delete"
	}
	if !created || !deleted {
		t.Fatalf("line changes must be audited: %+v", evs)
	}
}

func TestOtherOwnersCannotSeeOrChangeLines(t *testing.T) {
	r := newRig(t)
	nest, mongo := r.card("nestjs", "installed", "ready"), r.card("mongodb", "installed", "running")
	v, _ := r.wire(nest, mongo, "")
	if _, err := r.svc.List(r.ctx, "intruder", "ws1"); err == nil {
		t.Fatal("list as another owner should fail")
	}
	if err := r.svc.Delete(r.ctx, domain.ActorUser, "x", "intruder", "ws1", v.ID); err == nil {
		t.Fatal("delete as another owner should fail")
	}
}

func TestLinesLabelledForAnotherEnvironmentAreInactive(t *testing.T) {
	r := newRig(t)
	nest, mongo := r.card("nestjs", "installed", "ready"), r.card("mongodb", "installed", "running")
	v, err := r.svc.Create(r.ctx, domain.ActorUser, "u1", "u1", "ws1", wiring.Input{FromID: nest.ID, ToID: mongo.ID, Environment: "production", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	pv, _ := r.svc.Preview(r.ctx, "u1", "ws1", wiring.Input{FromID: nest.ID, ToID: r.card("redis", "installed", "running").ID, Environment: "production"})
	found := false
	for _, w := range pv.Warnings {
		found = found || strings.Contains(w, "production")
	}
	if !found {
		t.Fatalf("production labelled lines should warn: %v", pv.Warnings)
	}
	state := func(env string) string {
		_, _ = r.st.UpdateWorkspace(r.ctx, "ws1", func(w *domain.Workspace) error { w.ActiveEnvironment = env; return nil })
		l, _ := r.svc.List(r.ctx, "u1", "ws1")
		return l[0].State
	}
	// Before environments are in use, the label is informational.
	if l, _ := r.svc.List(r.ctx, "u1", "ws1"); l[0].State != component.WireHealthy {
		t.Fatalf("legacy behaviour: %s", l[0].State)
	}
	if got := state("development"); got != component.WireInactive {
		t.Fatalf("a production line must be inactive in development, got %s", got)
	}
	if env, _ := r.svc.Env(r.ctx, "ws1"); len(env) != 0 {
		t.Fatalf("an inactive line must inject nothing: %v", env)
	}
	if got := state("production"); got != component.WireHealthy {
		t.Fatalf("active in production: %s", got)
	}
	if env, _ := r.svc.Env(r.ctx, "ws1"); env["MONGODB_URI"] == "" {
		t.Fatalf("active line should inject: %v", env)
	}
	_ = v
}
