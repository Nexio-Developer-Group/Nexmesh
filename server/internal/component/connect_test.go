package component

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type cfx struct {
	*fixture
	probeCalls []map[string]any
	good       string // password that passes
	latency    int64
	now        time.Time
}

func newConnectFixture(t *testing.T) *cfx {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	bus := events.NewBus()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	key := bytes.Repeat([]byte{7}, 32)
	be, _ := secrets.NewStoreBackend(st, key)
	f := &cfx{good: "correct-horse-battery", latency: 41, now: time.Now()}
	svc := NewService(st, cat, bus, audit.New(st, log)).WithConnect(ConnectConfig{
		Secrets: secrets.New(be), Guard: netguard.New(netguard.Policy{AllowLoopback: true}), FingerprintKey: key, Log: log,
		Probe: func(ctx context.Context, p probe.Params, report probe.Report) *domain.TestResult {
			cp := map[string]any{}
			for k, v := range p.Fields {
				cp[k] = v
			}
			f.probeCalls = append(f.probeCalls, cp)
			r := &domain.TestResult{At: time.Now().UTC(), LatencyMS: f.latency, ServerVersion: "7.0.14"}
			for _, s := range probe.StageOrder {
				r.Stages = append(r.Stages, domain.StageResult{Stage: s, Status: "passed"})
				if report != nil {
					report(domain.StageResult{Stage: s, Status: "passed"})
				}
			}
			if p.Fields["password"] != f.good {
				r.FailedStage, r.Message = probe.StageAuth, "Host reachable but authentication failed. Check the username and password."
				for i := range r.Stages {
					if r.Stages[i].Stage == probe.StageAuth {
						r.Stages[i].Status = "failed"
					}
				}
				return r
			}
			r.Passed = true
			return r
		},
	})
	svc.conn.now = func() time.Time { return f.now }
	ctx := context.Background()
	ws := &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady}
	_ = st.CreateWorkspace(ctx, ws)
	f.fixture = &fixture{svc, st, ctx, ws, bus}
	return f
}

func mongoIn(pw string) ConnectionInput {
	return ConnectionInput{ConnName: "prod-orders-db", Environment: "production", Fields: map[string]any{
		"host": "orders.k3x9p.mongodb.net", "port": 27017, "database": "orders", "username": "orders_app", "password": pw}}
}

func (f *cfx) tick(d time.Duration) { f.now = f.now.Add(d) }

func (f *cfx) newCard(t *testing.T, id string) *domain.Component {
	c, err := f.svc.Add(f.ctx, "u1", "ws1", id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAddToWorkspaceRequiresPassingTestForExactValues(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")

	// No test yet.
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good)); errCode(err) != "test_required" {
		t.Fatalf("add without test: %v", err)
	}
	// A failing test does not enable Add.
	res, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn("wrong-password"))
	if err != nil || res.Passed {
		t.Fatal(err, res)
	}
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateTestFailed || !strings.Contains(got.Message, "authentication failed") {
		t.Fatalf("%+v", got)
	}
	f.tick(2 * time.Second)
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn("wrong-password")); errCode(err) != "test_required" {
		t.Fatalf("add after failed test: %v", err)
	}

	// A passing test enables Add for exactly those values.
	f.tick(2 * time.Second)
	res, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	if !res.Passed {
		t.Fatalf("%+v", res)
	}
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateTestPassed {
		t.Fatalf("%+v", got)
	}
	// Editing any field invalidates the result.
	edited := mongoIn(f.good)
	edited.Fields["database"] = "other"
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, edited); errCode(err) != "test_required" {
		t.Fatalf("edited field: %v", err)
	}
	// So does a different password.
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn("different-password")); errCode(err) != "test_required" {
		t.Fatalf("different password: %v", err)
	}
	// The exact tested values are accepted.
	added, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	if err != nil {
		t.Fatal(err)
	}
	if added.Mode != domain.ModeConnected || added.Observed != domain.StateConnected || added.ConnName != "prod-orders-db" || added.Environment != "production" {
		t.Fatalf("%+v", added)
	}
	if got := f.svc.ExposedAs(added); got[0] != "PROD_ORDERS_DB_URI" {
		t.Fatalf("%v", got)
	}
}

func TestStaleTestCannotAuthorizeAdd(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	f.tick(16 * time.Minute)
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good)); errCode(err) != "test_required" {
		t.Fatalf("%v", err)
	}
}

