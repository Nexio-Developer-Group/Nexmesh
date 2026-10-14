package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

func impls(t *testing.T) map[string]Store {
	m := map[string]Store{"memory": NewMemory()}
	if url := os.Getenv("NEXMESH_TEST_DATABASE_URL"); url != "" {
		p, err := NewPostgres(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		m["postgres"] = p
	}
	return m
}

func TestStoreContract(t *testing.T) {
	for name, s := range impls(t) {
		t.Run(name, func(t *testing.T) { contract(t, s) })
	}
}

func contract(t *testing.T, s Store) {
	ctx := context.Background()
	owner := NewID("u")
	w := &domain.Workspace{ID: NewID("ws"), OwnerID: owner, Name: "orders-api", OS: domain.OSUbuntu2404,
		CPU: 4, RAMGB: 8, StorageGB: 40, Status: domain.WSAllocating, CreatedAt: time.Now().UTC(), LastOpened: time.Now().UTC()}
	if err := s.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	dup := *w
	dup.ID = NewID("ws")
	if err := s.CreateWorkspace(ctx, &dup); err != ErrConflict {
		t.Fatalf("duplicate name want ErrConflict got %v", err)
	}
	got, err := s.UpdateWorkspace(ctx, w.ID, func(x *domain.Workspace) error { x.Status = domain.WSReady; return nil })
	if err != nil || got.Status != domain.WSReady {
		t.Fatalf("update: %v %+v", err, got)
	}
	list, _ := s.ListWorkspaces(ctx, owner)
	if len(list) != 1 || list[0].Status != domain.WSReady {
		t.Fatalf("list: %+v", list)
	}
	if _, err := s.GetWorkspace(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound got %v", err)
	}

	c := &domain.Component{ID: NewID("c"), WorkspaceID: w.ID, CatalogID: "mongodb", Desired: domain.StateDraft, Observed: domain.StateDraft, CreatedAt: time.Now().UTC()}
	if err := s.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	c2, err := s.UpdateComponent(ctx, c.ID, func(x *domain.Component) error { x.Version = "8.0.4"; return nil })
	if err != nil || c2.Version != "8.0.4" {
		t.Fatal(err, c2)
	}
	cs, _ := s.ListComponents(ctx, w.ID)
	if len(cs) != 1 {
		t.Fatalf("components %d", len(cs))
	}

	op := &domain.Operation{ID: NewID("op"), WorkspaceID: w.ID, Type: "provision", Status: domain.OpPending, CreatedAt: time.Now().UTC()}
	if err := s.CreateOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	pend, _ := s.ListOperationsByStatus(ctx, domain.OpPending)
	found := false
	for _, p := range pend {
		found = found || p.ID == op.ID
	}
	if !found {
		t.Fatal("pending op missing")
	}
	if _, err := s.UpdateOperation(ctx, op.ID, func(o *domain.Operation) error { o.Status = domain.OpRunning; return nil }); err != nil {
		t.Fatal(err)
	}
	pend, _ = s.ListOperationsByStatus(ctx, domain.OpPending)
	for _, p := range pend {
		if p.ID == op.ID {
			t.Fatal("op still pending after update")
		}
	}

	ac := &domain.AgentConfig{ID: NewID("ag"), WorkspaceID: w.ID, Name: "x", Provider: "anthropic", CreatedAt: time.Now().UTC()}
	if err := s.CreateAgentConfig(ctx, ac); err != nil {
		t.Fatal(err)
	}
	acs, _ := s.ListAgentConfigs(ctx, w.ID)
	if len(acs) != 1 {
		t.Fatal("agent configs")
	}

	if err := s.AppendAudit(ctx, &domain.AuditEvent{ID: NewID("a"), WorkspaceID: w.ID, ActorType: domain.ActorUser, Action: "workspace.create", At: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	au, _ := s.ListAudit(ctx, w.ID, 10)
	if len(au) != 1 {
		t.Fatal("audit")
	}

	sec := &domain.SecretRecord{ID: NewID("sec"), Ciphertext: []byte("ct"), WrappedKey: []byte("wk")}
	if err := s.PutSecret(ctx, sec); err != nil {
		t.Fatal(err)
	}
	gs, err := s.GetSecret(ctx, sec.ID)
	if err != nil || string(gs.Ciphertext) != "ct" {
		t.Fatal(err)
	}
	_ = s.DeleteSecret(ctx, sec.ID)
	if _, err := s.GetSecret(ctx, sec.ID); err != ErrNotFound {
		t.Fatal("secret not deleted")
	}

	if err := s.DeleteWorkspace(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.ListComponents(ctx, w.ID); len(cs) != 0 {
		t.Fatal("components not cascaded")
	}
}

func TestStoreUsersSessionsHealth(t *testing.T) {
	for name, s := range impls(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			email := NewID("e") + "@example.com"
			u := &domain.User{ID: NewID("usr"), Email: email, Name: "Ada", Role: domain.RoleOwner, PasswordHash: "h", CreatedAt: time.Now().UTC()}
			if err := s.CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
			dup := *u
			dup.ID = NewID("usr")
			if err := s.CreateUser(ctx, &dup); err != ErrConflict {
				t.Fatalf("duplicate email: %v", err)
			}
			if got, err := s.GetUserByEmail(ctx, email); err != nil || got.ID != u.ID || got.PasswordHash != "h" {
				t.Fatalf("by email: %v %+v", err, got)
			}
			if _, err := s.GetUser(ctx, "nope"); err != ErrNotFound {
				t.Fatalf("missing user: %v", err)
			}
			if n, err := s.CountUsers(ctx); err != nil || n < 1 {
				t.Fatalf("count: %v %d", err, n)
			}

			sess := &domain.Session{ID: NewID("s"), UserID: u.ID, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}
			if err := s.PutSession(ctx, sess); err != nil {
				t.Fatal(err)
			}
			if got, err := s.GetSession(ctx, sess.ID); err != nil || got.UserID != u.ID {
				t.Fatalf("session: %v %+v", err, got)
			}
			_ = s.DeleteSession(ctx, sess.ID)
			if _, err := s.GetSession(ctx, sess.ID); err != ErrNotFound {
				t.Fatalf("deleted session: %v", err)
			}

			comp := NewID("c")
			base := time.Now().UTC().Add(-time.Hour)
			for i := 0; i < MaxHealthSamples+20; i++ {
				_ = s.AppendHealth(ctx, comp, &domain.HealthSample{At: base.Add(time.Duration(i) * time.Second), State: "running", Healthy: i%2 == 0, LatencyMS: int64(i)})
			}
			got, err := s.ListHealth(ctx, comp, 5)
			if err != nil || len(got) != 5 || got[0].LatencyMS != int64(MaxHealthSamples+19) || got[4].LatencyMS != int64(MaxHealthSamples+15) {
				t.Fatalf("newest first: %v %+v", err, got)
			}
			all, _ := s.ListHealth(ctx, comp, 0)
			if len(all) != MaxHealthSamples {
				t.Fatalf("retention: %d samples, want %d", len(all), MaxHealthSamples)
			}
			if other, _ := s.ListHealth(ctx, NewID("c"), 10); len(other) != 0 {
				t.Fatal("samples leaked across components")
			}
		})
	}
}
