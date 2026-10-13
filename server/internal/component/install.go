package component

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// WithRuntime enables Install mode by providing the execution backend and the
// operations engine, and registers the install lifecycle handlers.
func (s *Service) WithRuntime(drv runtime.Driver, eng *ops.Engine) *Service {
	s.drv, s.eng = drv, eng
	eng.Register("install_component", s.installHandler)
	eng.Register("start_component", s.startHandler)
	eng.Register("stop_component", s.stopHandler)
	eng.Register("remove_component", s.removeHandler)
	eng.Register("install_runtime", s.runtimeHandler)
	return s
}

type InstallInput struct {
	Version  string         `json:"version"`
	Settings map[string]any `json:"settings"`
	Start    bool           `json:"start"`
	Confirm  bool           `json:"confirm"`
}

func vErr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

// Capacity is the workspace's resource budget and what installed components use.
type Capacity struct {
	CPU       float64 `json:"cpu"`
	RAMMB     int     `json:"ramMb"`
	StorageGB float64 `json:"storageGb"`
	UsedCPU   float64 `json:"usedCpu"`
	UsedRAMMB int     `json:"usedRamMb"`
	UsedStore float64 `json:"usedStorageGb"`
	FreeCPU   float64 `json:"freeCpu"`
	FreeRAMMB int     `json:"freeRamMb"`
	FreeStore float64 `json:"freeStorageGb"`
}

func (s *Service) capacity(ctx context.Context, w *domain.Workspace, exclude string) (Capacity, error) {
	comps, err := s.st.ListComponents(ctx, w.ID)
	if err != nil {
		return Capacity{}, err
	}
	c := Capacity{CPU: float64(w.CPU), RAMMB: w.RAMGB * 1024, StorageGB: float64(w.StorageGB)}
	for _, x := range comps {
		if x.ID == exclude || x.Mode != domain.ModeInstalled || x.Observed == domain.StateDraft {
			continue
		}
		if def, ok := s.cat.Get(x.CatalogID); ok && def.Resources != nil {
			c.UsedCPU += def.Resources.CPU
			c.UsedRAMMB += def.Resources.RAMMB
			c.UsedStore += def.Resources.StorageGB
		}
	}
	c.FreeCPU, c.FreeRAMMB, c.FreeStore = c.CPU-c.UsedCPU, c.RAMMB-c.UsedRAMMB, c.StorageGB-c.UsedStore
	return c, nil
}

func (s *Service) Capacity(ctx context.Context, owner, wsID string) (Capacity, error) {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return Capacity{}, err
	}
	return s.capacity(ctx, w, "")
}

func defaultPort(def *catalog.Component) int {
	switch {
	case len(def.Ports) > 0:
		return def.Ports[0]
	case def.Install != nil && len(def.Install.Ports) > 0:
		return def.Install.Ports[0]
	}
	return 0
}

