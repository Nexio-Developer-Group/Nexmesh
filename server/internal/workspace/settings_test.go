package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
)

func code(err error) string {
	var ve *runtime.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func ip(n int) *int       { return &n }
func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

func (e *env) ready(t *testing.T, name string) *domain.Workspace {
	w, err := e.svc.Create(e.ctx, "u1", CreateInput{Name: name, CPU: 4, RAMGB: 8, StorageGB: 40})
	if err != nil {
		t.Fatal(err)
	}
	return e.waitStatus(t, w.ID, domain.WSReady)
}

func TestSettingsNameAndDescription(t *testing.T) {
	e := newEnv(t)
	w := e.ready(t, "orders-api")
	other := e.ready(t, "other")
	got, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{Name: sp("orders-v2"), Description: sp("Stripe webhooks")})
	if err != nil || got.Name != "orders-v2" || got.Description != "Stripe webhooks" || got.OS != domain.OSUbuntu2404 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{Name: sp(other.Name)}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{Name: sp("Bad Name")}); code(err) != "invalid_name" {
		t.Fatalf("%v", err)
	}
	// Audit records what changed, from and to.
	evs, _ := e.st.ListAudit(e.ctx, w.ID, 20)
	found := false
	for _, ev := range evs {
		if ev.Action == "workspace.settings" {
			found = ev.Detail["name"] != nil
		}
	}
	if !found {
		t.Fatalf("settings audit event missing: %+v", evs)
	}
}

func TestSettingsCPUAndRAMApplyLive(t *testing.T) {
	e := newEnv(t)
	w := e.ready(t, "live")
	pv, err := e.svc.PreviewSettings(e.ctx, "u1", w.ID, SettingsChange{CPU: ip(6), RAMGB: ip(12)})
	if err != nil || pv.RestartRequired || len(pv.Changes) != 2 || !pv.Changes[0].AppliesImmediately {
		t.Fatalf("%v %+v", err, pv)
	}
	got, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{CPU: ip(6), RAMGB: ip(12)})
	if err != nil || got.CPU != 6 || got.RAMGB != 12 || got.RestartRequired || got.Status != domain.WSReady {
		t.Fatalf("%v %+v", err, got)
	}
	// Preview did not change anything on its own.
	pv, _ = e.svc.PreviewSettings(e.ctx, "u1", w.ID, SettingsChange{CPU: ip(6)})
	if len(pv.Changes) != 0 {
		t.Fatalf("no-op change should be empty: %+v", pv)
	}
}

func TestSettingsRejectUnavailableResources(t *testing.T) {
	e := newEnv(t)
	w := e.ready(t, "limits")
	for _, c := range []struct {
		in   SettingsChange
		want string
	}{
		{SettingsChange{RAMGB: ip(64)}, "ram_out_of_range"},
		{SettingsChange{CPU: ip(99)}, "cpu_out_of_range"},
		{SettingsChange{StorageGB: ip(5000)}, "storage_out_of_range"},
		{SettingsChange{GPU: bp(true)}, "gpu_unavailable"},
	} {
		if _, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, c.in); code(err) != c.want {
			t.Errorf("%+v: got %v, want %s", c.in, err, c.want)
		}
	}
	got, _ := e.svc.Get(e.ctx, "u1", w.ID)
	if got.CPU != 4 || got.RAMGB != 8 || got.StorageGB != 40 {
		t.Fatalf("a rejected change must not alter the workspace: %+v", got)
	}
}

func TestSettingsStorageGrowthNeedsConfirmedRestart(t *testing.T) {
	e := newEnv(t)
	w := e.ready(t, "grow")
	pv, _ := e.svc.PreviewSettings(e.ctx, "u1", w.ID, SettingsChange{StorageGB: ip(80)})
	if !pv.RestartRequired || pv.Changes[0].AppliesImmediately {
		t.Fatalf("%+v", pv)
	}
	if _, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{StorageGB: ip(80)}); code(err) != "restart_confirmation_required" {
		t.Fatalf("%v", err)
	}
	if got, _ := e.svc.Get(e.ctx, "u1", w.ID); got.StorageGB != 40 {
		t.Fatal("nothing may change before the restart is confirmed")
	}
	got, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{StorageGB: ip(80), ConfirmRestart: true})
	if err != nil || got.StorageGB != 80 || !got.RestartRequired {
		t.Fatalf("%v %+v", err, got)
	}
	// The restart completes in the background and clears the flag.
	for i := 0; i < 300; i++ {
		x, _ := e.st.GetWorkspace(e.ctx, w.ID)
		if !x.RestartRequired && x.Status == domain.WSReady {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("restart never completed")
}

func TestSettingsRestartOfStoppedWorkspaceKeepsItStopped(t *testing.T) {
	e := newEnv(t)
	w := e.ready(t, "stoppedgrow")
	_, _ = e.svc.Stop(e.ctx, "u1", w.ID)
	e.waitStatus(t, w.ID, domain.WSStopped)
	if _, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{StorageGB: ip(60), ConfirmRestart: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		x, _ := e.st.GetWorkspace(e.ctx, w.ID)
		if !x.RestartRequired {
			if x.Status != domain.WSStopped {
				t.Fatalf("status %s, want stopped", x.Status)
			}
			if o, _ := e.drv.InspectWorkspace(e.ctx, w.ID); o.Running {
				t.Fatal("container should be stopped again")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("never completed")
}

type usage float64

func (u usage) StorageUsedGB(context.Context, string) float64 { return float64(u) }

func TestSettingsStorageReduction(t *testing.T) {
	e := newEnv(t)
	w := e.ready(t, "shrink")
	// Backend without shrink support rejects any reduction.
	if _, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{StorageGB: ip(20), ConfirmRestart: true}); code(err) != "storage_shrink_unsupported" {
		t.Fatalf("%v", err)
	}
	// Backend that can shrink still rejects going below current usage.
	e2 := newEnv(t)
	caps, _ := e2.drv.Capabilities(e2.ctx)
	caps.StorageShrink = true
	*e2.drv = *runtime.NewFake(caps)
	e2.svc.WithUsage(usage(22))
	w2 := e2.ready(t, "shrink2")
	if _, err := e2.svc.ApplySettings(e2.ctx, "u1", w2.ID, SettingsChange{StorageGB: ip(20), ConfirmRestart: true}); code(err) != "storage_below_usage" {
		t.Fatalf("%v", err)
	}
	if _, err := e2.svc.ApplySettings(e2.ctx, "u1", w2.ID, SettingsChange{StorageGB: ip(30), ConfirmRestart: true}); err != nil {
		t.Fatalf("a reduction that stays above usage is allowed: %v", err)
	}
}

func TestSettingsCannotChangeWhileProvisioningOrFailed(t *testing.T) {
	e := newEnv(t)
	e.drv.Delay = 200 * time.Millisecond
	w, _ := e.svc.Create(e.ctx, "u1", CreateInput{Name: "busy", CPU: 1, RAMGB: 1, StorageGB: 5})
	if _, err := e.svc.ApplySettings(e.ctx, "u1", w.ID, SettingsChange{Description: sp("x")}); !errors.Is(err, ErrBadState) {
		t.Fatalf("%v", err)
	}
}

func TestSettingsOwnership(t *testing.T) {
	e := newEnv(t)
	w := e.ready(t, "mine")
	if _, err := e.svc.ApplySettings(e.ctx, "intruder", w.ID, SettingsChange{Description: sp("x")}); err == nil {
		t.Fatal("other users must not change settings")
	}
}
