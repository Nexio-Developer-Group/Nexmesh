package modelprovider

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
)

const goodKey = "sk-test-GOODKEY-1234567890"

var loop = netguard.New(netguard.Policy{AllowLoopback: true})

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(code int, msg string) map[string]any {
	return map[string]any{"error": map[string]any{"message": msg, "code": code}}
}

// fakeProvider implements the four wire protocols closely enough to verify
// headers, paths and payload shapes.
type overrides map[string]http.HandlerFunc

// reg registers a default handler unless the test overrides that route.
func (o overrides) reg(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	if alt, ok := o[pattern]; ok {
		h = alt
	}
	mux.HandleFunc(pattern, h)
}

func fakeProvider(t *testing.T, over overrides) *httptest.Server {
	mux := http.NewServeMux()
	over.reg(mux, "GET /v1/models", func(w http.ResponseWriter, r *http.Request) { // Anthropic
		if r.Header.Get("x-api-key") != goodKey || r.Header.Get("anthropic-version") == "" {
			jsonOut(w, 401, errBody(401, "invalid x-api-key "+r.Header.Get("x-api-key")))
			return
		}
		jsonOut(w, 200, map[string]any{"data": []map[string]string{{"id": "claude-sonnet-4-5", "display_name": "Claude Sonnet 4.5"}, {"id": "claude-haiku-4-5", "display_name": "Claude Haiku 4.5"}}})
	})
	over.reg(mux, "POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.Header.Get("x-api-key") != goodKey {
			jsonOut(w, 401, errBody(401, "bad key"))
			return
		}
		if b.MaxTokens != 1 || b.Model == "" {
			jsonOut(w, 400, errBody(400, "max_tokens must be 1 here"))
			return
		}
		jsonOut(w, 200, map[string]any{"content": []map[string]string{{"type": "text", "text": "pong"}}})
	})
	over.reg(mux, "GET /models", func(w http.ResponseWriter, r *http.Request) { // OpenAI-compatible
		if r.Header.Get("Authorization") != "Bearer "+goodKey {
			jsonOut(w, 401, errBody(401, "Incorrect API key provided: "+r.Header.Get("Authorization")))
			return
		}
		jsonOut(w, 200, map[string]any{"data": []map[string]string{{"id": "gpt-4.1"}, {"id": "text-embedding-3-small"}, {"id": "o-new"}}})
	})
	over.reg(mux, "POST /chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.Header.Get("Authorization") != "Bearer "+goodKey {
			jsonOut(w, 401, errBody(401, "bad key"))
			return
		}
		if b["model"] == "o-new" && b["max_tokens"] != nil {
			jsonOut(w, 400, errBody(400, "Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."))
			return
		}
		jsonOut(w, 200, map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "pong"}}}})
	})
	over.reg(mux, "GET /v1beta/models", func(w http.ResponseWriter, r *http.Request) { // Gemini
		if r.Header.Get("x-goog-api-key") != goodKey {
			jsonOut(w, 403, errBody(403, "API key not valid"))
			return
		}
		jsonOut(w, 200, map[string]any{"models": []map[string]any{
			{"name": "models/gemini-2.5-pro", "displayName": "Gemini 2.5 Pro", "inputTokenLimit": 1048576, "supportedGenerationMethods": []string{"generateContent"}},
			{"name": "models/text-embedding-004", "supportedGenerationMethods": []string{"embedContent"}},
		}})
	})
	over.reg(mux, "POST /v1beta/models/{m}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != goodKey {
			jsonOut(w, 403, errBody(403, "API key not valid"))
			return
		}
		jsonOut(w, 200, map[string]any{"candidates": []any{}})
	})
	over.reg(mux, "GET /api/tags", func(w http.ResponseWriter, r *http.Request) { // Ollama
		jsonOut(w, 200, map[string]any{"models": []map[string]string{{"name": "qwen2.5-coder:32b"}, {"name": "llama3.1:latest"}}})
	})
	over.reg(mux, "POST /api/generate", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Model == "missing" {
			jsonOut(w, 404, map[string]any{"error": "model 'missing' not found"})
			return
		}
		jsonOut(w, 200, map[string]any{"response": "pong", "done": true})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func test(t *testing.T, provider, proto, base, model, key string, g *netguard.Guard, mod func(*Config)) (*domain.TestResult, []Model) {
	t.Helper()
	cfg := Config{Protocol: proto, BaseURL: base, ModelID: model, APIKey: key, Timeout: 5 * time.Second}
	if mod != nil {
		mod(&cfg)
	}
	scrub := func(s string) string {
		if key != "" {
			s = strings.ReplaceAll(s, key, "[redacted]")
		}
		return s
	}
	r, ms := Run(context.Background(), Params{Provider: provider, Config: cfg, Guard: g, Scrub: scrub}, nil)
	b, _ := json.Marshal(r)
	if key != "" && strings.Contains(string(b), key) {
		t.Fatalf("result leaks the API key: %s", b)
	}
	return r, ms
}

func pass(t *testing.T, r *domain.TestResult) {
	t.Helper()
	if !r.Passed {
		t.Fatalf("expected pass, failed at %s: %s (%+v)", r.FailedStage, r.Message, r.Stages)
	}
}

func failAt(t *testing.T, r *domain.TestResult, stage, contains string) {
	t.Helper()
	if r.Passed || r.FailedStage != stage || !strings.Contains(r.Message, contains) {
		t.Fatalf("want failure at %s containing %q; got passed=%v stage=%s msg=%q", stage, contains, r.Passed, r.FailedStage, r.Message)
	}
}

func TestAnthropic(t *testing.T) {
	ts := fakeProvider(t, nil)
	r, models := test(t, "anthropic", ProtoAnthropic, ts.URL, "claude-sonnet-4-5", goodKey, loop, nil)
	pass(t, r)
	if len(models) != 2 || models[0].Name == "" {
		t.Fatalf("%+v", models)
	}
	if len(r.Stages) != 5 || r.Stages[4].Status != "passed" {
		t.Fatalf("%+v", r.Stages)
	}
	// Wrong key: rejected at authentication with a message that does not echo the key.
	r, _ = test(t, "anthropic", ProtoAnthropic, ts.URL, "claude-sonnet-4-5", "sk-wrong-key-xyz", loop, nil)
	failAt(t, r, StageAuth, "rejected")
	if strings.Contains(r.Message, "sk-wrong-key-xyz") {
		t.Fatal("echoed the key")
	}
	// Model not available: names the model and suggests others.
	r, _ = test(t, "anthropic", ProtoAnthropic, ts.URL, "claude-9", goodKey, loop, nil)
	failAt(t, r, StageModel, "claude-9")
	if !strings.Contains(r.Message, "claude-sonnet-4-5") {
		t.Fatalf("suggestions missing: %s", r.Message)
	}
	// Later stages are skipped after a failure.
	if r.Stages[4].Status != "skipped" {
		t.Fatalf("%+v", r.Stages)
	}
}

func TestOpenAICompatibleIncludingRenamedTokenParameter(t *testing.T) {
	ts := fakeProvider(t, nil)
	base := ts.URL // handlers are at /models and /chat/completions
	r, models := test(t, "custom", ProtoOpenAI, base, "gpt-4.1", goodKey, loop, nil)
	pass(t, r)
	if len(models) != 3 {
		t.Fatalf("compatible servers pass the list through: %+v", models)
	}
	// A model that rejects max_tokens is retried with max_completion_tokens.
	r, _ = test(t, "custom", ProtoOpenAI, base, "o-new", goodKey, loop, nil)
	pass(t, r)
	r, _ = test(t, "custom", ProtoOpenAI, base, "gpt-4.1", "nope-nope-nope", loop, nil)
	failAt(t, r, StageAuth, "rejected")
}

func TestOpenAIHostedModelFilter(t *testing.T) {
	for id, want := range map[string]bool{"gpt-4.1": true, "o3-mini": true, "chatgpt-4o-latest": true, "text-embedding-3-small": false, "whisper-1": false, "dall-e-3": false, "gpt-4o-audio-preview": false, "davinci-002": false} {
		if chatModel(id, true) != want {
			t.Errorf("%s: want %v", id, want)
		}
	}
	if !chatModel("anything", false) {
		t.Error("compatible servers must not be filtered")
	}
}

func TestGemini(t *testing.T) {
	ts := fakeProvider(t, nil)
	r, models := test(t, "google", ProtoGemini, ts.URL, "gemini-2.5-pro", goodKey, loop, nil)
	pass(t, r)
	if len(models) != 1 || models[0].ID != "gemini-2.5-pro" || models[0].ContextWindow != 1048576 {
		t.Fatalf("embedding models must be excluded and the context window read: %+v", models)
	}
	r, _ = test(t, "google", ProtoGemini, ts.URL, "gemini-2.5-pro", "bad-bad-bad-bad", loop, nil)
	failAt(t, r, StageAuth, "not permitted")
}

func TestOllamaNative(t *testing.T) {
	ts := fakeProvider(t, nil)
	r, _ := test(t, "custom", ProtoOllama, ts.URL, "qwen2.5-coder:32b", "", loop, nil)
	pass(t, r) // no key needed
	r, _ = test(t, "custom", ProtoOllama, ts.URL, "llama3.1", "", loop, nil)
	pass(t, r) // :latest is implied
	r, _ = test(t, "custom", ProtoOllama, ts.URL, "missing", "", loop, nil)
	failAt(t, r, StageModel, "not available")
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    any
		contain string
	}{
		{"quota", 429, errBody(429, "You exceeded your current quota, please check your plan and billing details"), "Quota"},
		{"rate", 429, errBody(429, "Too many requests"), "rate limiting"},
		{"server", 503, errBody(503, "overloaded"), "server error"},
		{"forbidden", 403, errBody(403, "no"), "not permitted"},
	}
	for _, c := range cases {
		ts := fakeProvider(t, overrides{"GET /v1/models": func(w http.ResponseWriter, r *http.Request) { jsonOut(w, c.status, c.body) }})
		r, _ := test(t, "anthropic", ProtoAnthropic, ts.URL, "m", goodKey, loop, nil)
		failAt(t, r, StageAuth, c.contain)
	}
	// A failing test request surfaces at the request stage.
	ts := fakeProvider(t, overrides{"POST /v1/messages": func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 429, errBody(429, "billing hard limit reached"))
	}})
	r, _ := test(t, "anthropic", ProtoAnthropic, ts.URL, "claude-sonnet-4-5", goodKey, loop, nil)
	failAt(t, r, StageRequest, "Quota")
}

