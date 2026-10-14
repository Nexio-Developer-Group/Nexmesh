package component

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

func TestHealthHistoryRecordsTransitionsNotRepeats(t *testing.T) {
	f := newConnectFixture(t)
	ctx := context.Background()
	c := f.newCard(t, "mongodb")

	f.svc.recordHealth(ctx, c, domain.StateConnected, 12, "ok")
	f.svc.recordHealth(ctx, c, domain.StateConnected, 13, "ok") // same state, just recorded: skipped
	f.svc.recordHealth(ctx, c, domain.StateDegraded, 0, "timeout")
	f.svc.recordHealth(ctx, c, domain.StateConnected, 9, "ok again")
	h, err := f.svc.Health(ctx, "u1", "ws1", c.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Samples) != 3 || h.Samples[0].State != domain.StateConnected || h.Samples[1].State != domain.StateDegraded || h.Samples[1].Healthy || h.Samples[2].LatencyMS != 12 {
		t.Fatalf("samples: %+v", h.Samples)
	}
	if h.UptimePercent < 66 || h.UptimePercent > 67 {
		t.Fatalf("uptime = %v", h.UptimePercent)
	}
	if h.Since == nil || !h.Since.Equal(h.Samples[0].At) {
		t.Fatalf("since: %v", h.Since)
	}
	if _, err := f.svc.Health(ctx, "intruder", "ws1", c.ID, 10); err == nil {
		t.Fatal("another user read the health history")
	}
}

func TestHeartbeatSampleAfterInterval(t *testing.T) {
	f := newConnectFixture(t)
	ctx := context.Background()
	c := f.newCard(t, "mongodb")
	_ = f.st.AppendHealth(ctx, c.ID, &domain.HealthSample{At: time.Now().Add(-2 * heartbeatEvery).UTC(), State: domain.StateConnected, Healthy: true})
	f.svc.recordHealth(ctx, c, domain.StateConnected, 5, "still ok")
	if hs, _ := f.svc.Health(ctx, "u1", "ws1", c.ID, 10); len(hs.Samples) != 2 {
		t.Fatalf("want a heartbeat sample after %v, got %d samples", heartbeatEvery, len(hs.Samples))
	}
}

func TestInstallTransitionsAreHistoryAndLogsAreScrubbed(t *testing.T) {
	f := newInstallFixture(t, 8)
	c := f.add(t, "redis")
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "7.4.1", Start: true, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	f.wait(t, c.ID, domain.StateRunning)
	h, _ := f.svc.Health(f.ctx, "u1", "ws1", c.ID, 50)
	states := []string{}
	for i := len(h.Samples) - 1; i >= 0; i-- {
		states = append(states, h.Samples[i].State)
	}
	if len(states) < 2 || states[0] != domain.StateInstalling || states[len(states)-1] != domain.StateRunning {
		t.Fatalf("install history: %v", states)
	}

	logs, err := f.svc.Logs(f.ctx, "u1", "ws1", c.ID, 100)
	if err != nil || !strings.Contains(logs, "ready to accept connections") {
		t.Fatalf("logs: %v %q", err, logs)
	}
	if _, err := f.svc.Logs(f.ctx, "intruder", "ws1", c.ID, 100); err == nil {
		t.Fatal("another user read logs")
	}
	rt := f.add(t, "python")
	if _, err := f.svc.Logs(f.ctx, "u1", "ws1", rt.ID, 100); err == nil {
		t.Fatal("runtimes have no container logs")
	}
}

func TestConnectedComponentsHaveNoLogs(t *testing.T) {
	f := newConnectFixture(t)
	c := f.newCard(t, "mongodb")
	if _, err := f.svc.Logs(context.Background(), "u1", "ws1", c.ID, 50); err == nil {
		t.Fatal("connected components have no container logs")
	}
}
