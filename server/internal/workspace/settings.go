package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// UsageProvider reports how much of the workspace storage allowance installed
// components use, so a reduction below current usage can be rejected.
type UsageProvider interface {
	StorageUsedGB(ctx context.Context, workspaceID string) float64
}

// WithUsage enables the "cannot reduce below current usage" check.
func (s *Service) WithUsage(u UsageProvider) *Service { s.usage = u; return s }

// SettingsChange is any subset of the editable settings (PRD 12.2). The
// operating system is not editable and has no field here.
type SettingsChange struct {
	Name           *string `json:"name"`
	Description    *string `json:"description"`
	CPU            *int    `json:"cpu"`
	RAMGB          *int    `json:"ramGb"`
	StorageGB      *int    `json:"storageGb"`
	GPU            *bool   `json:"gpu"`
	ConfirmRestart bool    `json:"confirmRestart"`
}

type Change struct {
	Field              string `json:"field"`
	From               any    `json:"from"`
	To                 any    `json:"to"`
	AppliesImmediately bool   `json:"appliesImmediately"`
}

type Preview struct {
	Changes         []Change `json:"changes"`
	RestartRequired bool     `json:"restartRequired"`
}

var ErrOSLocked = &runtime.ValidationError{Field: "os", Code: "os_locked",
	Message: "The operating system cannot be changed. Create a new workspace to use a different one."}

func verr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

// plan validates a change against the current workspace and describes it.
func (s *Service) plan(ctx context.Context, w *domain.Workspace, in SettingsChange) (*Preview, runtime.Resources, error) {
	cur := runtime.Resources{CPU: w.CPU, RAMGB: w.RAMGB, StorageGB: w.StorageGB, GPU: w.GPU}
	next := cur
	pv := &Preview{Changes: []Change{}}
	if w.Status != domain.WSReady && w.Status != domain.WSStopped {
		return nil, cur, ErrBadState
	}
	if in.Name != nil && *in.Name != w.Name {
		if err := ValidateName(*in.Name); err != nil {
			return nil, cur, err
		}
		list, err := s.st.ListWorkspaces(ctx, w.OwnerID)
		if err != nil {
			return nil, cur, err
		}
		for _, o := range list {
			if o.ID != w.ID && o.Name == *in.Name {
				return nil, cur, ErrNameTaken
			}
		}
		pv.Changes = append(pv.Changes, Change{"name", w.Name, *in.Name, true})
	}
	if in.Description != nil && *in.Description != w.Description {
		pv.Changes = append(pv.Changes, Change{"description", w.Description, *in.Description, true})
	}
	if in.CPU != nil && *in.CPU != w.CPU {
		next.CPU = *in.CPU
		pv.Changes = append(pv.Changes, Change{"cpu", w.CPU, *in.CPU, true})
	}
	if in.RAMGB != nil && *in.RAMGB != w.RAMGB {
		next.RAMGB = *in.RAMGB
		pv.Changes = append(pv.Changes, Change{"ramGb", w.RAMGB, *in.RAMGB, true})
	}
	if in.StorageGB != nil && *in.StorageGB != w.StorageGB {
		next.StorageGB = *in.StorageGB
		pv.Changes = append(pv.Changes, Change{"storageGb", w.StorageGB, *in.StorageGB, false})
		pv.RestartRequired = true
	}
	if in.GPU != nil && *in.GPU != w.GPU {
		next.GPU = *in.GPU
		pv.Changes = append(pv.Changes, Change{"gpu", w.GPU, *in.GPU, false})
		pv.RestartRequired = true
	}
	resChanged := next != cur
	if resChanged {
		caps, err := s.drv.Capabilities(ctx)
		if err != nil {
			return nil, cur, fmt.Errorf("execution backend unavailable: %w", err)
		}
		// Host free RAM already excludes this workspace's own allocation; only growth needs headroom.
		if caps.FreeRAMGB > 0 {
			caps.FreeRAMGB += float64(w.RAMGB)
		}
		if err := runtime.ValidateResources(next, caps); err != nil {
			return nil, cur, err
		}
		if next.StorageGB < cur.StorageGB {
			if !caps.StorageShrink {
				return nil, cur, verr("storageGb", "storage_shrink_unsupported",
					fmt.Sprintf("This execution backend can only increase storage. Keep %d GB or more.", cur.StorageGB))
			}
			if s.usage != nil {
				if used := s.usage.StorageUsedGB(ctx, w.ID); float64(next.StorageGB) < used {
					return nil, cur, verr("storageGb", "storage_below_usage",
						fmt.Sprintf("Cannot reduce storage to %d GB: %.3g GB is in use. Remove data or choose at least %d GB.", next.StorageGB, used, int(used)+1))
				}
			}
		}
	}
	return pv, next, nil
}

