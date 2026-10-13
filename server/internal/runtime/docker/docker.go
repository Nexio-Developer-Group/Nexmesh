// Package docker implements runtime.Driver on top of the Docker Engine by
// driving the `docker` CLI. The CLI keeps the dependency surface small and the
// generated commands easy to audit and unit test through the Runner seam.
//
// Isolation (PRD 20.3.2): one container per workspace plus one container per
// service on a private per-workspace bridge network; no privileged containers;
// no Docker socket mounted anywhere; all capabilities dropped except a small
// allow-list; no-new-privileges; PID limit.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
)

// Runner executes docker CLI commands. Tests substitute a recording runner.
type Runner interface {
	Run(ctx context.Context, stdout io.Writer, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, stdout io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

type Config struct {
	MaxCPU       int
	MaxRAMGB     int
	MaxStorageGB int
	// StorageQuota enables --storage-opt size=..., which needs overlay2 on
	// XFS with project quotas (spike 1, PRD 20.6). Off by default.
	StorageQuota bool
	// HostGateway adds host.docker.internal to workspace containers so the
	// workspace agent can reach the control plane published on the host.
	HostGateway bool
	MemInfoPath string // default /proc/meminfo
	Runner      Runner // default: the docker CLI
}

type Driver struct {
	cfg Config
	r   Runner
}

func New(cfg Config) *Driver {
	if cfg.Runner == nil {
		cfg.Runner = execRunner{}
	}
	if cfg.MemInfoPath == "" {
		cfg.MemInfoPath = "/proc/meminfo"
	}
	return &Driver{cfg: cfg, r: cfg.Runner}
}

func netName(id string) string       { return "nexmesh-" + id }
func volName(id string) string       { return "nexmesh-" + id + "-home" }
func ctrName(id string) string       { return "nexmesh-" + id }
func svcCtrName(ws, n string) string { return "nexmesh-" + ws + "-" + n }
func svcVolName(ws, svc, v string) string {
	return "nexmesh-" + ws + "-" + svc + "-" + v
}

func (d *Driver) out(ctx context.Context, args ...string) (string, error) {
	var b bytes.Buffer
	err := d.r.Run(ctx, &b, args...)
	return strings.TrimSpace(b.String()), err
}

func (d *Driver) Capabilities(ctx context.Context) (runtime.Capabilities, error) {
	caps := runtime.Capabilities{MaxCPU: d.cfg.MaxCPU, MaxRAMGB: d.cfg.MaxRAMGB, MaxStorageGB: d.cfg.MaxStorageGB}
	if free, ok := readMemAvailableGB(d.cfg.MemInfoPath); ok {
		caps.FreeRAMGB = free
	}
	info, err := d.out(ctx, "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		return caps, fmt.Errorf("docker daemon unreachable: %w", err)
	}
	if strings.Contains(info, "nvidia") {
		caps.GPU = true
	} else {
		caps.GPUReason = "this execution backend has no GPU drivers installed."
	}
	return caps, nil
}

func readMemAvailableGB(path string) (float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, err := strconv.ParseFloat(f[1], 64)
				if err == nil {
					return kb / 1024 / 1024, true
				}
			}
		}
	}
	return 0, false
}

// hardening flags shared by every container Nexmesh creates.
func hardening() []string {
	return []string{
		"--security-opt", "no-new-privileges",
		"--cap-drop", "ALL",
		"--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER",
		"--cap-add", "SETGID", "--cap-add", "SETUID", "--cap-add", "KILL",
		"--cap-add", "NET_BIND_SERVICE",
		"--pids-limit", "4096",
	}
}

func resourceFlags(r runtime.Resources) []string {
	mem := fmt.Sprintf("%dg", r.RAMGB)
	f := []string{"--cpus", strconv.Itoa(r.CPU), "--memory", mem, "--memory-swap", mem}
	if r.GPU {
		f = append(f, "--gpus", "all")
	}
	return f
}

