package definition

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/codeagent"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/gates"
	"github.com/nexio-developer-group/nexmesh/server/internal/rules"
	"github.com/nexio-developer-group/nexmesh/server/internal/secretscan"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
)

// P3 gives the definition access to the Phase 3 features it carries.
type P3 struct {
	Wires interface {
		List(ctx context.Context, owner, wsID string) ([]*wiring.View, error)
		Create(ctx context.Context, actorType, actorID, owner, wsID string, in wiring.Input) (*wiring.View, error)
	}
	Rules interface {
		List(ctx context.Context, owner, wsID string) ([]*rules.Rule, error)
		Save(ctx context.Context, owner, wsID string, in rules.SaveInput) (*rules.Rule, error)
	}
	Gates interface {
		GetConfig(ctx context.Context, owner, wsID string) (*gates.Config, error)
		SaveConfig(ctx context.Context, owner, wsID string, in gates.Config) (*gates.Config, error)
	}
	Agent interface {
		GetSettings(ctx context.Context, owner, wsID string) (*codeagent.Settings, error)
		SetSettings(ctx context.Context, owner, wsID string, in codeagent.Settings) (*codeagent.Settings, error)
	}
}

// WithPhase3 enables wires, environments, gates, rules and the agent policy.
func (s *Service) WithPhase3(p P3) *Service { s.p3 = &p; return s }

var envLabels = map[string]bool{"development": true, "staging": true, "production": true}

// exportPhase3 adds the Phase 3 sections. keys maps component ids to definition keys.
func (s *Service) exportPhase3(ctx context.Context, owner string, w *domain.Workspace, cards []*domain.Component, keys map[string]string, d *Definition) {
	if w.ActiveEnvironment != "" {
		d.Environments = &EnvironmentsDef{Active: w.ActiveEnvironment}
	}
	for i := range d.Components {
		// Components keep export order, so match profiles by key.
		_ = i
	}
	byKey := map[string]*domain.Component{}
	for _, c := range cards {
		byKey[keys[c.ID]] = c
	}
	for i := range d.Components {
		c := byKey[d.Components[i].Key]
		if c == nil || len(c.Profiles) == 0 {
			continue
		}
		def, _ := s.cat.Get(c.CatalogID)
		secretNames := map[string]bool{}
		var secretList []string
		if def != nil {
			for _, n := range def.SecretFields() {
				secretNames[n] = true
			}
			secretList = def.SecretFields()
		}
		d.Components[i].Profiles = map[string]ProfileDef{}
		for env, p := range c.Profiles {
			d.Components[i].Profiles[env] = ProfileDef{Connection: publicMap(p.Connection, func(k string) bool { return secretNames[k] }), SecretFields: secretList}
		}
	}
	if s.p3 == nil {
		return
	}
	if s.p3.Wires != nil {
		if ws, err := s.p3.Wires.List(ctx, owner, w.ID); err == nil {
			for _, x := range ws {
				from, to := keys[x.FromID], keys[x.ToID]
				if from == "" || to == "" {
					continue
				}
				d.Wires = append(d.Wires, WireDef{From: from, To: to, Name: x.Name, Environment: x.Environment})
			}
			sort.SliceStable(d.Wires, func(i, j int) bool {
				a, b := d.Wires[i], d.Wires[j]
				if a.From != b.From {
					return a.From < b.From
				}
				if a.To != b.To {
					return a.To < b.To
				}
				return a.Name < b.Name
			})
		}
	}
	if s.p3.Gates != nil {
		if cfg, err := s.p3.Gates.GetConfig(ctx, owner, w.ID); err == nil {
			for _, g := range cfg.Gates {
				d.Gates = append(d.Gates, GateDef{ID: g.ID, Name: g.Name, Kind: g.Kind, Command: g.Command, Policy: g.Policy, Enabled: g.Enabled, TimeoutSec: g.TimeoutSec})
			}
		}
	}
	if s.p3.Agent != nil {
		if st, err := s.p3.Agent.GetSettings(ctx, owner, w.ID); err == nil {
			d.AgentPolicy = &AgentPolicyDef{Level: st.Level}
		}
	}
	if s.p3.Rules != nil {
		// Rules are files in the workspace, so they can only be read while it runs.
		if s.agent != nil && s.agent.Online(w.ID) {
			if rs, err := s.p3.Rules.List(ctx, owner, w.ID); err == nil {
				for _, r := range rs {
					if r.ReadOnly || r.Problem != "" {
						continue
					}
					d.Rules = append(d.Rules, RuleDef{Name: r.Name, Title: r.Title, Scope: r.Scope, Components: r.Components, Paths: r.Paths, Protect: r.Protect, Body: r.Body})
				}
			}
		} else {
			d.Notes = append(d.Notes, "Agent rules were not included because the workspace is not running. Start it and export again to include them.")
		}
	}
}

var gateIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
var ruleNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)

// validatePhase3 checks the Phase 3 sections.
func validatePhase3(d *Definition, keys map[string]bool, add func(path, format string, a ...any)) {
	if d.Environments != nil && !envLabels[d.Environments.Active] {
		add("environments.active", "active must be development, staging or production.")
	}
	for i, c := range d.Components {
		for env, p := range c.Profiles {
			path := fmt.Sprintf("components[%d].profiles.%s", i, env)
			if !envLabels[env] {
				add(path, "environment must be development, staging or production.")
			}
			if c.Mode != "connect" {
				add(path, "only connected cards have environment connections.")
			}
			for k := range p.Connection {
				if sensitiveKey(k) {
					add(path+".connection."+k, "this file contains a value for the credential %q. Remove it; credentials are re-entered after import.", k)
				}
			}
		}
	}
	for i, v := range d.Variables {
		if v.Environment != "" && !envLabels[v.Environment] {
			add(fmt.Sprintf("variables[%d].environment", i), "environment must be development, staging or production.")
		}
	}
	seenWire := map[string]bool{}
	for i, wd := range d.Wires {
		p := fmt.Sprintf("wires[%d]", i)
		if !keys[wd.From] {
			add(p+".from", "unknown component key %q.", wd.From)
		}
		if !keys[wd.To] {
			add(p+".to", "unknown component key %q.", wd.To)
		}
		if wd.From == wd.To {
			add(p, "a card cannot connect to itself.")
		}
		if wd.Environment != "" && !envLabels[wd.Environment] {
			add(p+".environment", "environment must be development, staging or production.")
		}
		id := wd.From + ">" + wd.To + ":" + wd.Name
		if seenWire[id] {
			add(p, "duplicate connection %s to %s.", wd.From, wd.To)
		}
		seenWire[id] = true
	}
	seenGate := map[string]bool{}
	for i, g := range d.Gates {
		p := fmt.Sprintf("gates[%d]", i)
		if !gateIDRe.MatchString(g.ID) || seenGate[g.ID] {
			add(p+".id", "each gate needs a unique id of lowercase letters, digits and dashes.")
		}
		seenGate[g.ID] = true
		if strings.TrimSpace(g.Name) == "" || strings.TrimSpace(g.Command) == "" {
			add(p, "a gate needs a name and a command.")
		}
		if g.Policy != gates.PolicyRequired && g.Policy != gates.PolicyAdvisory {
			add(p+".policy", "policy must be required or advisory.")
		}
		if g.TimeoutSec < 0 || g.TimeoutSec > gates.MaxTimeout {
			add(p+".timeoutSec", "timeoutSec must be between 0 and %d.", gates.MaxTimeout)
		}
		if _, _, found := secretscan.Find(g.Command); found {
			add(p+".command", "this command contains something that looks like a credential. Read secrets from environment variables instead.")
		}
	}
	seenRule := map[string]bool{}
	for i, r := range d.Rules {
		p := fmt.Sprintf("rules[%d]", i)
		if !ruleNameRe.MatchString(r.Name) || seenRule[r.Name] {
			add(p+".name", "each rule needs a unique name of lowercase letters, digits and dashes.")
		}
		seenRule[r.Name] = true
		if strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Body) == "" {
			add(p, "a rule needs a title and text.")
		}
		switch r.Scope {
		case rules.ScopeGlobal:
		case rules.ScopeComponent:
			if len(r.Components) == 0 {
				add(p+".components", "a component rule needs at least one component.")
			}
		case rules.ScopePath:
			if len(r.Paths) == 0 {
				add(p+".paths", "a path rule needs at least one path pattern.")
			}
		default:
			add(p+".scope", "scope must be global, component or path.")
		}
		if _, _, found := secretscan.Find(r.Body); found {
			add(p+".body", "this rule contains something that looks like a credential.")
		}
	}
	if d.AgentPolicy != nil && !codeagent.ValidLevel(d.AgentPolicy.Level) {
		add("agentPolicy.level", "level must be read, propose or execute.")
	}
}