func TestEndpointWithoutModelListFallsBackToTheTestRequest(t *testing.T) {
	ts := fakeProvider(t, overrides{"GET /models": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }})
	r, models := test(t, "custom", ProtoOpenAI, ts.URL, "gpt-4.1", goodKey, loop, nil)
	pass(t, r)
	if len(models) != 0 || !strings.Contains(r.Stages[2].Message, "no model list") {
		t.Fatalf("%+v %+v", models, r.Stages)
	}
	// The key is then checked by the request itself.
	r, _ = test(t, "custom", ProtoOpenAI, ts.URL, "gpt-4.1", "bad-key-bad-key", loop, nil)
	failAt(t, r, StageRequest, "rejected")
}

func TestCustomAuthHeaderAndExtraHeaders(t *testing.T) {
	var got http.Header
	ts := fakeProvider(t, overrides{
		"GET /models": func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			if r.Header.Get("X-Api-Token") != goodKey {
				jsonOut(w, 401, errBody(401, "no"))
				return
			}
			jsonOut(w, 200, map[string]any{"data": []map[string]string{{"id": "m"}}})
		},
		"POST /chat/completions": func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]any{"ok": true}) },
	})
	r, _ := test(t, "custom", ProtoOpenAI, ts.URL, "m", goodKey, loop, func(c *Config) {
		c.AuthStyle, c.HeaderName = "header", "X-Api-Token"
		c.CustomHeaders = map[string]string{"X-Tenant": "acme"}
	})
	pass(t, r)
	if got.Get("X-Tenant") != "acme" || got.Get("Authorization") != "" {
		t.Fatalf("%v", got)
	}
}

