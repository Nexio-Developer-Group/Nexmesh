// Package gates runs quality checks in the workspace and tells the change-set
// review whether an agent change may be accepted (PRD F23). Gates are the only
// enforcement mechanism: a required gate that fails blocks acceptance of an
// agent change set unless a person overrides it with a recorded reason.
// The configuration lives with the workspace, not in files the agent can edit.
package gates

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

const (
	KindConfig = "gates"
	KindRun    = "gaterun"

	MaxGates       = 20
	DefaultTimeout = 120
	MaxTimeout     = 600
	MaxLog         = 64 << 10
	KeepRuns       = 30
)

// Gate kinds.
const (
	KindFormat    = "format"
	KindLint      = "lint"
	KindTypecheck = "typecheck"
	KindTest      = "test"
	KindBuild     = "build"
	KindSecrets   = "secrets"
	KindDeps      = "deps"
	KindCustom    = "custom"
)

var validKinds = map[string]bool{KindFormat: true, KindLint: true, KindTypecheck: true, KindTest: true, KindBuild: true, KindSecrets: true, KindDeps: true, KindCustom: true}

// Policies.
const (
	PolicyRequired = "required"
	PolicyAdvisory = "advisory"
)

// Run and result statuses.
const (
	RunRunning     = "running"
	RunDone        = "done"
	RunError       = "error"
	RunCancelled   = "cancelled"
	RunInterrupted = "interrupted"

	ResPassed  = "passed"
	ResFailed  = "failed"
	ResTimeout = "timeout"
	ResError   = "error"
	ResSkipped = "skipped"
)

type Gate struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Command    string `json:"command"`
	Policy     string `json:"policy"`
	Enabled    bool   `json:"enabled"`
	TimeoutSec int    `json:"timeoutSec"`
	// Detected says why a suggestion was made; it is empty for hand-made gates.
	Detected string `json:"detected,omitempty"`
}

type Config struct {
	Gates []Gate `json:"gates"`
	// Confirmed is true once a person has reviewed and saved the list.
	Confirmed bool `json:"confirmed"`
}

type Result struct {
	GateID    string `json:"gateId"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Policy    string `json:"policy"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exitCode"`
	Millis    int64  `json:"millis"`
	Log       string `json:"log,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type Run struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	SetID       string     `json:"setId,omitempty"`
	SetHash     string     `json:"setHash,omitempty"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	Results     []Result   `json:"results"`
	// Passed is true when every required gate in the run passed.
	Passed         bool     `json:"passed"`
	RequiredFailed []string `json:"requiredFailed"`
}

// FS is the workspace access gates need.
type FS interface {
	List(ctx context.Context, ws, path string) ([]wsfs.Entry, error)
	Read(ctx context.Context, ws, path string) (*wsfs.Text, error)
	Run(ctx context.Context, ws, command string, timeoutSec int) (*wsfs.RunResult, error)
}

// Trial applies a proposed change set temporarily while fn runs.
type Trial interface {
	WithTrial(ctx context.Context, owner, wsID, setID string, fn func(ctx context.Context, hash string, files []string) error) error
}

type Service struct {
	st    store.Store
	fs    FS
	aud   *audit.Recorder
	trial Trial
	now   func() time.Time

	mu   sync.Mutex
	runs map[string]*active
}

type active struct {
	ws     string
	cancel context.CancelFunc
	done   chan struct{}
}

func New(st store.Store, fs FS, aud *audit.Recorder, trial Trial) *Service {
	return &Service{st: st, fs: fs, aud: aud, trial: trial, now: func() time.Time { return time.Now().UTC() }, runs: map[string]*active{}}
}

func verr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

