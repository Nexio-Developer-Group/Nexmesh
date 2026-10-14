package codeagent

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

	"github.com/nexio-developer-group/nexmesh/server/internal/architecture"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
	"github.com/nexio-developer-group/nexmesh/server/internal/modelprovider"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

const dbSecret = "postgres://app:S3CR3T-PASSWORD@db:5432/app"

type fakeFS struct {
	mu    sync.Mutex
	files map[string]string
	ran   []string
}

func (f *fakeFS) List(_ context.Context, _, dir string) ([]wsfs.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := ""
	if dir != "." && dir != "" {
		prefix = dir + "/"
	}
	seen := map[string]bool{}
	var out []wsfs.Entry
	for p, c := range f.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name, _, nested := strings.Cut(rest, "/")
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
func (f *fakeFS) Read(_ context.Context, _, p string) (*wsfs.Text, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.files[p]
	if !ok {
		return nil, wsfs.ErrNotFound
	}
	return &wsfs.Text{Content: c}, nil
}
func (f *fakeFS) Write(_ context.Context, _, p, c string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[p] = c
	return nil
}
func (f *fakeFS) Delete(_ context.Context, _, p string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, p)
	return nil
}
func (f *fakeFS) Mkdir(context.Context, string, string) error { return nil }
func (f *fakeFS) Run(_ context.Context, _, cmd string, _ int) (*wsfs.RunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, cmd)
	return &wsfs.RunResult{Stdout: "ok from " + cmd + " " + dbSecret}, nil
}

type scripted struct {
	mu    sync.Mutex
	steps []func(req modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error)
	reqs  []modelprovider.ChatRequest
}

func (m *scripted) Chat(ctx context.Context, req modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error) {
	m.mu.Lock()
	m.reqs = append(m.reqs, req)
	i := len(m.reqs) - 1
	var f func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error)
	if i < len(m.steps) {
		f = m.steps[i]
	}
	m.mu.Unlock()
	if f == nil {
		return &modelprovider.ChatResponse{Text: "done"}, nil
	}
	return f(req)
}

func say(t string) func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error) {
	return func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error) {
		return &modelprovider.ChatResponse{Text: t, InputTokens: 10, OutputTokens: 5}, nil
	}
}

func use(name string, args map[string]any) func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error) {
	return func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error) {
		return &modelprovider.ChatResponse{ToolCalls: []modelprovider.ToolCall{{ID: "c-" + name, Name: name, Arguments: args}}}, nil
	}
}

type archStub struct{}

func (archStub) Build(context.Context, string, string, map[string]bool) (*architecture.View, error) {
	return &architecture.View{Nodes: []architecture.Node{{ID: "n1", Name: "Redis", Layer: "data", Mode: "installed", State: "running"}, {ID: "n2", Name: "FastAPI", Layer: "backend", Mode: "installed", State: "running"}},
		Edges: []architecture.Edge{{ID: "e1", From: "n2", To: "n1", State: "ok", Variables: 1}}}, nil
}

type varsStub struct{}

func (varsStub) List(context.Context, string, string) (*envvars.Listing, error) {
	return &envvars.Listing{Variables: []envvars.Row{{Name: "STRIPE_KEY", Secret: true, Value: "sk_live_SHOULD_NEVER_APPEAR"}, {Name: "LOG_LEVEL", Value: "debug-value-xyz"}},
		Generated: []envvars.Generated{{Name: "REDIS_URL", ComponentName: "Redis"}}}, nil
}

type fx struct {
	svc   *Service
	fs    *fakeFS
	st    store.Store
	model *scripted
	ctx   context.Context
	ws    string
}

func newFx(t *testing.T, steps ...func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error)) *fx {
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "orders", OS: "ubuntu-24.04", Status: domain.WSReady,
		Runtimes: map[string]domain.RuntimeState{"python": {Active: "3.12.3"}}})
	fs := &fakeFS{files: map[string]string{"main.py": "print('hi')\n", ".env": "DATABASE_URL=" + dbSecret + "\n", "src/util.py": "def helper():\n    return 1\n", "id_rsa": "PRIVATE KEY"}}
	aud := audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cs := changeset.New(st, fs, aud)
	m := &scripted{steps: steps}
	svc := New(st, fs, cs, aud, func(context.Context, string, string, string) (Model, error) { return m, nil }).
		WithContext(archStub{}, varsStub{}).
		WithSecretValues(func(context.Context, string) []string { return []string{dbSecret} })
	return &fx{svc: svc, fs: fs, st: st, model: m, ctx: ctx, ws: "ws1"}
}

