package deployexport_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/deployexport"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
	"gopkg.in/yaml.v3"
)

// memFS is an in-memory workspace behind the agent-call interface.
type memFS struct{ files map[string]string }

func (m *memFS) Call(_ context.Context, _ string, method string, params, out any) error {
	b, _ := json.Marshal(params)
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal(b, &p)
	p.Path = strings.Trim(p.Path, "/")
	isDir := func(d string) bool {
		if d == "" {
			return true
		}
		for f := range m.files {
			if strings.HasPrefix(f, d+"/") {
				return true
			}
		}
		return false
	}
	reply := func(v any) error {
		if out == nil {
			return nil
		}
		b, _ := json.Marshal(v)
		return json.Unmarshal(b, out)
	}
	switch method {
	case "fs.list":
		seen := map[string]bool{}
		var es []wsfs.Entry
		prefix := p.Path
		if prefix != "" {
			prefix += "/"
		}
		keys := make([]string, 0)
		for f := range m.files {
			keys = append(keys, f)
		}
		sort.Strings(keys)
		for _, f := range keys {
			if !strings.HasPrefix(f, prefix) {
				continue
			}
			rest := strings.TrimPrefix(f, prefix)
			name, _, nested := strings.Cut(rest, "/")
			if seen[name] {
				continue
			}
			seen[name] = true
			t := "file"
			if nested {
				t = "dir"
			}
			es = append(es, wsfs.Entry{Name: name, Path: path.Join(p.Path, name), Type: t})
		}
		return reply(map[string]any{"entries": es})
	case "fs.stat":
		if _, ok := m.files[p.Path]; ok || isDir(p.Path) {
			return reply(struct{}{})
		}
		return &agentproto.Error{Code: "not_found", Msg: "not found"}
	case "fs.read":
		c, ok := m.files[p.Path]
		if !ok {
			return &agentproto.Error{Code: "not_found", Msg: "not found"}
		}
		return reply(map[string]any{"content": c, "modTime": time.Now()})
	case "fs.write":
		m.files[p.Path] = p.Content
		return reply(struct{}{})
	case "fs.mkdir":
		return nil
	}
	return &agentproto.Error{Code: "bad_params", Msg: method}
}

type rig struct {
	ctx  context.Context
	st   *store.Memory
	fs   *memFS
	svc  *deployexport.Service
	wire *wiring.Service
}

func newRig(t *testing.T, files map[string]string) *rig {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	aud := audit.New(st, log)
	comp := component.NewService(st, cat, bus, aud)
	wire := wiring.New(st, cat, comp, bus, aud)
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "Shop Demo", Status: domain.WSReady,
		Runtimes:  map[string]domain.RuntimeState{"nodejs": {Active: "20.18.0"}, "python": {Active: "3.11.9"}},
		Variables: []domain.Variable{{Name: "STRIPE_KEY", Secret: true}, {Name: "LOG_LEVEL", Value: "debug"}}})
	fs := &memFS{files: files}
	return &rig{ctx, st, fs, deployexport.New(st, cat, comp, wire, wsfs.New(fs), aud), wire}
}

func (r *rig) card(id, mode, state string, settings map[string]any) *domain.Component {
	c := &domain.Component{ID: store.NewID("cmp"), WorkspaceID: "ws1", CatalogID: id, Mode: mode, Observed: state, Version: "1.0.0", Settings: settings, CreatedAt: time.Now()}
	if settings == nil {
		c.Settings = map[string]any{}
	}
	_ = r.st.CreateComponent(r.ctx, c)
	time.Sleep(2 * time.Millisecond)
	return c
}

func (r *rig) link(t *testing.T, from, to *domain.Component, name string) {
	if _, err := r.wire.Create(r.ctx, domain.ActorUser, "u1", "u1", "ws1", wiring.Input{FromID: from.ID, ToID: to.ID, Name: name, Confirm: true}); err != nil {
		t.Fatal(err)
	}
}

