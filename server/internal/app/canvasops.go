package app

import (
	"context"
	"regexp"
	"sort"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
)

// canvasOps lets the Code Agent's accepted proposals change the canvas: Draft
// cards and confirmed connection lines, as the user who accepted them.
type canvasOps struct {
	cat   *catalog.Catalog
	comps *component.Service
	wires *wiring.Service
}

func (c canvasOps) Catalog(id string) (string, bool) {
	d, ok := c.cat.Get(id)
	if !ok {
		return "", false
	}
	return d.Name, true
}

func (c canvasOps) AddDraft(ctx context.Context, owner, wsID, catalogID string, index int) (string, error) {
	comp, err := c.comps.Add(ctx, owner, wsID, catalogID, float64(80+260*(index%4)), float64(80+180*(index/4)))
	if err != nil {
		return "", err
	}
	return comp.ID, nil
}

func (c canvasOps) CheckWire(ctx context.Context, owner, wsID, fromID, toID string) error {
	_, err := c.wires.Preview(ctx, owner, wsID, wiring.Input{FromID: fromID, ToID: toID})
	return err
}

func (c canvasOps) AddWire(ctx context.Context, owner, wsID, fromID, toID string) (string, error) {
	v, err := c.wires.Create(ctx, domain.ActorUser, owner, owner, wsID, wiring.Input{FromID: fromID, ToID: toID, Confirm: true})
	if err != nil {
		return "", err
	}
	return v.ID, nil
}

// impactOf finds which variables and components a change set mentions, by the
// variable names the canvas and workspace define. It reads names only.
func impactOf(vars *envvars.Service, wires *wiring.Service, st store.Store, cat *catalog.Catalog, owner func(ctx context.Context, wsID string) string) changeset.ImpactFunc {
	return func(ctx context.Context, wsID string, texts []string) changeset.Impact {
		out := changeset.Impact{Components: []changeset.ImpactComponent{}, Variables: []string{}}
		l, err := vars.List(ctx, owner(ctx, wsID), wsID)
		if err != nil {
			return out
		}
		display := map[string]string{}
		if comps, err := st.ListComponents(ctx, wsID); err == nil {
			for _, c := range comps {
				if d, ok := cat.Get(c.CatalogID); ok {
					display[c.ID] = d.Name
				}
			}
		}
		// A variable belongs to the cards on both ends of the line that creates it.
		names := map[string][]changeset.ImpactComponent{}
		comp := func(id string) changeset.ImpactComponent {
			n := display[id]
			return changeset.ImpactComponent{ID: id, Name: n}
		}
		if ws, err := wires.List(ctx, owner(ctx, wsID), wsID); err == nil {
			for _, w := range ws {
				for _, v := range w.Variables {
					names[v] = append(names[v], comp(w.FromID), comp(w.ToID))
				}
			}
		}
		for _, g := range l.Generated {
			if _, ok := names[g.Name]; !ok {
				names[g.Name] = []changeset.ImpactComponent{comp(g.ComponentID)}
			}
		}
		for _, v := range l.Variables {
			if _, ok := names[v.Name]; !ok {
				names[v.Name] = nil
			}
		}
		seenVar, seenComp := map[string]bool{}, map[string]bool{}
		for name, owners := range names {
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
			hit := false
			for _, t := range texts {
				if re.MatchString(t) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			if !seenVar[name] {
				seenVar[name] = true
				out.Variables = append(out.Variables, name)
			}
			for _, c := range owners {
				if c.ID != "" && c.Name != "" && !seenComp[c.ID] {
					seenComp[c.ID] = true
					out.Components = append(out.Components, c)
				}
			}
		}
		sort.Strings(out.Variables)
		sort.Slice(out.Components, func(i, j int) bool { return out.Components[i].Name < out.Components[j].Name })
		return out
	}
}
