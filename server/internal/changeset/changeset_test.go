package changeset

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

type memFS struct {
	mu       sync.Mutex
	files    map[string]string
	failPath string
	writes   []string
}

func (m *memFS) Read(_ context.Context, _, p string) (*wsfs.Text, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.files[p]
	if !ok {
		return nil, wsfs.ErrNotFound
	}
	if c == "\x00bin" {
		return nil, wsfs.ErrBinary
	}
	return &wsfs.Text{Content: c}, nil
}
func (m *memFS) Write(_ context.Context, _, p, c string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == m.failPath {
		return errors.New("disk full")
	}
	m.files[p] = c
	m.writes = append(m.writes, p)
	return nil
}
func (m *memFS) Mkdir(context.Context, string, string) error { return nil }
func (m *memFS) Delete(_ context.Context, _, p string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, p)
	return nil
}

func fx(t *testing.T) (*Service, *memFS, context.Context) {
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	fs := &memFS{files: map[string]string{"a.txt": "one\ntwo\nthree\n", "b.txt": "bee\n"}}
	return New(st, fs, audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))), fs, ctx
}

func sp(s string) *string { return &s }

func vcode(err error) string {
	var ve *runtime.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func propose(t *testing.T, s *Service, ctx context.Context, ch ...Change) *View {
	t.Helper()
	set, err := s.Propose(ctx, "u1", "ws1", domain.ActorAgent, "agent1", ProposeInput{Title: "Edit things", Changes: ch})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(ctx, "u1", "ws1", set.ID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestProposeWritesNothing(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("one\n2\nthree\n")}, Change{"new/c.txt", sp("c\n")}, Change{"b.txt", nil})
	if len(fs.writes) != 0 || fs.files["a.txt"] != "one\ntwo\nthree\n" || fs.files["b.txt"] == "" {
		t.Fatal("staging must not touch files")
	}
	ops := map[string]string{}
	for _, f := range v.Files {
		ops[f.Path] = f.Op
	}
	if ops["a.txt"] != OpModify || ops["new/c.txt"] != OpCreate || ops["b.txt"] != OpDelete || v.Status != StatusProposed {
		t.Fatalf("%v %s", ops, v.Status)
	}
	if v.Files[0].Hunks == nil {
		t.Fatal("hunks expected")
	}
}

func TestProposeValidation(t *testing.T) {
	s, _, ctx := fx(t)
	bad := func(code string, in ProposeInput) {
		t.Helper()
		if _, err := s.Propose(ctx, "u1", "ws1", "agent", "a", in); vcode(err) != code {
			t.Fatalf("want %s got %v", code, err)
		}
	}
	bad("title_required", ProposeInput{Changes: []Change{{"a.txt", sp("x")}}})
	bad("no_changes", ProposeInput{Title: "t"})
	bad("no_changes", ProposeInput{Title: "t", Changes: []Change{{"a.txt", sp("one\ntwo\nthree\n")}}})
	bad("bad_path", ProposeInput{Title: "t", Changes: []Change{{"../etc/passwd", sp("x")}}})
	bad("bad_path", ProposeInput{Title: "t", Changes: []Change{{"/etc/passwd", sp("x")}}})
	bad("bad_path", ProposeInput{Title: "t", Changes: []Change{{"C:\\x", sp("x")}}})
	bad("bad_path", ProposeInput{Title: "t", Changes: []Change{{"a/../../x", sp("x")}}})
	bad("protected_path", ProposeInput{Title: "t", Changes: []Change{{".git/config", sp("x")}}})
	bad("not_found", ProposeInput{Title: "t", Changes: []Change{{"ghost.txt", nil}}})
	bad("duplicate_path", ProposeInput{Title: "t", Changes: []Change{{"a.txt", sp("1")}, {"./a.txt", sp("2")}}})
	bad("file_too_large", ProposeInput{Title: "t", Changes: []Change{{"big.txt", sp(strings.Repeat("x", MaxFileBytes+1))}}})
	if _, err := s.Propose(ctx, "intruder", "ws1", "agent", "a", ProposeInput{Title: "t", Changes: []Change{{"a.txt", sp("x")}}}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user: %v", err)
	}
}

func TestBinaryFilesAreRefused(t *testing.T) {
	s, fs, ctx := fx(t)
	fs.files["img.png"] = "\x00bin"
	if _, err := s.Propose(ctx, "u1", "ws1", "agent", "a", ProposeInput{Title: "t", Changes: []Change{{"img.png", sp("x")}}}); vcode(err) != "binary_file" {
		t.Fatal(err)
	}
}

func TestAcceptAllAppliesAndRevertRestores(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("one\n2\nthree\n")}, Change{"new/c.txt", sp("c\n")}, Change{"b.txt", nil})
	out, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusApplied || fs.files["a.txt"] != "one\n2\nthree\n" || fs.files["new/c.txt"] != "c\n" {
		t.Fatalf("%s %v", out.Status, fs.files)
	}
	if _, ok := fs.files["b.txt"]; ok {
		t.Fatal("b.txt should be deleted")
	}
	// Deciding twice is refused.
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); vcode(err) != "already_decided" {
		t.Fatal(err)
	}
	if _, err := s.Reject(ctx, "u1", "ws1", v.ID); vcode(err) != "already_decided" {
		t.Fatal(err)
	}
	cps, _ := s.Checkpoints(ctx, "u1", "ws1")
	if len(cps) != 1 || len(cps[0].Files) != 3 {
		t.Fatalf("%+v", cps)
	}
	if _, err := s.Revert(ctx, "u1", "ws1", cps[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if fs.files["a.txt"] != "one\ntwo\nthree\n" || fs.files["b.txt"] != "bee\n" {
		t.Fatalf("not restored: %v", fs.files)
	}
	if _, ok := fs.files["new/c.txt"]; ok {
		t.Fatal("created file should be removed on revert")
	}
	if got, _ := s.Get(ctx, "u1", "ws1", v.ID); got.Status != StatusReverted {
		t.Fatal(got.Status)
	}
	if _, err := s.Revert(ctx, "u1", "ws1", cps[0].ID, false); vcode(err) != "already_reverted" {
		t.Fatal(err)
	}
}

func TestAcceptPartHunksAndFiles(t *testing.T) {
	s, fs, ctx := fx(t)
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "l"+string(rune('a'+i%26)))
	}
	fs.files["big.txt"] = strings.Join(lines, "\n") + "\n"
	mod := append([]string{}, lines...)
	mod[1], mod[37] = "EARLY", "LATE"
	v := propose(t, s, ctx, Change{"big.txt", sp(strings.Join(mod, "\n") + "\n")}, Change{"b.txt", sp("changed\n")})
	var big FileView
	for _, f := range v.Files {
		if f.Path == "big.txt" {
			big = f
		}
	}
	if len(big.Hunks) != 2 {
		t.Fatalf("hunks %d", len(big.Hunks))
	}
	out, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{Files: []FileDecision{{Path: "big.txt", Hunks: []int{1}}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusPartial {
		t.Fatal(out.Status)
	}
	got := fs.files["big.txt"]
	if !strings.Contains(got, "LATE") || strings.Contains(got, "EARLY") {
		t.Fatalf("hunk selection ignored: %q", got)
	}
	if fs.files["b.txt"] != "bee\n" {
		t.Fatal("unchosen file must be untouched")
	}
	st := map[string]string{}
	for _, f := range out.Files {
		st[f.Path] = f.Status
	}
	if st["big.txt"] != "partial" || st["b.txt"] != "rejected" {
		t.Fatalf("%v", st)
	}
}

func TestAcceptRefusesWhenFilesChangedAndWritesNothing(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("X\n")}, Change{"b.txt", sp("Y\n")})
	fs.files["b.txt"] = "someone edited this\n"
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); vcode(err) != "files_changed" {
		t.Fatal(err)
	}
	if fs.files["a.txt"] != "one\ntwo\nthree\n" {
		t.Fatal("all-or-nothing: a.txt must be untouched")
	}
	// A file created after the proposal also counts.
	v2 := propose(t, s, ctx, Change{"n.txt", sp("n\n")})
	fs.files["n.txt"] = "exists now\n"
	if _, err := s.Accept(ctx, "u1", "ws1", v2.ID, Decision{All: true}); vcode(err) != "files_changed" {
		t.Fatal(err)
	}
}

