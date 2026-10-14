package runtime

import (
	"errors"
	"os"
	"path/filepath"

	"context"
	"fmt"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime/snapfs"
	"io"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Driver used by tests and by `NEXMESH_DRIVER=fake`
// local UI development. It simulates provisioning delay and port conflicts.
type Fake struct {
	mu       sync.Mutex
	caps     Capabilities
	Delay    time.Duration // per provisioning step
	ws       map[string]*fakeWS
	services map[string]*fakeSvc // key: workspaceID/name
	FailNext map[string]error    // injected failures by operation name
	// BusyPorts simulates ports already taken on the backend; starting a
	// service that exposes one fails with ErrPortInUse.
	BusyPorts map[int]bool
	ExecLog   []string // commands passed to Exec, for assertions

	// Runner, when set, is called whenever a workspace becomes running and
	// may start an in-process agent so local development has real files and
	// shells. The returned function stops it; remove asks it to delete the
	// workspace's files too.
	Runner func(spec WorkspaceSpec) (stop func(remove bool))
	stops  map[string]func(remove bool)

	// SnapshotDir, when set, names the directory holding a workspace's files
	// (the local agent's root); snapshots are then real tar.gz archives under
	// SnapshotRoot. Without it snapshots are bookkeeping only.
	SnapshotDir  func(workspaceID string) string
	SnapshotRoot string
	snaps        map[string]int64 // workspace/snapshot -> size, for the bookkeeping mode
}

func (f *Fake) launch(spec WorkspaceSpec) {
	if f.Runner == nil {
		return
	}
	if f.stops == nil {
		f.stops = map[string]func(bool){}
	}
	if _, running := f.stops[spec.ID]; running {
		return
	}
	f.stops[spec.ID] = f.Runner(spec)
}

func (f *Fake) halt(id string, remove bool) {
	if stop := f.stops[id]; stop != nil {
		stop(remove)
		delete(f.stops, id)
	}
}

type fakeWS struct {
	spec    WorkspaceSpec
	running bool
}

type fakeSvc struct {
	spec    ServiceSpec
	running bool
	logs    []string
}

func NewFake(caps Capabilities) *Fake {
	return &Fake{caps: caps, ws: map[string]*fakeWS{}, services: map[string]*fakeSvc{}, FailNext: map[string]error{}, BusyPorts: map[int]bool{}}
}

func (f *Fake) fail(op string) error {
	if err, ok := f.FailNext[op]; ok {
		delete(f.FailNext, op)
		return err
	}
	return nil
}

func (f *Fake) sleep(ctx context.Context) error {
	if f.Delay == 0 {
		return nil
	}
	select {
	case <-time.After(f.Delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *Fake) Capabilities(context.Context) (Capabilities, error) { return f.caps, nil }

func (f *Fake) CreateWorkspace(ctx context.Context, spec WorkspaceSpec, progress func(step, detail string)) error {
	f.mu.Lock()
	if err := f.fail("create"); err != nil {
		f.mu.Unlock()
		return err
	}
	f.mu.Unlock()
	if err := ValidateResources(spec.Resources, f.caps); err != nil {
		return err
	}
	progress("preparing", "pulling base image")
	if err := f.sleep(ctx); err != nil {
		return err
	}
	progress("starting", "starting workspace container")
	if err := f.sleep(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ws[spec.ID] = &fakeWS{spec: spec, running: true}
	f.launch(spec)
	return nil
}

func (f *Fake) get(id string) (*fakeWS, error) {
	w, ok := f.ws[id]
	if !ok {
		return nil, ErrNotFound
	}
	return w, nil
}

func (f *Fake) StartWorkspace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, err := f.get(id)
	if err != nil {
		return err
	}
	w.running = true
	f.launch(w.spec)
	return nil
}

func (f *Fake) StopWorkspace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, err := f.get(id)
	if err != nil {
		return err
	}
	w.running = false
	f.halt(id, false)
	return nil
}

func (f *Fake) RemoveWorkspace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.halt(id, true)
	delete(f.ws, id)
	for k, s := range f.services {
		if s.spec.WorkspaceID == id {
			delete(f.services, k)
		}
	}
	return nil
}

func (f *Fake) RecreateWorkspace(ctx context.Context, spec WorkspaceSpec, progress func(step, detail string)) error {
	f.mu.Lock()
	if err := f.fail("recreate"); err != nil {
		f.mu.Unlock()
		return err
	}
	if _, err := f.get(spec.ID); err != nil {
		f.mu.Unlock()
		return err
	}
	f.mu.Unlock()
	if err := ValidateResources(spec.Resources, f.caps); err != nil {
		return err
	}
	progress("starting", "replacing the workspace container")
	if err := f.sleep(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ws[spec.ID] = &fakeWS{spec: spec, running: true}
	f.halt(spec.ID, false)
	f.launch(spec)
	return nil
}

func (f *Fake) UpdateResources(_ context.Context, id string, res Resources) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, err := f.get(id)
	if err != nil {
		return err
	}
	if err := ValidateResources(res, f.caps); err != nil {
		return err
	}
	w.spec.Resources = res
	return nil
}

func (f *Fake) InspectWorkspace(_ context.Context, id string) (Observed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.ws[id]
	if !ok {
		return Observed{}, nil
	}
	return Observed{Exists: true, Running: w.running}, nil
}

func (f *Fake) Exec(_ context.Context, id string, cmd []string, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.get(id); err != nil {
		return err
	}
	if err := f.fail("exec"); err != nil {
		return err
	}
	f.ExecLog = append(f.ExecLog, strings.Join(cmd, " "))
	fmt.Fprintf(out, "$ %v\nok\n", cmd)
	return nil
}

func svcKey(ws, name string) string { return ws + "/" + name }

func (f *Fake) InstallService(ctx context.Context, spec ServiceSpec, progress func(detail string)) error {
	f.mu.Lock()
	if err := f.fail("install_service"); err != nil {
		f.mu.Unlock()
		return err
	}
	f.mu.Unlock()
	progress("pulling " + spec.Image)
	if err := f.sleep(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.services[svcKey(spec.WorkspaceID, spec.Name)] = &fakeSvc{spec: spec, logs: []string{"pulled " + spec.Image, "container created"}}
	return nil
}

func (f *Fake) StartService(_ context.Context, ws, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("start_service"); err != nil {
		return err
	}
	s, ok := f.services[svcKey(ws, name)]
	if !ok {
		return ErrNotFound
	}
	for _, p := range s.spec.Ports {
		if f.BusyPorts[p] {
			return ErrPortInUse
		}
	}
	s.running = true
	s.logs = append(s.logs, "ready to accept connections")
	return nil
}

func (f *Fake) StopService(_ context.Context, ws, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.services[svcKey(ws, name)]
	if !ok {
		return ErrNotFound
	}
	s.running = false
	s.logs = append(s.logs, "received stop signal, shutting down")
	return nil
}

func (f *Fake) RemoveService(_ context.Context, ws, name string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.services, svcKey(ws, name))
	return nil
}

func (f *Fake) ServiceLogs(_ context.Context, ws, name string, tail int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.services[svcKey(ws, name)]
	if !ok {
		return "", ErrNotFound
	}
	lines := s.logs
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func (f *Fake) InspectService(_ context.Context, ws, name string) (Observed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.services[svcKey(ws, name)]
	if !ok {
		return Observed{}, nil
	}
	return Observed{Exists: true, Running: s.running}, nil
}

// Shutdown stops every in-process agent the Runner started.
func (f *Fake) Shutdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.stops {
		f.halt(id, false)
	}
}

func (f *Fake) snapPath(ws, snap string) string {
	return filepath.Join(f.SnapshotRoot, ws, snap+".tgz")
}

func (f *Fake) SnapshotWorkspace(_ context.Context, id, snap string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("snapshot"); err != nil {
		return 0, err
	}
	if _, err := f.get(id); err != nil {
		return 0, err
	}
	if f.SnapshotDir != nil && f.SnapshotRoot != "" {
		return snapfs.Create(f.SnapshotDir(id), f.snapPath(id, snap))
	}
	if f.snaps == nil {
		f.snaps = map[string]int64{}
	}
	f.snaps[id+"/"+snap] = 1024
	return 1024, nil
}

func (f *Fake) RestoreSnapshot(_ context.Context, id, snap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("restore"); err != nil {
		return err
	}
	w, err := f.get(id)
	if err != nil {
		return err
	}
	if w.running {
		return errors.New("the workspace must be stopped before restoring a snapshot")
	}
	if f.SnapshotDir != nil && f.SnapshotRoot != "" {
		if _, err := os.Stat(f.snapPath(id, snap)); err != nil {
			return ErrNotFound
		}
		return snapfs.Restore(f.snapPath(id, snap), f.SnapshotDir(id))
	}
	if _, ok := f.snaps[id+"/"+snap]; !ok {
		return ErrNotFound
	}
	return nil
}

func (f *Fake) DeleteSnapshot(_ context.Context, id, snap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snaps, id+"/"+snap)
	if f.SnapshotRoot != "" {
		_ = os.Remove(f.snapPath(id, snap))
	}
	return nil
}