func (s *Service) owns(ctx context.Context, owner, wsID string) error {
	w, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil || w.OwnerID != owner {
		return store.ErrNotFound
	}
	return nil
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// GetConfig returns the saved gates (none before the first confirmation).
func (s *Service) GetConfig(ctx context.Context, owner, wsID string) (*Config, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	return s.config(ctx, wsID), nil
}

func (s *Service) config(ctx context.Context, wsID string) *Config {
	c, err := store.GetJSON[Config](ctx, s.st, KindConfig, wsID)
	if err != nil {
		return &Config{Gates: []Gate{}}
	}
	if c.Gates == nil {
		c.Gates = []Gate{}
	}
	return c
}

// SaveConfig validates and stores the list. Saving is the confirmation.
func (s *Service) SaveConfig(ctx context.Context, owner, wsID string, in Config) (*Config, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	if len(in.Gates) > MaxGates {
		return nil, verr("gates", "too_many_gates", "Use up to 20 gates.")
	}
	seen := map[string]bool{}
	out := &Config{Gates: make([]Gate, 0, len(in.Gates)), Confirmed: true}
	for _, g := range in.Gates {
		g.Name = strings.TrimSpace(g.Name)
		g.Command = strings.TrimSpace(g.Command)
		if g.ID == "" {
			g.ID = slug(g.Name)
		}
		if !idRe.MatchString(g.ID) {
			return nil, verr("id", "invalid_id", "Each gate needs a short id of lowercase letters, numbers and dashes.")
		}
		if seen[g.ID] {
			return nil, verr("id", "duplicate_gate", "Two gates share the id "+g.ID+".")
		}
		seen[g.ID] = true
		if g.Name == "" || len(g.Name) > 60 {
			return nil, verr("name", "invalid_name", "Give each gate a name of up to 60 characters.")
		}
		if !validKinds[g.Kind] {
			return nil, verr("kind", "invalid_kind", "Choose a kind for "+g.Name+".")
		}
		if g.Policy != PolicyRequired && g.Policy != PolicyAdvisory {
			return nil, verr("policy", "invalid_policy", "Choose required or advisory for "+g.Name+".")
		}
		builtin := g.Command == BuiltinSecrets || g.Command == BuiltinDeps
		if g.Command == "" || len(g.Command) > 1000 {
			return nil, verr("command", "invalid_command", "Give "+g.Name+" a command of up to 1000 characters.")
		}
		if strings.HasPrefix(g.Command, "builtin:") && !builtin {
			return nil, verr("command", "invalid_command", "That built-in check does not exist.")
		}
		if g.TimeoutSec <= 0 {
			g.TimeoutSec = DefaultTimeout
		}
		if g.TimeoutSec > MaxTimeout {
			return nil, verr("timeoutSec", "invalid_timeout", "A gate can run for up to 10 minutes.")
		}
		out.Gates = append(out.Gates, g)
	}
	if err := store.PutJSON(ctx, s.st, KindConfig, wsID, wsID, out); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "gates.configured", "", map[string]any{"gates": len(out.Gates)})
	return out, nil
}

func slug(s string) string {
	s = strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 30 {
		s = strings.Trim(s[:30], "-")
	}
	return s
}

// StartInput selects what to run. With a SetID the gates run against that
// proposed change set, applied temporarily; without one they run on the
// workspace as it is.
type StartInput struct {
	GateIDs []string `json:"gateIds"`
	SetID   string   `json:"setId"`
}

// Start begins a run in the background and returns it immediately.
func (s *Service) Start(ctx context.Context, owner, wsID string, in StartInput) (*Run, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	cfg := s.config(ctx, wsID)
	var gs []Gate
	want := map[string]bool{}
	for _, id := range in.GateIDs {
		want[id] = true
	}
	for _, g := range cfg.Gates {
		if g.Enabled && (len(want) == 0 || want[g.ID]) {
			gs = append(gs, g)
		}
	}
	if len(gs) == 0 {
		return nil, verr("gates", "no_gates", "No enabled gates to run. Review and save the gates first.")
	}
	s.mu.Lock()
	for _, a := range s.runs {
		if a.ws == wsID {
			s.mu.Unlock()
			return nil, verr("gates", "run_in_progress", "Gates are already running in this workspace.")
		}
	}
	run := &Run{ID: store.NewID("gr"), WorkspaceID: wsID, SetID: in.SetID, Status: RunRunning, StartedAt: s.now(), Results: []Result{}, RequiredFailed: []string{}}
	rctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(gs))*time.Duration(MaxTimeout)*time.Second)
	a := &active{ws: wsID, cancel: cancel, done: make(chan struct{})}
	s.runs[run.ID] = a
	s.mu.Unlock()
	s.save(ctx, run)
	s.aud.User(ctx, owner, wsID, "gates.run_started", run.ID, map[string]any{"gates": len(gs), "setId": in.SetID})
	snap := *run
	go func() {
		defer close(a.done)
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.runs, run.ID)
			s.mu.Unlock()
		}()
		s.execute(rctx, owner, run, gs)
	}()
	return &snap, nil
}