func (f *fx) session(t *testing.T) *Session {
	s, err := f.svc.CreateSession(f.ctx, "u1", f.ws, "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fx) send(t *testing.T, s *Session, text string) *Session {
	t.Helper()
	if _, err := f.svc.Send(f.ctx, "u1", f.ws, s.ID, text); err != nil {
		t.Fatal(err)
	}
	f.svc.Wait(s.ID)
	out, err := f.svc.GetSession(f.ctx, "u1", f.ws, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *fx) auditActions(wsID string) []string {
	evs, _ := f.st.ListAudit(f.ctx, wsID, 1000)
	var out []string
	for _, e := range evs {
		out = append(out, e.Action)
	}
	return out
}

func has(l []string, a string) bool {
	for _, x := range l {
		if x == a {
			return true
		}
	}
	return false
}

func vcode(err error) string {
	var ve *runtime.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func TestContextNeverCarriesSecretValues(t *testing.T) {
	f := newFx(t, say("hello"))
	c, err := f.svc.BuildContext(f.ctx, "u1", f.ws)
	if err != nil {
		t.Fatal(err)
	}
	text := c.Render()
	for _, bad := range []string{"sk_live_SHOULD_NEVER_APPEAR", "debug-value-xyz", "S3CR3T", "postgres://"} {
		if strings.Contains(text, bad) {
			t.Fatalf("context leaks %q:\n%s", bad, text)
		}
	}
	for _, want := range []string{"orders", "Redis", "FastAPI -> Redis", "STRIPE_KEY (secret)", "LOG_LEVEL", "REDIS_URL", "python 3.12.3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context lacks %q:\n%s", want, text)
		}
	}
	// And the system prompt actually sent to the model carries it.
	s := f.session(t)
	f.send(t, s, "what is here?")
	if !strings.Contains(f.model.reqs[0].System, "STRIPE_KEY") || strings.Contains(f.model.reqs[0].System, "sk_live") {
		t.Fatalf("system prompt: %s", f.model.reqs[0].System)
	}
}

func TestReadSearchAndSecretFilesAreForbiddenAndRecorded(t *testing.T) {
	f := newFx(t,
		use(ToolRead, map[string]any{"path": "main.py"}),
		use(ToolRead, map[string]any{"path": ".env"}),
		use(ToolRead, map[string]any{"path": "../../etc/passwd"}),
		use(ToolList, map[string]any{"path": "."}),
		use(ToolSearch, map[string]any{"query": "helper"}),
		use(ToolSearch, map[string]any{"query": "S3CR3T"}),
		say("finished"))
	s := f.send(t, f.session(t), "look around")
	calls := []Call{}
	for _, tn := range s.Turns {
		calls = append(calls, tn.Calls...)
	}
	if len(calls) != 6 || s.Status != StatusIdle {
		t.Fatalf("calls %d status %s", len(calls), s.Status)
	}
	if calls[0].Status != CallOK || !strings.Contains(calls[0].Result, "print('hi')") {
		t.Fatalf("read: %+v", calls[0])
	}
	if calls[1].Status != CallDenied || calls[2].Status != CallDenied {
		t.Fatalf(".env and path escape must be denied: %+v %+v", calls[1], calls[2])
	}
	if strings.Contains(calls[1].Result, "S3CR3T") {
		t.Fatal("denied result leaked")
	}
	if !strings.Contains(calls[3].Result, "main.py") || strings.Contains(calls[3].Result, "id_rsa") || strings.Contains(calls[3].Result, ".env") || !strings.Contains(calls[3].Result, "hidden") {
		t.Fatalf("list: %q", calls[3].Result)
	}
	if !strings.Contains(calls[4].Result, "src/util.py:1") {
		t.Fatalf("search: %q", calls[4].Result)
	}
	if !strings.Contains(calls[5].Result, "No matches") {
		t.Fatalf("search must skip secret files: %q", calls[5].Result)
	}
	acts := f.auditActions(f.ws)
	if !has(acts, "agent.action_denied") || !has(acts, "agent.run_started") || !has(acts, "agent.run_finished") {
		t.Fatalf("audit: %v", acts)
	}
	// The conversation sent back to the model never contained the secret.
	for _, r := range f.model.reqs {
		for _, m := range r.Messages {
			if strings.Contains(m.Content, "S3CR3T") {
				t.Fatal("secret reached the model")
			}
		}
	}
}

func TestProposeChangesStagesAndNeverWrites(t *testing.T) {
	f := newFx(t,
		use(ToolPropose, map[string]any{"title": "Add helper", "summary": "s", "changes": []any{map[string]any{"path": "main.py", "content": "print('hello')\n"}}}),
		say("I proposed a change."))
	s := f.send(t, f.session(t), "change the greeting")
	var call Call
	for _, tn := range s.Turns {
		if len(tn.Calls) > 0 {
			call = tn.Calls[0]
		}
	}
	if call.Status != CallOK || call.ChangeSetID == "" {
		t.Fatalf("%+v", call)
	}
	if f.fs.files["main.py"] != "print('hi')\n" {
		t.Fatal("agent wrote a file directly")
	}
	v, err := f.svc.changes.Get(f.ctx, "u1", f.ws, call.ChangeSetID)
	if err != nil || v.Actor != domain.ActorAgent || v.Status != changeset.StatusProposed {
		t.Fatalf("%v %+v", err, v)
	}
}

func TestProposingToSecretFilesIsDenied(t *testing.T) {
	f := newFx(t,
		use(ToolPropose, map[string]any{"title": "x", "changes": []any{map[string]any{"path": ".env", "content": "A=1"}}}),
		say("ok"))
	s := f.send(t, f.session(t), "write env")
	if c := s.Turns[1].Calls[0]; c.Status != CallDenied {
		t.Fatalf("%+v", c)
	}
	if sets, _ := f.svc.changes.List(f.ctx, "u1", f.ws); len(sets) != 0 {
		t.Fatal("nothing should be staged")
	}
}

func TestPermissionLevelsLimitToolsAndCalls(t *testing.T) {
	f := newFx(t, say("hi"))
	if _, err := f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelRead}); err != nil {
		t.Fatal(err)
	}
	f.send(t, f.session(t), "hi")
	names := func(r modelprovider.ChatRequest) []string {
		var n []string
		for _, d := range r.Tools {
			n = append(n, d.Name)
		}
		return n
	}
	if got := names(f.model.reqs[0]); has(got, ToolPropose) || has(got, ToolRun) {
		t.Fatalf("read level offered %v", got)
	}
	// A model that calls a tool it was not offered is refused and recorded.
	f2 := newFx(t, use(ToolPropose, map[string]any{"title": "t", "changes": []any{map[string]any{"path": "a", "content": "b"}}}),
		use(ToolRun, map[string]any{"command": "ls"}), use("delete_workspace", map[string]any{}), say("done"))
	f2.svc.SetSettings(f2.ctx, "u1", "ws1", Settings{Level: LevelRead})
	s := f2.send(t, f2.session(t), "do things")
	for i := 1; i <= 3; i++ {
		if c := s.Turns[i].Calls[0]; c.Status != CallDenied {
			t.Fatalf("call %d (%s): %+v", i, c.Name, c)
		}
	}
	if len(f2.fs.ran) != 0 {
		t.Fatal("command ran at read level")
	}
	if _, err := f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: "root"}); vcode(err) != "invalid_level" {
		t.Fatal(err)
	}
	if !has(f.auditActions(f.ws), "agent.permissions_changed") {
		t.Fatal("permission change not audited")
	}
}

