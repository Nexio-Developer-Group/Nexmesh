package gitcred_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitcred"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func rig(t *testing.T) (*gitcred.Service, store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	be, err := secrets.NewStoreBackend(st, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "n", Status: domain.WSReady})
	return gitcred.NewService(st, secrets.New(be), audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))), st, ctx
}

const tok = "ghp_ABCDEFGHIJ0123456789secret"

func TestAddStoresTokenEncryptedAndNeverReturnsIt(t *testing.T) {
	svc, st, ctx := rig(t)
	c, err := svc.Add(ctx, "u1", "ws1", gitcred.AddInput{Provider: "github", Token: tok})
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "github.com" || c.Username != "x-access-token" || c.Label != "github.com" {
		t.Fatalf("defaults: %+v", c)
	}
	l, _ := svc.List(ctx, "u1", "ws1")
	w, _ := st.GetWorkspace(ctx, "ws1")
	for _, blob := range []any{l, w.GitCredentials} {
		if strings.Contains(strings.TrimSpace(fmtAny(blob)), "ABCDEFGHIJ") {
			t.Fatal("token visible in metadata")
		}
	}
	rec, _ := st.GetSecret(ctx, "gitcred_ws1_"+c.ID)
	if strings.Contains(string(rec.Ciphertext), "ABCDEFGHIJ") {
		t.Fatal("token stored in plaintext")
	}
	evs, _ := st.ListAudit(ctx, "ws1", 10)
	if len(evs) != 1 || evs[0].Action != "git.credential_added" || strings.Contains(fmtAny(evs[0].Detail), "ABCDEFGHIJ") {
		t.Fatalf("audit: %+v", evs)
	}
	got, err := svc.Resolve(ctx, "ws1", "GitHub.com", "")
	if err != nil || got == nil || got.Token != tok || got.Username != "x-access-token" {
		t.Fatalf("resolve: %+v %v", got, err)
	}
}

func fmtAny(v any) string { return strings.ReplaceAll(strings.TrimSpace(jsonString(v)), "\n", "") }

func TestValidation(t *testing.T) {
	svc, _, ctx := rig(t)
	var ve *gitcred.ValidationError
	for name, in := range map[string]gitcred.AddInput{
		"provider":             {Provider: "nope", Token: tok},
		"custom no host":       {Provider: "custom", Token: tok, Username: "bob"},
		"url as host":          {Provider: "custom", Host: "https://git.example.com/x", Token: tok, Username: "bob"},
		"short token":          {Provider: "github", Token: "abc"},
		"space in token":       {Provider: "github", Token: "abcdefgh ijkl"},
		"bitbucket needs user": {Provider: "bitbucket", Token: tok},
	} {
		if _, err := svc.Add(ctx, "u1", "ws1", in); !errors.As(err, &ve) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	if _, err := svc.Add(ctx, "u1", "ws1", gitcred.AddInput{Provider: "custom", Host: "Git.Example.com:8443", Username: "bob", Token: tok}); err != nil {
		t.Fatalf("valid custom host: %v", err)
	}
	if _, err := svc.Add(ctx, "u1", "ws1", gitcred.AddInput{Provider: "custom", Host: "git.example.com:8443", Username: "bob", Token: tok}); !errors.As(err, &ve) {
		t.Fatal("duplicate host+user accepted")
	}
	if _, err := svc.Add(ctx, "intruder", "ws1", gitcred.AddInput{Provider: "github", Token: tok}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign user: %v", err)
	}
}

func TestRotateAndRevokeRemoveAccess(t *testing.T) {
	svc, st, ctx := rig(t)
	c, _ := svc.Add(ctx, "u1", "ws1", gitcred.AddInput{Provider: "gitlab", Token: tok})
	newTok := "glpat-NEWTOKEN-0123456789"
	if _, err := svc.Update(ctx, "u1", "ws1", c.ID, nil, &newTok); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Resolve(ctx, "ws1", "gitlab.com", ""); got.Token != newTok || got.Username != "oauth2" {
		t.Fatalf("rotation: %+v", got)
	}
	if got, err := svc.Resolve(ctx, "ws1", "bitbucket.org", ""); got != nil || err != nil {
		t.Fatalf("no credential must resolve to nil (public repos): %v %v", got, err)
	}
	if err := svc.Revoke(ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Resolve(ctx, "ws1", "gitlab.com", ""); got != nil {
		t.Fatal("revoked credential still resolves")
	}
	if _, err := st.GetSecret(ctx, "gitcred_ws1_"+c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("secret survived revoke")
	}
	if err := svc.Revoke(ctx, "u1", "ws1", c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double revoke: %v", err)
	}
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
