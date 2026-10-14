// Package architecture derives the architecture and dependency view from the
// canvas (PRD F2). It is computed from the same cards and connection lines the
// canvas shows, on every request, so it can never disagree with them. Exports
// contain names, versions, modes and states, never connection details or
// secret values.
package architecture

import (
	"context"
	"sort"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
)

// Layers, in display order.
var Layers = []Layer{
	{ID: "frontend", Label: "Frontend"},
	{ID: "backend", Label: "Backend"},
	{ID: "data", Label: "Data"},
	{ID: "messaging", Label: "Messaging"},
	{ID: "infrastructure", Label: "Infrastructure"},
}

type Layer struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Node struct {
	ID          string `json:"id"`
	Catalog     string `json:"catalog"`
	Name        string `json:"name"`
	Layer       string `json:"layer"`
	Mode        string `json:"mode"` // installed | connected | draft
	Version     string `json:"version,omitempty"`
	State       string `json:"state"`
	Environment string `json:"environment,omitempty"`
	Highlight   bool   `json:"highlight,omitempty"`
}

type Edge struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	To          string `json:"to"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Environment string `json:"environment,omitempty"`
	Variables   int    `json:"variables"`
	Highlight   bool   `json:"highlight,omitempty"`
}

// Drift is something on the canvas that does not match what is running.
type Drift struct {
	Kind    string `json:"kind"` // unreachable | isolated | dangling
	NodeID  string `json:"nodeId,omitempty"`
	EdgeID  string `json:"edgeId,omitempty"`
	Message string `json:"message"`
}

type View struct {
	Workspace string  `json:"workspace"`
	Layers    []Layer `json:"layers"`
	Nodes     []Node  `json:"nodes"`
	Edges     []Edge  `json:"edges"`
	Drift     []Drift `json:"drift"`
}

type Service struct {
	st    store.Store
	cat   *catalog.Catalog
	wires *wiring.Service
}

func New(st store.Store, cat *catalog.Catalog, wires *wiring.Service) *Service {
	return &Service{st: st, cat: cat, wires: wires}
}

// LayerOf maps a catalog entry to a layer: by category first, then by kind.
func LayerOf(def *catalog.Component) string {
	for _, c := range def.Categories {
		switch c {
		case "frontend":
			return "frontend"
		case "backend":
			return "backend"
		case "database":
			return "data"
		case "messaging":
			return "messaging"
		case "hosting":
			return "infrastructure"
		}
	}
	switch def.Kind {
	case "runtime", "framework":
		return "backend"
	}
	return "infrastructure"
}

func nodeState(c *domain.Component) string { return c.Observed }

func mode(c *domain.Component) string {
	switch c.Mode {
	case domain.ModeInstalled:
		return "installed"
	case domain.ModeConnected:
		return "connected"
	}
	return "draft"
}

// Build computes the view. highlight is a set of card ids (and wire ids) to
// mark, used to show what a change set touches (PRD F2, F18).
func (s *Service) Build(ctx context.Context, owner, wsID string, highlight map[string]bool) (*View, error) {
	w, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil || w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	comps, err := s.st.ListComponents(ctx, wsID)
	if err != nil {
		return nil, err
	}
	wires, err := s.wires.List(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	v := &View{Workspace: w.Name, Layers: Layers, Nodes: []Node{}, Edges: []Edge{}, Drift: []Drift{}}
	byID := map[string]*Node{}
	defs := map[string]*catalog.Component{}
	for _, c := range comps {
		def, ok := s.cat.Get(c.CatalogID)
		if !ok {
			continue
		}
		n := Node{ID: c.ID, Catalog: c.CatalogID, Name: def.Name, Layer: LayerOf(def), Mode: mode(c), Version: c.Version,
			State: nodeState(c), Environment: c.Environment, Highlight: highlight[c.ID]}
		if c.Mode == domain.ModeConnected && c.ConnName != "" {
			n.Name = def.Name + " (" + c.ConnName + ")"
		}
		v.Nodes = append(v.Nodes, n)
		defs[c.ID] = def
	}
	sort.SliceStable(v.Nodes, func(i, j int) bool {
		a, b := v.Nodes[i], v.Nodes[j]
		if a.Layer != b.Layer {
			return layerRank(a.Layer) < layerRank(b.Layer)
		}
		return a.Name < b.Name
	})
	for i := range v.Nodes {
		byID[v.Nodes[i].ID] = &v.Nodes[i]
	}

	connected := map[string]bool{}
	for _, wv := range wires {
		from, to := byID[wv.FromID], byID[wv.ToID]
		if from == nil || to == nil {
			v.Drift = append(v.Drift, Drift{Kind: "dangling", EdgeID: wv.ID, Message: "The connection " + wv.Name + " refers to a card that is no longer on the canvas."})
			continue
		}
		connected[wv.FromID], connected[wv.ToID] = true, true
		v.Edges = append(v.Edges, Edge{ID: wv.ID, From: wv.FromID, To: wv.ToID, Name: wv.Name, State: wv.State, Environment: wv.Environment,
			Variables: len(wv.Variables), Highlight: highlight[wv.ID] || (highlight[wv.FromID] && highlight[wv.ToID])})
		if wv.State == component.WireUnreachable {
			v.Drift = append(v.Drift, Drift{Kind: "unreachable", EdgeID: wv.ID, NodeID: wv.ToID, Message: from.Name + " depends on " + to.Name + ", which is not reachable. " + wv.Message})
		}
	}
	for _, c := range comps {
		n := byID[c.ID]
		def := defs[c.ID]
		if n == nil {
			continue
		}
		switch c.Observed {
		case domain.StateFailed, domain.StateTestFailed, domain.StateDegraded:
			if c.Mode != "" {
				msg := n.Name + " is " + strings.ReplaceAll(c.Observed, "_", " ") + "."
				if c.Message != "" {
					msg += " " + c.Message
				}
				v.Drift = append(v.Drift, Drift{Kind: "unreachable", NodeID: c.ID, Message: msg})
			}
		}
		if !connected[c.ID] && def.Wiring != nil && (len(def.Wiring.Consumes) > 0 || def.Wiring.Provides) && c.Mode != "" {
			v.Drift = append(v.Drift, Drift{Kind: "isolated", NodeID: c.ID, Message: n.Name + " has no connections."})
		}
	}
	return v, nil
}

func layerRank(id string) int {
	for i, l := range Layers {
		if l.ID == id {
			return i
		}
	}
	return len(Layers)
}
