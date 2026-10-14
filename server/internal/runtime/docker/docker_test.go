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
	err := d.InstallService(context.Background(), runtime.ServiceSpec{WorkspaceID: "ws1", Name: "mongodb", Image: "mongo:8.0.4",
		Env: map[string]string{"MONGO_INITDB_ROOT_USERNAME": "admin"}, RAMMB: 1024, CPU: 0.5,
		Volumes: []runtime.VolumeMount{{Name: "data", Path: "/data/db"}}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	run := strings.Join(r.find("create", "--name", "nexmesh-ws1-mongodb"), " ")
	for _, want := range []string{"--network nexmesh-ws1", "--network-alias mongodb", "nexmesh-ws1-mongodb-data:/data/db", "--memory 1024m", "--cpus 0.5", "--cap-drop ALL"} {
		if !strings.Contains(run, want) {
			t.Errorf("missing %q in %s", want, run)
		}
	}
	if strings.Contains(run, " -p ") || strings.Contains(run, "--publish") {
		t.Error("services must not publish host ports")
	}
	if r.find("start") != nil {
		t.Error("install must not start the service")
	}
	if err := d.StartService(context.Background(), "ws1", "mongodb"); err != nil {
		t.Fatal(err)
	}
	r.err = map[string]error{"start": errors.New("docker start: port is already allocated")}
	if err := d.StartService(context.Background(), "ws1", "mongodb"); !errors.Is(err, runtime.ErrPortInUse) {
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

func TestAgentImageRunsItsOwnEntrypointAndGetsHostGateway(t *testing.T) {
	r := &rec{reply: map[string]string{"info": `{"runc":{}}`}}
	d := newDriver(t, r)
	d.cfg.HostGateway = true
	spec := runtime.WorkspaceSpec{ID: "ws1", Name: "n", Image: "nexmesh/workspace:0.1", Resources: runtime.Resources{CPU: 1, RAMGB: 1, StorageGB: 10}}
	if err := d.CreateWorkspace(context.Background(), spec, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	create := strings.Join(r.find("create"), " ")
	if strings.Contains(create, "sleep infinity") {
		t.Errorf("agent images keep their entrypoint: %s", create)
	}
	if !strings.Contains(create, "--add-host host.docker.internal:host-gateway") {
		t.Errorf("control plane must be reachable from the workspace: %s", create)
	}
	r2 := &rec{reply: map[string]string{"info": `{"runc":{}}`}}
	spec.Image = "ubuntu:24.04"
	if err := newDriver(t, r2).CreateWorkspace(context.Background(), spec, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r2.find("create"), " "), "ubuntu:24.04 sleep infinity") {
		t.Error("stock Ubuntu needs a keep-alive command")
	}
}

func TestRecreateKeepsVolumeAndServices(t *testing.T) {
	r := &rec{reply: map[string]string{"info": `{"runc":{}}`}}
	d := newDriver(t, r)
	spec := runtime.WorkspaceSpec{ID: "ws1", Name: "n", Image: "ubuntu:24.04", Resources: runtime.Resources{CPU: 2, RAMGB: 4, StorageGB: 60}}
	if err := d.RecreateWorkspace(context.Background(), spec, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	if r.find("rm", "-f", "nexmesh-ws1") == nil || r.find("create") == nil || r.find("start", "nexmesh-ws1") == nil {
		t.Fatalf("calls: %v", r.calls)
	}
	for _, c := range r.calls {
		j := strings.Join(c, " ")
		if strings.HasPrefix(j, "volume rm") || strings.HasPrefix(j, "network rm") {
			t.Errorf("recreate must keep the volume and network: %s", j)
		}
	}
	if !strings.Contains(strings.Join(r.find("create"), " "), "--cpus 2") {
		t.Error("new limits not applied")
	}
}

func TestServiceCommandAndEnvArePassedThrough(t *testing.T) {
	r := &rec{}
	d := newDriver(t, r)
	err := d.InstallService(context.Background(), runtime.ServiceSpec{WorkspaceID: "ws1", Name: "redis", Image: "redis:7.4.1",
		Command: []string{"redis-server", "--protected-mode", "no"}, Env: map[string]string{"A": "b"}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	create := strings.Join(r.find("create", "--name", "nexmesh-ws1-redis"), " ")
	if !strings.HasSuffix(create, "redis:7.4.1 redis-server --protected-mode no") || !strings.Contains(create, "-e A=b") {
		t.Fatalf("%s", create)
	}
}

func TestSnapshotsRunInAHelperContainerWithoutNetwork(t *testing.T) {
	r := &rec{reply: map[string]string{"inspect": "ubuntu:24.04\n", "run": "1234\n"}}
	d := newDriver(t, r)
	size, err := d.SnapshotWorkspace(context.Background(), "ws_abc", "snap_1")
	if err != nil || size != 1234 {
		t.Fatalf("snapshot: %v %d", err, size)
	}
	run := r.find("run")
	joined := strings.Join(run, " ")
	for _, want := range []string{"--rm", "--network none", "--cap-drop ALL", "no-new-privileges", "nexmesh-ws_abc-home:/data:ro", "nexmesh-snapshots:/snap", "tar czf /snap/ws_abc/snap_1.tgz"} {
		if !strings.Contains(joined, want) {
			t.Errorf("helper missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, "/var/run/docker.sock") || strings.Contains(joined, "--privileged") {
		t.Fatal("helper must not get the socket or privileges")
	}

	// Identifiers are validated before they reach a shell script.
	for _, bad := range []string{"x; rm -rf /", "a b", "../x", ""} {
		if _, err := d.SnapshotWorkspace(context.Background(), "ws_abc", bad); err == nil {
			t.Errorf("snapshot id %q accepted", bad)
		}
		if err := d.DeleteSnapshot(context.Background(), "ws_abc", bad); err == nil {
			t.Errorf("delete id %q accepted", bad)
		}
	}
	if _, err := d.SnapshotWorkspace(context.Background(), "ws; id", "snap_1"); err == nil {
		t.Error("workspace id with shell metacharacters accepted")
	}
}

func TestStatsParsing(t *testing.T) {
	r := &rec{reply: map[string]string{
		"stats":   `{"CPUPerc":"12.50%","MemUsage":"10.5MiB / 256MiB"}`,
		"inspect": "/data\n/data/db\n",
		"exec":    "2048\t/data\n",
	}}
	d := New(Config{Runner: r})
	s, err := d.ServiceStats(context.Background(), "ws1", "redis")
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != 12.5 || s.MemUsedBytes != int64(10.5*(1<<20)) || s.MemLimitBytes != 256<<20 {
		t.Fatalf("%+v", s)
	}
	if s.DiskUsedBytes != 2*2048<<10 { // two mounts of 2048 KiB each
		t.Fatalf("disk %d", s.DiskUsedBytes)
	}
	if s.Simulated {
		t.Fatal("docker readings are real")
	}
	// The workspace's own files are measured under /workspace.
	if _, err := d.WorkspaceStats(context.Background(), "ws1"); err != nil {
		t.Fatal(err)
	}
	var sawDu bool
	for _, c := range r.calls {
		if c[0] == "exec" && c[len(c)-1] == "/workspace" {
			sawDu = true
		}
	}
	if !sawDu {
		t.Fatalf("expected du of /workspace in %v", r.calls)
	}
	// A missing container is ErrNotFound, and garbage output is an error, not a zero reading.
	r2 := &rec{err: map[string]error{"stats": errors.New("Error: No such container: x")}}
	if _, err := New(Config{Runner: r2}).ServiceStats(context.Background(), "ws1", "x"); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	r3 := &rec{reply: map[string]string{"stats": "not json"}}
	if _, err := New(Config{Runner: r3}).WorkspaceStats(context.Background(), "ws1"); err == nil {
		t.Fatal("unreadable stats output must be an error")
	}
	if parseSize("1.5GiB") != int64(1.5*(1<<30)) || parseSize("512kB") != 512000 || parseSize("0B") != 0 || parseSize("??") != 0 {
		t.Fatal("parseSize")
	}
}
