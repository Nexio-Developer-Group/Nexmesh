// Package wiring implements connection lines between cards (PRD F1): a
// consumer card (a backend) is wired to a provider card (a database, a queue,
// another backend) and Nexmesh derives the environment variables the consumer
// needs. Which pairs are valid comes from the catalog, never from code.
//
// A line stores only names. Values are resolved from the provider each time the
// environment is built, so secrets stay in the secrets store and a line never
// shows or copies them.
package wiring

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/connenv"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const Kind = "wire"

var ErrNotFound = store.ErrNotFound

func vErr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

type Service struct {
	st   store.Store
	cat  *catalog.Catalog
	comp *component.Service
	bus  *events.Bus
	aud  *audit.Recorder
}

func New(st store.Store, cat *catalog.Catalog, comp *component.Service, bus *events.Bus, aud *audit.Recorder) *Service {
	s := &Service{st: st, cat: cat, comp: comp, bus: bus, aud: aud}
	// A card that disappears takes its lines with it, and their variables.
	comp.OnRemoved(func(ctx context.Context, wsID, id string) { s.dropFor(ctx, wsID, id) })
	return s
}

// Input is a request to draw (or preview) a line.
type Input struct {
	FromID string `json:"fromId"`
	ToID   string `json:"toId"`
	Name   string `json:"name"`
	// Environment labels the line (PRD F4). Empty follows the provider.
	Environment string `json:"environment"`
	Confirm     bool   `json:"confirm"`
}

// Var is one variable a line creates. Values are never part of this type.
type Var struct {
	Name   string `json:"name"`
	Secret bool   `json:"secret"`
	// Overrides is set when a user variable or another line already uses the name.
	Conflict string `json:"conflict,omitempty"`
}

// Preview is what a line would do, shown before the user confirms (PRD F1).
type Preview struct {
	FromID    string   `json:"fromId"`
	FromName  string   `json:"fromName"`
	ToID      string   `json:"toId"`
	ToName    string   `json:"toName"`
	Name      string   `json:"name"`
	Variables []Var    `json:"variables"`
	State     string   `json:"state"`
	Warnings  []string `json:"warnings"`
}

