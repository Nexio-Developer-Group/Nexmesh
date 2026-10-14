package definition

import (
	"context"
	"errors"
	"fmt"

	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

// InvalidError carries every problem found in a definition.
type InvalidError struct{ Issues []Issue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("The definition has %d problem(s); the first is %s: %s", len(e.Issues), e.Issues[0].Path, e.Issues[0].Message)
}

type ImportOptions struct {
	// Name overrides the workspace name (the original may already exist).
	Name string
	// AutoInstall installs components the definition marks as installed, after explicit opt-in.
	AutoInstall bool
	// DryRun validates and reports without creating anything.
	DryRun bool
	// PostCreate commands run once in the workspace after the definition is applied (explicit opt-in).
	PostCreate []string
}

type ImportResult struct {
	Workspace *domain.Workspace `json:"workspace,omitempty"`
	Todos     []Todo            `json:"todos"`
	DryRun    bool              `json:"dryRun"`
}

// Import validates the definition and creates a new workspace from it. The
// workspace is provisioned first; the cards, variables and ports are applied
// by a follow-up operation once it is ready, so the steps always run in order.
func (s *Service) Import(ctx context.Context, owner string, d *Definition, o ImportOptions) (*ImportResult, error) {
	if o.Name != "" {
		d.Workspace.Name = o.Name
	}
	if issues := Validate(d, s.cat); len(issues) > 0 {
		return nil, &InvalidError{issues}
	}
	res := &ImportResult{Todos: Todos(d, s.cat, o.AutoInstall), DryRun: o.DryRun}
	if res.Todos == nil {
		res.Todos = []Todo{}
	}
	if o.DryRun {
		// Resource limits and name availability are checked by Create, so a dry run asks the same questions.
		if ok, err := s.ws.NameAvailable(ctx, owner, d.Workspace.Name); err == nil && !ok {
			return nil, workspace.ErrNameTaken
		}
		return res, nil
	}
	w, err := s.ws.Create(ctx, owner, workspace.CreateInput{Name: d.Workspace.Name, Description: d.Workspace.Description,
		CPU: d.Workspace.CPU, RAMGB: d.Workspace.RAMGB, StorageGB: d.Workspace.StorageGB, GPU: d.Workspace.GPU})
	if err != nil {
		return nil, err
	}
	if d.Workspace.IdleStopMinutes > 0 {
		_, _ = s.st.UpdateWorkspace(ctx, w.ID, func(x *domain.Workspace) error { x.IdleStopMinutes = d.Workspace.IdleStopMinutes; return nil })
		w.IdleStopMinutes = d.Workspace.IdleStopMinutes
	}
	m, err := d.ToMap()
	if err != nil {
		return nil, err
	}
	cmds := make([]any, len(o.PostCreate))
	for i, c := range o.PostCreate {
		cmds[i] = c
	}
	if _, err := s.eng.EnqueueParams(ctx, w.ID, "", "apply_definition", map[string]any{"definition": m, "autoInstall": o.AutoInstall, "owner": owner, "postCreate": cmds}); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, w.ID, "definition.import", w.Name, map[string]any{"components": len(d.Components), "autoInstall": o.AutoInstall, "postCreateCommands": len(o.PostCreate)})
	res.Workspace = w
	return res, nil
}

func (s *Service) applyHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	raw, _ := op.Params["definition"].(map[string]any)
	owner, _ := op.Params["owner"].(string)
	auto, _ := op.Params["autoInstall"].(bool)
	d, err := FromMap(raw)
	if err != nil || owner == "" {
		return errors.New("the stored definition could not be read")
	}
	wsID := op.WorkspaceID
	if w, err := s.st.GetWorkspace(ctx, wsID); err != nil || w.Status != domain.WSReady {
		return errors.New("the workspace is not ready, so the definition was not applied")
	}
	ids := map[string]string{} // definition key -> component id
	var failures int
	for _, c := range d.Components {
		card, err := s.comps.Add(ctx, owner, wsID, c.Catalog, c.X, c.Y)
		if err != nil {
			p.Logf("could not add %s: %v", c.Catalog, err)
			failures++
			continue
		}
		ids[c.Key] = card.ID
		switch c.Mode {
		case "install", "connect":
			mode := c.Mode
			_, _ = s.comps.Suggest(ctx, card.ID, mode, c.Version)
		}
		if c.Mode == "connect" {
			_, _ = s.st.UpdateComponent(ctx, card.ID, func(x *domain.Component) error {
				x.ConnName, x.Environment, x.Connection = c.Name, c.Environment, c.Connection
				return nil
			})
		}
		if c.Mode == "install" && auto {
			_, err := s.comps.Install(ctx, owner, wsID, card.ID, component.InstallInput{Version: c.Version, Settings: c.Settings, Start: c.Desired != "stopped", Confirm: true})
			if err != nil {
				p.Logf("could not start installing %s: %v", c.Catalog, err)
				failures++
				continue
			}
		}
		p.Logf("added %s (%s)", c.Catalog, c.Mode)
	}
	for _, v := range d.Variables {
		if v.Secret {
			continue // re-supplied by the user
		}
		val := v.Value
		if _, err := s.vars.Set(ctx, owner, wsID, envvars.SetInput{Name: v.Name, ComponentID: ids[v.Component], Environment: v.Environment, Value: &val, Description: v.Description}); err != nil {
			p.Logf("could not set variable %s: %v", v.Name, err)
			failures++
		}
	}
	failures += s.applyPhase3(ctx, owner, wsID, d, ids, p.Logf)
	if len(d.Ports) > 0 {
		_, _ = s.st.UpdateWorkspace(ctx, wsID, func(x *domain.Workspace) error {
			for _, pd := range d.Ports {
				x.Ports = append(x.Ports, domain.DeclaredPort{Port: pd.Port, Label: pd.Label})
			}
			return nil
		})
	}
	p.Logf("definition applied")
	if cmds, _ := op.Params["postCreate"].([]any); len(cmds) > 0 {
		// Queued last, so the runtimes installed above are in place when the commands run.
		if _, err := s.eng.EnqueueParams(ctx, wsID, "", "post_create", map[string]any{"commands": cmds, "owner": owner}); err != nil {
			p.Logf("could not queue the post-create commands: %v", err)
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d item(s) of the definition could not be applied; see the log", failures)
	}
	return nil
}