// PreviewSettings shows what a change would do without applying it.
func (s *Service) PreviewSettings(ctx context.Context, owner, id string, in SettingsChange) (*Preview, error) {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	pv, _, err := s.plan(ctx, w, in)
	return pv, err
}

// ApplySettings validates, records the new desired configuration and applies
// it. CPU and RAM change live; GPU and storage need the workspace container
// replaced, which requires explicit confirmation (PRD 12.2).
func (s *Service) ApplySettings(ctx context.Context, owner, id string, in SettingsChange) (*domain.Workspace, error) {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	pv, next, err := s.plan(ctx, w, in)
	if err != nil {
		return nil, err
	}
	if len(pv.Changes) == 0 {
		return w, nil
	}
	if pv.RestartRequired && !in.ConfirmRestart {
		return nil, verr("confirmRestart", "restart_confirmation_required", "These changes need the workspace to restart. Confirm to continue.")
	}
	wasRunning := w.Status == domain.WSReady
	updated, err := s.st.UpdateWorkspace(ctx, id, func(x *domain.Workspace) error {
		if in.Name != nil {
			x.Name = *in.Name
		}
		if in.Description != nil {
			x.Description = *in.Description
		}
		x.CPU, x.RAMGB, x.StorageGB, x.GPU = next.CPU, next.RAMGB, next.StorageGB, next.GPU
		x.RestartRequired = pv.RestartRequired
		return nil
	})
	if err != nil {
		return nil, err
	}
	detail := map[string]any{}
	var names []string
	for _, c := range pv.Changes {
		detail[c.Field] = map[string]any{"from": c.From, "to": c.To}
		names = append(names, c.Field)
	}
	s.aud.User(ctx, owner, id, "workspace.settings", strings.Join(names, ","), detail)

	switch {
	case pv.RestartRequired:
		if _, err := s.ops.EnqueueParams(ctx, id, "", "apply_settings", map[string]any{"restore": map[bool]string{true: "ready", false: "stopped"}[wasRunning]}); err != nil {
			return nil, err
		}
	case next.CPU != w.CPU || next.RAMGB != w.RAMGB:
		if err := s.drv.UpdateResources(ctx, id, next); err != nil {
			// Roll the desired state back so it keeps matching what is applied.
			_, _ = s.st.UpdateWorkspace(ctx, id, func(x *domain.Workspace) error {
				x.CPU, x.RAMGB = w.CPU, w.RAMGB
				return nil
			})
			return nil, err
		}
	}
	s.publish(updated)
	return updated, nil
}

func (s *Service) applySettingsHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	w, err := s.st.GetWorkspace(ctx, op.WorkspaceID)
	if err != nil {
		return err
	}
	s.setStep(ctx, w.ID, domain.WSStarting, domain.WSStarting, "")
	spec := runtime.WorkspaceSpec{ID: w.ID, Name: w.Name, Image: s.image,
		Resources: runtime.Resources{CPU: w.CPU, RAMGB: w.RAMGB, StorageGB: w.StorageGB, GPU: w.GPU}}
	if s.agents != nil {
		if tok, err := s.agents.IssueToken(ctx, w.ID); err == nil {
			spec.Env = map[string]string{"NEXMESH_CONTROL_URL": s.controlURL, "NEXMESH_WORKSPACE_ID": w.ID, "NEXMESH_AGENT_TOKEN": tok}
		}
	}
	p.Logf("applying settings: %d vCPU, %d GB RAM, %d GB storage, gpu=%v", w.CPU, w.RAMGB, w.StorageGB, w.GPU)
	if err := s.drv.RecreateWorkspace(ctx, spec, func(step, detail string) { p.Logf("%s: %s", step, detail) }); err != nil {
		s.setStep(context.Background(), w.ID, domain.WSFailed, domain.WSStarting, fmt.Sprintf("Applying settings failed: %v", err))
		return err
	}
	final := domain.WSReady
	if op.Params["restore"] == "stopped" {
		_ = s.drv.StopWorkspace(ctx, w.ID)
		final = domain.WSStopped
	}
	updated, err := s.st.UpdateWorkspace(ctx, w.ID, func(x *domain.Workspace) error {
		x.Status, x.Step, x.Error, x.RestartRequired = final, final, "", false
		return nil
	})
	if err == nil {
		s.publish(updated)
	}
	return nil
}

var _ = store.ErrNotFound
