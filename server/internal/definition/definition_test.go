package definition

import (
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
)

func sample() *Definition {
	return &Definition{SchemaVersion: SchemaVersion, Kind: Kind,
		Workspace:  WorkspaceDef{Name: "orders-api", OS: "ubuntu-24.04", CPU: 4, RAMGB: 8, StorageGB: 40},
		Components: []ComponentDef{{Key: "redis", Catalog: "redis", Mode: "install", Version: "7.4.1", X: 40, Y: 40}, {Key: "mongodb", Catalog: "mongodb", Mode: "connect", Name: "prod-db", Connection: map[string]any{"host": "h"}, SecretFields: []string{"password"}}},
		Variables:  []VariableDef{{Name: "APP_MODE", Value: "dev"}, {Name: "STRIPE_KEY", Secret: true}}}
}

func TestMarshalParseRoundTripAndJSONInput(t *testing.T) {
	b, err := Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "# Nexmesh workspace definition. Contains no secret values.") {
		t.Fatalf("header: %s", b)
	}
	d, err := Parse(b)
	if err != nil || d.Workspace.Name != "orders-api" || len(d.Components) != 2 || d.Components[1].SecretFields[0] != "password" {
		t.Fatalf("round trip: %v %+v", err, d)
	}
	// JSON is accepted too (YAML superset).
	js := `{"schemaVersion":1,"kind":"NexmeshWorkspace","workspace":{"name":"x-y","os":"ubuntu-24.04","cpu":1,"ramGb":1,"storageGb":5}}`
	if d, err := Parse([]byte(js)); err != nil || d.Workspace.Name != "x-y" {
		t.Fatalf("json: %v", err)
	}
}

