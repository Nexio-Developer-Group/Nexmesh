package stacks_test

import (
	"context"
	"encoding/json"
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
	"github.com/nexio-developer-group/nexmesh/server/internal/stacks"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type rig struct {
	svc *stacks.Service
	st  store.Store
	cmp *component.Service
	ctx context.Context
}

func newRig(t *testing.T) *rig {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	tpls, err := catalog.LoadTemplates("../../../catalog", cat)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	aud := audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cmp := component.NewService(st, cat, events.NewBus(), aud)
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	return &rig{svc: stacks.NewService(st, cat, tpls, cmp, aud), st: st, cmp: cmp, ctx: ctx}
}

func (r *rig) cards(t *testing.T) []*domain.Component {
	t.Helper()
	l, err := r.cmp.List(r.ctx, "u1", "ws1")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestApplyAddsDraftCardsAndInstallsNothing(t *testing.T) {
	r := newRig(t)
	g, _ := r.svc.List(r.ctx, "u1")
	if len(g.Curated) < 6 || len(g.Personal) != 0 {
		t.Fatalf("gallery: %+v", g)
	}
	res, err := r.svc.Apply(r.ctx, "u1", "ws1", "react-nestjs-mongodb")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 4 || len(res.Skipped) != 0 {
		t.Fatalf("result: %+v", res)
	}
	ids := map[string]*domain.Component{}
	for _, c := range r.cards(t) {
		ids[c.CatalogID] = c
		if c.Observed != domain.StateDraft || c.Mode != domain.ModeUnset || c.Desired != domain.StateDraft {
			t.Fatalf("%s is not an untouched Draft: %+v", c.CatalogID, c)
		}
	}
	for _, want := range []string{"nodejs", "react", "nestjs", "mongodb"} {
		if ids[want] == nil || ids[want].SuggestedMode != "install" {
			t.Fatalf("%s card or suggested mode missing: %+v", want, ids[want])
		}
	}
	// Nothing was queued or touched: no operations, no secrets.
	if ops, _ := r.st.ListOperations(r.ctx, "ws1"); len(ops) != 0 {
		t.Fatalf("applying a template queued operations: %+v", ops)
	}
	if _, _, err := func() (any, any, error) { _, err := r.svc.Apply(r.ctx, "u1", "ws1", "nope"); return nil, nil, err }(); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown template: %v", err)
	}
	if _, err := r.svc.Apply(r.ctx, "intruder", "ws1", "static-site"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user's workspace: %v", err)
	}
}

func TestApplyPlacesBelowExistingAndSkipsSingleInstanceTech(t *testing.T) {
	r := newRig(t)
	if _, err := r.cmp.Add(r.ctx, "u1", "ws1", "python", 100, 300); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cmp.Add(r.ctx, "u1", "ws1", "redis", 400, 300); err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.Apply(r.ctx, "u1", "ws1", "fastapi-redis")
	if err != nil {
		t.Fatal(err)
	}
	// python is already there (skipped); redis is a service, so a second instance is allowed.
	if len(res.Skipped) != 1 || res.Skipped[0] != "python" || len(res.Added) != 2 {
		t.Fatalf("result: skipped=%v added=%d", res.Skipped, len(res.Added))
	}
	for _, c := range res.Added {
		if c.Y < 300+230 {
			t.Fatalf("%s placed at y=%v, overlapping the existing row", c.CatalogID, c.Y)
		}
	}
}

func TestSaveCanvasKeepsNoSecretsOrValues(t *testing.T) {
	r := newRig(t)
	redis, _ := r.cmp.Add(r.ctx, "u1", "ws1", "redis", 500, 400)
	mongo, _ := r.cmp.Add(r.ctx, "u1", "ws1", "mongodb", 800, 400)
	_, _ = r.st.UpdateComponent(r.ctx, redis.ID, func(c *domain.Component) error {
		c.Mode, c.Version, c.Settings = domain.ModeInstalled, "7.2.6", map[string]any{"port": 6380, "token": "s3cr3t-setting"}
		return nil
	})
	_, _ = r.st.UpdateComponent(r.ctx, mongo.ID, func(c *domain.Component) error {
		c.Mode, c.ConnName, c.SecretRef = domain.ModeConnected, "prod-orders-db", "conn_secret_ref"
		c.Connection = map[string]any{"host": "orders.internal.example", "username": "orders_app"}
		return nil
	})

	e, err := r.svc.SaveCanvas(r.ctx, "u1", "ws1", "  My stack ", "cache + db")
	if err != nil || e.Name != "My stack" || !e.Personal || len(e.Components) != 2 {
		t.Fatalf("save: %v %+v", err, e)
	}
	raw, _ := json.Marshal(e)
	for _, leak := range []string{"s3cr3t", "prod-orders-db", "orders.internal", "orders_app", "conn_secret_ref", "6380", "token"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("template leaked %q: %s", leak, raw)
		}
	}
	got := map[string]struct{ mode, version string }{}
	minX, minY := 1e9, 1e9
	for _, c := range e.Components {
		got[c.ID] = struct{ mode, version string }{c.SuggestedMode, c.Version}
		minX, minY = min(minX, c.X), min(minY, c.Y)
	}
	if got["redis"].mode != "install" || got["redis"].version != "7.2.6" || got["mongodb"].mode != "connect" {
		t.Fatalf("modes/versions: %+v", got)
	}
	if minX != 40 || minY != 40 {
		t.Fatalf("positions not normalised: %v,%v", minX, minY)
	}

	// Duplicate names, empty canvases and foreign deletes are refused.
	if _, err := r.svc.SaveCanvas(r.ctx, "u1", "ws1", "my stack", ""); !errors.Is(err, stacks.ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := r.svc.Delete(r.ctx, "intruder", e.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	// The personal template applies like a curated one, into a fresh workspace.
	_ = r.st.CreateWorkspace(r.ctx, &domain.Workspace{ID: "ws2", OwnerID: "u1", Name: "w2", Status: domain.WSReady})
	res, err := r.svc.Apply(r.ctx, "u1", "ws2", e.ID)
	if err != nil || len(res.Added) != 2 {
		t.Fatalf("apply personal: %v %+v", err, res)
	}
	for _, c := range res.Added {
		if c.Observed != domain.StateDraft || c.ConnName != "" || c.SecretRef != "" || len(c.Connection) != 0 {
			t.Fatalf("applied card carries state: %+v", c)
		}
	}
	if err := r.svc.Delete(r.ctx, "u1", e.ID); err != nil {
		t.Fatal(err)
	}
	if g, _ := r.svc.List(r.ctx, "u1"); len(g.Personal) != 0 {
		t.Fatal("not deleted")
	}
	_ = r.svc
	var ve *stacks.ValidationError
	_ = r.st.CreateWorkspace(r.ctx, &domain.Workspace{ID: "ws3", OwnerID: "u1", Name: "empty", Status: domain.WSReady})
	if _, err := r.svc.SaveCanvas(r.ctx, "u1", "ws3", "x", ""); !errors.As(err, &ve) {
		t.Fatalf("empty canvas: %v", err)
	}
}