func (s *Service) save(ctx context.Context, r *Run) {
	_ = store.PutJSON(context.WithoutCancel(ctx), s.st, KindRun, r.WorkspaceID, r.ID, r)
}

func (s *Service) execute(ctx context.Context, owner string, run *Run, gs []Gate) {
	body := func(ctx context.Context) {
		for _, g := range gs {
			if ctx.Err() != nil {
				run.Results = append(run.Results, Result{GateID: g.ID, Name: g.Name, Kind: g.Kind, Policy: g.Policy, Status: ResSkipped, Log: "Not run: the run was cancelled."})
				continue
			}
			run.Results = append(run.Results, s.runGate(ctx, run.WorkspaceID, g))
			s.save(ctx, run)
		}
	}
	var err error
	if run.SetID != "" {
		err = s.trial.WithTrial(ctx, owner, run.WorkspaceID, run.SetID, func(ctx context.Context, hash string, _ []string) error {
			run.SetHash = hash
			body(ctx)
			return nil
		})
	} else {
		body(ctx)
	}
	now := s.now()
	run.FinishedAt = &now
	switch {
	case err != nil:
		run.Status, run.Error = RunError, err.Error()
	case ctx.Err() != nil:
		run.Status = RunCancelled
	default:
		run.Status = RunDone
	}
	run.Passed, run.RequiredFailed = summarize(run.Results, run.Status)
	s.save(ctx, run)
	s.prune(ctx, run.WorkspaceID)
	s.aud.Record(context.WithoutCancel(ctx), domain.ActorSystem, "gates", run.WorkspaceID, "gates.run_finished", run.ID, map[string]any{"status": run.Status, "passed": run.Passed, "failed": run.RequiredFailed})
}

func summarize(rs []Result, status string) (bool, []string) {
	failed := []string{}
	for _, r := range rs {
		if r.Policy == PolicyRequired && r.Status != ResPassed {
			failed = append(failed, r.Name)
		}
	}
	return status == RunDone && len(failed) == 0, failed
}

func (s *Service) runGate(ctx context.Context, wsID string, g Gate) Result {
	res := Result{GateID: g.ID, Name: g.Name, Kind: g.Kind, Policy: g.Policy}
	start := time.Now()
	switch g.Command {
	case BuiltinSecrets, BuiltinDeps:
		var ok bool
		if g.Command == BuiltinSecrets {
			ok, res.Log = s.scanSecrets(ctx, wsID)
		} else {
			ok, res.Log = s.checkDeps(ctx, wsID)
		}
		res.Status = ResPassed
		if !ok {
			res.Status, res.ExitCode = ResFailed, 1
		}
	default:
		out, err := s.fs.Run(ctx, wsID, g.Command, g.TimeoutSec)
		switch {
		case err != nil:
			res.Status, res.Log = ResError, "The command could not be run: "+truncate(err.Error(), 300)
		default:
			res.ExitCode = out.ExitCode
			res.Log = "$ " + g.Command + "\n" + out.Stdout
			if out.Stderr != "" {
				res.Log += "\n[stderr]\n" + out.Stderr
			}
			res.Truncated = out.Truncated
			switch {
			case out.TimedOut:
				res.Status = ResTimeout
				res.Log += fmt.Sprintf("\n[timed out after %d seconds]", g.TimeoutSec)
			case out.ExitCode != 0:
				res.Status = ResFailed
			default:
				res.Status = ResPassed
			}
		}
	}
	res.Millis = time.Since(start).Milliseconds()
	if len(res.Log) > MaxLog {
		res.Log, res.Truncated = res.Log[:MaxLog]+"\n[log truncated]", true
	}
	return res
}

func (s *Service) prune(ctx context.Context, wsID string) {
	rs, err := store.ListJSON[Run](ctx, s.st, KindRun, wsID)
	if err != nil || len(rs) <= KeepRuns {
		return
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].StartedAt.Before(rs[j].StartedAt) })
	for _, r := range rs[:len(rs)-KeepRuns] {
		_ = s.st.DeleteDoc(ctx, KindRun, r.ID)
	}
}

func (s *Service) isActive(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runs[id]
	return ok
}

