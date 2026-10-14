package definition

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// Difference is one way the running workspace departs from the definition.
type Difference struct {
	Category string `json:"category"` // workspace | runtime | component | variable | port | agent
	Subject  string `json:"subject"`
	Field    string `json:"field,omitempty"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	// Kind is what happened: changed | missing | extra | unhealthy
	Kind string `json:"kind"`
}

type Drift struct {
	InSync      bool         `json:"inSync"`
	Differences []Difference `json:"differences"`
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// Drift compares the definition (desired state) with the workspace as it is now (observed state).
// Every difference is listed; secret values are never compared or shown.
func (s *Service) Drift(ctx context.Context, owner, wsID string, d *Definition) (*Drift, error) {
	cur, err := s.Export(ctx, owner, wsID) // same capture rules as an export, so both sides are comparable
	if err != nil {
		return nil, err
	}
	rep := &Drift{Differences: []Difference{}}
	add := func(cat, subj, field, exp, act, kind string) {
		rep.Differences = append(rep.Differences, Difference{cat, subj, field, exp, act, kind})
	}
	cmp := func(cat, subj, field string, exp, act any) {
		if str(exp) != str(act) {
			add(cat, subj, field, str(exp), str(act), "changed")
		}
	}

	w, wd := cur.Workspace, d.Workspace
	cmp("workspace", "resources", "cpu", wd.CPU, w.CPU)
	cmp("workspace", "resources", "ramGb", wd.RAMGB, w.RAMGB)
	cmp("workspace", "resources", "storageGb", wd.StorageGB, w.StorageGB)
	cmp("workspace", "resources", "gpu", wd.GPU, w.GPU)
	cmp("workspace", "settings", "idleStopMinutes", wd.IdleStopMinutes, w.IdleStopMinutes)
	if wd.OS != "" {
		cmp("workspace", "os", "os", wd.OS, w.OS)
	}

	// Runtimes: versions the definition lists must be installed; the active one must match.
	rts := make([]string, 0, len(d.Runtimes))
	for id := range d.Runtimes {
		rts = append(rts, id)
	}
	sort.Strings(rts)
	for _, id := range rts {
		want, have := d.Runtimes[id], cur.Runtimes[id]
		for _, v := range want.Versions {
			if !contains(have.Versions, v) {
				add("runtime", id, "version "+v, "installed", "not installed", "missing")
			}
		}
		if want.Active != "" && want.Active != have.Active {
			add("runtime", id, "active version", want.Active, orNone(have.Active), "changed")
		}
	}

	// Components: match by catalog id, then connection name (connected) or position (others).
	live := map[string][]ComponentDef{}
	for _, c := range cur.Components {
		live[c.Catalog] = append(live[c.Catalog], c)
	}
	used := map[string]map[int]bool{}
	for _, want := range d.Components {
		subject := want.Catalog
		if want.Name != "" {
			subject = want.Name + " (" + want.Catalog + ")"
		}
		idx := -1
		cands := live[want.Catalog]
		if used[want.Catalog] == nil {
			used[want.Catalog] = map[int]bool{}
		}
		for i, c := range cands {
			if used[want.Catalog][i] {
				continue
			}
			if want.Mode == "connect" && want.Name != "" && c.Name != want.Name {
				continue
			}
			idx = i
			break
		}
		if idx < 0 {
			add("component", subject, "", want.Mode, "not on the canvas", "missing")
			continue
		}
		used[want.Catalog][idx] = true
		have := cands[idx]
		cmp("component", subject, "mode", want.Mode, have.Mode)
		if want.Mode == "install" && want.Version != "" {
			cmp("component", subject, "version", want.Version, have.Version)
		}
		if want.Mode == "install" && want.Desired != "" {
			cmp("component", subject, "desired state", want.Desired, have.Desired)
		}
		if want.Environment != "" {
			cmp("component", subject, "environment", want.Environment, have.Environment)
		}
		for k, v := range want.Settings {
			cmp("component", subject, "setting "+k, v, have.Settings[k])
		}
		for k, v := range want.Connection {
			cmp("component", subject, "connection "+k, v, have.Connection[k])
		}
	}
	for cat, cands := range live {
		for i, c := range cands {
			if used[cat][i] {
				continue
			}
			subj := cat
			if c.Name != "" {
				subj = c.Name + " (" + cat + ")"
			}
			add("component", subj, "", "not in the definition", c.Mode, "extra")
		}
	}
	// Desired state vs observed health of installed services.
	if cards, err := s.comps.List(ctx, owner, wsID); err == nil {
		for _, c := range cards {
			if c.Mode == domain.ModeInstalled && c.Desired == domain.StateRunning && c.Observed != domain.StateRunning && c.Observed != domain.StateReady {
				add("component", c.CatalogID, "state", "running", c.Observed, "unhealthy")
			}
			if c.Mode == domain.ModeConnected && (c.Observed == domain.StateDegraded || c.Observed == domain.StateDisconnect) {
				add("component", c.CatalogID, "state", "connected", c.Observed, "unhealthy")
			}
		}
	}

	// Variables by name and scope; values compared only for plain variables.
	type vk struct{ name, comp, env string }
	haveVars := map[vk]VariableDef{}
	for _, v := range cur.Variables {
		haveVars[vk{v.Name, v.Component, v.Environment}] = v
	}
	wantVars := map[vk]bool{}
	for _, v := range d.Variables {
		k := vk{v.Name, v.Component, v.Environment}
		wantVars[k] = true
		have, ok := haveVars[k]
		switch {
		case !ok:
			add("variable", v.Name, "", ifSecret(v.Secret, "secret"), "not set", "missing")
		case have.Secret != v.Secret:
			add("variable", v.Name, "secret", fmt.Sprint(v.Secret), fmt.Sprint(have.Secret), "changed")
		case !v.Secret && v.Value != have.Value:
			add("variable", v.Name, "value", v.Value, have.Value, "changed")
		}
	}
	for k, v := range haveVars {
		if !wantVars[k] {
			add("variable", v.Name, "", "not in the definition", ifSecret(v.Secret, "secret"), "extra")
		}
	}

	wantPorts := map[int]PortDef{}
	for _, p := range d.Ports {
		wantPorts[p.Port] = p
	}
	havePorts := map[int]PortDef{}
	for _, p := range cur.Ports {
		havePorts[p.Port] = p
	}
	for n, p := range wantPorts {
		if h, ok := havePorts[n]; !ok {
			add("port", fmt.Sprint(n), "", "declared", "not declared", "missing")
		} else if p.Label != h.Label {
			add("port", fmt.Sprint(n), "label", p.Label, h.Label, "changed")
		}
	}
	for n := range havePorts {
		if _, ok := wantPorts[n]; !ok {
			add("port", fmt.Sprint(n), "", "not in the definition", "declared", "extra")
		}
	}

	haveAgents := map[string]AgentDef{}
	for _, a := range cur.Agents {
		haveAgents[a.Name] = a
	}
	for _, a := range d.Agents {
		h, ok := haveAgents[a.Name]
		if !ok {
			add("agent", a.Name, "", a.Provider, "not configured", "missing")
			continue
		}
		cmp("agent", a.Name, "provider", a.Provider, h.Provider)
		cmp("agent", a.Name, "model", a.Model, h.Model)
		delete(haveAgents, a.Name)
	}
	for name := range haveAgents {
		add("agent", name, "", "not in the definition", "configured", "extra")
	}

	driftPhase3(d, cur, add, cmp)

	sort.SliceStable(rep.Differences, func(i, j int) bool {
		a, b := rep.Differences[i], rep.Differences[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.Field < b.Field
	})
	rep.InSync = len(rep.Differences) == 0
	s.aud.User(ctx, owner, wsID, "definition.drift_check", "", map[string]any{"differences": len(rep.Differences)})
	return rep, nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

func ifSecret(secret bool, label string) string {
	if secret {
		return label
	}
	return "set"
}
