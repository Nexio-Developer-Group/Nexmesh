package workspace

import (
	"context"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
)

// Adopt re-registers workspaces recorded in a persistent store with a backend
// that lost them, which is what happens to the local (fake) driver on every
// restart. Their files are untouched; only the runtime record and the local
// agent are recreated. Stopped workspaces stay stopped. It returns the ids it
// adopted. Backends that keep their own state (Docker) report the workspace as
// existing, so nothing is touched.
func (s *Service) Adopt(ctx context.Context) []string {
	list, err := s.st.ListWorkspaces(ctx, "")
	if err != nil {
		return nil
	}
	var adopted []string
	for _, w := range list {
		if w.Status != domain.WSReady && w.Status != domain.WSStopped {
			continue
		}
		if obs, err := s.drv.InspectWorkspace(ctx, w.ID); err == nil && obs.Exists {
			continue
		}
		spec := runtime.WorkspaceSpec{ID: w.ID, Name: w.Name, Image: s.image,
			Resources: runtime.Resources{CPU: w.CPU, RAMGB: w.RAMGB, StorageGB: w.StorageGB, GPU: w.GPU}}
		if s.agents != nil {
			tok, err := s.agents.IssueToken(ctx, w.ID)
			if err != nil {
				continue
			}
			spec.Env = map[string]string{"NEXMESH_CONTROL_URL": s.controlURL, "NEXMESH_WORKSPACE_ID": w.ID, "NEXMESH_AGENT_TOKEN": tok}
		}
		if err := s.drv.CreateWorkspace(ctx, spec, func(string, string) {}); err != nil {
			s.log.Warn("could not adopt workspace", "workspace", w.ID, "err", err)
			continue
		}
		if w.Status == domain.WSStopped {
			_ = s.drv.StopWorkspace(ctx, w.ID)
		}
		adopted = append(adopted, w.ID)
	}
	return adopted
}
