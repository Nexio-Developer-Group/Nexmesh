package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

func TestPersistentSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "data.json")
	p, err := OpenPersistent(path)
	if err != nil {
		t.Fatal(err)
	}
	w := &domain.Workspace{ID: "ws1", OwnerID: "u", Name: "keep-me", Status: domain.WSReady}
	if err := p.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	_ = p.PutSecret(ctx, &domain.SecretRecord{ID: "s1", Ciphertext: []byte("ct"), WrappedKey: []byte("wk")})
	_ = p.AppendAudit(ctx, &domain.AuditEvent{ID: "a1", WorkspaceID: "ws1", Action: "x"})
	_ = p.PutDoc(ctx, "conn", "ws1", "c1", []byte(`{"a":1}`))
	p.Close()

	q, err := OpenPersistent(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	got, err := q.GetWorkspace(ctx, "ws1")
	if err != nil || got.Name != "keep-me" {
		t.Fatalf("workspace not restored: %v %+v", err, got)
	}
	if s, err := q.GetSecret(ctx, "s1"); err != nil || string(s.WrappedKey) != "wk" {
		t.Fatalf("secret not restored: %v", err)
	}
	if ds, _ := q.ListDocs(ctx, "conn", "ws1"); len(ds) != 1 || string(ds[0]) != `{"a":1}` {
		t.Fatalf("documents not restored: %q", ds)
	}
	if a, _ := q.ListAudit(ctx, "ws1", 10); len(a) != 1 {
		t.Fatalf("audit not restored: %d", len(a))
	}
	if fi, _ := os.Stat(path); fi == nil || fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("data file must not be group/world readable: %v", fi)
	}
}

func TestPersistentRejectsDamagedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := OpenPersistent(path); err == nil {
		t.Fatal("expected an error for a damaged data file")
	}
}