func TestSafeCommandsRunRiskyWaitForApprovalForbiddenNeverRun(t *testing.T) {
	f := newFx(t,
		use(ToolRun, map[string]any{"command": "go test ./..."}),
		use(ToolRun, map[string]any{"command": "printenv"}),
		use(ToolRun, map[string]any{"command": "rm -rf build"}),
		say("finished"))
	f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelExecute})
	s := f.session(t)
	if _, err := f.svc.Send(f.ctx, "u1", f.ws, s.ID, "run things"); err != nil {
		t.Fatal(err)
	}
	// Wait for the risky command to pause the run.
	var cur *Session
	for i := 0; i < 200; i++ {
		cur, _ = f.svc.GetSession(f.ctx, "u1", f.ws, s.ID)
		if cur.Status == StatusAwaiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cur.Status != StatusAwaiting || cur.Pending == nil || cur.Pending.Command != "rm -rf build" {
		t.Fatalf("expected approval wait: %+v", cur)
	}
	if has(f.fs.ran, "rm -rf build") || has(f.fs.ran, "printenv") {
		t.Fatalf("risky/forbidden ran early: %v", f.fs.ran)
	}
	if err := f.svc.Approve(f.ctx, "u1", f.ws, s.ID, "wrong-id", true); vcode(err) != "stale_approval" {
		t.Fatal(err)
	}
	if err := f.svc.Approve(f.ctx, "u2", f.ws, s.ID, cur.Pending.CallID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("approval by another user")
	}
	if err := f.svc.Approve(f.ctx, "u1", f.ws, s.ID, cur.Pending.CallID, true); err != nil {
		t.Fatal(err)
	}
	f.svc.Wait(s.ID)
	done, _ := f.svc.GetSession(f.ctx, "u1", f.ws, s.ID)
	if done.Status != StatusIdle {
		t.Fatalf("%s %s", done.Status, done.Error)
	}
	if !has(f.fs.ran, "go test ./...") || !has(f.fs.ran, "rm -rf build") || has(f.fs.ran, "printenv") {
		t.Fatalf("ran: %v", f.fs.ran)
	}
	if c := done.Turns[2].Calls[0]; c.Status != CallDenied {
		t.Fatalf("printenv: %+v", c)
	}
	// Output is masked of known secret values before the model sees it.
	if strings.Contains(done.Turns[1].Calls[0].Result, "S3CR3T") || !strings.Contains(done.Turns[1].Calls[0].Result, "[hidden]") {
		t.Fatalf("not masked: %q", done.Turns[1].Calls[0].Result)
	}
	acts := f.auditActions(f.ws)
	if !has(acts, "agent.command_approved") || !has(acts, "agent.command_run") || !has(acts, "agent.action_denied") {
		t.Fatalf("audit: %v", acts)
	}
}

