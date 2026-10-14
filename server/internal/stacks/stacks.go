// Package stacks implements stack templates (PRD F3): curated starting points
// and personal templates saved from a canvas. Applying a template only adds
// Draft cards; nothing installs or connects until the user confirms each one.
package stacks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const MaxPersonal = 20

// ValidationError is a user-correctable problem.
type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Msg }

var ErrNameTaken = &ValidationError{"name", "You already have a template with this name."}

type Service struct {
	st      store.Store
	cat     *catalog.Catalog
	curated []*catalog.Template
	comps   *component.Service
	aud     *audit.Recorder
}

func NewService(st store.Store, cat *catalog.Catalog, curated []*catalog.Template, comps *component.Service, aud *audit.Recorder) *Service {
	return &Service{st: st, cat: cat, curated: curated, comps: comps, aud: aud}
}

// Entry is one template in the gallery.
type Entry struct {
	ID          string                      `json:"id"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Personal    bool                        `json:"personal"`
	Components  []catalog.TemplateComponent `json:"components"`
}

type Gallery struct {
	Curated  []Entry `json:"curated"`
	Personal []Entry `json:"personal"`
}

func (s *Service) List(ctx context.Context, owner string) (*Gallery, error) {
	g := &Gallery{Curated: []Entry{}, Personal: []Entry{}}
	for _, t := range s.curated {
		g.Curated = append(g.Curated, Entry{ID: t.ID, Name: t.Name, Description: t.Description, Components: t.Components})
	}
	mine, err := s.st.ListTemplates(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, t := range mine {
		e := Entry{ID: t.ID, Name: t.Name, Description: t.Description, Personal: true}
		for _, c := range t.Components {
			e.Components = append(e.Components, catalog.TemplateComponent{ID: c.CatalogID, X: c.X, Y: c.Y, SuggestedMode: c.SuggestedMode, Version: c.Version})
		}
		g.Personal = append(g.Personal, e)
	}
	return g, nil
}

func (s *Service) find(ctx context.Context, owner, id string) (*Entry, error) {
	g, err := s.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, list := range [][]Entry{g.Curated, g.Personal} {
		for i := range list {
			if list[i].ID == id {
				return &list[i], nil
			}
		}
	}
	return nil, store.ErrNotFound
}

// ApplyResult says which cards were added and which were skipped.
type ApplyResult struct {
	Added   []*domain.Component `json:"added"`
	Skipped []string            `json:"skipped"` // catalog ids already on the canvas (single-instance technologies)
}

// Apply adds the template's cards as Drafts, placed below what is already on the canvas.
func (s *Service) Apply(ctx context.Context, owner, wsID, templateID string) (*ApplyResult, error) {
	t, err := s.find(ctx, owner, templateID)
	if err != nil {
		return nil, err
	}
	existing, err := s.comps.List(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	onCanvas := map[string]bool{}
	maxY := math.Inf(-1)
	for _, c := range existing {
		onCanvas[c.CatalogID] = true
		maxY = math.Max(maxY, c.Y)
	}
	minY := math.Inf(1)
	for _, tc := range t.Components {
		minY = math.Min(minY, tc.Y)
	}
	offset := 0.0
	if len(existing) > 0 {
		offset = maxY + 230 - minY // a new row beneath the existing cards
	}
	res := &ApplyResult{Added: []*domain.Component{}, Skipped: []string{}}
	for _, tc := range t.Components {
		def, ok := s.cat.Get(tc.ID)
		if !ok {
			continue // a personal template saved before a catalog change
		}
		if onCanvas[tc.ID] && def.Kind != "service" {
			res.Skipped = append(res.Skipped, tc.ID)
			continue
		}
		c, err := s.comps.Add(ctx, owner, wsID, tc.ID, tc.X, tc.Y+offset)
		if err != nil {
			return res, err
		}
		patched, err := s.comps.Suggest(ctx, c.ID, tc.SuggestedMode, tc.Version)
		if err == nil {
			c = patched
		}
		res.Added = append(res.Added, c)
	}
	s.aud.User(ctx, owner, wsID, "template.apply", t.ID, map[string]any{"added": len(res.Added), "skipped": len(res.Skipped), "personal": t.Personal})
	return res, nil
}

// SaveCanvas stores the workspace's cards as a personal template. Only the
// technologies, positions, modes and versions are kept: no names, settings,
// connection values or secrets.
func (s *Service) SaveCanvas(ctx context.Context, owner, wsID, name, description string) (*Entry, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len(name) > 60 {
		return nil, &ValidationError{"name", "Name the template (up to 60 characters)."}
	}
	if len(description) > 300 {
		return nil, &ValidationError{"description", "Description must be under 300 characters."}
	}
	cards, err := s.comps.List(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, &ValidationError{"name", "The canvas is empty. Add some components first."}
	}
	if len(cards) > 12 {
		return nil, &ValidationError{"name", "A template can hold at most 12 components."}
	}
	mine, err := s.st.ListTemplates(ctx, owner)
	if err != nil {
		return nil, err
	}
	if len(mine) >= MaxPersonal {
		return nil, &ValidationError{"name", fmt.Sprintf("You can keep %d personal templates. Delete one first.", MaxPersonal)}
	}
	for _, m := range mine {
		if strings.EqualFold(m.Name, name) {
			return nil, ErrNameTaken
		}
	}
	minX, minY := math.Inf(1), math.Inf(1)
	for _, c := range cards {
		minX, minY = math.Min(minX, c.X), math.Min(minY, c.Y)
	}
	t := &domain.Template{ID: store.NewID("tpl"), OwnerID: owner, Name: name, Description: description, CreatedAt: time.Now().UTC()}
	for _, c := range cards {
		tc := domain.TemplateComponent{CatalogID: c.CatalogID, X: c.X - minX + 40, Y: c.Y - minY + 40}
		switch c.Mode {
		case domain.ModeInstalled:
			tc.SuggestedMode, tc.Version = "install", c.Version
		case domain.ModeConnected:
			tc.SuggestedMode = "connect"
		default:
			tc.SuggestedMode = c.SuggestedMode
		}
		if def, ok := s.cat.Get(c.CatalogID); ok && tc.Version != "" && !def.HasVersion(tc.Version) {
			tc.Version = ""
		}
		t.Components = append(t.Components, tc)
	}
	if err := s.st.PutTemplate(ctx, t); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, ErrNameTaken
		}
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "template.save", t.ID, map[string]any{"components": len(t.Components)})
	g, _ := s.List(ctx, owner)
	for _, e := range g.Personal {
		if e.ID == t.ID {
			return &e, nil
		}
	}
	return nil, nil
}

// Delete removes one of the owner's personal templates.
func (s *Service) Delete(ctx context.Context, owner, id string) error {
	t, err := s.st.GetTemplate(ctx, id)
	if err != nil || t.OwnerID != owner {
		return store.ErrNotFound
	}
	return s.st.DeleteTemplate(ctx, id)
}
