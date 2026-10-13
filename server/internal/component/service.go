// Package component manages the cards on a workspace canvas: Draft cards
// dropped from the catalog, install lifecycle (Install mode) and, in
// connect.go, external connections (Connect mode).
package component

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

var (
	ErrUnknownCatalog = &runtime.ValidationError{Field: "catalogId", Code: "unknown_component", Message: "That technology is not in the catalog."}
	ErrWorkspaceState = errors.New("the workspace must be running to change its components")
	ErrBadComponent   = errors.New("this component is not in a state that allows that action")
)

type Service struct {
	st  store.Store
	cat *catalog.Catalog
	bus *events.Bus
	aud *audit.Recorder
	drv runtime.Driver // set by WithRuntime
	eng *ops.Engine
}

func NewService(st store.Store, cat *catalog.Catalog, bus *events.Bus, aud *audit.Recorder) *Service {
	return &Service{st: st, cat: cat, bus: bus, aud: aud}
}

func (s *Service) publish(c *domain.Component) {
	s.bus.Publish(c.WorkspaceID, events.Event{Type: "component", Data: c})
}

func (s *Service) publishWorkspace(w *domain.Workspace) {
	s.bus.Publish(w.ID, events.Event{Type: "workspace", Data: w})
}

func (s *Service) publishRemoved(wsID, id string) {
	s.bus.Publish(wsID, events.Event{Type: "component_removed", Data: map[string]string{"id": id}})
}

// workspace loads the workspace and enforces ownership.
func (s *Service) workspace(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

func (s *Service) component(ctx context.Context, owner, wsID, id string) (*domain.Component, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	c, err := s.st.GetComponent(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.WorkspaceID != wsID {
		return nil, store.ErrNotFound
	}
	return c, nil
}

func (s *Service) List(ctx context.Context, owner, wsID string) ([]*domain.Component, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	return s.st.ListComponents(ctx, wsID)
}

func (s *Service) Get(ctx context.Context, owner, wsID, id string) (*domain.Component, error) {
	return s.component(ctx, owner, wsID, id)
}

// Add drops a technology on the canvas as a Draft card. Nothing is installed
// or connected until the user confirms in the properties panel (PRD 10).
func (s *Service) Add(ctx context.Context, owner, wsID, catalogID string, x, y float64) (*domain.Component, error) {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	if w.Status != domain.WSReady {
		return nil, ErrWorkspaceState
	}
	def, ok := s.cat.Get(catalogID)
	if !ok {
		return nil, ErrUnknownCatalog
	}
	now := time.Now().UTC()
	c := &domain.Component{ID: store.NewID("cmp"), WorkspaceID: wsID, CatalogID: def.ID, Mode: domain.ModeUnset,
		Version: def.Versions.Default, Settings: map[string]any{}, Desired: domain.StateDraft, Observed: domain.StateDraft,
		X: x, Y: y, CreatedAt: now, UpdatedAt: now}
	if err := s.st.CreateComponent(ctx, c); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "component.add", def.ID, map[string]any{"componentId": c.ID})
	s.publish(c)
	return c, nil
}

type Patch struct {
	X        *float64       `json:"x"`
	Y        *float64       `json:"y"`
	Version  *string        `json:"version"`
	Settings map[string]any `json:"settings"`
}

// Update moves a card or changes desired install settings (Draft only for the latter).
func (s *Service) Update(ctx context.Context, owner, wsID, id string, p Patch) (*domain.Component, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	def, _ := s.cat.Get(c.CatalogID)
	if (p.Version != nil || p.Settings != nil) && c.Observed != domain.StateDraft {
		return nil, ErrBadComponent
	}
	if p.Version != nil && !def.HasVersion(*p.Version) {
		return nil, &runtime.ValidationError{Field: "version", Code: "unsupported_version",
			Message: fmt.Sprintf("%s %s is not a supported version.", def.Name, *p.Version)}
	}
	return s.st.UpdateComponent(ctx, id, func(c *domain.Component) error {
		if p.X != nil {
			c.X = *p.X
		}
		if p.Y != nil {
			c.Y = *p.Y
		}
		if p.Version != nil {
			c.Version = *p.Version
		}
		if p.Settings != nil {
			c.Settings = p.Settings
		}
		return nil
	})
}

// Remove deletes a Draft or Disconnected card. Installed or connected cards
// need the explicit remove / disconnect lifecycle actions.
func (s *Service) Remove(ctx context.Context, owner, wsID, id string) error {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return err
	}
	if c.Observed != domain.StateDraft && c.Observed != domain.StateDisconnect && c.Observed != domain.StateTestFailed && c.Observed != domain.StateTestPassed {
		return ErrBadComponent
	}
	if c.SecretRef != "" {
		_ = s.st.DeleteSecret(ctx, c.SecretRef)
	}
	if err := s.st.DeleteComponent(ctx, id); err != nil {
		return err
	}
	s.aud.User(ctx, owner, wsID, "component.remove", c.CatalogID, map[string]any{"componentId": id})
	s.bus.Publish(wsID, events.Event{Type: "component_removed", Data: map[string]string{"id": id}})
	return nil
}

// Summary describes a workspace's cards for the dashboard, e.g.
// "6 components · 2 connected · 1 failed".
func (s *Service) Summary(ctx context.Context, wsID string) string {
	list, err := s.st.ListComponents(ctx, wsID)
	if err != nil || len(list) == 0 {
		return ""
	}
	connected, failed := 0, 0
	for _, c := range list {
		if c.Mode == domain.ModeConnected {
			connected++
		}
		if c.Observed == domain.StateFailed || c.Observed == domain.StateTestFailed {
			failed++
		}
	}
	out := fmt.Sprintf("%d component", len(list))
	if len(list) != 1 {
		out += "s"
	}
	if connected > 0 {
		out += fmt.Sprintf(" · %d connected", connected)
	}
	if failed > 0 {
		out += fmt.Sprintf(" · %d failed", failed)
	}
	return out
}