// Stats for the simulated runtime are generated, and say so (Simulated). The
// workspace's disk figure is real when its files live in a directory.
func simulated(seed string, limit int64) Stats {
	h := uint64(1469598103934665603)
	for _, c := range seed {
		h = (h ^ uint64(c)) * 1099511628211
	}
	// Drift slowly with time so a trend is visible without being random noise.
	t := uint64(time.Now().Unix() / 15)
	cpu := float64((h>>3)%700)/100 + float64((h+t*7)%300)/100
	mem := int64(float64(limit) * (0.15 + float64((h>>9)%250)/1000 + float64((h+t*3)%100)/1000))
	return Stats{CPUPercent: cpu, MemUsedBytes: mem, MemLimitBytes: limit, DiskUsedBytes: -1, Simulated: true}
}

func (f *Fake) WorkspaceStats(_ context.Context, id string) (Stats, error) {
	f.mu.Lock()
	w, err := f.get(id)
	dirFn := f.SnapshotDir
	f.mu.Unlock()
	if err != nil {
		return Stats{}, err
	}
	if !w.running {
		return Stats{}, ErrNotFound
	}
	s := simulated(id, int64(w.spec.Resources.RAMGB)<<30)
	if dirFn != nil {
		var total int64
		_ = filepath.Walk(dirFn(id), func(_ string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode().IsRegular() {
				total += fi.Size()
			}
			return nil
		})
		s.DiskUsedBytes = total
	}
	return s, nil
}

func (f *Fake) ServiceStats(_ context.Context, ws, name string) (Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.services[svcKey(ws, name)]
	if !ok || !s.running {
		return Stats{}, ErrNotFound
	}
	return simulated(ws+"/"+name, 256<<20), nil
}