func intSetting(m map[string]any, k string) (int, bool) {
	switch v := m[k].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// validateInstall runs the control-plane prerequisite checks (PRD 10 step 4):
// resource limits, port conflicts, dependencies, compatibility.
func (s *Service) validateInstall(ctx context.Context, w *domain.Workspace, c *domain.Component, def *catalog.Component, in InstallInput) error {
	if !in.Confirm {
		return vErr("confirm", "confirmation_required", "Confirm the install before anything is changed.")
	}
	if !def.SupportsMode(catalog.ModeInstall) {
		return vErr("mode", "mode_unsupported", def.Name+" cannot be installed.")
	}
	if !def.HasVersion(in.Version) {
		return vErr("version", "unsupported_version", fmt.Sprintf("%s %s is not a supported version.", def.Name, in.Version))
	}
	if def.Install != nil {
		for _, req := range def.Install.Requires {
			if w.Runtimes[req].Active == "" {
				reqDef, _ := s.cat.Get(req)
				name := req
				if reqDef != nil {
					name = reqDef.Name
				}
				return vErr("requires", "missing_dependency", fmt.Sprintf("%s requires %s. Install %s first.", def.Name, name, name))
			}
		}
	}
	comps, err := s.st.ListComponents(ctx, w.ID)
	if err != nil {
		return err
	}
	port, hasPort := intSetting(in.Settings, "port")
	if hasPort && (port < 1 || port > 65535) {
		return vErr("port", "invalid_port", "Port must be between 1 and 65535.")
	}
	for _, o := range comps {
		if o.ID == c.ID || o.Mode != domain.ModeInstalled || o.Observed == domain.StateDraft {
			continue
		}
		if o.CatalogID == def.ID {
			return vErr("catalogId", "already_installed", def.Name+" is already installed in this workspace.")
		}
		if op, ok := intSetting(o.Settings, "port"); hasPort && ok && op == port {
			od, _ := s.cat.Get(o.CatalogID)
			name := o.CatalogID
			if od != nil {
				name = od.Name
			}
			return vErr("port", "port_conflict", fmt.Sprintf("Port %d is already used by %s.", port, name))
		}
	}
	if def.Resources != nil {
		cp, err := s.capacity(ctx, w, c.ID)
		if err != nil {
			return err
		}
		switch {
		case def.Resources.RAMMB > cp.FreeRAMMB:
			return vErr("resources", "insufficient_resources", fmt.Sprintf("%s needs %s RAM; this workspace has %s free.", def.Name, fmtMB(def.Resources.RAMMB), fmtMB(cp.FreeRAMMB)))
		case def.Resources.CPU > cp.FreeCPU+1e-9:
			return vErr("resources", "insufficient_resources", fmt.Sprintf("%s needs %.2g vCPU; this workspace has %.2g free.", def.Name, def.Resources.CPU, cp.FreeCPU))
		case def.Resources.StorageGB > cp.FreeStore+1e-9:
			return vErr("resources", "insufficient_resources", fmt.Sprintf("%s needs %.3g GB storage; this workspace has %.3g free.", def.Name, def.Resources.StorageGB, cp.FreeStore))
		}
	}
	return nil
}

func fmtMB(mb int) string {
	if mb >= 1024 && mb%1024 == 0 {
		return fmt.Sprintf("%d GB", mb/1024)
	}
	return fmt.Sprintf("%.1f GB", float64(mb)/1024)
}

// Install persists the desired configuration, then queues the install. The
// card moves to Installing; the observed state is written back by the worker.
func (s *Service) Install(ctx context.Context, owner, wsID, id string, in InstallInput) (*domain.Operation, error) {
	if s.eng == nil {
		return nil, errors.New("install is not enabled")
	}
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	if w.Status != domain.WSReady {
		return nil, ErrWorkspaceState
	}
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	if c.Observed != domain.StateDraft && c.Observed != domain.StateFailed {
		return nil, ErrBadComponent
	}
	if c.Mode == domain.ModeConnected {
		return nil, ErrBadComponent
	}
	def, ok := s.cat.Get(c.CatalogID)
	if !ok {
		return nil, ErrUnknownCatalog
	}
	if in.Settings == nil {
		in.Settings = map[string]any{}
	}
	if _, ok := in.Settings["port"]; !ok && defaultPort(def) != 0 {
		in.Settings["port"] = defaultPort(def)
	}
	if err := s.validateInstall(ctx, w, c, def, in); err != nil {
		return nil, err
	}
	desired := domain.StateStopped
	if in.Start {
		desired = domain.StateRunning
	}
	c2, err := s.st.UpdateComponent(ctx, id, func(c *domain.Component) error {
		c.Mode, c.Version, c.Settings = domain.ModeInstalled, in.Version, in.Settings
		c.Desired, c.Observed, c.Progress, c.Message = desired, domain.StateInstalling, 0, "queued"
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish(c2)
	s.aud.User(ctx, owner, wsID, "component.install", def.ID, map[string]any{"componentId": id, "version": in.Version, "start": in.Start})
	return s.eng.Enqueue(ctx, wsID, id, "install_component")
}

// Retry re-runs a failed install with its stored settings.
func (s *Service) Retry(ctx context.Context, owner, wsID, id string) (*domain.Operation, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	if c.Observed != domain.StateFailed || c.Mode != domain.ModeInstalled {
		return nil, ErrBadComponent
	}
	return s.Install(ctx, owner, wsID, id, InstallInput{Version: c.Version, Settings: c.Settings, Start: c.Desired == domain.StateRunning, Confirm: true})
}

func (s *Service) setState(ctx context.Context, id, state, msg string, progress int) {
	if c, err := s.st.UpdateComponent(ctx, id, func(c *domain.Component) error {
		c.Observed, c.Message = state, msg
		if progress >= 0 {
			c.Progress = progress
		}
		return nil
	}); err == nil {
		s.publish(c)
	}
}

func (s *Service) fail(id, msg string, err error) error {
	s.setState(context.Background(), id, domain.StateFailed, msg, -1)
	return err
}

func serviceName(def *catalog.Component) string { return def.ID }

func (s *Service) serviceSpec(w *domain.Workspace, c *domain.Component, def *catalog.Component) runtime.ServiceSpec {
	spec := runtime.ServiceSpec{WorkspaceID: w.ID, Name: serviceName(def), Image: def.Install.Image + ":" + c.Version, Env: map[string]string{}}
	maps := def.Install.Env
	for k, v := range maps {
		spec.Env[k] = v
	}
	if p, ok := intSetting(c.Settings, "port"); ok {
		spec.Ports = []int{p}
	}
	for _, v := range def.Install.Volumes {
		size := v.SizeGB
		if g, ok := intSetting(c.Settings, "volumeGb"); ok && g > 0 {
			size = float64(g)
		}
		spec.Volumes = append(spec.Volumes, runtime.VolumeMount{Name: v.Name, Path: v.Path, SizeGB: size})
	}
	if def.Resources != nil {
		spec.CPU, spec.RAMMB = def.Resources.CPU, def.Resources.RAMMB
	}
	return spec
}

// lineWriter turns command output into operation log lines.
type lineWriter struct {
	p   ops.Progress
	buf bytes.Buffer
}

func (l *lineWriter) Write(b []byte) (int, error) {
	l.buf.Write(b)
	for {
		line, err := l.buf.ReadString('\n')
		if err != nil {
			l.buf.WriteString(line)
			break
		}
		if t := strings.TrimRight(line, "\r\n"); t != "" {
			l.p.Logf("%s", t)
		}
	}
	return len(b), nil
}

func (l *lineWriter) Flush() {
	if rest := strings.TrimSpace(l.buf.String()); rest != "" {
		l.p.Logf("%s", rest)
	}
	l.buf.Reset()
}

var _ io.Writer = (*lineWriter)(nil)

// installScript builds the in-workspace command for non-image strategies. All
// interpolated values come from the catalog or are validated against its
// supported-version list, never from free-form user input.
func installScript(def *catalog.Component, version string) (string, error) {
	in := def.Install
	switch in.Strategy {
	case "mise":
		return miseScript(in.Tool, version, true), nil
	case "apt":
		return "set -e; export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq " + strings.Join(in.Packages, " "), nil
	case "package":
		var pk []string
		for _, p := range in.Packages {
			pk = append(pk, strings.ReplaceAll(p, "{version}", version))
		}
		dir := "/workspace/.nexmesh/frameworks/" + def.ID
		switch in.Manager {
		case "npm":
			return fmt.Sprintf(`set -e; export PATH="$HOME/.local/share/mise/shims:$PATH"; mkdir -p %s && cd %s && npm install --no-audit --no-fund %s`, dir, dir, shellQuoteAll(pk)), nil
		case "pip":
			return fmt.Sprintf(`set -e; export PATH="$HOME/.local/share/mise/shims:$PATH"; mkdir -p %s && python -m venv %s/venv && %s/venv/bin/pip install %s`, dir, dir, dir, shellQuoteAll(pk)), nil
		}
	case "scaffold":
		if in.Command == "static-site" {
			return `set -e; mkdir -p /workspace/site && [ -f /workspace/site/index.html ] || printf '<!doctype html>\n<html lang="en">\n<head><meta charset="utf-8"><title>Hello</title><link rel="stylesheet" href="style.css"></head>\n<body><h1>Hello from Nexmesh</h1></body>\n</html>\n' > /workspace/site/index.html; [ -f /workspace/site/style.css ] || echo 'body{font-family:system-ui}' > /workspace/site/style.css`, nil
		}
	}
	return "", fmt.Errorf("unsupported install strategy %q for %s", in.Strategy, def.ID)
}

func shellQuoteAll(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// miseScript installs (and optionally activates) a runtime version through mise.
func miseScript(tool, version string, activate bool) string {
	s := fmt.Sprintf(`set -e
export PATH="$HOME/.local/bin:$PATH"
if ! command -v mise >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq && apt-get install -y -qq curl ca-certificates git >/dev/null
  curl -fsSL https://mise.run | sh
fi
mise install %s@%s
`, tool, version)
	if activate {
		s += fmt.Sprintf("mise use -g %s@%s\n", tool, version)
	}
	return s
}

func (s *Service) installHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	c, err := s.st.GetComponent(ctx, op.ComponentID)
	if err != nil {
		return err
	}
	w, err := s.st.GetWorkspace(ctx, c.WorkspaceID)
	if err != nil {
		return err
	}
	def, ok := s.cat.Get(c.CatalogID)
	if !ok {
		return s.fail(c.ID, "Unknown catalog entry.", ErrUnknownCatalog)
	}
	p.Logf("installing %s %s", def.Name, c.Version)
	p.Set(5)
	s.setState(ctx, c.ID, domain.StateInstalling, "preparing", 5)

	finalState := domain.StateReady
	switch def.Install.Strategy {
	case "image":
		spec := s.serviceSpec(w, c, def)
		if err := s.drv.InstallService(ctx, spec, func(d string) { p.Logf("%s", d); s.setState(ctx, c.ID, domain.StateInstalling, d, 40) }); err != nil {
			return s.fail(c.ID, "install failed: "+firstLine(err), err)
		}
		p.Set(70)
		finalState = domain.StateStopped
		if c.Desired == domain.StateRunning {
			s.setState(ctx, c.ID, domain.StateInstalling, "starting", 80)
			if err := s.drv.StartService(ctx, w.ID, serviceName(def)); err != nil {
				port, _ := intSetting(c.Settings, "port")
				msg := "start failed: " + firstLine(err)
				if errors.Is(err, runtime.ErrPortInUse) {
					msg = fmt.Sprintf("start failed: port %d already in use", port)
				}
				return s.fail(c.ID, msg, err)
			}
			finalState = domain.StateRunning
		}
	default:
		script, err := installScript(def, c.Version)
		if err != nil {
			return s.fail(c.ID, err.Error(), err)
		}
		lw := &lineWriter{p: p}
		s.setState(ctx, c.ID, domain.StateInstalling, "running installer", 30)
		err = s.drv.Exec(ctx, w.ID, []string{"bash", "-lc", script}, lw)
		lw.Flush()
		if err != nil {
			return s.fail(c.ID, "install failed: "+firstLine(err), err)
		}
		if def.Kind == "runtime" {
			s.recordRuntime(ctx, w.ID, def.ID, c.Version, true)
		}
	}
	s.setState(ctx, c.ID, finalState, "", 100)
	p.Logf("%s %s is %s", def.Name, c.Version, finalState)
	return nil
}

func firstLine(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		msg = msg[:i]
	}
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

func (s *Service) recordRuntime(ctx context.Context, wsID, id, version string, activate bool) {
	_, _ = s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		if w.Runtimes == nil {
			w.Runtimes = map[string]domain.RuntimeState{}
		}
		r := w.Runtimes[id]
		if !slices.Contains(r.Installed, version) {
			r.Installed = append(r.Installed, version)
		}
		if activate || r.Active == "" {
			r.Active = version
		}
		w.Runtimes[id] = r
		return nil
	})
	if w, err := s.st.GetWorkspace(ctx, wsID); err == nil {
		s.publishWorkspace(w)
	}
}

// Lifecycle actions on installed service components.

func (s *Service) Start(ctx context.Context, owner, wsID, id string) (*domain.Operation, error) {
	return s.lifecycle(ctx, owner, wsID, id, "start_component", domain.StateStopped, domain.StateRunning, "component.start")
}

func (s *Service) Stop(ctx context.Context, owner, wsID, id string) (*domain.Operation, error) {
	return s.lifecycle(ctx, owner, wsID, id, "stop_component", domain.StateRunning, domain.StateStopped, "component.stop")
}

func (s *Service) lifecycle(ctx context.Context, owner, wsID, id, opType, from, to, action string) (*domain.Operation, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	def, _ := s.cat.Get(c.CatalogID)
	if c.Mode != domain.ModeInstalled || c.Observed != from || def == nil || def.Install == nil || def.Install.Strategy != "image" {
		return nil, ErrBadComponent
	}
	_, _ = s.st.UpdateComponent(ctx, id, func(c *domain.Component) error { c.Desired = to; return nil })
	s.aud.User(ctx, owner, wsID, action, def.ID, map[string]any{"componentId": id})
	return s.eng.Enqueue(ctx, wsID, id, opType)
}

func (s *Service) startHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	c, err := s.st.GetComponent(ctx, op.ComponentID)
	if err != nil {
		return err
	}
	def, _ := s.cat.Get(c.CatalogID)
	if err := s.drv.StartService(ctx, c.WorkspaceID, serviceName(def)); err != nil {
		msg := "start failed: " + firstLine(err)
		if errors.Is(err, runtime.ErrPortInUse) {
			port, _ := intSetting(c.Settings, "port")
			msg = fmt.Sprintf("start failed: port %d already in use", port)
		}
		return s.fail(c.ID, msg, err)
	}
	s.setState(ctx, c.ID, domain.StateRunning, "", -1)
	return nil
}

