package component

import (
	"context"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// Adopt recreates installed service containers that the backend no longer
// knows about (the local driver forgets them on restart), then starts the ones
// that were running. It leaves cards in their recorded state, so the user sees
// the same canvas as before the restart.
func (s *Service) Adopt(ctx context.Context) {
	if s.drv == nil {
		return
	}
	list, err := s.st.ListWorkspaces(ctx, "")
	if err != nil {
		return
	}
	for _, w := range list {
		if w.Status != domain.WSReady {
			continue
		}
		comps, err := s.st.ListComponents(ctx, w.ID)
		if err != nil {
			continue
		}
		for _, c := range comps {
			def, ok := s.cat.Get(c.CatalogID)
			if !ok || c.Mode != domain.ModeInstalled || def.Install == nil || def.Install.Strategy != "image" {
				continue
			}
			if c.Observed != domain.StateRunning && c.Observed != domain.StateStopped {
				continue
			}
			if obs, err := s.drv.InspectService(ctx, w.ID, serviceName(def)); err == nil && obs.Exists {
				continue
			}
			if err := s.drv.InstallService(ctx, s.serviceSpec(w, c, def), func(string) {}); err != nil {
				continue
			}
			if c.Observed == domain.StateRunning {
				_ = s.drv.StartService(ctx, w.ID, serviceName(def))
			}
		}
	}
}