func byPath(p *deployexport.Plan) map[string]deployexport.File {
	m := map[string]deployexport.File{}
	for _, f := range p.Files {
		m[f.Path] = f
	}
	return m
}

var project = map[string]string{
	"api/package.json":      `{"scripts":{"build":"nest build","start":"node dist/main"},"dependencies":{"@nestjs/core":"10","mongoose":"8"}}`,
	"api/package-lock.json": "{}",
	"web/package.json":      `{"scripts":{"build":"vite build"},"dependencies":{"react":"19"}}`,
	"web/pnpm-lock.yaml":    "",
	"README.md":             "hi",
}

func TestComposeHasInstalledServicesAndDockerfilesForDetectedProjects(t *testing.T) {
	r := newRig(t, map[string]string{})
	for k, v := range project {
		r.fs.files[k] = v
	}
	mongo := r.card("mongodb", "installed", "running", nil)
	nest := r.card("nestjs", "installed", "ready", nil)
	react := r.card("react", "installed", "ready", nil)
	r.link(t, nest, mongo, "")
	r.link(t, react, nest, "api")

	plan, err := r.svc.Build(r.ctx, "u1", "ws1")
	if err != nil {
		t.Fatal(err)
	}
	files := byPath(plan)
	for _, want := range []string{"docker-compose.yml", "api/Dockerfile", "web/Dockerfile", "api/.dockerignore", ".env.example"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("missing %s; have %v", want, plan.Files)
		}
	}
	var doc struct {
		Services map[string]struct {
			Image       string            `yaml:"image"`
			Build       map[string]string `yaml:"build"`
			Ports       []string          `yaml:"ports"`
			Environment map[string]string `yaml:"environment"`
			DependsOn   []string          `yaml:"depends_on"`
			Volumes     []string          `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal([]byte(files["docker-compose.yml"].Content), &doc); err != nil {
		t.Fatalf("compose is not valid YAML: %v\n%s", err, files["docker-compose.yml"].Content)
	}
	m := doc.Services["mongodb"]
	if m.Image != "mongo:1.0.0" || len(m.Ports) != 1 || !strings.Contains(m.Volumes[0], "/data/db") {
		t.Fatalf("mongodb service: %+v", m)
	}
	api := doc.Services["api"]
	if api.Build["context"] != "./api" || api.Environment["MONGODB_URI"] != "mongodb://mongodb:27017/" || len(api.DependsOn) != 1 || api.DependsOn[0] != "mongodb" {
		t.Fatalf("api service: %+v", api)
	}
	web := doc.Services["web"]
	if web.Build["context"] != "./web" || !strings.Contains(web.Environment["API_URL"], "localhost:3000") {
		t.Fatalf("web service: %+v", web)
	}
	if len(doc.Volumes) == 0 {
		t.Fatal("named volumes should be declared")
	}

	api1 := files["api/Dockerfile"].Content
	for _, want := range []string{"FROM node:20-alpine", "npm ci", "run build", `CMD ["node", "dist/main.js"]`, "Generated by Nexmesh"} {
		if !strings.Contains(api1, want) {
			t.Errorf("api Dockerfile missing %q:\n%s", want, api1)
		}
	}
	web1 := files["web/Dockerfile"].Content
	for _, want := range []string{"corepack enable && pnpm install --frozen-lockfile", "FROM nginx:alpine", "/usr/share/nginx/html"} {
		if !strings.Contains(web1, want) {
			t.Errorf("web Dockerfile missing %q:\n%s", want, web1)
		}
	}
}

func TestConnectedCardsAreReferencesNotContainersAndNoSecretIsWritten(t *testing.T) {
	r := newRig(t, map[string]string{"api/package.json": project["api/package.json"]})
	redis := r.card("redis", "connected", "connected", nil)
	_, _ = r.st.UpdateComponent(r.ctx, redis.ID, func(c *domain.Component) error {
		c.ConnName = "prod-cache"
		c.Environment = "production"
		c.Connection = map[string]any{"host": "cache.internal.example", "password": "hunter2-super-secret"}
		return nil
	})
	rabbit := r.card("rabbitmq", "installed", "running", nil)
	nest := r.card("nestjs", "installed", "ready", nil)
	r.link(t, nest, redis, "")
	r.link(t, nest, rabbit, "")

	plan, err := r.svc.Build(r.ctx, "u1", "ws1")
	if err != nil {
		t.Fatal(err)
	}
	files := byPath(plan)
	compose := files["docker-compose.yml"].Content
	if strings.Contains(compose, "redis:") && strings.Contains(compose, "image: redis") {
		t.Fatal("a connected component must not become a container")
	}
	if !strings.Contains(compose, "PROD_CACHE_URI: ${PROD_CACHE_URI}") {
		t.Fatalf("connected provider should be an environment reference:\n%s", compose)
	}
	if !strings.Contains(compose, "RABBITMQ_DEFAULT_PASS: ${RABBITMQ_DEFAULT_PASS}") {
		t.Fatalf("a credential in a service's own environment must become a reference:\n%s", compose)
	}
	if strings.Contains(compose, "nexmesh\n") && strings.Contains(compose, "PASS: nexmesh") {
		t.Fatal("default password leaked")
	}
	env := files[".env.example"].Content
	for _, want := range []string{"PROD_CACHE_URI=\n", "RABBITMQ_DEFAULT_PASS=\n", "STRIPE_KEY=\n", "LOG_LEVEL=\n"} {
		if !strings.Contains(env, want) {
			t.Errorf(".env.example missing %q:\n%s", want, env)
		}
	}
	for name, f := range files {
		for _, bad := range []string{"hunter2", "cache.internal.example", "debug\n"} {
			if strings.Contains(f.Content, bad) && name != "docker-compose.yml" || (strings.Contains(f.Content, "hunter2") || strings.Contains(f.Content, "cache.internal.example")) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
	if !strings.Contains(strings.Join(plan.Notes, " "), "connected service") {
		t.Errorf("the plan should explain why a connected card is not a container: %v", plan.Notes)
	}
}

func TestMissingProjectsAreExplainedAndNothingOverwrittenWithoutConsent(t *testing.T) {
	r := newRig(t, map[string]string{"api/package.json": project["api/package.json"], "api/Dockerfile": "FROM scratch # mine\n", "docker-compose.yml": "# Generated by Nexmesh\nold: true\n"})
	r.card("mongodb", "installed", "running", nil)
	r.card("nestjs", "installed", "ready", nil)
	r.card("fastapi", "installed", "ready", nil) // no Python project exists

	plan, _ := r.svc.Build(r.ctx, "u1", "ws1")
	files := byPath(plan)
	if !files["api/Dockerfile"].Exists || files["api/Dockerfile"].Ours {
		t.Fatalf("a hand-written Dockerfile is detected as existing and not ours: %+v", files["api/Dockerfile"])
	}
	if !files["docker-compose.yml"].Ours {
		t.Fatal("a previously generated file is recognised as ours")
	}
	if !strings.Contains(strings.Join(plan.Notes, " "), "No FastAPI project was found") {
		t.Fatalf("notes: %v", plan.Notes)
	}

	if _, err := r.svc.Write(r.ctx, "u1", "ws1", deployexport.WriteInput{Paths: []string{"api/Dockerfile"}}); err == nil {
		t.Fatal("writing needs confirmation")
	}
	res, err := r.svc.Write(r.ctx, "u1", "ws1", deployexport.WriteInput{Paths: []string{"api/Dockerfile", "docker-compose.yml"}, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 2 || r.fs.files["api/Dockerfile"] != "FROM scratch # mine\n" {
		t.Fatalf("existing files must be kept without overwrite: %+v", res)
	}
	res, err = r.svc.Write(r.ctx, "u1", "ws1", deployexport.WriteInput{Paths: []string{"docker-compose.yml", ".env.example"}, Overwrite: true, Confirm: true})
	if err != nil || len(res.Written) != 2 || !strings.Contains(r.fs.files["docker-compose.yml"], "mongodb:") {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := r.svc.Write(r.ctx, "u1", "ws1", deployexport.WriteInput{Paths: []string{"../../etc/passwd"}, Confirm: true}); err == nil {
		t.Fatal("only planned files can be written")
	}
	evs, _ := r.st.ListAudit(r.ctx, "ws1", 10)
	found := false
	for _, e := range evs {
		found = found || e.Action == "export.compose_write"
	}
	if !found {
		t.Fatal("writing files must be audited")
	}
}

func TestPythonGoAndStaticTemplates(t *testing.T) {
	r := newRig(t, map[string]string{
		"svc/requirements.txt": "fastapi\nuvicorn\n", "svc/app/main.py": "",
		"tools/go.mod": "module x", "site/index.html": "<h1>x</h1>",
	})
	r.card("fastapi", "installed", "ready", nil)
	r.card("go", "installed", "ready", nil)
	r.card("htmlcss", "installed", "ready", nil)
	plan, _ := r.svc.Build(r.ctx, "u1", "ws1")
	files := byPath(plan)
	if c := files["svc/Dockerfile"].Content; !strings.Contains(c, "FROM python:3.11-slim") || !strings.Contains(c, `"app.main:app"`) || !strings.Contains(c, "8000") {
		t.Errorf("fastapi:\n%s", c)
	}
	if c := files["tools/Dockerfile"].Content; !strings.Contains(c, "golang:") || !strings.Contains(c, "distroless") {
		t.Errorf("go:\n%s", c)
	}
	if c := files["site/Dockerfile"].Content; !strings.Contains(c, "nginx:alpine") {
		t.Errorf("static:\n%s", c)
	}
}

func TestEmptyCanvasAndOwnership(t *testing.T) {
	r := newRig(t, map[string]string{})
	plan, err := r.svc.Build(r.ctx, "u1", "ws1")
	if err != nil || len(plan.Files) != 0 || len(plan.Notes) == 0 {
		t.Fatalf("%v %+v", err, plan)
	}
	if _, err := r.svc.Build(r.ctx, "intruder", "ws1"); err == nil {
		t.Fatal("another owner cannot export")
	}
}

// If the Docker CLI is present, the real tool must accept what was generated
// (PRD F25: the compose file starts the services in a standard environment).
func TestComposeIsAcceptedByDockerComposeConfig(t *testing.T) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker CLI not installed")
	}
	if out, err := exec.Command(bin, "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose plugin not available: %s", out)
	}
	r := newRig(t, map[string]string{})
	for k, v := range project {
		r.fs.files[k] = v
	}
	mongo := r.card("mongodb", "installed", "running", nil)
	r.card("rabbitmq", "installed", "running", nil)
	redis := r.card("redis", "connected", "connected", nil)
	_, _ = r.st.UpdateComponent(r.ctx, redis.ID, func(c *domain.Component) error { c.ConnName = "cache"; return nil })
	nest := r.card("nestjs", "installed", "ready", nil)
	react := r.card("react", "installed", "ready", nil)
	r.link(t, nest, mongo, "")
	r.link(t, nest, redis, "")
	r.link(t, react, nest, "api")
	plan, err := r.svc.Build(r.ctx, "u1", "ws1")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, f := range plan.Files {
		p := filepath.Join(dir, f.Path)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "compose", "-f", filepath.Join(dir, "docker-compose.yml"), "config", "--quiet")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker compose config rejected the generated file: %v\n%s\n%s", err, out, plan.Files[0].Content)
	}
}