func TestInputValidation(t *testing.T) {
	ts := fakeProvider(t, nil)
	cases := []struct {
		name, provider, proto, base, model, key, contain string
	}{
		{"no protocol", "custom", "", ts.URL, "m", "", "how to call"},
		{"bad url", "custom", ProtoOpenAI, "ftp://x", "m", "", "http:// or https://"},
		{"url credentials", "custom", ProtoOpenAI, "http://u:p@h/v1", "m", "", "Do not put credentials"},
		{"no model", "custom", ProtoOpenAI, ts.URL, "", "", "Model is required"},
		{"hosted needs key", "anthropic", ProtoAnthropic, ts.URL, "m", "", "API key is required"},
	}
	for _, c := range cases {
		r, _ := test(t, c.provider, c.proto, c.base, c.model, c.key, loop, nil)
		failAt(t, r, StageInput, c.contain)
	}
	r, _ := test(t, "custom", ProtoOpenAI, ts.URL, "m", goodKey, loop, func(c *Config) { c.AuthStyle, c.HeaderName = "header", "bad header!" })
	failAt(t, r, StageInput, "header name")
}

func TestEgressPolicy(t *testing.T) {
	ts := fakeProvider(t, nil) // loopback server
	strict := netguard.New(netguard.Policy{})
	r, _ := test(t, "custom", ProtoOpenAI, ts.URL, "gpt-4.1", goodKey, strict, nil)
	failAt(t, r, StageNetwork, "loopback")
	r, _ = test(t, "custom", ProtoOpenAI, "http://169.254.169.254/v1", "m", "", netguard.New(netguard.Policy{AllowPrivate: true, AllowLoopback: true}), nil)
	failAt(t, r, StageNetwork, "metadata")
	r, _ = test(t, "custom", ProtoOpenAI, "http://10.0.4.12:11434/v1", "m", "", strict, nil)
	failAt(t, r, StageNetwork, "private network")
	r, _ = test(t, "custom", ProtoOpenAI, "http://127.0.0.1:1/v1", "m", "", loop, nil)
	if len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "loopback or private") {
		t.Fatalf("private addresses must warn: %v", r.Warnings)
	}
}

