package definition

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

type Service struct {
	st    store.Store
	cat   *catalog.Catalog
	ws    *workspace.Service
	comps *component.Service
	vars  *envvars.Service
	eng   *ops.Engine
	aud   *audit.Recorder
	agent Agent
}

func NewService(st store.Store, cat *catalog.Catalog, ws *workspace.Service, comps *component.Service, vars *envvars.Service, eng *ops.Engine, aud *audit.Recorder) *Service {
	s := &Service{st: st, cat: cat, ws: ws, comps: comps, vars: vars, eng: eng, aud: aud}
	eng.Register("apply_definition", s.applyHandler)
	eng.Register("post_create", s.postCreateHandler)
	return s
}

// sensitiveKey mirrors the audit scrubber: such keys never leave in a definition.
var sensitiveWords = []string{"password", "secret", "apikey", "api_key", "token", "key", "credential", "header", "passphrase"}

func sensitiveKey(k string) bool {
	l := strings.ToLower(k)
	for _, w := range sensitiveWords {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

func publicMap(m map[string]any, drop func(string) bool) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range m {
		if sensitiveKey(k) || (drop != nil && drop(k)) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// kindRank orders components so prerequisites (runtimes, frameworks) come before what needs them.
func kindRank(kind string) int {
	switch kind {
	case "runtime":
		return 0
	case "framework":
		return 1
	case "tool", "scripting":
		return 2
	}
	return 3
}

// Export captures the workspace as a definition. It never includes secret
// values: credential fields are listed by name, secret variables carry no value.
func (s *Service) Export(ctx context.Context, owner, wsID string) (*Definition, error) {
	w, err := s.ws.Get(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	d := &Definition{SchemaVersion: SchemaVersion, Kind: Kind, ExportedAt: time.Now().UTC(),
		Workspace: WorkspaceDef{Name: w.Name, Description: w.Description, OS: w.OS, CPU: w.CPU, RAMGB: w.RAMGB, StorageGB: w.StorageGB, GPU: w.GPU, IdleStopMinutes: w.IdleStopMinutes}}
	if len(w.Runtimes) > 0 {
		d.Runtimes = map[string]RuntimeDef{}
		for id, r := range w.Runtimes {
			d.Runtimes[id] = RuntimeDef{Versions: append([]string{}, r.Installed...), Active: r.Active}
		}
	}
	cards, err := s.comps.List(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(cards, func(i, j int) bool {
		ri, rj := 3, 3
		if def, ok := s.cat.Get(cards[i].CatalogID); ok {
			ri = kindRank(def.Kind)
		}
		if def, ok := s.cat.Get(cards[j].CatalogID); ok {
			rj = kindRank(def.Kind)
		}
		if ri != rj {
			return ri < rj
		}
		return cards[i].CreatedAt.Before(cards[j].CreatedAt)
	})
	keys := map[string]string{} // component id -> key
	seen := map[string]int{}
	for _, c := range cards {
		seen[c.CatalogID]++
		key := c.CatalogID
		if seen[c.CatalogID] > 1 {
			key = fmt.Sprintf("%s-%d", c.CatalogID, seen[c.CatalogID])
		}
		keys[c.ID] = key
		cd := ComponentDef{Key: key, Catalog: c.CatalogID, Mode: "draft", Version: c.Version, X: c.X, Y: c.Y}
		def, _ := s.cat.Get(c.CatalogID)
		switch c.Mode {
		case domain.ModeInstalled:
			cd.Mode = "install"
			cd.Desired = c.Desired
			cd.Settings = publicMap(c.Settings, nil)
		case domain.ModeConnected:
			cd.Mode, cd.Name, cd.Environment, cd.Version = "connect", c.ConnName, c.Environment, ""
			secretNames := map[string]bool{}
			if def != nil {
				for _, n := range def.SecretFields() {
					secretNames[n] = true
				}
				cd.SecretFields = def.SecretFields()
			}
			cd.Connection = publicMap(c.Connection, func(k string) bool { return secretNames[k] })
		default:
			if c.SuggestedMode == "install" || c.SuggestedMode == "connect" {
				cd.Mode = "draft"
			}
		}
		d.Components = append(d.Components, cd)
	}
	rows, err := s.vars.List(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	for _, v := range rows.Variables {
		vd := VariableDef{Name: v.Name, Secret: v.Secret, Description: v.Description, Component: keys[v.ComponentID]}
		if !v.Secret {
			vd.Value = v.Value
		}
		d.Variables = append(d.Variables, vd)
	}
	for _, p := range w.Ports {
		d.Ports = append(d.Ports, PortDef{Port: p.Port, Label: p.Label})
	}
	cfgs, err := s.st.ListAgentConfigs(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for _, a := range cfgs {
		d.Agents = append(d.Agents, AgentDef{Name: a.Name, Provider: a.Provider, Model: a.ModelID, Endpoint: a.Endpoint, Protocol: a.Protocol, Default: a.IsDefault, NeedsKey: a.SecretRef != ""})
	}
	s.aud.User(ctx, owner, wsID, "definition.export", w.Name, map[string]any{"components": len(d.Components), "variables": len(d.Variables)})
	return d, nil
}
