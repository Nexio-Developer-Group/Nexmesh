package wiring

import (
	"context"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
)

// Source adds the variables lines generate to a base environment source (the
// connected cards' own variables). Line variables win over the base, and user
// variables win over both (see envvars).
type Source struct {
	Base  envvars.Source
	Wires *Service
}

func (s Source) ConnectionEnv(ctx context.Context, wsID string) (map[string]string, error) {
	env, err := s.Base.ConnectionEnv(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if env == nil {
		env = map[string]string{}
	}
	wired, err := s.Wires.Env(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for k, v := range wired {
		env[k] = v
	}
	return env, nil
}

func (s Source) ExposedAs(c *domain.Component) []string {
	out := s.Base.ExposedAs(c)
	seen := map[string]bool{}
	for _, n := range out {
		seen[n] = true
	}
	for _, n := range s.Wires.ExposedFor(context.Background(), c.WorkspaceID, c.ID) {
		if !seen[n] {
			out = append(out, n)
		}
	}
	return out
}