func TestPasswordNeverStoredInPlaintextOrExposed(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	added, _ := f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))

	// Component record: no password, only a reference.
	raw, _ := json.Marshal(added)
	if bytes.Contains(raw, []byte(f.good)) {
		t.Fatalf("component leaks the password: %s", raw)
	}
	if _, ok := added.Connection["password"]; ok {
		t.Fatal("password present in non-secret connection settings")
	}
	if added.SecretRef == "" {
		t.Fatal("secret reference missing")
	}
	// Stored secret is ciphertext.
	rec, _ := f.st.GetSecret(f.ctx, added.SecretRef)
	if bytes.Contains(rec.Ciphertext, []byte(f.good)) {
		t.Fatal("secret stored in plaintext")
	}
	// Public form hides even the reference.
	if Public(added).SecretRef != "" {
		t.Fatal("Public must drop the secret reference")
	}
	// Audit events never contain the password.
	evs, _ := f.st.ListAudit(f.ctx, "ws1", 100)
	for _, e := range evs {
		b, _ := json.Marshal(e)
		if bytes.Contains(b, []byte(f.good)) {
			t.Fatalf("audit leaks password: %s", b)
		}
	}
	if len(evs) < 3 {
		t.Fatalf("expected audit events for add, test and connect, got %d", len(evs))
	}
	// Events published to the browser never contain it either.
	ch, cancel := f.bus.Subscribe("ws1")
	defer cancel()
	f.tick(2 * time.Second)
	_, _ = f.svc.Retest(f.ctx, "u1", "ws1", c.ID)
	for len(ch) > 0 {
		ev := <-ch
		b, _ := json.Marshal(ev)
		if bytes.Contains(b, []byte(f.good)) {
			t.Fatalf("event leaks password: %s", b)
		}
	}
}

func TestPendingSecretsLetAddWorkWithoutRetyping(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	// After a page reload the password field is empty (write-only); the tested secret is still held.
	reload := mongoIn("")
	delete(reload.Fields, "password")
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, reload); err != nil {
		t.Fatalf("add using held secret: %v", err)
	}
}

func TestRetestUpdatesHealthAndDegrades(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	_, _ = f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	f.tick(2 * time.Second)
	f.latency = 55
	res, err := f.svc.Retest(f.ctx, "u1", "ws1", c.ID)
	if err != nil || !res.Passed {
		t.Fatal(err, res)
	}
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateConnected || !strings.Contains(got.Message, "55 ms") {
		t.Fatalf("%+v", got)
	}
	// The probe received the stored password, merged in memory.
	if f.probeCalls[len(f.probeCalls)-1]["password"] != f.good {
		t.Fatal("retest did not use the stored secret")
	}
	// Credentials expire: health check degrades the card.
	f.good = "rotated-elsewhere"
	f.svc.HealthOnce(f.ctx)
	got, _ := f.st.GetComponent(f.ctx, c.ID)
	if got.Observed != domain.StateDegraded || !strings.Contains(got.Message, "authentication failed") {
		t.Fatalf("%+v", got)
	}
	// ...and recovers once reachable again.
	f.good = "correct-horse-battery"
	f.svc.HealthOnce(f.ctx)
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateConnected {
		t.Fatalf("%+v", got)
	}
	evs, _ := f.st.ListAudit(f.ctx, "ws1", 100)
	changed := 0
	for _, e := range evs {
		if e.Action == "connection.health_changed" {
			changed++
		}
	}
	if changed != 2 {
		t.Fatalf("health transitions audited: %d", changed)
	}
}

func TestEditRequiresRetestAndRotateWorks(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	_, _ = f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))

	edit := ConnectionInput{Fields: map[string]any{"host": "new-host.example.com", "port": 27017, "database": "orders", "username": "orders_app"}}
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, edit); errCode(err) != "test_required" {
		t.Fatalf("edit without retest: %v", err)
	}
	f.tick(2 * time.Second)
	if res, _ := f.svc.Test(f.ctx, "u1", "ws1", c.ID, edit); !res.Passed {
		t.Fatalf("%+v", res) // the stored password was merged in
	}
	got, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, edit)
	if err != nil || got.Connection["host"] != "new-host.example.com" || got.ConnName != "prod-orders-db" {
		t.Fatalf("%v %+v", err, got)
	}

	// Rotate: a bad new password does not replace the stored one.
	f.tick(2 * time.Second)
	res, _, err := f.svc.Rotate(f.ctx, "u1", "ws1", c.ID, map[string]string{"password": "nope-nope-nope"})
	if err != nil || res.Passed {
		t.Fatal(err, res)
	}
	if st := f.svc.secretsFor(f.ctx, activeID(c.ID)); st["password"] != f.good {
		t.Fatal("failed rotation must keep the old secret")
	}
	// A good one does.
	f.good = "brand-new-password-1"
	f.tick(2 * time.Second)
	res, c2, err := f.svc.Rotate(f.ctx, "u1", "ws1", c.ID, map[string]string{"password": f.good})
	if err != nil || !res.Passed || c2 == nil {
		t.Fatal(err, res)
	}
	if st := f.svc.secretsFor(f.ctx, activeID(c.ID)); st["password"] != "brand-new-password-1" {
		t.Fatal("rotation not stored")
	}
	if _, _, err := f.svc.Rotate(f.ctx, "u1", "ws1", c.ID, map[string]string{"host": "x"}); errCode(err) != "unknown_secret" {
		t.Fatalf("%v", err)
	}
}

