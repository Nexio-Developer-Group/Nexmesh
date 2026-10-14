package envvars_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type src struct{}

func (src) ConnectionEnv(context.Context, string) (map[string]string, error) {
	return map[string]string{"PROD_ORDERS_DB_URI": "mongodb://generated", "PROD_ORDERS_DB_HOST": "h"}, nil
}
func (src) ExposedAs(c *domain.Component) []string {
	return []string{"PROD_ORDERS_DB_URI", "PROD_ORDERS_DB_HOST"}
}

type rig struct {
	svc *envvars.Service
	st  store.Store
	ctx context.Context
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	key := make([]byte, 32)
	be, err := secrets.NewStoreBackend(st, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "n", Status: domain.WSReady})
	_ = st.CreateComponent(ctx, &domain.Component{ID: "c1", WorkspaceID: "ws1", CatalogID: "mongodb", Mode: domain.ModeConnected, ConnName: "prod-orders-db"})
	return &rig{svc: envvars.NewService(st, secrets.New(be), audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), src{}), st: st, ctx: ctx}
}

func sp(s string) *string { return &s }

func TestSecretValuesAreWriteOnlyAndEncrypted(t *testing.T) {
	r := newRig(t)
	if _, err := r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "STRIPE_KEY", Secret: true, Value: sp("sk_live_SUPERSECRET")}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "LOG_LEVEL", Value: sp("debug"), Description: "verbosity"}); err != nil {
		t.Fatal(err)
	}
	l, _ := r.svc.List(r.ctx, "u1", "ws1")
	var stripe, level envvars.Row
	for _, v := range l.Variables {
		if v.Name == "STRIPE_KEY" {
			stripe = v
		} else {
			level = v
		}
	}
	if !stripe.Secret || stripe.Value != "" || level.Value != "debug" {
		t.Fatalf("listing: %+v %+v", stripe, level)
	}
	// Nothing in the stored workspace document contains the secret.
	w, _ := r.st.GetWorkspace(r.ctx, "ws1")
	for _, v := range w.Variables {
		if strings.Contains(v.Value+v.Description, "SUPERSECRET") {
			t.Fatal("secret persisted in workspace document")
		}
	}
	rec, _ := r.st.GetSecret(r.ctx, "var_ws1_workspace_STRIPE_KEY")
	if strings.Contains(string(rec.Ciphertext), "SUPERSECRET") {
		t.Fatal("secret stored in plaintext")
	}
	env, _ := r.svc.Environment(r.ctx, "ws1")
	if env["STRIPE_KEY"] != "sk_live_SUPERSECRET" || env["LOG_LEVEL"] != "debug" {
		t.Fatalf("effective env: %v", env)
	}
	// Audit never contains values.
	evs, _ := r.st.ListAudit(r.ctx, "ws1", 10)
	if len(evs) != 2 {
		t.Fatalf("audit: %d", len(evs))
	}
	for _, e := range evs {
		for _, v := range e.Detail {
			if s, _ := v.(string); strings.Contains(s, "SUPERSECRET") || s == "debug" {
				t.Fatalf("audit leaked: %+v", e)
			}
		}
	}
}

func TestPrecedenceAndOverrideMarking(t *testing.T) {
	r := newRig(t)
	_, _ = r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "PROD_ORDERS_DB_URI", Value: sp("mongodb://workspace-override")})
	env, _ := r.svc.Environment(r.ctx, "ws1")
	if env["PROD_ORDERS_DB_URI"] != "mongodb://workspace-override" || env["PROD_ORDERS_DB_HOST"] != "h" {
		t.Fatalf("workspace must beat generated: %v", env)
	}
	_, _ = r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "PROD_ORDERS_DB_URI", ComponentID: "c1", Value: sp("mongodb://component")})
	env, _ = r.svc.Environment(r.ctx, "ws1")
	if env["PROD_ORDERS_DB_URI"] != "mongodb://component" {
		t.Fatalf("component must beat workspace: %v", env)
	}
	l, _ := r.svc.List(r.ctx, "u1", "ws1")
	over := 0
	for _, v := range l.Variables {
		if v.Overrides {
			over++
		}
	}
	marked := false
	for _, g := range l.Generated {
		if g.Name == "PROD_ORDERS_DB_URI" && g.Overridden {
			marked = true
		}
	}
	if over != 2 || !marked || len(l.Generated) != 2 {
		t.Fatalf("override marking: over=%d marked=%v gen=%+v", over, marked, l.Generated)
	}
}

func TestValidationAndOwnership(t *testing.T) {
	r := newRig(t)
	var ve *envvars.ValidationError
	for name, in := range map[string]envvars.SetInput{
		"1BAD":          {Name: "1BAD", Value: sp("x")},
		"has-dash":      {Name: "has-dash", Value: sp("x")},
		"NEXMESH_TOKEN": {Name: "NEXMESH_TOKEN", Value: sp("x")},
		"empty-value":   {Name: "OK_NAME"},
	} {
		if _, err := r.svc.Set(r.ctx, "u1", "ws1", in); !errors.As(err, &ve) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	if _, err := r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "X", ComponentID: "nope", Value: sp("v")}); !errors.As(err, &ve) || ve.Field != "componentId" {
		t.Fatalf("foreign component: %v", err)
	}
	if _, err := r.svc.Set(r.ctx, "intruder", "ws1", envvars.SetInput{Name: "X", Value: sp("v")}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user: %v", err)
	}
	if _, err := r.svc.List(r.ctx, "intruder", "ws1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("other user listed variables")
	}
}

func TestEditKeepsSecretAndConversionNeedsValue(t *testing.T) {
	r := newRig(t)
	_, _ = r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "TOKEN", Secret: true, Value: sp("abc")})
	if _, err := r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "TOKEN", Secret: true, Description: "now documented"}); err != nil {
		t.Fatal(err)
	}
	env, _ := r.svc.Environment(r.ctx, "ws1")
	if env["TOKEN"] != "abc" {
		t.Fatalf("description edit lost the secret: %v", env)
	}
	var ve *envvars.ValidationError
	if _, err := r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "TOKEN", Secret: false}); !errors.As(err, &ve) {
		t.Fatalf("secret->plain without value must fail: %v", err)
	}
	if _, err := r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "TOKEN", Secret: false, Value: sp("plain")}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.GetSecret(r.ctx, "var_ws1_workspace_TOKEN"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("old secret not removed on conversion")
	}
}

func TestDeleteRemovesSecret(t *testing.T) {
	r := newRig(t)
	_, _ = r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "TOKEN", Secret: true, Value: sp("abc")})
	if err := r.svc.Delete(r.ctx, "u1", "ws1", "", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.GetSecret(r.ctx, "var_ws1_workspace_TOKEN"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("secret survived delete")
	}
	if err := r.svc.Delete(r.ctx, "u1", "ws1", "", "TOKEN"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}
	env, _ := r.svc.Environment(r.ctx, "ws1")
	if _, ok := env["TOKEN"]; ok {
		t.Fatal("deleted variable still in environment")
	}
}