func TestDecliningARiskyCommand(t *testing.T) {
	f := newFx(t, use(ToolRun, map[string]any{"command": "npm install left-pad"}), say("ok, skipping"))
	f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelExecute})
	s := f.session(t)
	f.svc.Send(f.ctx, "u1", f.ws, s.ID, "install it")
	var cur *Session
	for i := 0; i < 200; i++ {
		cur, _ = f.svc.GetSession(f.ctx, "u1", f.ws, s.ID)
		if cur.Status == StatusAwaiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := f.svc.Approve(f.ctx, "u1", f.ws, s.ID, cur.Pending.CallID, false); err != nil {
		t.Fatal(err)
	}
	f.svc.Wait(s.ID)
	done, _ := f.svc.GetSession(f.ctx, "u1", f.ws, s.ID)
	if len(f.fs.ran) != 0 || done.Turns[1].Calls[0].Status != CallRejected || done.Status != StatusIdle {
		t.Fatalf("%v %+v", f.fs.ran, done.Turns[1].Calls[0])
	}
}

func TestStopCancelsAWaitingRun(t *testing.T) {
	f := newFx(t, use(ToolRun, map[string]any{"command": "make deploy"}))
	f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelExecute})
	s := f.session(t)
	f.svc.Send(f.ctx, "u1", f.ws, s.ID, "deploy")
	for i := 0; i < 200; i++ {
		if cur, _ := f.svc.GetSession(f.ctx, "u1", f.ws, s.ID); cur.Status == StatusAwaiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := f.svc.Stop(f.ctx, "u1", f.ws, s.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.Wait(s.ID)
	done, _ := f.svc.GetSession(f.ctx, "u1", f.ws, s.ID)
	if done.Status != StatusIdle || len(f.fs.ran) != 0 {
		t.Fatalf("%s %v", done.Status, f.fs.ran)
	}
}

func TestModelErrorsSurfaceSafelyAndAllowRetry(t *testing.T) {
	f := newFx(t, func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error) {
		return nil, &modelprovider.Error{Kind: modelprovider.KindUnauthorized, Msg: "The key was rejected (invalid or revoked)."}
	}, say("recovered"))
	s := f.send(t, f.session(t), "hi")
	if s.Status != StatusError || !strings.Contains(s.Error, "rejected") {
		t.Fatalf("%s %q", s.Status, s.Error)
	}
	s = f.send(t, s, "try again")
	if s.Status != StatusIdle || s.Turns[len(s.Turns)-1].Content != "recovered" {
		t.Fatalf("%s %+v", s.Status, s.Turns)
	}
}

func TestStepLimitStopsRunawayLoops(t *testing.T) {
	loopStep := use(ToolList, map[string]any{})
	steps := make([]func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error), MaxSteps+5)
	for i := range steps {
		steps[i] = loopStep
	}
	f := newFx(t, steps...)
	s := f.send(t, f.session(t), "loop")
	if s.Status != StatusIdle || len(f.model.reqs) != MaxSteps || !strings.Contains(s.Turns[len(s.Turns)-1].Content, "maximum number of steps") {
		t.Fatalf("%s %d", s.Status, len(f.model.reqs))
	}
}