func TestDisconnectRemovesSecretsAndKeepsCard(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	added, _ := f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	if _, err := f.svc.Disconnect(f.ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.GetSecret(f.ctx, added.SecretRef); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("secret survived disconnect")
	}
	got, _ := f.st.GetComponent(f.ctx, c.ID)
	if got.Mode != "" || got.Observed != domain.StateDisconnect || got.Connection != nil || got.SecretRef != "" {
		t.Fatalf("%+v", got)
	}
	// The card can be reused: connect again or install.
	f.tick(2 * time.Second)
	if res, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good)); err != nil || !res.Passed {
		t.Fatal(err, res)
	}
	if err := f.svc.Remove(f.ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatalf("removing a card that is only test-passed: %v", err)
	}
}

func TestConnectionNamesAreValidatedAndUnique(t *testing.T) {
	f := newConnectFixture(t)
	a := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", a.ID, mongoIn(f.good))
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", a.ID, mongoIn(f.good)); err != nil {
		t.Fatal(err)
	}
	b := f.newCard(t, "mysql")
	in := ConnectionInput{ConnName: "prod-orders-db", Fields: map[string]any{"host": "h", "port": 3306, "database": "d", "username": "u", "password": f.good}}
	f.tick(2 * time.Second)
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", b.ID, in)
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", b.ID, in); errCode(err) != "name_taken" {
		t.Fatalf("%v", err)
	}
	in.ConnName = "Bad Name!"
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", b.ID, in); errCode(err) != "invalid_conn_name" {
		t.Fatalf("%v", err)
	}
	in.ConnName, in.Environment = "ok-name", "prod"
	if _, err := f.svc.Test(f.ctx, "u1", "ws1", b.ID, in); errCode(err) != "invalid_environment" {
		t.Fatalf("%v", err)
	}
}

func TestOnlyServiceComponentsCanBeConnected(t *testing.T) {
	f := newConnectFixture(t)
	for _, id := range []string{"nodejs", "nestjs", "react", "nginx", "certbot", "bash"} {
		c := f.newCard(t, id)
		if _, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, ConnectionInput{Fields: map[string]any{"host": "h"}}); !errors.Is(err, ErrNotConnectable) {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestTestRateLimit(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "redis")
	in := ConnectionInput{Fields: map[string]any{"host": "h", "port": 6379}}
	if _, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, in); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second test within a second: %v", err)
	}
	for i := 0; i < 9; i++ {
		f.tick(1100 * time.Millisecond)
		if _, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, in); err != nil && !errors.Is(err, ErrRateLimited) {
			t.Fatal(err)
		}
	}
	f.tick(1100 * time.Millisecond)
	if _, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, in); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("eleventh test in a minute must be limited: %v", err)
	}
	f.tick(time.Minute)
	if _, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, in); err != nil {
		t.Fatalf("limit should reset: %v", err)
	}
}

func TestConnectionEnvForAgent(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	_, _ = f.svc.Save(f.ctx, "u1", "ws1", c.ID, mongoIn(f.good))
	env, err := f.svc.ConnectionEnv(f.ctx, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if env["PROD_ORDERS_DB_HOST"] != "orders.k3x9p.mongodb.net" || env["PROD_ORDERS_DB_PASSWORD"] != f.good || !strings.HasPrefix(env["PROD_ORDERS_DB_URI"], "mongodb://orders_app:") {
		t.Fatalf("%v", env)
	}
	// Disconnected components expose nothing.
	_, _ = f.svc.Disconnect(f.ctx, "u1", "ws1", c.ID)
	if env, _ := f.svc.ConnectionEnv(f.ctx, "ws1"); len(env) != 0 {
		t.Fatalf("%v", env)
	}
}

func TestFingerprintDependsOnKeyAndValues(t *testing.T) {
	f := newConnectFixture(t)
	a := f.svc.fingerprint("redis", map[string]any{"host": "h", "password": "p"})
	if a != f.svc.fingerprint("redis", map[string]any{"password": "p", "host": "h"}) {
		t.Fatal("order must not matter")
	}
	if a == f.svc.fingerprint("redis", map[string]any{"host": "h", "password": "q"}) || a == f.svc.fingerprint("mysql", map[string]any{"host": "h", "password": "p"}) {
		t.Fatal("fingerprint must change with values and component")
	}
	f.svc.conn.FingerprintKey = bytes.Repeat([]byte{9}, 32)
	if a == f.svc.fingerprint("redis", map[string]any{"host": "h", "password": "p"}) {
		t.Fatal("fingerprint must depend on the server key")
	}
	_ = fmt.Sprint
}