func TestWriteFailureRollsBack(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("A\n")}, Change{"b.txt", sp("B\n")})
	fs.failPath = "b.txt"
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); vcode(err) != "write_failed" {
		t.Fatal(err)
	}
	if fs.files["a.txt"] != "one\ntwo\nthree\n" || fs.files["b.txt"] != "bee\n" {
		t.Fatalf("not rolled back: %v", fs.files)
	}
	if cps, _ := s.Checkpoints(ctx, "u1", "ws1"); len(cps) != 0 {
		t.Fatal("failed apply must not leave a checkpoint")
	}
	if got, _ := s.Get(ctx, "u1", "ws1", v.ID); got.Status != StatusProposed {
		t.Fatal("set stays proposed so it can be retried")
	}
}

func TestRejectTouchesNothing(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("A\n")})
	out, err := s.Reject(ctx, "u1", "ws1", v.ID)
	if err != nil || out.Status != StatusRejected || fs.files["a.txt"] != "one\ntwo\nthree\n" || len(fs.writes) != 0 {
		t.Fatalf("%v %v", out, err)
	}
}

func TestRevertProtectsLaterEdits(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("A\n")})
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); err != nil {
		t.Fatal(err)
	}
	fs.files["a.txt"] = "A\nand my later work\n"
	cps, _ := s.Checkpoints(ctx, "u1", "ws1")
	if _, err := s.Revert(ctx, "u1", "ws1", cps[0].ID, false); vcode(err) != "edited_since" {
		t.Fatal(err)
	}
	if fs.files["a.txt"] != "A\nand my later work\n" {
		t.Fatal("must not overwrite")
	}
	if _, err := s.Revert(ctx, "u1", "ws1", cps[0].ID, true); err != nil || fs.files["a.txt"] != "one\ntwo\nthree\n" {
		t.Fatalf("forced revert: %v", err)
	}
}

