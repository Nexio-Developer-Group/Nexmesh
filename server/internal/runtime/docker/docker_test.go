package docker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
)

type rec struct {
	calls [][]string
	reply map[string]string // first arg -> stdout
	err   map[string]error
}

func (r *rec) Run(_ context.Context, out io.Writer, args ...string) error {
	r.calls = append(r.calls, args)
	if e, ok := r.err[args[0]]; ok {
		return e
	}
	if s, ok := r.reply[args[0]]; ok {
		_, _ = io.WriteString(out, s)
	}
	return nil
}

func (r *rec) find(prefix ...string) []string {
	for _, c := range r.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return c
		}
	}
	return nil
}

func newDriver(t *testing.T, r *rec) *Driver {
	mem := filepath.Join(t.TempDir(), "meminfo")
	_ = os.WriteFile(mem, []byte("MemTotal: 33554432 kB\nMemAvailable: 16777216 kB\n"), 0o644)
	return New(Config{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200, MemInfoPath: mem, Runner: r})
}

func TestCreateWorkspaceIsHardenedAndLimited(t *testing.T) {
	r := &rec{reply: map[string]string{"info": `{"runc":{}}`}}
	d := newDriver(t, r)
	var steps []string
	err := d.CreateWorkspace(context.Background(), runtime.WorkspaceSpec{ID: "ws1", Name: "orders", Image: "ubuntu:24.04",
		Resources: runtime.Resources{CPU: 4, RAMGB: 8, StorageGB: 40}}, func(s, _ string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	create := r.find("create")
	joined := " " + strings.Join(create, " ") + " "
	for _, want := range []string{" --cpus 4 ", " --memory 8g ", " --cap-drop ALL ", " no-new-privileges ", " --pids-limit 4096 ", " nexmesh-ws1-home:/workspace "} {
		if !strings.Contains(joined, want) {
			t.Errorf("create missing %q in %s", want, joined)
		}
	}
	for _, bad := range []string{"--privileged", "docker.sock", "--gpus"} {
		if strings.Contains(joined, bad) {
			t.Errorf("create must not contain %q", bad)
		}
	}
	if len(steps) < 2 || steps[len(steps)-1] != "starting" {
		t.Errorf("steps %v", steps)
	}
	if r.find("start", "nexmesh-ws1") == nil {
		t.Error("container not started")
	}
}

func TestGPURejectedWithoutNvidiaRuntime(t *testing.T) {
	r := &rec{reply: map[string]string{"info": `{"runc":{}}`}}
	err := newDriver(t, r).CreateWorkspace(context.Background(), runtime.WorkspaceSpec{ID: "w", Image: "ubuntu:24.04",
		Resources: runtime.Resources{CPU: 1, RAMGB: 1, StorageGB: 10, GPU: true}}, func(string, string) {})
	var ve *runtime.ValidationError
	if !errors.As(err, &ve) || ve.Code != "gpu_unavailable" {
		t.Fatalf("got %v", err)
	}
	r2 := &rec{reply: map[string]string{"info": `{"nvidia":{},"runc":{}}`}}
	if err := newDriver(t, r2).CreateWorkspace(context.Background(), runtime.WorkspaceSpec{ID: "w", Image: "ubuntu:24.04",
		Resources: runtime.Resources{CPU: 1, RAMGB: 1, StorageGB: 10, GPU: true}}, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	if c := r2.find("create"); !strings.Contains(strings.Join(c, " "), "--gpus all") {
		t.Error("gpu flag missing")
	}
}

func TestFreeRAMComesFromHost(t *testing.T) {
	r := &rec{reply: map[string]string{"info": `{"runc":{}}`}}
	caps, err := newDriver(t, r).Capabilities(context.Background())
	if err != nil || caps.FreeRAMGB != 16 {
		t.Fatalf("%+v %v", caps, err)
	}
}

func TestServiceSidecar(t *testing.T) {
	r := &rec{}
	d := newDriver(t, r)
	err := d.StartService(context.Background(), runtime.ServiceSpec{WorkspaceID: "ws1", Name: "mongodb", Image: "mongo:8.0.4",
		Env: map[string]string{"MONGO_INITDB_ROOT_USERNAME": "admin"}, RAMMB: 1024, CPU: 0.5,
		Volumes: []runtime.VolumeMount{{Name: "data", Path: "/data/db"}}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	run := strings.Join(r.find("run"), " ")
	for _, want := range []string{"--network nexmesh-ws1", "--network-alias mongodb", "nexmesh-ws1-mongodb-data:/data/db", "--memory 1024m", "--cpus 0.5", "--cap-drop ALL"} {
		if !strings.Contains(run, want) {
			t.Errorf("missing %q in %s", want, run)
		}
	}
	if strings.Contains(run, " -p ") || strings.Contains(run, "--publish") {
		t.Error("services must not publish host ports")
	}
	r.err = map[string]error{"run": errors.New("docker run: port is already allocated")}
	if err := d.StartService(context.Background(), runtime.ServiceSpec{WorkspaceID: "ws1", Name: "x", Image: "x:1"}, func(string) {}); !errors.Is(err, runtime.ErrPortInUse) {
		t.Fatalf("want ErrPortInUse got %v", err)
	}
}

func TestInspectMissing(t *testing.T) {
	r := &rec{err: map[string]error{"inspect": errors.New("Error: No such object")}}
	o, err := newDriver(t, r).InspectWorkspace(context.Background(), "gone")
	if err != nil || o.Exists {
		t.Fatalf("%+v %v", o, err)
	}
}
