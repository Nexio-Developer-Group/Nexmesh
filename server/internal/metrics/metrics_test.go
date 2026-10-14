package metrics_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/metrics"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func rig(t *testing.T) (*metrics.Service, *store.Memory, *runtime.Fake, context.Context) {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 8, MaxRAMGB: 16, MaxStorageGB: 100})
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady, CPU: 2, RAMGB: 4, StorageGB: 20})
	_ = drv.CreateWorkspace(ctx, runtime.WorkspaceSpec{ID: "ws1", Resources: runtime.Resources{CPU: 2, RAMGB: 4, StorageGB: 20}}, func(string, string) {})
	return metrics.New(st, cat, drv), st, drv, ctx
}

func TestInstalledServicesReportUsageAndStoppedOnesSayWhy(t *testing.T) {
	m, st, drv, ctx := rig(t)
	_ = drv.InstallService(ctx, runtime.ServiceSpec{WorkspaceID: "ws1", Name: "redis", Image: "redis:7"}, func(string) {})
	_ = drv.StartService(ctx, "ws1", "redis")
	run := &domain.Component{ID: "c1", WorkspaceID: "ws1", CatalogID: "redis", Mode: domain.ModeInstalled, Observed: domain.StateRunning}
	stopped := &domain.Component{ID: "c2", WorkspaceID: "ws1", CatalogID: "mysql", Mode: domain.ModeInstalled, Observed: domain.StateStopped}
	_ = st.CreateComponent(ctx, run)
	_ = st.CreateComponent(ctx, stopped)

	snap, err := m.Collect(ctx, "u1", "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Limits.RAMGB != 4 || snap.Workspace == nil || snap.Workspace.Current == nil {
		t.Fatalf("workspace reading: %+v", snap)
	}
	if !snap.Simulated {
		t.Fatal("readings from the simulated runtime must say so")
	}
	by := map[string]metrics.Card{}
	for _, c := range snap.Components {
		by[c.CatalogID] = c
	}
	r := by["redis"]
	if r.Current == nil || r.Current.MemLimitMB != 256 || r.Current.MemUsedMB <= 0 || r.Current.MemUsedMB > 256 || r.Current.CPUPercent < 0 {
		t.Fatalf("redis reading: %+v", r.Current)
	}
	if s := by["mysql"]; s.Current != nil || s.Unavailable == "" {
		t.Fatalf("a stopped service has no reading and explains why: %+v", s)
	}
}

func TestTrendGrowsAndIsBounded(t *testing.T) {
	m, st, drv, ctx := rig(t)
	_ = drv.InstallService(ctx, runtime.ServiceSpec{WorkspaceID: "ws1", Name: "redis", Image: "redis:7"}, func(string) {})
	_ = drv.StartService(ctx, "ws1", "redis")
	_ = st.CreateComponent(ctx, &domain.Component{ID: "c1", WorkspaceID: "ws1", CatalogID: "redis", Mode: domain.ModeInstalled, Observed: domain.StateRunning})
	for i := 0; i < 3; i++ {
		m.Sample(ctx, "ws1")
	}
	snap, _ := m.Collect(ctx, "u1", "ws1")
	// Samples taken within five seconds update the latest point, so a fast poller cannot flood the trend.
	if n := len(snap.Components[0].History); n != 1 {
		t.Fatalf("rapid sampling should keep one point, got %d", n)
	}
	m.Forget("ws1")
	if snap, _ := m.Collect(ctx, "u1", "ws1"); len(snap.Components[0].History) != 1 {
		t.Fatal("history restarts after Forget")
	}
}

func TestConnectedCardsShowLatencyTrendFromHealthChecks(t *testing.T) {
	m, st, _, ctx := rig(t)
	_ = st.CreateComponent(ctx, &domain.Component{ID: "c9", WorkspaceID: "ws1", CatalogID: "redis", Mode: domain.ModeConnected, ConnName: "cache", Observed: domain.StateConnected})
	if snap, _ := m.Collect(ctx, "u1", "ws1"); snap.Components[0].Unavailable == "" {
		t.Fatal("no health check yet should be explained")
	}
	now := time.Now()
	for i, ms := range []int64{20, 25, 90} {
		_ = st.AppendHealth(ctx, "c9", &domain.HealthSample{At: now.Add(time.Duration(i) * time.Minute), State: domain.StateConnected, Healthy: true, LatencyMS: ms})
	}
	snap, _ := m.Collect(ctx, "u1", "ws1")
	c := snap.Components[0]
	if c.Mode != "connected" || len(c.History) != 3 || c.History[0].LatencyMS != 20 || c.Current.LatencyMS != 90 {
		t.Fatalf("latency trend should read oldest to newest: %+v", c)
	}
}

func TestNoReadingsWithoutStatsSupportAndOwnership(t *testing.T) {
	cat, _ := catalog.Load("../../../catalog")
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	m := metrics.New(st, cat, noStats{})
	snap, err := m.Collect(ctx, "u1", "ws1")
	if err != nil || snap.Workspace != nil || len(snap.Components) != 0 {
		t.Fatalf("a backend without metrics reports none: %v %+v", err, snap)
	}
	if _, err := m.Collect(ctx, "other", "ws1"); err == nil {
		t.Fatal("another owner cannot read metrics")
	}
}

type noStats struct{ runtime.Driver }
