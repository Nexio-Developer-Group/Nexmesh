package gates

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

type memFS struct {
	mu    sync.Mutex
	files map[string]string
	// run decides a command's outcome; it may read the files.
	run func(cmd string, files map[string]string) *wsfs.RunResult
}

func (m *memFS) List(_ context.Context, _, dir string) ([]wsfs.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := ""
	if dir != "." && dir != "" {
		prefix = dir + "/"
	}
	seen := map[string]bool{}
	var out []wsfs.Entry
	for p, c := range m.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		name, _, nested := strings.Cut(strings.TrimPrefix(p, prefix), "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		e := wsfs.Entry{Name: name, Path: prefix + name, Type: "file", Size: int64(len(c))}
		if nested {
			e.Type = "dir"
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (m *memFS) Read(_ context.Context, _, p string) (*wsfs.Text, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.files[p]
	if !ok {
		return nil, wsfs.ErrNotFound
	}
	return &wsfs.Text{Content: c}, nil
}
func (m *memFS) Write(_ context.Context, _, p, c string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[p] = c
	return nil
}
func (m *memFS) Delete(_ context.Context, _, p string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, p)
	return nil
}
func (m *memFS) Mkdir(context.Context, string, string) error { return nil }
func (m *memFS) Run(ctx context.Context, _, cmd string, _ int) (*wsfs.RunResult, error) {
	m.mu.Lock()
	snapshot := map[string]string{}
	for k, v := range m.files {
		snapshot[k] = v
	}
	run := m.run
	m.mu.Unlock()
	if strings.HasPrefix(cmd, "sleep") {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if run != nil {
		return run(cmd, snapshot), nil
	}
	return &wsfs.RunResult{Stdout: "ok"}, nil
}

type fx struct {
	svc *Service
	cs  *changeset.Service
	fs  *memFS
	ctx context.Context
}

func newFx(t *testing.T) *fx {
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	fs := &memFS{files: map[string]string{"main.go": "package main\n"}}
	aud := audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cs := changeset.New(st, fs, aud)
	svc := New(st, fs, aud, cs)
	cs.WithGates(svc)
	return &fx{svc: svc, cs: cs, fs: fs, ctx: ctx}
}

func vcode(err error) string {
	var ve *runtime.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func (f *fx) config(t *testing.T, gs ...Gate) {
	t.Helper()
	if _, err := f.svc.SaveConfig(f.ctx, "u1", "ws1", Config{Gates: gs}); err != nil {
		t.Fatal(err)
	}
}

func gate(id, kind, cmd, policy string) Gate {
	return Gate{ID: id, Name: strings.ToUpper(id), Kind: kind, Command: cmd, Policy: policy, Enabled: true}
}

func (f *fx) runAndWait(t *testing.T, in StartInput) *Run {
	t.Helper()
	r, err := f.svc.Start(f.ctx, "u1", "ws1", in)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.Wait(r.ID)
	out, err := f.svc.GetRun(f.ctx, "u1", "ws1", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDetectGoNodePython(t *testing.T) {
	f := newFx(t)
	f.fs.files = map[string]string{
		"go.mod": "module x\n", "package.json": `{"scripts":{"lint":"eslint .","test":"vitest","build":"vite build","typecheck":"tsc -b"},"dependencies":{"a":"1.0.0"}}`,
		"pnpm-lock.yaml": "lock", "pyproject.toml": "[tool.ruff]\n[tool.pytest.ini_options]\n",
	}
	gs, err := f.svc.Detect(f.ctx, "u1", "ws1")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Gate{}
	for _, g := range gs {
		byID[g.ID] = g
	}
	for id, cmd := range map[string]string{"go-test": "go test ./...", "node-lint": "pnpm run lint", "node-test": "pnpm run test", "node-build": "pnpm run build", "node-typecheck": "pnpm run typecheck", "py-lint": "ruff check .", "py-test": "pytest -q", "secrets": BuiltinSecrets} {
		if byID[id].Command != cmd {
			t.Errorf("%s: %q want %q", id, byID[id].Command, cmd)
		}
		if byID[id].Detected == "" {
			t.Errorf("%s has no reason", id)
		}
	}
	if byID["deps"].Policy != PolicyAdvisory || byID["go-test"].Policy != PolicyRequired {
		t.Fatal("default policies")
	}
	if cfg, _ := f.svc.GetConfig(f.ctx, "u1", "ws1"); cfg.Confirmed || len(cfg.Gates) != 0 {
		t.Fatal("detection must not save anything")
	}
	// An empty project still gets the built-in checks, and the npm default script is ignored.
	f.fs.files = map[string]string{"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`}
	gs, _ = f.svc.Detect(f.ctx, "u1", "ws1")
	for _, g := range gs {
		if g.ID == "node-test" {
			t.Fatal("placeholder test script suggested")
		}
	}
	if len(gs) != 2 {
		t.Fatalf("%d", len(gs))
	}
	if _, err := f.svc.Detect(f.ctx, "intruder", "ws1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ownership")
	}
}

func TestSaveConfigValidation(t *testing.T) {
	f := newFx(t)
	ok := gate("test", KindTest, "go test ./...", PolicyRequired)
	cases := map[string]Gate{}
	mut := func(code string, fn func(g *Gate)) {
		g := ok
		fn(&g)
		cases[code] = g
	}
	mut("invalid_name", func(g *Gate) { g.Name = "" })
	mut("invalid_kind", func(g *Gate) { g.Kind = "weird" })
	mut("invalid_policy", func(g *Gate) { g.Policy = "maybe" })
	mut("invalid_command", func(g *Gate) { g.Command = "" })
	mut("invalid_timeout", func(g *Gate) { g.TimeoutSec = 99999 })
	mut("invalid_id", func(g *Gate) { g.ID = "Bad ID!" })
	for code, g := range cases {
		if _, err := f.svc.SaveConfig(f.ctx, "u1", "ws1", Config{Gates: []Gate{g}}); vcode(err) != code {
			t.Errorf("%s: %v", code, err)
		}
	}
	if _, err := f.svc.SaveConfig(f.ctx, "u1", "ws1", Config{Gates: []Gate{ok, ok}}); vcode(err) != "duplicate_gate" {
		t.Fatal("duplicate")
	}
	bad := ok
	bad.Command = "builtin:rm-rf"
	if _, err := f.svc.SaveConfig(f.ctx, "u1", "ws1", Config{Gates: []Gate{bad}}); vcode(err) != "invalid_command" {
		t.Fatal("unknown builtin")
	}
	c, err := f.svc.SaveConfig(f.ctx, "u1", "ws1", Config{Gates: []Gate{ok}})
	if err != nil || !c.Confirmed || c.Gates[0].TimeoutSec != DefaultTimeout {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := f.svc.SaveConfig(f.ctx, "intruder", "ws1", Config{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ownership")
	}
}

func TestRunStatusesPolicyAndLogs(t *testing.T) {
	f := newFx(t)
	f.fs.run = func(cmd string, _ map[string]string) *wsfs.RunResult {
		switch cmd {
		case "lint":
			return &wsfs.RunResult{ExitCode: 1, Stdout: "main.go:1: unused variable", Stderr: "details"}
		case "slow":
			return &wsfs.RunResult{TimedOut: true, ExitCode: -1}
		}
		return &wsfs.RunResult{Stdout: "all good"}
	}
	f.config(t, gate("fmt", KindFormat, "fmt", PolicyAdvisory), gate("lint", KindLint, "lint", PolicyAdvisory), gate("test", KindTest, "test", PolicyRequired))
	r := f.runAndWait(t, StartInput{})
	if r.Status != RunDone || !r.Passed || len(r.RequiredFailed) != 0 {
		t.Fatalf("an advisory failure must not fail the run: %+v", r)
	}
	var lint Result
	for _, x := range r.Results {
		if x.GateID == "lint" {
			lint = x
		}
	}
	if lint.Status != ResFailed || lint.ExitCode != 1 || !strings.Contains(lint.Log, "unused variable") || !strings.Contains(lint.Log, "[stderr]") {
		t.Fatalf("%+v", lint)
	}
	f.config(t, gate("test", KindTest, "test", PolicyRequired), gate("slow", KindTest, "slow", PolicyRequired))
	r = f.runAndWait(t, StartInput{})
	if r.Passed || len(r.RequiredFailed) != 1 || r.RequiredFailed[0] != "SLOW" || r.Results[1].Status != ResTimeout {
		t.Fatalf("%+v", r)
	}
	// Selecting gates by id, and disabled gates are skipped.
	g := gate("off", KindLint, "lint", PolicyRequired)
	g.Enabled = false
	f.config(t, gate("test", KindTest, "test", PolicyRequired), g)
	if r = f.runAndWait(t, StartInput{}); len(r.Results) != 1 {
		t.Fatalf("disabled gate ran: %d", len(r.Results))
	}
	if _, err := f.svc.Start(f.ctx, "u1", "ws1", StartInput{GateIDs: []string{"off"}}); vcode(err) != "no_gates" {
		t.Fatal(err)
	}
	runs, _ := f.svc.ListRuns(f.ctx, "u1", "ws1", "")
	if len(runs) != 3 || runs[0].Results[0].Log != "" {
		t.Fatalf("runs: %d", len(runs))
	}
	if _, err := f.svc.GetRun(f.ctx, "intruder", "ws1", r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ownership")
	}
}

func TestOneRunAtATimeAndCancel(t *testing.T) {
	f := newFx(t)
	f.config(t, gate("slow", KindTest, "sleep 5", PolicyRequired), gate("after", KindTest, "echo", PolicyRequired))
	r, err := f.svc.Start(f.ctx, "u1", "ws1", StartInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(f.ctx, "u1", "ws1", StartInput{}); vcode(err) != "run_in_progress" {
		t.Fatal(err)
	}
	if err := f.svc.Cancel(f.ctx, "u1", "ws1", r.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.Wait(r.ID)
	out, _ := f.svc.GetRun(f.ctx, "u1", "ws1", r.ID)
	if out.Status != RunCancelled || out.Passed || out.Results[len(out.Results)-1].Status != ResSkipped {
		t.Fatalf("%+v", out)
	}
}

func TestGatesRunAgainstTheProposedChangeAndBlockAcceptance(t *testing.T) {
	f := newFx(t)
	f.fs.run = func(cmd string, files map[string]string) *wsfs.RunResult {
		if strings.Contains(files["main.go"], "BROKEN") {
			return &wsfs.RunResult{ExitCode: 1, Stdout: "build failed on the proposed content"}
		}
		return &wsfs.RunResult{Stdout: "fine"}
	}
	f.config(t, gate("build", KindBuild, "go build", PolicyRequired))
	prop := func(content string) *changeset.View {
		set, err := f.cs.Propose(f.ctx, "u1", "ws1", domain.ActorAgent, "ag", changeset.ProposeInput{Title: "t", Changes: []changeset.Change{{Path: "main.go", Content: &content}}})
		if err != nil {
			t.Fatal(err)
		}
		v, _ := f.cs.Get(f.ctx, "u1", "ws1", set.ID)
		return v
	}
	bad := prop("package main // BROKEN\n")
	if bad.Gate == nil || bad.Gate.Ran || !bad.Gate.Configured {
		t.Fatalf("%+v", bad.Gate)
	}
	if _, err := f.cs.Accept(f.ctx, "u1", "ws1", bad.ID, changeset.Decision{All: true}); vcode(err) != "gates_not_run" {
		t.Fatal(err)
	}
	r := f.runAndWait(t, StartInput{SetID: bad.ID})
	if r.Status != RunDone || r.Passed || r.SetHash != bad.Hash || r.Results[0].Status != ResFailed || !strings.Contains(r.Results[0].Log, "proposed content") {
		t.Fatalf("%+v", r)
	}
	if f.fs.files["main.go"] != "package main\n" {
		t.Fatalf("the trial leaked into the workspace: %q", f.fs.files["main.go"])
	}
	if _, err := f.cs.Accept(f.ctx, "u1", "ws1", bad.ID, changeset.Decision{All: true}); vcode(err) != "gates_failed" {
		t.Fatal(err)
	}
	if got, _ := f.cs.Get(f.ctx, "u1", "ws1", bad.ID); got.Gate == nil || !got.Gate.Ran || got.Gate.Passed || got.Gate.Failing[0] != "BUILD" {
		t.Fatalf("%+v", got.Gate)
	}
	// Overriding is possible, explicit and recorded.
	out, err := f.cs.Accept(f.ctx, "u1", "ws1", bad.ID, changeset.Decision{All: true, OverrideGates: "prototype, fixing next"})
	if err != nil || out.GateOverride == nil || out.GateOverride.Failing[0] != "BUILD" {
		t.Fatalf("%v %+v", err, out)
	}
	// A good change passes the gate and is accepted without override.
	f.fs.files["main.go"] = "package main\n"
	good := prop("package main // fine\n")
	if r := f.runAndWait(t, StartInput{SetID: good.ID}); !r.Passed {
		t.Fatalf("%+v", r)
	}
	if o, err := f.cs.Accept(f.ctx, "u1", "ws1", good.ID, changeset.Decision{All: true}); err != nil || o.GateOverride != nil {
		t.Fatalf("%v %+v", err, o)
	}
}

func TestRunDoesNotCountWhenGateConfigChanged(t *testing.T) {
	f := newFx(t)
	f.config(t, gate("build", KindBuild, "go build", PolicyRequired))
	c := "package main // x\n"
	set, _ := f.cs.Propose(f.ctx, "u1", "ws1", domain.ActorAgent, "ag", changeset.ProposeInput{Title: "t", Changes: []changeset.Change{{Path: "main.go", Content: &c}}})
	f.runAndWait(t, StartInput{SetID: set.ID})
	if v := f.svc.Verdict(f.ctx, "ws1", set.ID, set.Hash); !v.Ran || !v.Passed {
		t.Fatalf("%+v", v)
	}
	f.config(t, gate("build", KindBuild, "go build", PolicyRequired), gate("test", KindTest, "go test", PolicyRequired))
	if v := f.svc.Verdict(f.ctx, "ws1", set.ID, set.Hash); v.Ran {
		t.Fatalf("a new required gate must force a re-run: %+v", v)
	}
	// And a run for different content never counts.
	if v := f.svc.Verdict(f.ctx, "ws1", set.ID, "other-hash"); v.Ran {
		t.Fatal("hash mismatch counted")
	}
	// With no required gates nothing is configured, so nothing blocks.
	f.config(t, gate("lint", KindLint, "lint", PolicyAdvisory))
	if v := f.svc.Verdict(f.ctx, "ws1", set.ID, set.Hash); v.Configured {
		t.Fatal("advisory gates must not block")
	}
}

func TestSecretScanFindsRealSecretsWithoutPrintingThem(t *testing.T) {
	f := newFx(t)
	const aws = "AKIAIOSFODNN7ABCDEFG"
	f.fs.files = map[string]string{
		"src/config.py":     "KEY = '" + aws + "'\n",
		"src/ok.py":         "password = os.environ['PASSWORD']\napi_key = 'your-api-key-here-xxxxx'\n",
		".env":              "STRIPE=sk_live_abcdefghijklmnopqrstu\n",
		".env.example":      "STRIPE=sk_live_xxxxxxxxxxxxxxxxxxxx\n",
		"node_modules/a.js": "const k = 'AKIAIOSFODNN7ABCDEFG'\n",
		"deploy/db.yml":     "url: postgres://admin:hunter2pass@db.internal:5432/app\n",
	}
	f.config(t, gate("secrets", KindSecrets, BuiltinSecrets, PolicyRequired))
	r := f.runAndWait(t, StartInput{})
	log := r.Results[0].Log
	if r.Results[0].Status != ResFailed || !strings.Contains(log, "src/config.py:1") || !strings.Contains(log, "AWS access key") || !strings.Contains(log, "deploy/db.yml:1") {
		t.Fatalf("%+v", r.Results[0])
	}
	for _, bad := range []string{aws, "hunter2pass", "sk_live_abcdefghijklmnopqrstu"} {
		if strings.Contains(log, bad) {
			t.Fatalf("the log prints a secret: %s", log)
		}
	}
	if strings.Contains(log, "node_modules") || strings.Contains(log, ".env") || strings.Contains(log, "ok.py") {
		t.Fatalf("false positives: %s", log)
	}
	f.fs.files = map[string]string{"main.go": "package main\n"}
	if r := f.runAndWait(t, StartInput{}); r.Results[0].Status != ResPassed {
		t.Fatalf("%+v", r.Results[0])
	}
}

func TestDependencyHygiene(t *testing.T) {
	f := newFx(t)
	f.config(t, gate("deps", KindDeps, BuiltinDeps, PolicyAdvisory))
	f.fs.files = map[string]string{
		"package.json":     `{"dependencies":{"a":"*","b":"latest","c":"github:x/y","d":"1.2.3"}}`,
		"requirements.txt": "flask==3.0.0\nrequests\n# c\n",
		"go.mod":           "module x\nrequire a v1\n",
	}
	r := f.runAndWait(t, StartInput{})
	log := r.Results[0].Log
	for _, want := range []string{"no lockfile", `"*"`, `"latest"`, "installed from a URL", "requests", "go.sum is missing"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in:\n%s", want, log)
		}
	}
	if strings.Contains(log, "flask") || strings.Contains(log, " d ") {
		t.Fatalf("pinned dependencies flagged:\n%s", log)
	}
	if r.Results[0].Status != ResFailed || !r.Passed {
		t.Fatalf("advisory failure: %+v", r)
	}
	f.fs.files = map[string]string{"README.md": "x"}
	if r := f.runAndWait(t, StartInput{}); r.Results[0].Status != ResPassed {
		t.Fatal("nothing to check should pass")
	}
}

func TestRestartMarksRunningRunsInterrupted(t *testing.T) {
	f := newFx(t)
	_ = store.PutJSON(f.ctx, f.svc.st, KindRun, "ws1", "gr_x", &Run{ID: "gr_x", WorkspaceID: "ws1", Status: RunRunning, StartedAt: time.Now()})
	r, _ := f.svc.GetRun(f.ctx, "u1", "ws1", "gr_x")
	if r.Status != RunInterrupted {
		t.Fatal(r.Status)
	}
}