// todosPhase3 lists what the user must review after an import.
func todosPhase3(d *Definition, cat *catalog.Catalog) []Todo {
	var out []Todo
	for _, c := range d.Components {
		name := c.Catalog
		if def, ok := cat.Get(c.Catalog); ok {
			name = def.Name
		}
		envs := make([]string, 0, len(c.Profiles))
		for env := range c.Profiles {
			envs = append(envs, env)
		}
		sort.Strings(envs)
		for _, env := range envs {
			for _, f := range c.Profiles[env].SecretFields {
				out = append(out, Todo{"credential", name + " (" + env + ")", "Enter the " + f + " for the " + env + " connection and test it."})
			}
		}
	}
	custom := 0
	for _, g := range d.Gates {
		if !strings.HasPrefix(g.Command, "builtin:") {
			custom++
		}
	}
	if custom > 0 {
		out = append(out, Todo{"review_gates", fmt.Sprintf("%d quality gate(s)", custom), "Imported gates that run commands start switched off. Review them under Quality gates, then enable and save."})
	}
	return out
}

// applyPhase3 creates wires, environment settings, gates, rules and the agent
// policy once the cards exist. It returns how many items failed.
func (s *Service) applyPhase3(ctx context.Context, owner, wsID string, d *Definition, ids map[string]string, logf func(string, ...any)) int {
	failures := 0
	for _, c := range d.Components {
		if len(c.Profiles) == 0 || ids[c.Key] == "" {
			continue
		}
		cid := ids[c.Key]
		for env, p := range c.Profiles {
			env, p := env, p
			if _, err := s.st.UpdateComponent(ctx, cid, func(x *domain.Component) error {
				if x.Profiles == nil {
					x.Profiles = map[string]*domain.ConnProfile{}
				}
				x.Profiles[env] = &domain.ConnProfile{Connection: p.Connection}
				return nil
			}); err != nil {
				logf("could not set the %s connection of %s: %v", env, c.Catalog, err)
				failures++
			}
		}
	}
	if d.Environments != nil {
		if _, err := s.st.UpdateWorkspace(ctx, wsID, func(x *domain.Workspace) error { x.ActiveEnvironment = d.Environments.Active; return nil }); err != nil {
			logf("could not set the active environment: %v", err)
			failures++
		}
	}
	if s.p3 == nil {
		return failures
	}
	if s.p3.Wires != nil {
		for _, wd := range d.Wires {
			if _, err := s.p3.Wires.Create(ctx, domain.ActorUser, owner, owner, wsID, wiring.Input{FromID: ids[wd.From], ToID: ids[wd.To], Name: wd.Name, Environment: wd.Environment, Confirm: true}); err != nil {
				logf("could not connect %s to %s: %v", wd.From, wd.To, err)
				failures++
			}
		}
	}
	if s.p3.Gates != nil && len(d.Gates) > 0 {
		var gs []gates.Gate
		for _, g := range d.Gates {
			// Commands from a file start switched off until the user reviews them.
			on := g.Enabled && strings.HasPrefix(g.Command, "builtin:")
			gs = append(gs, gates.Gate{ID: g.ID, Name: g.Name, Kind: g.Kind, Command: g.Command, Policy: g.Policy, Enabled: on, TimeoutSec: g.TimeoutSec})
		}
		if _, err := s.p3.Gates.SaveConfig(ctx, owner, wsID, gates.Config{Gates: gs}); err != nil {
			logf("could not save the quality gates: %v", err)
			failures++
		}
	}
	if s.p3.Rules != nil {
		for _, r := range d.Rules {
			if _, err := s.p3.Rules.Save(ctx, owner, wsID, rules.SaveInput{Name: r.Name, Title: r.Title, Scope: r.Scope, Components: r.Components, Paths: r.Paths, Protect: r.Protect, Body: r.Body}); err != nil {
				logf("could not write the rule %s: %v", r.Name, err)
				failures++
			}
		}
	}
	if s.p3.Agent != nil && d.AgentPolicy != nil {
		if _, err := s.p3.Agent.SetSettings(ctx, owner, wsID, codeagent.Settings{Level: d.AgentPolicy.Level}); err != nil {
			logf("could not set the agent permission level: %v", err)
			failures++
		}
	}
	return failures
}

