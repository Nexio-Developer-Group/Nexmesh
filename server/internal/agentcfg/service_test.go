package agentcfg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/modelprovider"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const goodKey = "sk-ant-GOOD-0123456789"

type fx struct {
	svc   *Service
	st    store.Store
	ctx   context.Context
	calls []modelprovider.Config
	now   time.Time
	good  string
}

func newFx(t *testing.T) *fx {
	provs, err := catalog.LoadProviders("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	key := bytes.Repeat([]byte{5}, 32)
	be, _ := secrets.NewStoreBackend(st, key)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &fx{st: st, ctx: context.Background(), now: time.Now(), good: goodKey}
	f.svc = NewService(st, secrets.New(be), netguard.New(netguard.Policy{AllowLoopback: true}), audit.New(st, log), provs, key)
	f.svc.now = func() time.Time { return f.now }
	f.svc.run = func(ctx context.Context, p modelprovider.Params, report func(domain.StageResult)) (*domain.TestResult, []modelprovider.Model) {
		f.calls = append(f.calls, p.Config)
		r := &domain.TestResult{At: time.Now().UTC(), LatencyMS: 640}
		if p.Config.APIKey != f.good && p.Provider != "custom" {
			r.FailedStage, r.Message = modelprovider.StageAuth, "The key was rejected (invalid or revoked)."
			return r, nil
		}
		r.Passed = true
		return r, []modelprovider.Model{{ID: "claude-sonnet-4-5"}}
	}
	_ = st.CreateWorkspace(f.ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	return f
}

func (f *fx) tick(d time.Duration) { f.now = f.now.Add(d) }

func claude(name, key string) Input {
	return Input{Name: name, Provider: "anthropic", ModelID: "claude-sonnet-4-5", APIKey: key}
}

func code(err error) string {
	var ve *runtime.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func TestSaveRequiresPassingTestForExactValues(t *testing.T) {
	f := newFx(t)
	if _, err := f.svc.Create(f.ctx, "u1", "ws1", claude("Claude for backend work", f.good)); code(err) != "test_required" {
		t.Fatalf("save without test: %v", err)
	}
	res, err := f.svc.Test(f.ctx, "u1", claude("Claude for backend work", "wrong-key-1234567"))
	if err != nil || res.Passed {
		t.Fatal(err, res)
	}
	f.tick(2 * time.Second)
	if _, err := f.svc.Create(f.ctx, "u1", "ws1", claude("Claude for backend work", "wrong-key-1234567")); code(err) != "test_required" {
		t.Fatalf("a failed test must not authorize save: %v", err)
	}
	res, _ = f.svc.Test(f.ctx, "u1", claude("Claude for backend work", f.good))
	if !res.Passed || len(res.Models) == 0 {
		t.Fatalf("%+v", res)
	}
	// Editing any field invalidates the result: model, name, key.
	for _, edit := range []func(*Input){func(i *Input) { i.ModelID = "other" }, func(i *Input) { i.Name = "renamed" }, func(i *Input) { i.APIKey = "different-key-1234" }, func(i *Input) { i.Endpoint = "https://proxy.example.com" }} {
		in := claude("Claude for backend work", f.good)
		edit(&in)
		if _, err := f.svc.Create(f.ctx, "u1", "ws1", in); code(err) != "test_required" {
			t.Fatalf("edited values must need a new test: %v", err)
		}
	}
	// Another user's pass does not authorize this user.
	if _, err := f.svc.Create(f.ctx, "u2", "ws1", claude("Claude for backend work", f.good)); err == nil {
		t.Fatal("tests are per user")
	}
	c, err := f.svc.Create(f.ctx, "u1", "ws1", claude("Claude for backend work", f.good))
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsDefault || c.Provider != "anthropic" || c.ModelID != "claude-sonnet-4-5" || c.LastTest == nil || !c.LastTest.Passed {
		t.Fatalf("%+v", c)
	}
}

func TestStaleTestExpires(t *testing.T) {
	f := newFx(t)
	_, _ = f.svc.Test(f.ctx, "u1", claude("a", f.good))
	f.tick(16 * time.Minute)
	if err := f.svc.RequireTested("u1", claude("a", f.good)); code(err) != "test_required" {
		t.Fatalf("%v", err)
	}
}

func saved(t *testing.T, f *fx, name, key string) *domain.AgentConfig {
	t.Helper()
	f.tick(2 * time.Second)
	if _, err := f.svc.Test(f.ctx, "u1", claude(name, key)); err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.Create(f.ctx, "u1", "ws1", claude(name, key))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestKeyIsEncryptedWriteOnlyAndNeverLeaks(t *testing.T) {
	f := newFx(t)
	c := saved(t, f, "Claude", f.good)
	raw, _ := json.Marshal(c)
	if bytes.Contains(raw, []byte(f.good)) {
		t.Fatalf("configuration leaks the key: %s", raw)
	}
	rec, _ := f.st.GetSecret(f.ctx, c.SecretRef)
	if bytes.Contains(rec.Ciphertext, []byte(f.good)) {
		t.Fatal("key stored in plaintext")
	}
	evs, _ := f.st.ListAudit(f.ctx, "ws1", 50)
	if len(evs) == 0 {
		t.Fatal("no audit events")
	}
	for _, e := range evs {
		b, _ := json.Marshal(e)
		if bytes.Contains(b, []byte(f.good)) {
			t.Fatalf("audit leaks the key: %s", b)
		}
	}
	// The stored key is only visible to the model-calling component through inputOf.
	if got := f.svc.inputOf(f.ctx, c).APIKey; got != f.good {
		t.Fatal("stored key not retrievable internally")
	}
}

func TestDefaultSelectionAndDeletion(t *testing.T) {
	f := newFx(t)
	a := saved(t, f, "Claude", f.good)
	b := saved(t, f, "Claude two", f.good)
	if !a.IsDefault || b.IsDefault {
		t.Fatalf("first is default: %v %v", a.IsDefault, b.IsDefault)
	}
	if err := f.svc.SetDefault(f.ctx, "u1", "ws1", b.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := f.svc.List(f.ctx, "u1", "ws1")
	defaults := 0
	for _, c := range list {
		if c.IsDefault {
			defaults++
			if c.ID != b.ID {
				t.Fatal("wrong default")
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("exactly one default, got %d", defaults)
	}
	// Deleting the default hands the role to another configuration and removes the key.
	if err := f.svc.Delete(f.ctx, "u1", "ws1", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.GetSecret(f.ctx, b.SecretRef); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("secret survived delete")
	}
	left, _ := f.st.GetAgentConfig(f.ctx, a.ID)
	if !left.IsDefault {
		t.Fatal("default not reassigned")
	}
}

func TestNameUniqueness(t *testing.T) {
	f := newFx(t)
	saved(t, f, "Claude", f.good)
	f.tick(2 * time.Second)
	_, _ = f.svc.Test(f.ctx, "u1", claude("claude", f.good))
	if _, err := f.svc.Create(f.ctx, "u1", "ws1", claude("claude", f.good)); code(err) != "name_taken" {
		t.Fatalf("%v", err)
	}
}

func TestEditKeepsStoredKeyAndNeedsRetest(t *testing.T) {
	f := newFx(t)
	c := saved(t, f, "Claude", f.good)
	edit := Input{Name: "Claude", Provider: "anthropic", ModelID: "claude-haiku-4-5", MaxOutputTokens: 2048}
	if _, err := f.svc.Update(f.ctx, "u1", "ws1", c.ID, edit); code(err) != "test_required" {
		t.Fatalf("%v", err)
	}
	f.tick(2 * time.Second)
	res, err := f.svc.TestEdit(f.ctx, "u1", "ws1", c.ID, edit)
	if err != nil || !res.Passed {
		t.Fatal(err, res)
	}
	if f.calls[len(f.calls)-1].APIKey != f.good {
		t.Fatal("the stored key must be used when the key is omitted")
	}
	got, err := f.svc.Update(f.ctx, "u1", "ws1", c.ID, edit)
	if err != nil || got.ModelID != "claude-haiku-4-5" || got.Params["maxOutputTokens"].(int) != 2048 || got.SecretRef == "" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestRetestUsesStoredKeyAndRecordsResult(t *testing.T) {
	f := newFx(t)
	c := saved(t, f, "Claude", f.good)
	f.tick(2 * time.Second)
	f.good = "rotated-away-0000000" // the provider revoked the key
	res, err := f.svc.Retest(f.ctx, "u1", "ws1", c.ID)
	if err != nil || res.Passed || res.FailedStage != modelprovider.StageAuth {
		t.Fatal(err, res)
	}
	got, _ := f.st.GetAgentConfig(f.ctx, c.ID)
	if got.LastTest.Passed {
		t.Fatal("failed retest must be recorded")
	}
}

func TestReplaceKeyOnlyAfterPassingTest(t *testing.T) {
	f := newFx(t)
	c := saved(t, f, "Claude", f.good)
	f.tick(2 * time.Second)
	res, newc, err := f.svc.ReplaceKey(f.ctx, "u1", "ws1", c.ID, "bad-new-key-123456")
	if err != nil || res.Passed || newc != nil {
		t.Fatal(err, res)
	}
	if got := f.svc.inputOf(f.ctx, c).APIKey; got != f.good {
		t.Fatal("a failed replacement must keep the old key")
	}
	f.good = "brand-new-key-9999999"
	f.tick(2 * time.Second)
	res, newc, err = f.svc.ReplaceKey(f.ctx, "u1", "ws1", c.ID, f.good)
	if err != nil || !res.Passed || newc == nil {
		t.Fatal(err, res)
	}
	if got := f.svc.inputOf(f.ctx, c).APIKey; got != f.good {
		t.Fatal("new key not stored")
	}
	if _, _, err := f.svc.ReplaceKey(f.ctx, "u1", "ws1", c.ID, " "); code(err) != "key_required" {
		t.Fatalf("%v", err)
	}
}

func TestInputValidation(t *testing.T) {
	f := newFx(t)
	bad := []struct {
		name string
		in   Input
		want string
	}{
		{"provider", Input{Name: "x", Provider: "nope", ModelID: "m"}, "unknown_provider"},
		{"name", Input{Provider: "anthropic", ModelID: "m"}, "invalid_name"},
		{"tokens", Input{Name: "x", Provider: "anthropic", ModelID: "m", MaxOutputTokens: -1}, "invalid_value"},
		{"temperature", Input{Name: "x", Provider: "anthropic", ModelID: "m", Temperature: fp(3)}, "invalid_value"},
		{"retries", Input{Name: "x", Provider: "anthropic", ModelID: "m", Retries: ip(9)}, "invalid_value"},
		{"timeout", Input{Name: "x", Provider: "anthropic", ModelID: "m", TimeoutSeconds: 9999}, "invalid_value"},
		{"protocol", Input{Name: "x", Provider: "custom", ModelID: "m", Protocol: "grpc", Endpoint: "http://h"}, "unsupported_protocol"},
	}
	for _, c := range bad {
		if _, err := f.svc.Test(f.ctx, "u1", c.in); code(err) != c.want {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }

func TestTestRateLimit(t *testing.T) {
	f := newFx(t)
	in := claude("a", f.good)
	if _, err := f.svc.Test(f.ctx, "u1", in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Test(f.ctx, "u1", in); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("%v", err)
	}
	f.tick(1100 * time.Millisecond)
	if _, err := f.svc.Test(f.ctx, "u1", in); err != nil {
		t.Fatal(err)
	}
}

func TestOwnership(t *testing.T) {
	f := newFx(t)
	c := saved(t, f, "Claude", f.good)
	if _, err := f.svc.List(f.ctx, "intruder", "ws1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if err := f.svc.Delete(f.ctx, "intruder", "ws1", c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

// End to end against a fake local Ollama server through the real adapters.
func TestCustomLocalModelEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "qwen2.5-coder:32b"}}})
	})
	mux.HandleFunc("POST /api/generate", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"done":true}`)) })
	ts := httptest.NewServer(mux)
	defer ts.Close()

	f := newFx(t)
	f.svc.run = modelprovider.Run
	in := Input{Name: "Local Qwen coder", Provider: "custom", Protocol: "ollama", Endpoint: ts.URL, ModelID: "qwen2.5-coder:32b",
		Capabilities: Capabilities{Streaming: true, Tools: false}}
	res, err := f.svc.Test(f.ctx, "u1", in)
	if err != nil || !res.Passed {
		t.Fatalf("%v %+v", err, res)
	}
	var warned bool
	for _, w := range res.Warnings {
		warned = warned || strings.Contains(w, "loopback or private")
	}
	if !warned {
		t.Fatalf("loopback endpoints must warn: %v", res.Warnings)
	}
	c, err := f.svc.Create(f.ctx, "u1", "ws1", in)
	if err != nil || c.Protocol != "ollama" || c.Endpoint != ts.URL || c.SecretRef != "" {
		t.Fatalf("%v %+v", err, c)
	}
	// Models for the dropdown.
	f.tick(2 * time.Second)
	ms, err := f.svc.Models(f.ctx, "u1", in)
	if err != nil || len(ms) != 1 {
		t.Fatalf("%v %+v", err, ms)
	}
}