func TestAcceptValidatesSelection(t *testing.T) {
	s, _, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("A\n")})
	for code, d := range map[string]Decision{
		"nothing_selected": {},
		"unknown_file":     {Files: []FileDecision{{Path: "zzz"}}},
		"unknown_hunk":     {Files: []FileDecision{{Path: "a.txt", Hunks: []int{9}}}},
	} {
		if _, err := s.Accept(ctx, "u1", "ws1", v.ID, d); vcode(err) != code {
			t.Fatalf("%s: %v", code, err)
		}
	}
	if _, err := s.Accept(ctx, "intruder", "ws1", v.ID, Decision{All: true}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ownership")
	}
}

func TestNewerProposalSupersedesOlderForSameFiles(t *testing.T) {
	s, _, ctx := fx(t)
	v1 := propose(t, s, ctx, Change{"a.txt", sp("1\n")})
	_ = propose(t, s, ctx, Change{"a.txt", sp("2\n")})
	if got, _ := s.Get(ctx, "u1", "ws1", v1.ID); got.Status != StatusSuperseded {
		t.Fatal(got.Status)
	}
}

func TestConcurrentAcceptsApplyOnce(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("A\n")})
	var wg sync.WaitGroup
	var okN int
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okN != 1 || len(fs.writes) != 1 {
		t.Fatalf("applied %d times, %d writes", okN, len(fs.writes))
	}
}

func TestCheckpointsArePruned(t *testing.T) {
	s, _, ctx := fx(t)
	for i := 0; i < KeepCheckpoints+5; i++ {
		v := propose(t, s, ctx, Change{"a.txt", sp(strings.Repeat("x", i+1) + "\n")})
		if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); err != nil {
			t.Fatal(err)
		}
	}
	if cps, _ := s.Checkpoints(ctx, "u1", "ws1"); len(cps) != KeepCheckpoints {
		t.Fatalf("%d", len(cps))
	}
}

func TestProposalRecordsAppliedRulesAndWarnings(t *testing.T) {
	s, _, ctx := fx(t)
	s.WithRules(func(_ context.Context, _ string, files []string) ([]AppliedRule, []string) {
		return []AppliedRule{{ID: "r1", Title: "Style", Scope: "global", Hash: "abc"}}, []string{files[0] + " is protected"}
	})
	v := propose(t, s, ctx, Change{"a.txt", sp("A\n")})
	if len(v.Rules) != 1 || v.Rules[0].Hash != "abc" || len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "a.txt") {
		t.Fatalf("%+v %v", v.Rules, v.Warnings)
	}
	plain, _, _ := fx(t)
	if got := propose(t, plain, ctx, Change{"a.txt", sp("A\n")}); got.Rules == nil {
		t.Fatal("rules must serialise as an empty list, not null")
	}
}

type fakeGater struct{ v GateVerdict }

func (g *fakeGater) Verdict(context.Context, string, string, string) GateVerdict { return g.v }

