package devcontainer

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/definition"
)

func cat(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func find(rep []ReportItem, field string) *ReportItem {
	for i := range rep {
		if rep[i].Field == field {
			return &rep[i]
		}
	}
	return nil
}

func TestJSONCCommentsAndTrailingCommas(t *testing.T) {
	src := `{
  // the name
  "name": "demo // not a comment",
  /* block
     comment */
  "forwardPorts": [3000, 8080,],
  "containerEnv": { "URL": "http://x/*not-a-comment*/", "ESC": "a\"b//c", },
}`
	m, err := ParseJSONC([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if m["name"] != "demo // not a comment" || len(m["forwardPorts"].([]any)) != 2 {
		t.Fatalf("%v", m)
	}
	env := m["containerEnv"].(map[string]any)
	if env["URL"] != "http://x/*not-a-comment*/" || env["ESC"] != `a"b//c` {
		t.Fatalf("strings must be untouched: %v", env)
	}
	for _, bad := range []string{"", "   ", "[1,2]", `{"a": }`, "not json"} {
		if _, err := ParseJSONC([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const sample = `{
  "name": "My Orders App!",
  "image": "mcr.microsoft.com/devcontainers/base:debian-12",
  "features": {
    "ghcr.io/devcontainers/features/node:1": { "version": "20" },
    "ghcr.io/devcontainers/features/python:1": {"version": "3.11"},
    "ghcr.io/devcontainers/features/go:1": "1.20",
    "ghcr.io/devcontainers/features/git:1": {},
    "ghcr.io/devcontainers/features/docker-in-docker:2": {}
  },
  "forwardPorts": [3000, "db:5432", 8080],
  "portsAttributes": { "3000": { "label": "web", "onAutoForward": "notify" }, "9000-9010": {} },
  "containerEnv": { "APP_MODE": "dev", "API_TOKEN": "abc123", "HOME_DIR": "${localEnv:HOME}" },
  "remoteEnv": { "LOG": "debug" },
  "hostRequirements": { "cpus": 4, "memory": "8gb", "storage": "64gb" },
  "postCreateCommand": "npm install",
  "onCreateCommand": ["make", "setup"],
  "postStartCommand": "npm run dev",
  "initializeCommand": "echo host",
  "remoteUser": "vscode",
  "mounts": ["source=a,target=b,type=bind"],
  "customizations": { "vscode": { "extensions": ["a.b"] }, "jetbrains": {} },
  "mysteryField": true,
  "dockerFile": "Dockerfile"
}`

func TestImportBuildsDefinitionAndReportsEveryField(t *testing.T) {
	c := cat(t)
	imp, err := Import([]byte(sample), c)
	if err != nil {
		t.Fatal(err)
	}
	d := imp.Definition
	if d.Workspace.Name != "my-orders-app" || d.Workspace.CPU != 4 || d.Workspace.RAMGB != 8 || d.Workspace.StorageGB != 64 || d.Workspace.OS != "ubuntu-24.04" {
		t.Fatalf("workspace: %+v", d.Workspace)
	}
	rt := map[string]string{}
	for _, comp := range d.Components {
		rt[comp.Catalog] = comp.Version
	}
	if rt["nodejs"] != "20.18.0" || rt["python"] != "3.11.10" || rt["go"] != "1.22.9" && rt["go"] != "1.23.3" {
		t.Fatalf("runtimes: %v", rt)
	}
	if len(d.Ports) != 2 || d.Ports[0].Port != 3000 || d.Ports[0].Label != "web" || d.Ports[1].Port != 8080 {
		t.Fatalf("ports: %+v", d.Ports)
	}
	vars := map[string]definition.VariableDef{}
	for _, v := range d.Variables {
		vars[v.Name] = v
	}
	if vars["APP_MODE"].Value != "dev" || vars["LOG"].Value != "debug" {
		t.Fatalf("variables: %+v", vars)
	}
	if v, ok := vars["API_TOKEN"]; !ok || !v.Secret || v.Value != "" {
		t.Fatalf("secret-looking variable must not carry its value: %+v", v)
	}
	if _, ok := vars["HOME_DIR"]; ok {
		t.Fatal("unresolvable substitution must not be imported")
	}
	if got := strings.Join(imp.PostCreate, " | "); got != "make setup | npm install" {
		t.Fatalf("post-create commands: %q", got)
	}
	if issues := definition.Validate(d, c); len(issues) != 0 {
		t.Fatalf("the produced definition must be valid: %+v", issues)
	}

	// The report accounts for every field in the file, with the right status.
	want := map[string]string{
		"name": Adapted, "image": Adapted, "features.ghcr.io/devcontainers/features/node:1": Adapted, "features.ghcr.io/devcontainers/features/git:1": Adapted,
		"features.ghcr.io/devcontainers/features/docker-in-docker:2": Unsupported, "forwardPorts": "", "portsAttributes.3000": Applied,
		"portsAttributes.3000.onAutoForward": Unsupported, "portsAttributes.9000-9010": Unsupported, "containerEnv.APP_MODE": Applied, "containerEnv.API_TOKEN": Adapted,
		"containerEnv.HOME_DIR": Unsupported, "remoteEnv.LOG": Applied, "hostRequirements.cpus": Applied, "postCreateCommand": Applied, "onCreateCommand": Applied,
		"postStartCommand": Unsupported, "initializeCommand": Unsupported, "remoteUser": Unsupported, "mounts": Unsupported, "customizations.vscode": Unsupported,
		"customizations.jetbrains": Unsupported, "mysteryField": Unsupported, "dockerFile": Unsupported,
	}
	for field, status := range want {
		it := find(imp.Report, field)
		if it == nil {
			t.Errorf("no report entry for %s", field)
			continue
		}
		if status != "" && it.Status != status {
			t.Errorf("%s: status %s, want %s (%s)", field, it.Status, status, it.Detail)
		}
		if it.Detail == "" {
			t.Errorf("%s: empty detail", field)
		}
	}
	if it := find(imp.Report, "forwardPorts"); it == nil {
		t.Error("forwardPorts missing")
	}
	// "db:5432" is reported rather than dropped.
	dbReported := false
	for _, it := range imp.Report {
		dbReported = dbReported || (it.Field == "forwardPorts" && it.Status == Unsupported && strings.Contains(it.Detail, "db:5432"))
	}
	if !dbReported {
		t.Error("host:port forward was dropped silently")
	}
}

func TestImportComposeAndMinimalFiles(t *testing.T) {
	c := cat(t)
	imp, err := Import([]byte(`{"dockerComposeFile":"docker-compose.yml","service":"app","workspaceFolder":"/w"}`), c)
	if err != nil {
		t.Fatal(err)
	}
	if it := find(imp.Report, "dockerComposeFile"); it == nil || it.Status != Unsupported {
		t.Fatalf("compose: %+v", imp.Report)
	}
	if it := find(imp.Report, "workspaceFolder"); it == nil {
		t.Fatalf("workspaceFolder dropped: %+v", imp.Report)
	}
	// Language base images imply a runtime.
	imp, _ = Import([]byte(`{"name":"x1","image":"mcr.microsoft.com/devcontainers/javascript-node:22"}`), c)
	if len(imp.Definition.Components) != 1 || imp.Definition.Components[0].Catalog != "nodejs" || imp.Definition.Components[0].Version != "22.11.0" {
		t.Fatalf("image runtime: %+v", imp.Definition.Components)
	}
	// Defaults are announced, not hidden.
	if it := find(imp.Report, "hostRequirements"); it == nil || it.Status != Adapted {
		t.Fatalf("default resources must be reported: %+v", imp.Report)
	}
	if it := find(imp.Report, "name"); it == nil || it.Status != Applied {
		t.Fatalf("name: %+v", it)
	}
}

func sampleDef() *definition.Definition {
	return &definition.Definition{SchemaVersion: 1, Kind: definition.Kind,
		Workspace: definition.WorkspaceDef{Name: "orders-api", OS: "ubuntu-24.04", CPU: 4, RAMGB: 8, StorageGB: 40},
		Components: []definition.ComponentDef{
			{Key: "nodejs", Catalog: "nodejs", Mode: "install", Version: "22.11.0"},
			{Key: "python", Catalog: "python", Mode: "install", Version: "3.12.7"},
			{Key: "redis", Catalog: "redis", Mode: "install", Version: "7.4.1"},
			{Key: "mongodb", Catalog: "mongodb", Mode: "connect", Name: "prod-db", SecretFields: []string{"password"}}},
		Variables: []definition.VariableDef{{Name: "APP_MODE", Value: "dev"}, {Name: "STRIPE_KEY", Secret: true}},
		Ports:     []definition.PortDef{{Port: 3000, Label: "web"}, {Port: 8080}}}
}

// The exported file is checked against the dev container specification's own JSON schema.
func TestExportValidatesAgainstTheOfficialSchema(t *testing.T) {
	ex, err := Export(sampleDef())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("testdata/devContainer.base.schema.json")
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://example.test/devcontainer.schema.json", mustJSON(t, raw)); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("https://example.test/devcontainer.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(ex.File, &doc); err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(doc); err != nil {
		t.Fatalf("export violates the dev container schema: %v\n%s", err, ex.File)
	}
	// The negative control: the schema really rejects an unknown top-level field.
	bad := map[string]any{"name": "x", "image": "y", "notAField": 1}
	if err := sch.Validate(bad); err == nil {
		t.Fatal("schema did not reject an invalid file; the validation above proves nothing")
	}
}

func mustJSON(t *testing.T, raw []byte) any {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestExportContentAndRoundTrip(t *testing.T) {
	ex, _ := Export(sampleDef())
	if out := os.Getenv("NEXMESH_WRITE_DEVCONTAINER"); out != "" { // lets a contributor check the file with the devcontainer CLI
		_ = os.WriteFile(out, ex.File, 0o644)
	}
	s := string(ex.File)
	for _, want := range []string{`"image": "mcr.microsoft.com/devcontainers/base:ubuntu-24.04"`, `features/node:1`, `features/python:1`, `"forwardPorts"`, `"label": "web"`, `"APP_MODE": "dev"`, `"cpus": 4`, `"memory": "8gb"`, `"nexmesh"`, `"STRIPE_KEY"`} {
		if !strings.Contains(s, want) {
			t.Errorf("export missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "password") && !strings.Contains(s, "secretVariables") {
		t.Errorf("credential field leaked into the file:\n%s", s)
	}
	if r := find(ex.Report, "variables.STRIPE_KEY"); r == nil || r.Status != Unsupported {
		t.Fatalf("secret variable must be reported: %+v", ex.Report)
	}
	if r := find(ex.Report, "components.redis"); r == nil || r.Status != Unsupported {
		t.Fatalf("service components can't be started by a dev container: %+v", ex.Report)
	}
	// Importing our own export gives back the runtimes, ports and plain variables.
	back, err := Import(ex.File, cat(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Definition.Components) != 2 || len(back.Definition.Ports) != 2 || back.Definition.Workspace.CPU != 4 || back.Definition.Workspace.RAMGB != 8 {
		t.Fatalf("round trip: %+v", back.Definition)
	}
}
