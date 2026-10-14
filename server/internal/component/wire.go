package component

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/connenv"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// Wire line states shown on the canvas (PRD F1: line status from observed state).
const (
	WireHealthy     = "healthy"
	WireDegraded    = "degraded"
	WireUnreachable = "unreachable"
	WirePending     = "pending"  // the provider is not installed or connected yet
	WireInactive    = "inactive" // labelled for an environment that is not the active one
)

// OnRemoved registers a hook run after a card is deleted, so features that
// reference cards (connection lines) can clean up.
func (s *Service) OnRemoved(fn func(ctx context.Context, workspaceID, componentID string)) {
	s.onRemoved = append(s.onRemoved, fn)
}

func (s *Service) fireRemoved(ctx context.Context, wsID, id string) {
	for _, fn := range s.onRemoved {
		fn(ctx, wsID, id)
	}
}

// WireDefaultName is the connection name a line to this card starts with.
func WireDefaultName(c *domain.Component) string {
	if c.Mode == domain.ModeConnected && c.ConnName != "" {
		return c.ConnName
	}
	return c.CatalogID
}

// WireNames lists the variables a line to this card creates (names only).
func WireNames(def *catalog.Component, name string) []string {
	if def.Kind == "service" {
		return connenv.Names(def.ID, name)
	}
	p := connenv.Prefix(name)
	return []string{p + "_URL", p + "_PORT"}
}

// WireState maps a provider card's observed state to a line state.
func WireState(c *domain.Component) string {
	switch c.Mode {
	case domain.ModeConnected:
		switch c.Observed {
		case domain.StateConnected:
			return WireHealthy
		case domain.StateDegraded:
			return WireDegraded
		case domain.StateTesting, domain.StateTestPassed, domain.StateDisconnect:
			return WirePending
		}
		return WireUnreachable
	case domain.ModeInstalled:
		switch c.Observed {
		case domain.StateRunning, domain.StateReady:
			return WireHealthy
		case domain.StateInstalling:
			return WirePending
		}
		return WireUnreachable
	}
	return WirePending
}

// WireValues resolves the variables a line to this card provides, secret
// values included. It is used only to build the environment served to the
// workspace agent, never returned to a browser.
func (s *Service) WireValues(ctx context.Context, c *domain.Component, name string) map[string]string {
	def, ok := s.cat.Get(c.CatalogID)
	if !ok || WireState(c) == WirePending {
		return nil
	}
	if def.Kind == "service" {
		if c.Mode == domain.ModeConnected {
			ac := s.activeConn(ctx, c)
			if ac == nil {
				return nil
			}
			return connenv.Build(def.ID, name, ac.Fields)
		}
		port := firstPort(def, c)
		host := serviceName(def)
		fields := map[string]any{"host": host, "port": port}
		if def.ID == "kafka" {
			fields = map[string]any{"bootstrapServers": net.JoinHostPort(host, strconv.Itoa(port))}
		}
		return connenv.Build(def.ID, name, fields)
	}
	// A backend framework: processes in the workspace reach it on loopback.
	port := firstPort(def, c)
	if port == 0 {
		return nil
	}
	p := connenv.Prefix(name)
	return map[string]string{p + "_URL": fmt.Sprintf("http://localhost:%d", port), p + "_PORT": strconv.Itoa(port)}
}

func firstPort(def *catalog.Component, c *domain.Component) int {
	if p, ok := intSetting(c.Settings, "port"); ok && p > 0 {
		return p
	}
	if def.Install != nil && len(def.Install.Ports) > 0 {
		return def.Install.Ports[0]
	}
	if len(def.Ports) > 0 {
		return def.Ports[0]
	}
	return 0
}
