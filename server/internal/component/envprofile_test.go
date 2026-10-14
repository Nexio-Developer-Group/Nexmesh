package component

import (
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

func devIn(pw string) ConnectionInput {
	return ConnectionInput{ConnName: "orders-db", Environment: "development", Fields: map[string]any{
		"host": "dev-db.internal", "port": 27017, "database": "orders", "username": "dev_user", "password": pw}}
}

func stagingIn(pw string) ConnectionInput {
	return ConnectionInput{Environment: "staging", Fields: map[string]any{
		"host": "staging-db.internal", "port": 27017, "database": "orders", "username": "stg_user", "password": pw}}
}

func (f *cfx) connected(t *testing.T) *domain.Component {
	c := f.newCard(t, "mongodb")
	if res, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, devIn(f.good)); err != nil || !res.Passed {
		t.Fatal(err, res)
	}
	got, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, devIn(f.good))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f *cfx) activate(env string) {
	_, _ = f.st.UpdateWorkspace(f.ctx, "ws1", func(w *domain.Workspace) error { w.ActiveEnvironment = env; return nil })
}

func (f *cfx) env(t *testing.T) map[string]string {
	e, err := f.svc.ConnectionEnv(f.ctx, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestProfileNeverBorrowsAnotherEnvironmentsCredentials(t *testing.T) {
	f := newConnectFixture(t)
	c := f.connected(t)
	f.tick(2 * time.Second)

	// No password given for staging: the development password must not be used.
	res, err := f.svc.Test(f.ctx, "u1", "ws1", c.ID, stagingIn(""))
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatal("a staging test without a password must fail, not reuse the development one")
	}
	if last := f.probeCalls[len(f.probeCalls)-1]; last["password"] != nil && last["password"] != "" {
		t.Fatalf("the probe saw a password the user did not enter for staging: %v", last["password"])
	}
	f.tick(2 * time.Second)
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good)); errCode(err) != "test_required" {
		t.Fatalf("saving a profile needs its own passing test: %v", err)
	}
}

func TestSwitchingEnvironmentChangesInjectedValues(t *testing.T) {
	f := newConnectFixture(t)
	c := f.connected(t)

	// Before anyone picks an environment nothing changes: the connection applies.
	if e := f.env(t); e["ORDERS_DB_HOST"] != "dev-db.internal" {
		t.Fatalf("legacy behaviour should be unchanged: %v", e)
	}

	f.tick(2 * time.Second)
	if res, _ := f.svc.Test(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good)); !res.Passed {
		t.Fatalf("%+v", res)
	}
	got, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good))
	if err != nil {
		t.Fatal(err)
	}
	if got.Profiles["staging"] == nil || got.Profiles["staging"].Connection["host"] != "staging-db.internal" {
		t.Fatalf("profile not stored: %+v", got.Profiles)
	}
	if got.Environment != "development" || got.Connection["host"] != "dev-db.internal" {
		t.Fatalf("adding a profile must not touch the main connection: %+v", got)
	}
	if pub := Public(got); pub.Profiles["staging"].SecretRef != "" {
		t.Fatal("profile secret reference must not leave the server")
	}

	f.activate("development")
	e := f.env(t)
	if e["ORDERS_DB_HOST"] != "dev-db.internal" || !strings.Contains(e["ORDERS_DB_URI"], "dev_user") {
		t.Fatalf("development values: %v", e)
	}
	f.activate("staging")
	e = f.env(t)
	if e["ORDERS_DB_HOST"] != "staging-db.internal" || !strings.Contains(e["ORDERS_DB_URI"], "stg_user") {
		t.Fatalf("staging values: %v", e)
	}
	for k, v := range e {
		if strings.Contains(v, "dev-db") || strings.Contains(v, "dev_user") {
			t.Fatalf("development value leaked into staging: %s=%s", k, v)
		}
	}
	f.activate("production")
	if e := f.env(t); len(e) != 0 {
		t.Fatalf("no production connection: nothing may be injected, got %v", e)
	}
	if f.svc.ExposedAs(got) != nil {
		t.Fatal("no names should be offered for an environment without a connection")
	}
}

func TestHealthFollowsTheActiveEnvironment(t *testing.T) {
	f := newConnectFixture(t)
	c := f.connected(t)
	f.tick(2 * time.Second)
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good))
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good)); err != nil {
		t.Fatal(err)
	}
	state := func() (string, string) {
		got, _ := f.st.GetComponent(f.ctx, c.ID)
		return got.Observed, got.Message
	}

	f.activate("production")
	f.svc.HealthForWorkspace(f.ctx, "ws1")
	if s, msg := state(); s != domain.StateDisconnect || !strings.Contains(msg, "No production connection") {
		t.Fatalf("card without a production connection: %s %q", s, msg)
	}
	f.activate("staging")
	f.svc.HealthForWorkspace(f.ctx, "ws1")
	if s, _ := state(); s != domain.StateConnected {
		t.Fatalf("staging has a connection: %s", s)
	}
	last := f.probeCalls[len(f.probeCalls)-1]
	if last["host"] != "staging-db.internal" {
		t.Fatalf("health check must probe the active environment, probed %v", last["host"])
	}
}

func TestRemovingAProfileAndDisconnectingDeleteTheirSecrets(t *testing.T) {
	f := newConnectFixture(t)
	c := f.connected(t)
	f.tick(2 * time.Second)
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good))
	if _, err := f.svc.Save(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.conn.Secrets.Get(f.ctx, profileSecretID(c.ID, "staging")); err != nil {
		t.Fatal("staging secret should exist")
	}
	if _, err := f.svc.DeleteProfile(f.ctx, "u1", "ws1", c.ID, "development"); errCode(err) != "main_connection" {
		t.Fatalf("the main connection is removed by disconnecting: %v", err)
	}
	got, err := f.svc.DeleteProfile(f.ctx, "u1", "ws1", c.ID, "staging")
	if err != nil || len(got.Profiles) != 0 {
		t.Fatal(err, got)
	}
	if _, err := f.svc.conn.Secrets.Get(f.ctx, profileSecretID(c.ID, "staging")); err == nil {
		t.Fatal("the secret must be deleted with the profile")
	}

	// Disconnect removes every profile secret too.
	f.tick(2 * time.Second)
	_, _ = f.svc.Test(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good))
	_, _ = f.svc.Save(f.ctx, "u1", "ws1", c.ID, stagingIn(f.good))
	if _, err := f.svc.Disconnect(f.ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.conn.Secrets.Get(f.ctx, profileSecretID(c.ID, "staging")); err == nil {
		t.Fatal("disconnect must delete profile secrets")
	}
	if after, _ := f.st.GetComponent(f.ctx, c.ID); len(after.Profiles) != 0 {
		t.Fatal("profiles must be cleared on disconnect")
	}
}

func TestAConnectedCardCannotBeRemovedFromTheCanvasDirectly(t *testing.T) {
	f := newConnectFixture(t)
	c := f.connected(t)
	f.activate("production") // observed becomes "disconnected" for a card with no production connection
	f.svc.HealthForWorkspace(f.ctx, "ws1")
	if err := f.svc.Remove(f.ctx, "u1", "ws1", c.ID); err == nil {
		t.Fatal("removing a connected card would orphan its connections; disconnect first")
	}
}