func (d *Driver) CreateWorkspace(ctx context.Context, spec runtime.WorkspaceSpec, progress func(step, detail string)) error {
	caps, err := d.Capabilities(ctx)
	if err != nil {
		return err
	}
	if err := runtime.ValidateResources(spec.Resources, caps); err != nil {
		return err
	}
	progress("preparing", "creating private network and volume")
	lbl := []string{"--label", "nexmesh.workspace=" + spec.ID, "--label", "nexmesh.managed=true"}
	if _, err := d.out(ctx, append([]string{"network", "create"}, append(lbl, netName(spec.ID))...)...); err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	if _, err := d.out(ctx, append([]string{"volume", "create"}, append(lbl, volName(spec.ID))...)...); err != nil {
		return err
	}
	progress("preparing", "pulling "+spec.Image)
	if _, err := d.out(ctx, "pull", "--quiet", spec.Image); err != nil {
		return err
	}
	if err := d.createContainer(ctx, spec); err != nil {
		return err
	}
	progress("starting", "starting workspace container")
	_, err = d.out(ctx, "start", ctrName(spec.ID))
	return err
}

// isStockImage reports whether the image is a plain distribution image (no
// Nexmesh agent entrypoint).
func isStockImage(image string) bool {
	return strings.HasPrefix(image, "ubuntu:") || image == "ubuntu"
}

func (d *Driver) createContainer(ctx context.Context, spec runtime.WorkspaceSpec) error {
	lbl := []string{"--label", "nexmesh.workspace=" + spec.ID, "--label", "nexmesh.managed=true"}
	args := []string{"create", "--name", ctrName(spec.ID), "--hostname", spec.Name, "--network", netName(spec.ID),
		"--network-alias", "workspace", "-v", volName(spec.ID) + ":/workspace", "-w", "/workspace"}
	args = append(args, lbl...)
	args = append(args, hardening()...)
	args = append(args, resourceFlags(spec.Resources)...)
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	if d.cfg.StorageQuota {
		args = append(args, "--storage-opt", fmt.Sprintf("size=%dG", spec.Resources.StorageGB))
	}
	if d.cfg.HostGateway {
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
	}
	args = append(args, spec.Image)
	if isStockImage(spec.Image) {
		// A stock Ubuntu image has no agent: keep the container alive for installs.
		args = append(args, "sleep", "infinity")
	}
	_, err := d.out(ctx, args...)
	return err
}

func (d *Driver) RecreateWorkspace(ctx context.Context, spec runtime.WorkspaceSpec, progress func(step, detail string)) error {
	caps, err := d.Capabilities(ctx)
	if err != nil {
		return err
	}
	if err := runtime.ValidateResources(spec.Resources, caps); err != nil {
		return err
	}
	progress("starting", "replacing the workspace container (volume and services are kept)")
	_, _ = d.out(ctx, "stop", "--time", "10", ctrName(spec.ID))
	if _, err := d.out(ctx, "rm", "-f", ctrName(spec.ID)); err != nil && !strings.Contains(err.Error(), "No such") {
		return err
	}
	if err := d.createContainer(ctx, spec); err != nil {
		return err
	}
	_, err = d.out(ctx, "start", ctrName(spec.ID))
	return err
}

func (d *Driver) StartWorkspace(ctx context.Context, id string) error {
	_, err := d.out(ctx, "start", ctrName(id))
	return err
}

func (d *Driver) StopWorkspace(ctx context.Context, id string) error {
	_, err := d.out(ctx, "stop", "--time", "10", ctrName(id))
	return err
}

func (d *Driver) RemoveWorkspace(ctx context.Context, id string) error {
	// Remove service sidecars first (label-selected), then the workspace.
	ids, _ := d.out(ctx, "ps", "-aq", "--filter", "label=nexmesh.workspace="+id)
	for _, c := range strings.Fields(ids) {
		_, _ = d.out(ctx, "rm", "-f", c)
	}
	vols, _ := d.out(ctx, "volume", "ls", "-q", "--filter", "label=nexmesh.workspace="+id)
	for _, v := range strings.Fields(vols) {
		_, _ = d.out(ctx, "volume", "rm", "-f", v)
	}
	_, _ = d.out(ctx, "network", "rm", netName(id))
	return nil
}