// driftPhase3 compares the Phase 3 sections.
func driftPhase3(want, have *Definition, add func(cat, subj, field, exp, act, kind string), cmp func(cat, subj, field string, exp, act any)) {
	// Environments.
	we, he := "", ""
	if want.Environments != nil {
		we = want.Environments.Active
	}
	if have.Environments != nil {
		he = have.Environments.Active
	}
	if we != "" || he != "" {
		cmp("environment", "active environment", "active", orNone(we), orNone(he))
	}
	// Environment connections of cards, matched by key.
	haveByKey := map[string]ComponentDef{}
	for _, c := range have.Components {
		haveByKey[c.Key] = c
	}
	for _, c := range want.Components {
		h, ok := haveByKey[c.Key]
		for env, p := range c.Profiles {
			subj := c.Catalog + " (" + env + ")"
			hp, hok := h.Profiles[env]
			if !ok || !hok {
				add("environment", subj, "", "connection set", "no connection", "missing")
				continue
			}
			for k, v := range p.Connection {
				cmp("environment", subj, "connection "+k, v, hp.Connection[k])
			}
		}
		if ok {
			for env := range h.Profiles {
				if _, wanted := c.Profiles[env]; !wanted {
					add("environment", c.Catalog+" ("+env+")", "", "not in the definition", "connection set", "extra")
				}
			}
		}
	}
	// Wires.
	wk := func(w WireDef) string { return w.From + " → " + w.To + ifName(w.Name) }
	haveW := map[string]WireDef{}
	for _, w := range have.Wires {
		haveW[wk(w)] = w
	}
	wantW := map[string]bool{}
	for _, w := range want.Wires {
		wantW[wk(w)] = true
		h, ok := haveW[wk(w)]
		if !ok {
			add("wire", wk(w), "", "connected", "not connected", "missing")
			continue
		}
		cmp("wire", wk(w), "environment", orNone(w.Environment), orNone(h.Environment))
	}
	for k := range haveW {
		if !wantW[k] {
			add("wire", k, "", "not in the definition", "connected", "extra")
		}
	}
	// Gates.
	haveG := map[string]GateDef{}
	for _, g := range have.Gates {
		haveG[g.ID] = g
	}
	for _, g := range want.Gates {
		h, ok := haveG[g.ID]
		if !ok {
			add("gate", g.Name, "", g.Policy, "not configured", "missing")
			continue
		}
		cmp("gate", g.Name, "command", g.Command, h.Command)
		cmp("gate", g.Name, "policy", g.Policy, h.Policy)
		cmp("gate", g.Name, "enabled", g.Enabled, h.Enabled)
		delete(haveG, g.ID)
	}
	for _, g := range haveG {
		add("gate", g.Name, "", "not in the definition", g.Policy, "extra")
	}
	// Rules.
	haveR := map[string]RuleDef{}
	for _, r := range have.Rules {
		haveR[r.Name] = r
	}
	for _, r := range want.Rules {
		h, ok := haveR[r.Name]
		if !ok {
			add("rule", r.Title, "", r.Scope, "no such rule file", "missing")
			continue
		}
		cmp("rule", r.Title, "scope", r.Scope, h.Scope)
		cmp("rule", r.Title, "text", strings.TrimSpace(r.Body), strings.TrimSpace(h.Body))
		cmp("rule", r.Title, "paths", strings.Join(r.Paths, ","), strings.Join(h.Paths, ","))
		cmp("rule", r.Title, "protected paths", strings.Join(r.Protect, ","), strings.Join(h.Protect, ","))
		delete(haveR, r.Name)
	}
	if len(have.Notes) == 0 { // rules unreadable (workspace stopped) are not reported as extra
		for _, r := range haveR {
			add("rule", r.Title, "", "not in the definition", r.Scope, "extra")
		}
	}
	if want.AgentPolicy != nil && have.AgentPolicy != nil {
		cmp("agent", "permission level", "level", want.AgentPolicy.Level, have.AgentPolicy.Level)
	}
}

func ifName(n string) string {
	if n == "" {
		return ""
	}
	return " (" + n + ")"
}
