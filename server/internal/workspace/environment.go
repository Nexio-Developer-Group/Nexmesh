package workspace

import (
	"context"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
)

// WithOnEnvChange registers a hook run after the active environment changes,
// so connected cards can re-check against the newly active connection.
func (s *Service) WithOnEnvChange(fn func(ctx context.Context, workspaceID string)) *Service {
	s.onEnvChange = append(s.onEnvChange, fn)
	return s
}

// SetActiveEnvironment chooses which environment's values are injected into the
// workspace (PRD F4). Switching to production needs explicit confirmation
// (PRD 18.4 rule 7). The first call turns on strict per-environment matching.
func (s *Service) SetActiveEnvironment(ctx context.Context, owner, id, env string, confirm bool) (*domain.Workspace, error) {
	if !domain.ValidEnvironment(env) {
		return nil, &runtime.ValidationError{Field: "environment", Code: "invalid_environment", Message: "Environment must be development, staging or production."}
	}
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if env == domain.EnvProduction && !confirm && w.ActiveEnv() != env {
		return nil, &runtime.ValidationError{Field: "confirm", Code: "confirmation_required",
			Message: "Production values will be injected into this workspace. Confirm to switch."}
	}
	from := w.ActiveEnv()
	updated, err := s.st.UpdateWorkspace(ctx, id, func(x *domain.Workspace) error {
		x.ActiveEnvironment = env
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, id, "environment.switch", env, map[string]any{"from": from, "to": env})
	s.publish(updated)
	for _, fn := range s.onEnvChange {
		go fn(context.WithoutCancel(ctx), id)
	}
	return updated, nil
}