func TestSessionRulesAndOwnership(t *testing.T) {
	f := newFx(t, say("a"), say("b"))
	s := f.session(t)
	if _, err := f.svc.Send(f.ctx, "u1", f.ws, s.ID, "  "); vcode(err) != "empty_message" {
		t.Fatal(err)
	}
	if _, err := f.svc.GetSession(f.ctx, "u2", f.ws, s.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("other user must not read the session")
	}
	if _, err := f.svc.Send(f.ctx, "u2", f.ws, s.ID, "hi"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("other user must not send")
	}
	got := f.send(t, s, "first question")
	if got.Title != "first question" || got.InputTokens != 10 {
		t.Fatalf("%+v", got)
	}
	if err := f.svc.DeleteSession(f.ctx, "u1", f.ws, s.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.svc.ListSessions(f.ctx, "u1", f.ws); len(l) != 0 {
		t.Fatal("not deleted")
	}
}

func TestRestartMarksRunningSessionsInterrupted(t *testing.T) {
	f := newFx(t)
	s := f.session(t)
	s.Status = StatusRunning
	f.svc.save(f.ctx, s)
	got, _ := f.svc.GetSession(f.ctx, "u1", f.ws, s.ID)
	if got.Status != StatusInterrupted {
		t.Fatal(got.Status)
	}
	if l, _ := f.svc.ListSessions(f.ctx, "u1", f.ws); l[0].Status != StatusInterrupted {
		t.Fatal(l[0].Status)
	}
}

func TestOnlyOneRunAtATime(t *testing.T) {
	release := make(chan struct{})
	f := newFx(t, func(modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error) {
		<-release
		return &modelprovider.ChatResponse{Text: "x"}, nil
	})
	s := f.session(t)
	f.svc.Send(f.ctx, "u1", f.ws, s.ID, "one")
	if _, err := f.svc.Send(f.ctx, "u1", f.ws, s.ID, "two"); vcode(err) != "busy" {
		t.Fatal(err)
	}
	close(release)
	f.svc.Wait(s.ID)
}

type fakeCanvas struct {
	mu      sync.Mutex
	drafts  []string
	wires   [][2]string
	wireErr error
}

func (c *fakeCanvas) Catalog(id string) (string, bool) {
	if id == "redis" {
		return "Redis", true
	}
	return "", false
}
func (c *fakeCanvas) AddDraft(_ context.Context, _, _, id string, _ int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drafts = append(c.drafts, id)
	return "comp_new", nil
}
func (c *fakeCanvas) CheckWire(context.Context, string, string, string, string) error {
	return c.wireErr
}
func (c *fakeCanvas) AddWire(_ context.Context, _, _, from, to string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wires = append(c.wires, [2]string{from, to})
	return "wire_new", nil
}

func TestCanvasProposalsAreSuggestionsUntilAccepted(t *testing.T) {
	f := newFx(t,
		use(ToolCard, map[string]any{"catalog_id": "redis", "reason": "cache"}),
		use(ToolCard, map[string]any{"catalog_id": "nonexistent", "reason": "x"}),
		use(ToolWire, map[string]any{"from": "FastAPI", "to": "redis", "reason": "uses cache"}),
		use(ToolWire, map[string]any{"from": "Ghost", "to": "redis", "reason": "x"}),
		say("proposed"))
	cv := &fakeCanvas{}
	f.svc.WithCanvas(cv)
	s := f.send(t, f.session(t), "improve the architecture")
	var calls []Call
	for _, tn := range s.Turns {
		calls = append(calls, tn.Calls...)
	}
	if len(calls) != 4 || calls[0].Status != CallOK || calls[0].ProposalID == "" || calls[1].Status != CallError || calls[2].Status != CallOK || calls[3].Status != CallError {
		t.Fatalf("%+v", calls)
	}
	if len(cv.drafts) != 0 || len(cv.wires) != 0 {
		t.Fatal("the agent changed the canvas by itself")
	}
	props, _ := f.svc.ListProposals(f.ctx, "u1", f.ws)
	if len(props) != 2 {
		t.Fatalf("%d", len(props))
	}
	var cardP, wireP *Proposal
	for _, p := range props {
		if p.Kind == ProposalCard {
			cardP = p
		} else {
			wireP = p
		}
	}
	if wireP.FromID != "n2" || wireP.ToID != "n1" || wireP.FromName != "FastAPI" {
		t.Fatalf("%+v", wireP)
	}
	if _, err := f.svc.AcceptProposal(f.ctx, "u2", f.ws, cardP.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ownership")
	}
	got, err := f.svc.AcceptProposal(f.ctx, "u1", f.ws, cardP.ID)
	if err != nil || got.Status != ProposalAccepted || got.ResultID != "comp_new" || len(cv.drafts) != 1 || cv.drafts[0] != "redis" {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := f.svc.AcceptProposal(f.ctx, "u1", f.ws, cardP.ID); vcode(err) != "already_decided" {
		t.Fatal(err)
	}
	if got, _ := f.svc.RejectProposal(f.ctx, "u1", f.ws, wireP.ID); got.Status != ProposalRejected || len(cv.wires) != 0 {
		t.Fatalf("%+v", got)
	}
	acts := f.auditActions(f.ws)
	if !has(acts, "agent.canvas_proposal") || !has(acts, "agent.canvas_proposal_accepted") || !has(acts, "agent.canvas_proposal_rejected") {
		t.Fatalf("%v", acts)
	}
}

func TestCanvasToolsOnlyOfferedWhenAllowed(t *testing.T) {
	f := newFx(t, say("x"))
	f.svc.WithCanvas(&fakeCanvas{})
	f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelRead})
	f.send(t, f.session(t), "hi")
	for _, d := range f.model.reqs[0].Tools {
		if d.Name == ToolCard || d.Name == ToolWire {
			t.Fatal("read level may not propose canvas changes")
		}
	}
	f2 := newFx(t, use(ToolCard, map[string]any{"catalog_id": "redis", "reason": "x"}), say("x"))
	f2.svc.WithCanvas(&fakeCanvas{})
	f2.svc.SetSettings(f2.ctx, "u1", "ws1", Settings{Level: LevelRead})
	s := f2.send(t, f2.session(t), "add redis")
	if s.Turns[1].Calls[0].Status != CallDenied {
		t.Fatalf("%+v", s.Turns[1].Calls[0])
	}
}

