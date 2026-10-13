package runtime

import (
	"context"
	"fmt"
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
}

type fakeWS struct {
	spec    WorkspaceSpec
	running bool
}

type fakeSvc struct {
	spec    ServiceSpec
	running bool
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
	return nil
}

func (f *Fake) RemoveWorkspace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ws, id)
	for k, s := range f.services {
		if s.spec.WorkspaceID == id {
			delete(f.services, k)
		}
	}
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
	f.services[svcKey(spec.WorkspaceID, spec.Name)] = &fakeSvc{spec: spec}
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
	return nil
}

func (f *Fake) RemoveService(_ context.Context, ws, name string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.services, svcKey(ws, name))
	return nil
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
