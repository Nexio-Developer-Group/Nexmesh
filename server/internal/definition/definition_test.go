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