func TestAutoAcceptIsOptInAndRespectsGates(t *testing.T) {
	propose := use(ToolPropose, map[string]any{"title": "Auto", "changes": []any{map[string]any{"path": "main.py", "content": "print('auto')\n"}}})
	// Off by default: staged only.
	f := newFx(t, propose, say("ok"))
	f.send(t, f.session(t), "edit")
	if f.fs.files["main.py"] != "print('hi')\n" {
		t.Fatal("applied without opt-in")
	}
	// On: applied, with a checkpoint so it can be reverted.
	f = newFx(t, propose, say("ok"))
	f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelPropose, AutoAccept: true})
	s := f.send(t, f.session(t), "edit")
	if f.fs.files["main.py"] != "print('auto')\n" {
		t.Fatalf("not applied: %q", f.fs.files["main.py"])
	}
	if !strings.Contains(s.Turns[1].Calls[0].Result, "Applied") {
		t.Fatalf("%+v", s.Turns[1].Calls[0])
	}
	cps, _ := f.svc.changes.Checkpoints(f.ctx, "u1", f.ws)
	if len(cps) != 1 {
		t.Fatal("auto-accept must still checkpoint")
	}
	if !has(f.auditActions(f.ws), "agent.auto_accepted") {
		t.Fatal("not audited")
	}
	// Required gates still apply: it stays staged.
	f = newFx(t, propose, say("ok"))
	f.svc.changes.WithGates(stubGater{changeset.GateVerdict{Configured: true}})
	f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelPropose, AutoAccept: true})
	s = f.send(t, f.session(t), "edit")
	if f.fs.files["main.py"] != "print('hi')\n" || !strings.Contains(s.Turns[1].Calls[0].Result, "not applied automatically") {
		t.Fatalf("gates ignored: %q %+v", f.fs.files["main.py"], s.Turns[1].Calls[0])
	}
	// Read level cannot auto-accept anything.
	f = newFx(t, propose, say("ok"))
	f.svc.SetSettings(f.ctx, "u1", f.ws, Settings{Level: LevelRead, AutoAccept: true})
	f.send(t, f.session(t), "edit")
	if f.fs.files["main.py"] != "print('hi')\n" {
		t.Fatal("read level wrote a file")
	}
}

type stubGater struct{ v changeset.GateVerdict }

func (g stubGater) Verdict(context.Context, string, string, string) changeset.GateVerdict { return g.v }