// View is a stored line with its live state.
type View struct {
	*domain.Wire
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

func (s *Service) workspace(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil || w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

func (s *Service) wires(ctx context.Context, wsID string) ([]*domain.Wire, error) {
	return store.ListJSON[domain.Wire](ctx, s.st, Kind, wsID)
}

func (s *Service) card(ctx context.Context, wsID, id string) (*domain.Component, *catalog.Component, error) {
	c, err := s.st.GetComponent(ctx, id)
	if err != nil || c.WorkspaceID != wsID {
		return nil, nil, vErr("fromId", "card_not_found", "That card is not on this canvas.")
	}
	def, ok := s.cat.Get(c.CatalogID)
	if !ok {
		return nil, nil, vErr("fromId", "unknown_catalog", "That card's technology is not in the catalog.")
	}
	return c, def, nil
}

func label(c *domain.Component, def *catalog.Component) string {
	if c.Mode == domain.ModeConnected && c.ConnName != "" {
		return def.Name + " (" + c.ConnName + ")"
	}
	return def.Name
}

// CheckPair applies the catalog rules and explains a refusal.
func CheckPair(from, to *catalog.Component) error {
	if from.Wiring == nil || len(from.Wiring.Consumes) == 0 {
		return vErr("fromId", "cannot_consume", fmt.Sprintf("%s does not take connections from other cards.", from.Name))
	}
	if to.Wiring == nil || !to.Wiring.Provides {
		return vErr("toId", "cannot_provide", fmt.Sprintf("Other cards cannot connect to %s.", to.Name))
	}
	for _, want := range from.Wiring.Consumes {
		for _, have := range to.Categories {
			if want == have {
				return nil
			}
		}
	}
	return vErr("toId", "invalid_pair", fmt.Sprintf("%s connects to %s cards, and %s is %s.",
		from.Name, strings.Join(from.Wiring.Consumes, " or "), to.Name, strings.Join(to.Categories, "/")))
}

func isSecretName(n string) bool {
	return strings.HasSuffix(n, "_PASSWORD") || strings.HasSuffix(n, "_URI") || strings.HasSuffix(n, "_API_KEY") || strings.HasSuffix(n, "_TOKEN")
}

// Preview validates a line without storing it.
func (s *Service) Preview(ctx context.Context, owner, wsID string, in Input) (*Preview, error) {
	return s.plan(ctx, owner, wsID, in)
}

func (s *Service) plan(ctx context.Context, owner, wsID string, in Input) (*Preview, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	fromC, fromDef, err := s.card(ctx, wsID, in.FromID)
	if err != nil {
		return nil, err
	}
	toC, toDef, err := s.card(ctx, wsID, in.ToID)
	if err != nil {
		return nil, vErr("toId", "card_not_found", "That card is not on this canvas.")
	}
	if fromC.ID == toC.ID {
		return nil, vErr("toId", "self_connection", "A card cannot connect to itself.")
	}
	if err := CheckPair(fromDef, toDef); err != nil {
		return nil, err
	}
	existing, err := s.wires(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for _, w := range existing {
		if w.FromID == fromC.ID && w.ToID == toC.ID {
			return nil, vErr("toId", "duplicate", "These cards are already connected.")
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = component.WireDefaultName(toC)
	}
	name = strings.ToLower(name)
	if !connenv.ValidName(name) {
		return nil, vErr("name", "invalid_name", "Use lowercase letters, digits, hyphens or underscores, starting with a letter.")
	}
	if !validEnv(in.Environment) {
		return nil, vErr("environment", "invalid_environment", "Environment must be development, staging or production.")
	}
	names := component.WireNames(toDef, name)
	taken := map[string]string{}
	for _, w := range existing {
		if w.FromID != fromC.ID {
			continue
		}
		for _, v := range w.Variables {
			taken[v] = "the line to " + w.Name
		}
	}
	w, _ := s.st.GetWorkspace(ctx, wsID)
	for _, v := range w.Variables {
		taken[v.Name] = "a variable you defined"
	}
	pv := &Preview{FromID: fromC.ID, FromName: label(fromC, fromDef), ToID: toC.ID, ToName: label(toC, toDef), Name: name,
		State: component.WireState(toC), Warnings: []string{}}
	for _, n := range names {
		v := Var{Name: n, Secret: isSecretName(n) && toDef.Kind == "service"}
		v.Conflict = taken[n]
		pv.Variables = append(pv.Variables, v)
		if v.Conflict != "" {
			pv.Warnings = append(pv.Warnings, fmt.Sprintf("%s is already set by %s. The variable you defined wins; for another line choose a different name.", n, v.Conflict))
		}
	}
	for _, v := range pv.Variables {
		if strings.HasPrefix(v.Conflict, "the line to") {
			return nil, vErr("name", "name_in_use", fmt.Sprintf("%s would clash with %s. Give this connection a different name.", v.Name, v.Conflict))
		}
	}
	if pv.State == component.WirePending {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("%s is not installed or connected yet. The variables appear in the environment once it is.", toDef.Name))
	}
	if toC.Environment == "production" || in.Environment == "production" {
		pv.Warnings = append(pv.Warnings, "This line points at a production-labelled resource. Tools and agents treat it as read-only by default.")
	}
	return pv, nil
}

func validEnv(e string) bool {
	return e == "" || e == "development" || e == "staging" || e == "production"
}

// Create draws a line. Confirm must be true: the user has seen the preview.
func (s *Service) Create(ctx context.Context, actorType, actorID, owner, wsID string, in Input) (*View, error) {
	if !in.Confirm {
		return nil, vErr("confirm", "confirmation_required", "Review the variables and confirm before the line is created.")
	}
	pv, err := s.plan(ctx, owner, wsID, in)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(pv.Variables))
	for _, v := range pv.Variables {
		names = append(names, v.Name)
	}
	w := &domain.Wire{ID: store.NewID("wire"), WorkspaceID: wsID, FromID: in.FromID, ToID: in.ToID, Name: pv.Name,
		Environment: in.Environment, Variables: names, CreatedAt: time.Now().UTC()}
	if err := store.PutJSON(ctx, s.st, Kind, wsID, w.ID, w); err != nil {
		return nil, err
	}
	s.aud.Record(ctx, actorType, actorID, wsID, "wire.create", pv.FromName+" -> "+pv.ToName,
		map[string]any{"wireId": w.ID, "name": w.Name, "variables": names})
	s.changed(wsID)
	return s.view(ctx, w), nil
}

// Delete removes a line and, with it, the variables it generated.
func (s *Service) Delete(ctx context.Context, actorType, actorID, owner, wsID, id string) error {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return err
	}
	w, err := store.GetJSON[domain.Wire](ctx, s.st, Kind, id)
	if err != nil || w.WorkspaceID != wsID {
		return ErrNotFound
	}
	if err := s.st.DeleteDoc(ctx, Kind, id); err != nil {
		return err
	}
	s.aud.Record(ctx, actorType, actorID, wsID, "wire.delete", w.Name, map[string]any{"wireId": id, "variables": w.Variables})
	s.changed(wsID)
	return nil
}

func (s *Service) dropFor(ctx context.Context, wsID, cardID string) {
	list, err := s.wires(ctx, wsID)
	if err != nil {
		return
	}
	for _, w := range list {
		if w.FromID == cardID || w.ToID == cardID {
			_ = s.st.DeleteDoc(ctx, Kind, w.ID)
			s.aud.Record(ctx, domain.ActorSystem, "", wsID, "wire.delete", w.Name,
				map[string]any{"wireId": w.ID, "variables": w.Variables, "reason": "a connected card was removed"})
		}
	}
	s.changed(wsID)
}

func (s *Service) changed(wsID string) {
	s.bus.Publish(wsID, events.Event{Type: "wires_changed", Data: map[string]string{}})
}

// active reports whether a line applies in the workspace's active environment.
// Lines with no environment label always apply, and so does everything until
// someone chooses an active environment.
func active(ws *domain.Workspace, w *domain.Wire) bool {
	return !ws.UsesEnvironments() || w.Environment == "" || w.Environment == ws.ActiveEnv()
}

func (s *Service) view(ctx context.Context, w *domain.Wire) *View {
	v := &View{Wire: w, State: component.WirePending}
	if ws, err := s.st.GetWorkspace(ctx, w.WorkspaceID); err == nil && !active(ws, w) {
		v.State, v.Message = component.WireInactive, "Labelled "+w.Environment+"; the active environment is "+ws.ActiveEnv()+"."
		return v
	}
	to, err := s.st.GetComponent(ctx, w.ToID)
	if err != nil {
		v.State, v.Message = component.WireUnreachable, "The provider card no longer exists."
		return v
	}
	v.State = component.WireState(to)
	switch v.State {
	case component.WirePending:
		v.Message = "Waiting for the provider to be installed or connected."
	case component.WireUnreachable:
		v.Message = "The provider is " + strings.ReplaceAll(to.Observed, "_", " ") + "."
		if to.Message != "" {
			v.Message += " " + to.Message
		}
	case component.WireDegraded:
		v.Message = to.Message
	}
	return v
}

// List returns the workspace's lines with live state.
func (s *Service) List(ctx context.Context, owner, wsID string) ([]*View, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	list, err := s.wires(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]*View, 0, len(list))
	for _, w := range list {
		out = append(out, s.view(ctx, w))
	}
	return out, nil
}

// Env resolves the variables every line of the workspace provides, secret
// values included. It feeds the effective environment served to the agent.
func (s *Service) Env(ctx context.Context, wsID string) (map[string]string, error) {
	list, err := s.wires(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	ws, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for _, w := range list {
		if !active(ws, w) {
			continue
		}
		to, err := s.st.GetComponent(ctx, w.ToID)
		if err != nil {
			continue
		}
		for k, v := range s.comp.WireValues(ctx, to, w.Name) {
			out[k] = v
		}
	}
	return out, nil
}

// ExposedFor lists the variable names lines provide to one consumer card.
func (s *Service) ExposedFor(ctx context.Context, wsID, consumerID string) []string {
	list, err := s.wires(ctx, wsID)
	if err != nil {
		return nil
	}
	ws, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil
	}
	var out []string
	for _, w := range list {
		if w.FromID == consumerID && active(ws, w) {
			out = append(out, w.Variables...)
		}
	}
	sort.Strings(out)
	return out
}
