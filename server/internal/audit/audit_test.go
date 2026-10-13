package audit

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func TestScrubRedactsSensitiveKeysRecursively(t *testing.T) {
	in := map[string]any{
		"host": "h", "password": "p", "apiKey": "k", "Authorization": "Bearer x", "token": "t",
		"nested":    map[string]any{"secret": "s", "port": 1},
		"secretRef": "connsec_1", "componentId": "c1", "customHeaders": map[string]any{"X-Tenant": "acme"},
	}
	out := Scrub(in)
	for _, k := range []string{"password", "apiKey", "token"} {
		if out[k] != "[redacted]" {
			t.Errorf("%s not redacted: %v", k, out[k])
		}
	}
	if out["nested"].(map[string]any)["secret"] != "[redacted]" || out["nested"].(map[string]any)["port"] != 1 {
		t.Errorf("%v", out["nested"])
	}
	if out["host"] != "h" || out["componentId"] != "c1" || out["secretRef"] != "connsec_1" {
		t.Errorf("non-secret fields must survive: %v", out)
	}
	if in["password"] != "p" {
		t.Error("Scrub must not mutate its input")
	}
	if Scrub(nil) != nil {
		t.Error("nil in, nil out")
	}
}

func TestRecordStoresActorAndScrubbedDetail(t *testing.T) {
	st := store.NewMemory()
	r := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	r.User(ctx, "u1", "ws1", "connection.add", "mongodb", map[string]any{"password": "hunter2", "host": "h"})
	r.Record(ctx, domain.ActorAgent, "agt", "ws1", "file.write", "x", nil)
	evs, _ := st.ListAudit(ctx, "ws1", 10)
	if len(evs) != 2 {
		t.Fatalf("%d", len(evs))
	}
	byAction := map[string]*domain.AuditEvent{}
	for _, e := range evs {
		byAction[e.Action] = e
	}
	if e := byAction["connection.add"]; e.ActorType != domain.ActorUser || e.ActorID != "u1" || e.Detail["password"] != "[redacted]" || e.Detail["host"] != "h" {
		t.Fatalf("%+v", e)
	}
	if byAction["file.write"].ActorType != domain.ActorAgent {
		t.Fatal("agent actor type must be recorded")
	}
}