// GetRun returns a run with its logs.
func (s *Service) GetRun(ctx context.Context, owner, wsID, id string) (*Run, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	r, err := store.GetJSON[Run](ctx, s.st, KindRun, id)
	if err != nil || r.WorkspaceID != wsID {
		return nil, store.ErrNotFound
	}
	if r.Status == RunRunning && !s.isActive(id) {
		r.Status = RunInterrupted
	}
	return r, nil
}

// RunSummary lists a run without logs.
type RunSummary struct {
	ID             string     `json:"id"`
	SetID          string     `json:"setId,omitempty"`
	Status         string     `json:"status"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	Passed         bool       `json:"passed"`
	RequiredFailed []string   `json:"requiredFailed"`
	Results        []Result   `json:"results"`
}

// ListRuns returns recent runs newest first, without logs. A non-empty setID
// limits it to runs for that change set.
func (s *Service) ListRuns(ctx context.Context, owner, wsID, setID string) ([]RunSummary, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	rs, err := store.ListJSON[Run](ctx, s.st, KindRun, wsID)
	if err != nil {
		return nil, err
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].StartedAt.After(rs[j].StartedAt) })
	out := []RunSummary{}
	for _, r := range rs {
		if setID != "" && r.SetID != setID {
			continue
		}
		st := r.Status
		if st == RunRunning && !s.isActive(r.ID) {
			st = RunInterrupted
		}
		sum := RunSummary{ID: r.ID, SetID: r.SetID, Status: st, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Passed: r.Passed, RequiredFailed: r.RequiredFailed}
		for _, x := range r.Results {
			x.Log = ""
			sum.Results = append(sum.Results, x)
		}
		out = append(out, sum)
	}
	return out, nil
}

// Cancel stops a running run.
func (s *Service) Cancel(ctx context.Context, owner, wsID, id string) error {
	if _, err := s.GetRun(ctx, owner, wsID, id); err != nil {
		return err
	}
	s.mu.Lock()
	a := s.runs[id]
	s.mu.Unlock()
	if a != nil {
		a.cancel()
	}
	return nil
}

// Wait blocks until a run ends (tests and shutdown).
func (s *Service) Wait(id string) {
	s.mu.Lock()
	a := s.runs[id]
	s.mu.Unlock()
	if a != nil {
		<-a.done
	}
}

// Close cancels every running run.
func (s *Service) Close() {
	s.mu.Lock()
	var as []*active
	for _, a := range s.runs {
		as = append(as, a)
	}
	s.mu.Unlock()
	for _, a := range as {
		a.cancel()
	}
	for _, a := range as {
		<-a.done
	}
}

// Verdict implements changeset.Gater: do the gates allow this exact content?
func (s *Service) Verdict(ctx context.Context, wsID, setID, hash string) changeset.GateVerdict {
	cfg := s.config(ctx, wsID)
	v := changeset.GateVerdict{Failing: []string{}}
	for _, g := range cfg.Gates {
		if g.Enabled && g.Policy == PolicyRequired {
			v.Configured = true
		}
	}
	if !v.Configured {
		return v
	}
	rs, _ := store.ListJSON[Run](ctx, s.st, KindRun, wsID)
	var latest *Run
	for _, r := range rs {
		if r.SetID == setID && r.SetHash == hash && r.Status == RunDone {
			if latest == nil || r.StartedAt.After(latest.StartedAt) {
				latest = r
			}
		}
	}
	if latest == nil {
		return v
	}
	// Required gates configured now must all have been part of the run;
	// if the configuration changed since, the run no longer counts.
	ran := map[string]Result{}
	for _, r := range latest.Results {
		ran[r.GateID] = r
	}
	for _, g := range cfg.Gates {
		if !g.Enabled || g.Policy != PolicyRequired {
			continue
		}
		r, ok := ran[g.ID]
		if !ok {
			return v
		}
		if r.Status != ResPassed {
			v.Failing = append(v.Failing, g.Name)
		}
	}
	v.Ran, v.RunID = true, latest.ID
	v.Passed = len(v.Failing) == 0
	return v
}

// ForgetWorkspace cancels gate runs for a workspace that is being deleted.
func (s *Service) ForgetWorkspace(wsID string) {
	s.mu.Lock()
	var as []*active
	for _, a := range s.runs {
		if a.ws == wsID {
			as = append(as, a)
		}
	}
	s.mu.Unlock()
	for _, a := range as {
		a.cancel()
	}
	for _, a := range as {
		<-a.done
	}
}