func TestWarnings(t *testing.T) {
	ts := fakeProvider(t, nil)
	r, _ := test(t, "anthropic", ProtoAnthropic, ts.URL, "claude-sonnet-4-5", goodKey, loop, nil)
	var hostedNote, private bool
	for _, w := range r.Warnings {
		hostedNote = hostedNote || strings.Contains(w, "sent to this provider")
		private = private || strings.Contains(w, "loopback or private")
	}
	if !hostedNote || !private {
		t.Fatalf("%v", r.Warnings)
	}
	r, _ = test(t, "custom", ProtoOllama, ts.URL, "qwen2.5-coder:32b", "", loop, nil)
	for _, w := range r.Warnings {
		if strings.Contains(w, "sent to this provider") {
			t.Fatal("self-hosted models keep traffic in your infrastructure; no provider notice")
		}
	}
}

func TestTLSHandling(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]any{"models": []map[string]string{{"name": "m"}}})
	})
	mux.HandleFunc("POST /api/generate", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]any{"done": true}) })
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}))

	r, _ := test(t, "custom", ProtoOllama, ts.URL, "m", "", loop, nil)
	failAt(t, r, StageAuth, "certificate is not trusted") // listing fails first on TLS
	r, _ = test(t, "custom", ProtoOllama, ts.URL, "m", "", loop, func(c *Config) { c.CACertificate = caPEM })
	pass(t, r)
	r, _ = test(t, "custom", ProtoOllama, ts.URL, "m", "", loop, func(c *Config) { c.TLSInsecure = true })
	pass(t, r)
	r, _ = test(t, "custom", ProtoOllama, ts.URL, "m", "", loop, func(c *Config) { c.CACertificate = "garbage" })
	failAt(t, r, StageInput, "CA certificate")
}

func TestListModelsForDropdown(t *testing.T) {
	ts := fakeProvider(t, nil)
	ms, e := ListModels(context.Background(), Params{Provider: "anthropic", Config: Config{Protocol: ProtoAnthropic, BaseURL: ts.URL, APIKey: goodKey}, Guard: loop})
	if e != nil || len(ms) != 2 {
		t.Fatalf("%v %+v", e, ms)
	}
	if _, e := ListModels(context.Background(), Params{Provider: "anthropic", Config: Config{Protocol: ProtoAnthropic, BaseURL: ts.URL, APIKey: "bad-bad-bad-bad"}, Guard: loop}); e == nil || e.Kind != KindUnauthorized {
		t.Fatalf("%v", e)
	}
	// An endpoint with no list yields an empty list so the UI falls back to manual entry.
	ts2 := fakeProvider(t, overrides{"GET /models": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }})
	ms, e = ListModels(context.Background(), Params{Provider: "custom", Config: Config{Protocol: ProtoOpenAI, BaseURL: ts2.URL, APIKey: goodKey}, Guard: loop})
	if e != nil || len(ms) != 0 {
		t.Fatalf("%v %+v", e, ms)
	}
}

func TestTimeout(t *testing.T) {
	ts := fakeProvider(t, overrides{"GET /api/tags": func(w http.ResponseWriter, r *http.Request) { time.Sleep(800 * time.Millisecond) }})
	r, _ := test(t, "custom", ProtoOllama, ts.URL, "m", "", loop, func(c *Config) { c.Timeout = 150 * time.Millisecond })
	failAt(t, r, StageAuth, "did not respond in time")
}