func (d *Driver) UpdateResources(ctx context.Context, id string, res runtime.Resources) error {
	caps, err := d.Capabilities(ctx)
	if err != nil {
		return err
	}
	if err := runtime.ValidateResources(res, caps); err != nil {
		return err
	}
	mem := fmt.Sprintf("%dg", res.RAMGB)
	_, err = d.out(ctx, "update", "--cpus", strconv.Itoa(res.CPU), "--memory", mem, "--memory-swap", mem, ctrName(id))
	return err
}

func (d *Driver) inspect(ctx context.Context, name string) (runtime.Observed, error) {
	out, err := d.out(ctx, "inspect", "--format", "{{.State.Running}}|{{.State.Status}}", name)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return runtime.Observed{}, nil
		}
		return runtime.Observed{}, err
	}
	parts := strings.SplitN(out, "|", 2)
	o := runtime.Observed{Exists: true, Running: parts[0] == "true"}
	if len(parts) == 2 {
		o.Detail = parts[1]
	}
	return o, nil
}

func (d *Driver) InspectWorkspace(ctx context.Context, id string) (runtime.Observed, error) {
	return d.inspect(ctx, ctrName(id))
}

func (d *Driver) Exec(ctx context.Context, id string, cmd []string, out io.Writer) error {
	return d.r.Run(ctx, out, append([]string{"exec", ctrName(id)}, cmd...)...)
}

func (d *Driver) InstallService(ctx context.Context, spec runtime.ServiceSpec, progress func(string)) error {
	name := svcCtrName(spec.WorkspaceID, spec.Name)
	progress("pulling " + spec.Image)
	if _, err := d.out(ctx, "pull", "--quiet", spec.Image); err != nil {
		return err
	}
	args := []string{"create", "--name", name, "--network", netName(spec.WorkspaceID), "--network-alias", spec.Name,
		"--label", "nexmesh.workspace=" + spec.WorkspaceID, "--label", "nexmesh.service=" + spec.Name,
		"--restart", "unless-stopped"}
	args = append(args, hardening()...)
	if spec.CPU > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(spec.CPU, 'f', -1, 64))
	}
	if spec.RAMMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", spec.RAMMB))
	}
	for _, v := range spec.Volumes {
		vn := svcVolName(spec.WorkspaceID, spec.Name, v.Name)
		if _, err := d.out(ctx, "volume", "create", "--label", "nexmesh.workspace="+spec.WorkspaceID, vn); err != nil {
			return err
		}
		args = append(args, "-v", vn+":"+v.Path)
	}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)
	progress("creating container " + name)
	_, err := d.out(ctx, args...)
	return err
}

func (d *Driver) StartService(ctx context.Context, ws, name string) error {
	if _, err := d.out(ctx, "start", svcCtrName(ws, name)); err != nil {
		if strings.Contains(err.Error(), "port is already allocated") || strings.Contains(err.Error(), "address already in use") {
			return runtime.ErrPortInUse
		}
		return err
	}
	return nil
}

func (d *Driver) StopService(ctx context.Context, ws, name string) error {
	_, err := d.out(ctx, "stop", "--time", "10", svcCtrName(ws, name))
	return err
}

func (d *Driver) RemoveService(ctx context.Context, ws, name string, deleteData bool) error {
	if _, err := d.out(ctx, "rm", "-f", svcCtrName(ws, name)); err != nil && !strings.Contains(err.Error(), "No such") {
		return err
	}
	if deleteData {
		vols, _ := d.out(ctx, "volume", "ls", "-q", "--filter", "name=^"+svcVolName(ws, name, ""))
		for _, v := range strings.Fields(vols) {
			_, _ = d.out(ctx, "volume", "rm", "-f", v)
		}
	}
	return nil
}

func (d *Driver) InspectService(ctx context.Context, ws, name string) (runtime.Observed, error) {
	return d.inspect(ctx, svcCtrName(ws, name))
}