func TestParseRejectsUnknownKindVersionFieldsAndSize(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":     "",
		"not yaml":  "::: [",
		"kind":      "schemaVersion: 1\nkind: Other\nworkspace: {name: ab}",
		"version":   "schemaVersion: 9\nkind: NexmeshWorkspace\nworkspace: {name: ab}",
		"unknown":   "schemaVersion: 1\nkind: NexmeshWorkspace\nworkspace: {name: ab}\nmystery: 1",
		"too large": strings.Repeat("a", maxFileBytes+1),
	} {
		_, err := Parse([]byte(doc))
		var pe *ParseError
		if err == nil || !errorsAs(err, &pe) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func errorsAs(err error, target **ParseError) bool {
	pe, ok := err.(*ParseError)
	if ok {
		*target = pe
	}
	return ok
}

func TestValidateReportsEveryProblem(t *testing.T) {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	if issues := Validate(sample(), cat); len(issues) != 0 {
		t.Fatalf("valid sample rejected: %+v", issues)
	}
	d := sample()
	d.Workspace.Name = "Bad Name!"
	d.Workspace.CPU = 0
	d.Components[0].Version = "0.0.1"
	d.Components[1].Connection["password"] = "hunter2"
	d.Components[1].Connection["nonsense"] = 1
	d.Components = append(d.Components, ComponentDef{Key: "redis", Catalog: "cobol", Mode: "install"})
	d.Variables[1].Value = "sk_live_x"
	d.Variables = append(d.Variables, VariableDef{Name: "9BAD"}, VariableDef{Name: "OK", Component: "ghost"})
	d.Ports = []PortDef{{Port: 70000}}
	issues := Validate(d, cat)
	paths := map[string]bool{}
	for _, i := range issues {
		paths[i.Path] = true
	}
	for _, want := range []string{"workspace.name", "workspace", "components[0].version", "components[1].connection.password", "components[1].connection.nonsense",
		"components[2].key", "components[2].catalog", "variables[1].value", "variables[2].name", "variables[3].component", "ports[0].port"} {
		if !paths[want] {
			t.Errorf("no issue reported for %s (got %v)", want, paths)
		}
	}
	for _, i := range issues {
		if strings.Contains(i.Message, "hunter2") || strings.Contains(i.Message, "sk_live") {
			t.Fatalf("issue message echoes a secret value: %q", i.Message)
		}
	}
}

func TestTodosListWhatTheUserMustSupply(t *testing.T) {
	cat, _ := catalog.Load("../../../catalog")
	d := sample()
	d.Agents = []AgentDef{{Name: "default", Provider: "anthropic", NeedsKey: true}}
	got := map[string]int{}
	for _, td := range Todos(d, cat, false) {
		got[td.Kind]++
	}
	if got["credential"] != 1 || got["secret_variable"] != 1 || got["agent_key"] != 1 || got["confirm_install"] != 1 {
		t.Fatalf("todos without auto-install: %v", got)
	}
	got = map[string]int{}
	for _, td := range Todos(d, cat, true) {
		got[td.Kind]++
	}
	if got["confirm_install"] != 0 {
		t.Fatalf("auto-install must not ask for confirmation again: %v", got)
	}
}

func TestPhase3SectionsValidateAndNeverCarryCredentials(t *testing.T) {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	base := func() *Definition {
		d := sample()
		d.Wires = []WireDef{{From: "redis", To: "mongodb", Name: "orders-db", Environment: "staging"}}
		d.Environments = &EnvironmentsDef{Active: "staging"}
		d.Gates = []GateDef{{ID: "tests", Name: "Tests", Kind: "test", Command: "go test ./...", Policy: "required", Enabled: true}}
		d.Rules = []RuleDef{{Name: "style", Title: "Style", Scope: "global", Body: "Be brief."}}
		d.AgentPolicy = &AgentPolicyDef{Level: "propose"}
		return d
	}
	if issues := Validate(base(), cat); len(issues) != 0 {
		t.Fatalf("valid definition rejected: %+v", issues)
	}
	cases := map[string]func(d *Definition){
		"wires[0].from":        func(d *Definition) { d.Wires[0].From = "ghost" },
		"wires[0].to":          func(d *Definition) { d.Wires[0].To = "ghost" },
		"wires[0].environment": func(d *Definition) { d.Wires[0].Environment = "qa" },
		"environments.active":  func(d *Definition) { d.Environments.Active = "qa" },
		"gates[0].policy":      func(d *Definition) { d.Gates[0].Policy = "maybe" },
		"gates[0].id":          func(d *Definition) { d.Gates[0].ID = "Bad ID" },
		"gates[0].command": func(d *Definition) {
			d.Gates[0].Command = "curl -H 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123' x"
		},
		"rules[0].scope":           func(d *Definition) { d.Rules[0].Scope = "galaxy" },
		"rules[0].paths":           func(d *Definition) { d.Rules[0].Scope = "path" },
		"rules[0].body":            func(d *Definition) { d.Rules[0].Body = "use password = 'hunter2hunter2'" },
		"agentPolicy.level":        func(d *Definition) { d.AgentPolicy.Level = "root" },
		"variables[1].environment": func(d *Definition) { d.Variables[1].Environment = "qa" },
		"components[1].profiles.staging.connection.password": func(d *Definition) {
			d.Components[1].Profiles = map[string]ProfileDef{"staging": {Connection: map[string]any{"password": "x"}}}
		},
	}
	for path, mutate := range cases {
		d := base()
		mutate(d)
		found := false
		for _, is := range Validate(d, cat) {
			if strings.HasPrefix(is.Path, path) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no issue reported", path)
		}
	}
	// A round trip keeps every section, and auto-accept has no place in the schema.
	b, _ := Marshal(base())
	d, err := Parse(b)
	if err != nil || len(d.Wires) != 1 || d.Environments.Active != "staging" || d.Gates[0].ID != "tests" || d.Rules[0].Name != "style" || d.AgentPolicy.Level != "propose" {
		t.Fatalf("%v %+v", err, d)
	}
	if _, err := Parse([]byte(strings.Replace(string(b), "level: propose", "level: propose\n  autoAccept: true", 1))); err == nil {
		t.Fatal("autoAccept must be rejected as an unknown field")
	}
	// Imported command gates ask for review.
	var kinds []string
	for _, td := range Todos(base(), cat, false) {
		kinds = append(kinds, td.Kind)
	}
	if !strings.Contains(strings.Join(kinds, ","), "review_gates") {
		t.Fatalf("todos: %v", kinds)
	}
}