func (s *Service) stopHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	c, err := s.st.GetComponent(ctx, op.ComponentID)
	if err != nil {
		return err
	}
	def, _ := s.cat.Get(c.CatalogID)
	if err := s.drv.StopService(ctx, c.WorkspaceID, serviceName(def)); err != nil {
		return s.fail(c.ID, "stop failed: "+firstLine(err), err)
	}
	s.setState(ctx, c.ID, domain.StateStopped, "", -1)
	return nil
}

// RemoveInstalled queues removal of an installed component. Service volumes
// are kept unless deleteData is set.
func (s *Service) RemoveInstalled(ctx context.Context, owner, wsID, id string, deleteData bool) (*domain.Operation, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	if c.Mode != domain.ModeInstalled || c.Observed == domain.StateInstalling {
		return nil, ErrBadComponent
	}
	s.aud.User(ctx, owner, wsID, "component.remove", c.CatalogID, map[string]any{"componentId": id, "deleteData": deleteData})
	return s.eng.EnqueueParams(ctx, wsID, id, "remove_component", map[string]any{"deleteData": deleteData})
}

func (s *Service) removeHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	c, err := s.st.GetComponent(ctx, op.ComponentID)
	if err != nil {
		return err
	}
	def, _ := s.cat.Get(c.CatalogID)
	if def != nil && def.Install != nil && def.Install.Strategy == "image" {
		del, _ := op.Params["deleteData"].(bool)
		if err := s.drv.RemoveService(ctx, c.WorkspaceID, serviceName(def), del); err != nil {
			return s.fail(c.ID, "remove failed: "+firstLine(err), err)
		}
		p.Logf("removed %s (data %s)", def.Name, map[bool]string{true: "deleted", false: "kept"}[del])
	} else {
		p.Logf("%s remains installed in the workspace filesystem; the card was removed from the canvas", def.Name)
	}
	if err := s.st.DeleteComponent(ctx, c.ID); err != nil {
		return err
	}
	s.publishRemoved(c.WorkspaceID, c.ID)
	return nil
}

