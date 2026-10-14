package component

import (
	"context"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// healthy reports whether a state counts as up for history and uptime.
func healthy(state string) bool {
	switch state {
	case domain.StateRunning, domain.StateConnected, domain.StateReady:
		return true
	}
	return false
}

// heartbeatEvery is how often an unchanged state is re-recorded, so a long
// healthy stretch shows as steady samples rather than silence.
const heartbeatEvery = 5 * time.Minute

// recordHealth appends a sample on every state change and as a periodic
// heartbeat while the state is unchanged. Failures are ignored: history must
// never break the action that triggered it.
func (s *Service) recordHealth(ctx context.Context, c *domain.Component, state string, latencyMS int64, msg string) {
	now := time.Now().UTC()
	if last, err := s.st.ListHealth(ctx, c.ID, 1); err == nil && len(last) == 1 && last[0].State == state && now.Sub(last[0].At) < heartbeatEvery {
		return
	}
	_ = s.st.AppendHealth(ctx, c.ID, &domain.HealthSample{At: now, State: state, Healthy: healthy(state), LatencyMS: latencyMS, Message: msg})
}

// HealthHistory is the API view of a component's recent health.
type HealthHistory struct {
	Samples []*domain.HealthSample `json:"samples"` // newest first
	// UptimePercent is the share of healthy samples in the window (0 with no data).
	UptimePercent float64 `json:"uptimePercent"`
	// Since is when the current state began.
	Since *time.Time `json:"since,omitempty"`
}

func (s *Service) Health(ctx context.Context, owner, wsID, id string, limit int) (*HealthHistory, error) {
	if _, err := s.component(ctx, owner, wsID, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > store.MaxHealthSamples {
		limit = 100
	}
	samples, err := s.st.ListHealth(ctx, id, limit)
	if err != nil {
		return nil, err
	}
	h := &HealthHistory{Samples: samples}
	up := 0
	for _, x := range samples {
		if x.Healthy {
			up++
		}
	}
	if len(samples) > 0 {
		h.UptimePercent = float64(up) * 100 / float64(len(samples))
		since := samples[0].At
		for _, x := range samples {
			if x.State != samples[0].State {
				break
			}
			since = x.At
		}
		h.Since = &since
	}
	return h, nil
}

// Logs returns the recent output of an installed service. Output is passed
// through the secrets scrubber so a credential printed by a service never
// reaches the browser.
func (s *Service) Logs(ctx context.Context, owner, wsID, id string, tail int) (string, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return "", err
	}
	def, ok := s.cat.Get(c.CatalogID)
	if !ok || c.Mode != domain.ModeInstalled || def.Install == nil || def.Install.Strategy != "image" {
		return "", ErrBadComponent
	}
	if s.drv == nil {
		return "", ErrBadComponent
	}
	out, err := s.drv.ServiceLogs(ctx, wsID, serviceName(def), tail)
	if err != nil {
		return "", err
	}
	if s.conn != nil && s.conn.Secrets != nil {
		out = s.conn.Secrets.Scrub(out)
	}
	if len(out) > 256<<10 {
		out = out[len(out)-(256<<10):]
	}
	return out, nil
}