func TestRequiredGatesBlockAgentSetsUntilPassedOrOverridden(t *testing.T) {
	s, fs, ctx := fx(t)
	g := &fakeGater{v: GateVerdict{Configured: true}}
	s.WithGates(g)
	v := propose(t, s, ctx, Change{"a.txt", sp("A\n")})
	if v.Gate == nil || v.Gate.Ran {
		t.Fatalf("view gate: %+v", v.Gate)
	}
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); vcode(err) != "gates_not_run" {
		t.Fatal(err)
	}
	g.v = GateVerdict{Configured: true, Ran: true, Failing: []string{"Tests"}}
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); vcode(err) != "gates_failed" {
		t.Fatal(err)
	}
	if fs.files["a.txt"] != "one\ntwo\nthree\n" {
		t.Fatal("a blocked accept must not write")
	}
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true, OverrideGates: "x"}); vcode(err) != "invalid_reason" {
		t.Fatal(err)
	}
	out, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true, OverrideGates: "known flaky test, fixing separately"})
	if err != nil || out.GateOverride == nil || out.GateOverride.Failing[0] != "Tests" || out.GateOverride.By != "u1" {
		t.Fatalf("%v %+v", err, out)
	}
	if !has(auditActions(s, ctx), "gates.overridden") {
		t.Fatal("override not audited")
	}
	// A passing run needs no override; a user-authored set is never gated.
	g.v = GateVerdict{Configured: true, Ran: true, Passed: true}
	v2 := propose(t, s, ctx, Change{"b.txt", sp("B\n")})
	if o, err := s.Accept(ctx, "u1", "ws1", v2.ID, Decision{All: true}); err != nil || o.GateOverride != nil {
		t.Fatalf("%v %+v", err, o)
	}
	g.v = GateVerdict{Configured: true}
	us, _ := s.Propose(ctx, "u1", "ws1", domain.ActorUser, "u1", ProposeInput{Title: "mine", Changes: []Change{{"a.txt", sp("mine\n")}}})
	if _, err := s.Accept(ctx, "u1", "ws1", us.ID, Decision{All: true}); err != nil {
		t.Fatalf("user sets are not gated: %v", err)
	}
}

func auditActions(s *Service, ctx context.Context) []string {
	evs, _ := s.st.ListAudit(ctx, "ws1", 500)
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

func TestTrialAppliesTemporarilyAndAlwaysRestores(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("TRIAL\n")}, Change{"new.txt", sp("n\n")}, Change{"b.txt", nil})
	seen := map[string]string{}
	err := s.WithTrial(ctx, "u1", "ws1", v.ID, func(_ context.Context, hash string, files []string) error {
		fs.mu.Lock()
		for k, val := range fs.files {
			seen[k] = val
		}
		fs.mu.Unlock()
		if hash != v.Hash || len(files) != 3 {
			t.Errorf("hash/files: %s %v", hash, files)
		}
		// Nothing else may touch files while it runs.
		if _, err := s.Propose(ctx, "u1", "ws1", "agent", "a", ProposeInput{Title: "t", Changes: []Change{{"c.txt", sp("c")}}}); vcode(err) != "gates_running" {
			t.Errorf("propose during trial: %v", err)
		}
		if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); vcode(err) != "gates_running" {
			t.Errorf("accept during trial: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen["a.txt"] != "TRIAL\n" || seen["new.txt"] != "n\n" || seen["b.txt"] != "" {
		t.Fatalf("trial content: %v", seen)
	}
	if fs.files["a.txt"] != "one\ntwo\nthree\n" || fs.files["b.txt"] != "bee\n" {
		t.Fatalf("not restored: %v", fs.files)
	}
	if _, ok := fs.files["new.txt"]; ok {
		t.Fatal("created file left behind")
	}
	// Errors in fn still restore; the set stays proposed and usable.
	boom := errors.New("boom")
	if err := s.WithTrial(ctx, "u1", "ws1", v.ID, func(context.Context, string, []string) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if fs.files["a.txt"] != "one\ntwo\nthree\n" {
		t.Fatal("not restored after error")
	}
	if _, err := s.Accept(ctx, "u1", "ws1", v.ID, Decision{All: true}); err != nil {
		t.Fatalf("accept after trial: %v", err)
	}
}

func TestTrialRefusesStaleFilesAndRecoversFromACrash(t *testing.T) {
	s, fs, ctx := fx(t)
	v := propose(t, s, ctx, Change{"a.txt", sp("TRIAL\n")})
	fs.files["a.txt"] = "edited by someone\n"
	if err := s.WithTrial(ctx, "u1", "ws1", v.ID, func(context.Context, string, []string) error { return nil }); vcode(err) != "files_changed" {
		t.Fatal(err)
	}
	// Simulate a crash mid-trial: the trial record exists and files hold proposed content.
	fs.files["a.txt"] = "TRIAL\n"
	_ = store.PutJSON(ctx, s.st, kindTrial, "ws1", "ws1", &trial{WorkspaceID: "ws1", Files: []CheckpointFile{{Path: "a.txt", Existed: true, Content: "original\n"}}})
	if _, err := s.Propose(ctx, "u1", "ws1", "agent", "a", ProposeInput{Title: "t", Changes: []Change{{"b.txt", sp("x\n")}}}); err != nil {
		t.Fatal(err)
	}
	if fs.files["a.txt"] != "original\n" {
		t.Fatalf("crash recovery did not restore: %q", fs.files["a.txt"])
	}
}