// Global runtimes (PRD 9). Installing a runtime card and the Runtimes panel
// share this path, so both record the version in the workspace configuration.

type RuntimeInstall struct {
	Version  string `json:"version"`
	Activate bool   `json:"activate"`
}

func (s *Service) RuntimeNames() []string { return []string{"python", "nodejs", "go"} }

func (s *Service) Runtimes(ctx context.Context, owner, wsID string) (map[string]domain.RuntimeState, error) {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	out := map[string]domain.RuntimeState{}
	for _, n := range s.RuntimeNames() {
		out[n] = w.Runtimes[n]
	}
	return out, nil
}

func (s *Service) InstallRuntime(ctx context.Context, owner, wsID, name string, in RuntimeInstall) (*domain.Operation, error) {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	if w.Status != domain.WSReady {
		return nil, ErrWorkspaceState
	}
	def, ok := s.cat.Get(name)
	if !ok || def.Kind != "runtime" {
		return nil, vErr("runtime", "unknown_runtime", "Unknown runtime.")
	}
	if !def.HasVersion(in.Version) {
		return nil, vErr("version", "unsupported_version", fmt.Sprintf("%s %s is not a supported version.", def.Name, in.Version))
	}
	s.aud.User(ctx, owner, wsID, "runtime.install", name, map[string]any{"version": in.Version, "activate": in.Activate})
	return s.eng.EnqueueParams(ctx, wsID, "", "install_runtime", map[string]any{"name": name, "version": in.Version, "activate": in.Activate})
}

func (s *Service) runtimeHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	name, _ := op.Params["name"].(string)
	version, _ := op.Params["version"].(string)
	activate, _ := op.Params["activate"].(bool)
	def, ok := s.cat.Get(name)
	if !ok || def.Install == nil || !def.HasVersion(version) {
		return fmt.Errorf("invalid runtime request")
	}
	p.Logf("installing %s %s", def.Name, version)
	lw := &lineWriter{p: p}
	err := s.drv.Exec(ctx, op.WorkspaceID, []string{"bash", "-lc", miseScript(def.Install.Tool, version, activate)}, lw)
	lw.Flush()
	if err != nil {
		return err
	}
	s.recordRuntime(ctx, op.WorkspaceID, name, version, activate)
	return nil
}

var _ = store.ErrNotFound
