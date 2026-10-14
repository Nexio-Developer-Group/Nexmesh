package inspector_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/inspector"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type rig struct {
	ctx  context.Context
	st   *store.Memory
	comp *component.Service
	sec  *secrets.Service
	svc  *inspector.Service
}

func newRig(t *testing.T) *rig {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	key := bytes.Repeat([]byte{9}, 32)
	be, _ := secrets.NewStoreBackend(st, key)
	sec := secrets.New(be)
	aud := audit.New(st, log)
	comp := component.NewService(st, cat, events.NewBus(), aud).WithConnect(component.ConnectConfig{
		Secrets: sec, Guard: netguard.New(netguard.Policy{AllowLoopback: true}), FingerprintKey: key, Log: log,
		Probe: func(ctx context.Context, p probe.Params, report probe.Report) *domain.TestResult {
			return &domain.TestResult{Passed: true, At: time.Now().UTC(), LatencyMS: 1}
		}})
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	return &rig{ctx, st, comp, sec, inspector.New(comp, aud)}
}

func (r *rig) connect(t *testing.T, catalogID, name string, fields map[string]any) *domain.Component {
	c, err := r.comp.Add(r.ctx, "u1", "ws1", catalogID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	in := component.ConnectionInput{ConnName: name, Environment: "development", Fields: fields}
	if _, err := r.comp.Test(r.ctx, "u1", "ws1", c.ID, in); err != nil {
		t.Fatal(err)
	}
	got, err := r.comp.Save(r.ctx, "u1", "ws1", c.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDescribeExplainsWhyACardCannotBeInspected(t *testing.T) {
	r := newRig(t)
	draft, _ := r.comp.Add(r.ctx, "u1", "ws1", "redis", 0, 0)
	c, err := r.svc.Describe(r.ctx, "u1", "ws1", draft.ID)
	if err != nil || c.Available || !strings.Contains(c.Reason, "Connect") || !c.ReadOnly {
		t.Fatalf("draft: %+v %v", c, err)
	}
	inst := &domain.Component{ID: "ci", WorkspaceID: "ws1", CatalogID: "redis", Mode: domain.ModeInstalled, Observed: domain.StateRunning}
	_ = r.st.CreateComponent(r.ctx, inst)
	if c, _ := r.svc.Describe(r.ctx, "u1", "ws1", "ci"); c.Available || !strings.Contains(c.Reason, "terminal") {
		t.Fatalf("installed: %+v", c)
	}
	api := r.connect(t, "externalapi", "billing", map[string]any{"baseUrl": "https://api.example.com", "authType": "none"})
	if c, _ := r.svc.Describe(r.ctx, "u1", "ws1", api.ID); c.Available || !strings.Contains(c.Reason, "no inspector") {
		t.Fatalf("external api: %+v", c)
	}
	rd := r.connect(t, "redis", "cache", map[string]any{"host": "127.0.0.1", "port": 6379, "password": "pw"})
	c, _ = r.svc.Describe(r.ctx, "u1", "ws1", rd.ID)
	if !c.Available || len(c.Ops) != 3 || c.Ops[0].Name != "keys" {
		t.Fatalf("redis: %+v", c)
	}
	if _, err := r.svc.Describe(r.ctx, "intruder", "ws1", rd.ID); err == nil {
		t.Fatal("another owner cannot describe")
	}
	if _, err := r.svc.Run(r.ctx, domain.ActorUser, "u1", "u1", "ws1", api.ID, probe.InspectRequest{Op: "x"}); err == nil {
		t.Fatal("a card without an inspector is refused")
	} else {
		var f *inspector.Failure
		if !errors.As(err, &f) || f.Code != "not_inspectable" {
			t.Fatalf("%v", err)
		}
	}
}

func TestRateLimitPerCard(t *testing.T) {
	r := newRig(t)
	rd := r.connect(t, "redis", "cache", map[string]any{"host": "127.0.0.1", "port": 1, "password": "pw"}) // nothing listens: each call fails fast
	var last error
	for i := 0; i < 45; i++ {
		_, last = r.svc.Run(r.ctx, domain.ActorUser, "u1", "u1", "ws1", rd.ID, probe.InspectRequest{Op: "keys"})
	}
	if !errors.Is(last, inspector.ErrRateLimited) {
		t.Fatalf("expected the rate limit after 40 calls in a minute, got %v", last)
	}
}

// Against a real Redis: every request is audited without result data, the
// statement is recorded without the password, and refused writes are recorded too.
func TestRunAuditsWithoutResultsAndScrubsSecrets(t *testing.T) {
	port := os.Getenv("NX_IT_REDIS_PORT")
	pass := os.Getenv("NX_IT_PASS")
	if os.Getenv("NEXMESH_IT") != "1" || port == "" {
		t.Skip("needs the integration Redis (scripts/it/start-services.sh, NEXMESH_IT=1)")
	}
	r := newRig(t)
	var n int
	fmt.Sscan(port, &n)
	rd := r.connect(t, "redis", "cache", map[string]any{"host": "127.0.0.1", "port": n, "password": pass, "tls": false})

	res, err := r.svc.Run(r.ctx, domain.ActorUser, "u1", "u1", "ws1", rd.ID, probe.InspectRequest{Op: "command", Args: map[string]any{"command": "PING"}})
	if err != nil || res.Production {
		t.Fatalf("%v %+v", err, res)
	}
	_, err = r.svc.Run(r.ctx, domain.ActorUser, "u1", "u1", "ws1", rd.ID, probe.InspectRequest{Op: "command", Args: map[string]any{"command": "SET secret-key " + pass}})
	var f *inspector.Failure
	if !errors.As(err, &f) || f.Code != "read_only" {
		t.Fatalf("a write must be refused: %v", err)
	}

	evs, _ := r.st.ListAudit(r.ctx, "ws1", 20)
	var ok, refused bool
	for _, e := range evs {
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), pass) {
			t.Fatalf("the password is in the audit log: %s", raw)
		}
		switch e.Action {
		case "inspect.query":
			ok = e.Detail["op"] == "command" && e.Detail["rows"] != nil && e.Detail["readOnly"] == true && e.ActorType == domain.ActorUser
		case "inspect.query_failed":
			refused = e.Detail["error"] == "read_only"
		}
		if e.Action == "inspect.query" {
			if _, has := e.Detail["result"]; has {
				t.Fatal("results must never be audited")
			}
		}
	}
	if !ok || !refused {
		t.Fatalf("both the query and the refusal must be audited: %+v", evs)
	}
}
