// Package workspace implements the workspace lifecycle: validated creation,
// asynchronous provisioning with real step states, start/stop, deletion and
// the desired-vs-observed reconciler (PRD Sections 6, 12, 13.1).
package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const BaseImage = "ubuntu:24.04"

var (
	ErrNameTaken   = errors.New("a workspace with this name already exists")
	ErrConfirmName = errors.New("confirmation name does not match the workspace name")
	ErrBadState    = errors.New("workspace is not in a state that allows this action")
	nameRe         = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,46}[a-z0-9]$`)
)

// AgentTokens issues registration tokens for workspace agents.
type AgentTokens interface {
	IssueToken(ctx context.Context, workspaceID string) (string, error)
}

type Service struct {
	usage       UsageProvider
	agents      AgentTokens
	controlURL  string
	st          store.Store
	drv         runtime.Driver
	ops         *ops.Engine
	bus         *events.Bus
	aud         *audit.Recorder
	log         *slog.Logger
	image       string
	onStart     []func(ctx context.Context, workspaceID string)
	onEnvChange []func(ctx context.Context, workspaceID string)
	cleanup     []func(ctx context.Context, w *domain.Workspace)
	idle        *idleState
	defaultIdle int
}

func NewService(st store.Store, drv runtime.Driver, eng *ops.Engine, bus *events.Bus, aud *audit.Recorder, log *slog.Logger) *Service {
	s := &Service{st: st, drv: drv, ops: eng, bus: bus, aud: aud, log: log, image: BaseImage}
	eng.Register("provision", s.provision)
	eng.Register("start_workspace", s.startWorkspace)
	eng.Register("stop_workspace", s.stopWorkspace)
	eng.Register("apply_settings", s.applySettingsHandler)
	s.registerSnapshotOps(eng)
	return s
}

type CreateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CPU         int    `json:"cpu"`
	RAMGB       int    `json:"ramGb"`
	StorageGB   int    `json:"storageGb"`
	GPU         bool   `json:"gpu"`
}

// ValidateName applies the naming rule used for container and network names.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return &runtime.ValidationError{Field: "name", Code: "invalid_name",
			Message: "Use 2 to 48 lowercase letters, digits or hyphens, starting and ending with a letter or digit."}
	}
	return nil
}

// WithImage sets the workspace base image (default Ubuntu 24.04).
// WithCleanup registers a hook run when a workspace is deleted, for features
// that keep per-workspace secrets outside the workspace document.
func (s *Service) WithCleanup(fn func(ctx context.Context, w *domain.Workspace)) *Service {
	s.cleanup = append(s.cleanup, fn)
	return s
}

// WithOnStart registers a hook run after a workspace becomes Ready again, so
// connected components can report their observed state on resume.
func (s *Service) WithOnStart(fn func(ctx context.Context, workspaceID string)) *Service {
	s.onStart = append(s.onStart, fn)
	return s
}

// WithDefaultIdle sets the idle-stop timeout new workspaces start with (0 = never).
func (s *Service) WithDefaultIdle(minutes int) *Service { s.defaultIdle = minutes; return s }

func (s *Service) WithImage(image string) *Service {
	if image != "" {
		s.image = image
	}
	return s
}

// WithAgent enables workspace-agent registration: each provisioned container
// receives the control-plane URL and a one-time token in its environment.
func (s *Service) WithAgent(t AgentTokens, controlURL string) *Service {
	s.agents, s.controlURL = t, controlURL
	return s
}

func (s *Service) publish(w *domain.Workspace) {
	s.bus.Publish(w.ID, events.Event{Type: "workspace", Data: w})
}

// NameAvailable reports whether the owner can use the name.
func (s *Service) NameAvailable(ctx context.Context, owner, name string) (bool, error) {
	list, err := s.st.ListWorkspaces(ctx, owner)
	if err != nil {
		return false, err
	}
	for _, w := range list {
		if w.Name == name {
			return false, nil
		}
	}
	return true, nil
}

// Create validates the request synchronously (so impossible combinations are
// rejected before anything exists), persists the desired configuration, then
// queues provisioning.
func (s *Service) Create(ctx context.Context, owner string, in CreateInput) (*domain.Workspace, error) {
	if err := ValidateName(in.Name); err != nil {
		return nil, err
	}
	caps, err := s.drv.Capabilities(ctx)
	if err != nil {
		return nil, fmt.Errorf("execution backend unavailable: %w", err)
	}
	res := runtime.Resources{CPU: in.CPU, RAMGB: in.RAMGB, StorageGB: in.StorageGB, GPU: in.GPU}
	if err := runtime.ValidateResources(res, caps); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	w := &domain.Workspace{ID: store.NewID("ws"), OwnerID: owner, Name: in.Name, Description: in.Description,
		OS: domain.OSUbuntu2404, CPU: in.CPU, RAMGB: in.RAMGB, StorageGB: in.StorageGB, GPU: in.GPU,
		Status: domain.WSAllocating, Step: domain.WSAllocating, CreatedAt: now, LastOpened: now,
		Runtimes: map[string]domain.RuntimeState{}, IdleStopMinutes: s.defaultIdle}
	if err := s.st.CreateWorkspace(ctx, w); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, ErrNameTaken
		}
		return nil, err
	}
	if _, err := s.ops.Enqueue(ctx, w.ID, "", "provision"); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, w.ID, "workspace.create", w.Name, map[string]any{
		"cpu": w.CPU, "ramGb": w.RAMGB, "storageGb": w.StorageGB, "gpu": w.GPU, "os": w.OS})
	s.publish(w)
	return w, nil
}

func (s *Service) setStep(ctx context.Context, id, status, step, errMsg string) {
	w, err := s.st.UpdateWorkspace(ctx, id, func(w *domain.Workspace) error {
		w.Status, w.Step, w.Error = status, step, errMsg
		return nil
	})
	if err == nil {
		s.publish(w)
	}
}

func (s *Service) provision(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	w, err := s.st.GetWorkspace(ctx, op.WorkspaceID)
	if err != nil {
		return err
	}
	spec := runtime.WorkspaceSpec{ID: w.ID, Name: w.Name, Image: s.image,
		Resources: runtime.Resources{CPU: w.CPU, RAMGB: w.RAMGB, StorageGB: w.StorageGB, GPU: w.GPU}}
	if s.agents != nil {
		tok, err := s.agents.IssueToken(ctx, w.ID)
		if err != nil {
			return err
		}
		spec.Env = map[string]string{"NEXMESH_CONTROL_URL": s.controlURL, "NEXMESH_WORKSPACE_ID": w.ID, "NEXMESH_AGENT_TOKEN": tok}
	}
	s.setStep(ctx, w.ID, domain.WSAllocating, domain.WSAllocating, "")
	p.Logf("allocating resources: %d vCPU, %d GB RAM, %d GB storage", w.CPU, w.RAMGB, w.StorageGB)
	p.Set(10)
	err = s.drv.CreateWorkspace(ctx, spec, func(step, detail string) {
		p.Logf("%s: %s", step, detail)
		s.setStep(ctx, w.ID, step, step, "")
		if step == domain.WSPreparing {
			p.Set(40)
		} else {
			p.Set(75)
		}
	})
	if err != nil {
		_ = s.drv.RemoveWorkspace(context.Background(), w.ID)
		msg := fmt.Sprintf("Provisioning failed: %v", err)
		s.setStep(context.Background(), w.ID, domain.WSFailed, w.Step, msg)
		s.aud.Record(ctx, domain.ActorSystem, "", w.ID, "workspace.provision_failed", w.Name, map[string]any{"error": err.Error()})
		return err
	}
	s.setStep(ctx, w.ID, domain.WSReady, domain.WSReady, "")
	p.Logf("workspace ready")
	return nil
}

func (s *Service) Get(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

func (s *Service) List(ctx context.Context, owner string) ([]*domain.Workspace, error) {
	return s.st.ListWorkspaces(ctx, owner)
}

// Touch records that the workspace was opened.
func (s *Service) Touch(ctx context.Context, owner, id string) {
	if _, err := s.Get(ctx, owner, id); err != nil {
		return
	}
	_, _ = s.st.UpdateWorkspace(ctx, id, func(w *domain.Workspace) error { w.LastOpened = time.Now().UTC(); return nil })
}

// Delete requires the caller to type the workspace name (PRD design prompt P2).
func (s *Service) Delete(ctx context.Context, owner, id, confirmName string) error {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return err
	}
	if confirmName != w.Name {
		return ErrConfirmName
	}
	for _, sn := range w.Snapshots { // before the container goes: the Docker driver needs its image
		_ = s.drv.DeleteSnapshot(ctx, id, sn.ID)
	}
	if err := s.drv.RemoveWorkspace(ctx, id); err != nil {
		return err
	}
	comps, _ := s.st.ListComponents(ctx, id)
	for _, c := range comps {
		if c.SecretRef != "" {
			_ = s.st.DeleteSecret(ctx, c.SecretRef)
		}
	}
	cfgs, _ := s.st.ListAgentConfigs(ctx, id)
	for _, c := range cfgs {
		if c.SecretRef != "" {
			_ = s.st.DeleteSecret(ctx, c.SecretRef)
		}
	}
	for _, fn := range s.cleanup {
		fn(ctx, w)
	}
	if err := s.st.DeleteWorkspace(ctx, id); err != nil {
		return err
	}
	s.aud.User(ctx, owner, id, "workspace.delete", w.Name, nil)
	return nil
}

func (s *Service) Start(ctx context.Context, owner, id string) (*domain.Operation, error) {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if w.Status != domain.WSStopped {
		return nil, ErrBadState
	}
	s.aud.User(ctx, owner, id, "workspace.start", w.Name, nil)
	return s.ops.Enqueue(ctx, id, "", "start_workspace")
}

func (s *Service) Stop(ctx context.Context, owner, id string) (*domain.Operation, error) {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if w.Status != domain.WSReady {
		return nil, ErrBadState
	}
	s.aud.User(ctx, owner, id, "workspace.stop", w.Name, nil)
	return s.ops.EnqueueParams(ctx, id, "", "stop_workspace", map[string]any{"reason": "user"})
}

func (s *Service) startWorkspace(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	s.setStep(ctx, op.WorkspaceID, domain.WSStarting, domain.WSStarting, "")
	if err := s.drv.StartWorkspace(ctx, op.WorkspaceID); err != nil {
		s.setStep(ctx, op.WorkspaceID, domain.WSFailed, domain.WSStarting, fmt.Sprintf("Start failed: %v", err))
		return err
	}
	_, _ = s.st.UpdateWorkspace(ctx, op.WorkspaceID, func(w *domain.Workspace) error { w.StopReason = ""; return nil })
	s.setStep(ctx, op.WorkspaceID, domain.WSReady, domain.WSReady, "")
	for _, fn := range s.onStart {
		fn(ctx, op.WorkspaceID)
	}
	return nil
}

func (s *Service) stopWorkspace(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	if err := s.drv.StopWorkspace(ctx, op.WorkspaceID); err != nil {
		return err
	}
	reason, _ := op.Params["reason"].(string)
	if reason == "" {
		reason = "user"
	}
	_, _ = s.st.UpdateWorkspace(ctx, op.WorkspaceID, func(w *domain.Workspace) error { w.StopReason = reason; return nil })
	s.setStep(ctx, op.WorkspaceID, domain.WSStopped, domain.WSStopped, "")
	return nil
}

// Reconcile compares desired state with what the backend reports and writes
// the observed result back (PRD 13.1). It never starts or stops anything.
func (s *Service) Reconcile(ctx context.Context) {
	list, err := s.st.ListWorkspaces(ctx, "")
	if err != nil {
		return
	}
	for _, w := range list {
		if w.Status != domain.WSReady && w.Status != domain.WSStopped {
			continue
		}
		obs, err := s.drv.InspectWorkspace(ctx, w.ID)
		if err != nil {
			continue
		}
		switch {
		case !obs.Exists:
			s.setStep(ctx, w.ID, domain.WSFailed, w.Step, "The workspace container no longer exists on the execution backend.")
		case obs.Running && w.Status == domain.WSStopped:
			s.setStep(ctx, w.ID, domain.WSReady, domain.WSReady, "")
		case !obs.Running && w.Status == domain.WSReady:
			s.setStep(ctx, w.ID, domain.WSStopped, domain.WSStopped, "")
		}
	}
}

// RunReconciler calls Reconcile on an interval until ctx ends.
func (s *Service) RunReconciler(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Reconcile(ctx)
		}
	}
}
