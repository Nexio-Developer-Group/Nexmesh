// Package changeset stages file changes proposed by the Code Agent (or anyone)
// so a person can review, accept all, accept part or reject them before a
// single byte reaches the workspace (PRD F18). Every accept first records a
// checkpoint of the files it touches, so it can be undone without Git.
package changeset

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

const (
	KindSet        = "changeset"
	KindCheckpoint = "checkpoint"

	MaxFiles     = 100
	MaxFileBytes = 1 << 20
	MaxSetBytes  = 5 << 20
	// KeepCheckpoints bounds storage; the oldest are pruned.
	KeepCheckpoints = 50
)

// Set statuses.
const (
	StatusProposed   = "proposed"
	StatusApplied    = "applied"
	StatusPartial    = "partially_applied"
	StatusRejected   = "rejected"
	StatusReverted   = "reverted"
	StatusSuperseded = "superseded"
)

// File operations.
const (
	OpCreate = "create"
	OpModify = "modify"
	OpDelete = "delete"
)

// FS is the part of the workspace file view this package needs.
type FS interface {
	Read(ctx context.Context, ws, path string) (*wsfs.Text, error)
	Write(ctx context.Context, ws, path, content string, expected time.Time) error
	Delete(ctx context.Context, ws, path string, recursive bool) error
	Mkdir(ctx context.Context, ws, path string) error
}

// AppliedRule is a rule that applied to a change set, with the version used.
type AppliedRule struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Scope string `json:"scope"`
	Hash  string `json:"hash"`
}

// GateVerdict says whether the quality gates allow a change set to be accepted.
type GateVerdict struct {
	// Configured is true when at least one required gate is enabled.
	Configured bool     `json:"configured"`
	Ran        bool     `json:"ran"` // a run exists for exactly this content
	Passed     bool     `json:"passed"`
	Failing    []string `json:"failing"` // required gates that failed
	RunID      string   `json:"runId,omitempty"`
}

// Gater reports the gate state of a change set's exact content.
type Gater interface {
	Verdict(ctx context.Context, wsID, setID, contentHash string) GateVerdict
}

// GateOverride records that a person accepted a change set despite its gates.
type GateOverride struct {
	Reason  string    `json:"reason"`
	By      string    `json:"by"`
	At      time.Time `json:"at"`
	Failing []string  `json:"failing"`
}

// Resolver says which rules apply to a set of files and warns about protected paths.
type Resolver func(ctx context.Context, wsID string, files []string) ([]AppliedRule, []string)

// FileChange is one file in a set. Before and After are full contents so the
// hunks can be recomputed and partially applied.
type FileChange struct {
	Path    string `json:"path"`
	Op      string `json:"op"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Status  string `json:"status"` // pending | applied | partial | rejected
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	// Applied is the text written for this file when it was accepted (whole or in part).
	Applied string `json:"applied,omitempty"`
}

// Set is a proposed group of changes.
type Set struct {
	ID          string       `json:"id"`
	WorkspaceID string       `json:"workspaceId"`
	Title       string       `json:"title"`
	Summary     string       `json:"summary,omitempty"`
	Actor       string       `json:"actor"` // user | agent
	ActorID     string       `json:"actorId"`
	Status      string       `json:"status"`
	Files       []FileChange `json:"files"`
	// Rules lists the rules that applied when the set was proposed (PRD F21).
	Rules []AppliedRule `json:"rules"`
	// Hash identifies the proposed content so a gate run can be tied to it.
	Hash         string        `json:"hash"`
	GateOverride *GateOverride `json:"gateOverride,omitempty"`
	Warnings     []string      `json:"warnings,omitempty"`
	CheckpointID string        `json:"checkpointId,omitempty"`
	CreatedAt    time.Time     `json:"createdAt"`
	DecidedAt    *time.Time    `json:"decidedAt,omitempty"`
}

// Checkpoint stores the files exactly as they were before a set was applied.
type Checkpoint struct {
	ID          string           `json:"id"`
	WorkspaceID string           `json:"workspaceId"`
	SetID       string           `json:"setId,omitempty"`
	Label       string           `json:"label"`
	Files       []CheckpointFile `json:"files"`
	CreatedAt   time.Time        `json:"createdAt"`
	RevertedAt  *time.Time       `json:"revertedAt,omitempty"`
}

type CheckpointFile struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Content string `json:"content,omitempty"`
	// Written is what the apply put there; a revert warns if the file differs now.
	Written       string `json:"written,omitempty"`
	WrittenExists bool   `json:"writtenExists"`
}

type Service struct {
	st  store.Store
	fs  FS
	aud *audit.Recorder
	now func() time.Time

	resolve Resolver
	gater   Gater
	trials  map[string]bool // workspaces with gates running on a trial apply
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
}

// WithRules records the applicable rules on every proposal.
func (s *Service) WithRules(r Resolver) *Service { s.resolve = r; return s }

// WithGates makes required quality gates block agent change sets (PRD F23).
func (s *Service) WithGates(g Gater) *Service { s.gater = g; return s }

func New(st store.Store, fs FS, aud *audit.Recorder) *Service {
	return &Service{st: st, fs: fs, aud: aud, now: func() time.Time { return time.Now().UTC() }, locks: map[string]*sync.Mutex{}, trials: map[string]bool{}}
}

func verr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

func (s *Service) lock(ws string) func() {
	s.mu.Lock()
	l, ok := s.locks[ws]
	if !ok {
		l = &sync.Mutex{}
		s.locks[ws] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Service) workspace(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil || w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

// CleanPath validates a workspace-relative path.
func CleanPath(p string) (string, error) {
	if strings.ContainsRune(p, 0) || strings.TrimSpace(p) == "" {
		return "", verr("path", "bad_path", "Give a file path inside the workspace.")
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':') {
		return "", verr("path", "bad_path", "Paths must be relative to the workspace folder.")
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", verr("path", "bad_path", "That path leaves the workspace folder.")
	}
	if c == ".git" || strings.HasPrefix(c, ".git/") {
		return "", verr("path", "protected_path", "Changes inside .git are never staged. Use Git itself for that.")
	}
	return c, nil
}

// Change is one requested change. A nil Content deletes the file.
type Change struct {
	Path    string  `json:"path"`
	Content *string `json:"content"`
}

type ProposeInput struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Changes []Change `json:"changes"`
}

// Propose stages changes against the workspace's current files. Nothing is
// written. Files whose content would not change are left out.
func (s *Service) Propose(ctx context.Context, owner, wsID, actor, actorID string, in ProposeInput) (*Set, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	if err := s.busy(ctx, wsID); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, verr("title", "title_required", "Give the change a short title.")
	}
	if len(title) > 120 {
		title = title[:120]
	}
	if len(in.Changes) == 0 {
		return nil, verr("changes", "no_changes", "There is nothing to change.")
	}
	if len(in.Changes) > MaxFiles {
		return nil, verr("changes", "too_many_files", "A change set holds up to 100 files.")
	}
	set := &Set{ID: store.NewID("cs"), WorkspaceID: wsID, Title: title, Summary: strings.TrimSpace(in.Summary), Actor: actor, ActorID: actorID,
		Status: StatusProposed, CreatedAt: s.now()}
	seen := map[string]bool{}
	total := 0
	for _, c := range in.Changes {
		p, err := CleanPath(c.Path)
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, verr("path", "duplicate_path", p+" appears twice in one change set.")
		}
		seen[p] = true
		cur, exists, err := s.current(ctx, wsID, p)
		if err != nil {
			return nil, err
		}
		fc := FileChange{Path: p, Status: "pending", Before: cur}
		switch {
		case c.Content == nil:
			if !exists {
				return nil, verr("path", "not_found", p+" does not exist, so it cannot be deleted.")
			}
			fc.Op = OpDelete
		case !exists:
			fc.Op, fc.After = OpCreate, *c.Content
		default:
			fc.Op, fc.After = OpModify, *c.Content
			if fc.After == fc.Before {
				continue
			}
		}
		if len(fc.After) > MaxFileBytes {
			return nil, verr("content", "file_too_large", p+" is larger than 1 MB.")
		}
		total += len(fc.After) + len(fc.Before)
		fc.Added, fc.Removed = Stats(fc.Before, fc.After)
		set.Files = append(set.Files, fc)
	}
	if len(set.Files) == 0 {
		return nil, verr("changes", "no_changes", "Those files already have exactly this content.")
	}
	if total > MaxSetBytes {
		return nil, verr("changes", "set_too_large", "This change set is larger than 5 MB.")
	}
	sort.Slice(set.Files, func(i, j int) bool { return set.Files[i].Path < set.Files[j].Path })
	set.Hash = contentHash(set)
	set.Rules = []AppliedRule{}
	if s.resolve != nil {
		paths := make([]string, 0, len(set.Files))
		for _, f := range set.Files {
			paths = append(paths, f.Path)
		}
		rs, warns := s.resolve(ctx, wsID, paths)
		if rs != nil {
			set.Rules = rs
		}
		set.Warnings = warns
	}
	// A newer proposal for the same files replaces an older undecided one.
	if err := s.supersede(ctx, wsID, seen); err != nil {
		return nil, err
	}
	if err := store.PutJSON(ctx, s.st, KindSet, wsID, set.ID, set); err != nil {
		return nil, err
	}
	s.aud.Record(ctx, actor, actorID, wsID, "changeset.proposed", set.ID, map[string]any{"title": set.Title, "files": len(set.Files)})
	return set, nil
}

func (s *Service) supersede(ctx context.Context, wsID string, paths map[string]bool) error {
	all, err := store.ListJSON[Set](ctx, s.st, KindSet, wsID)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.Status != StatusProposed {
			continue
		}
		covered := true
		for _, f := range o.Files {
			if !paths[f.Path] {
				covered = false
			}
		}
		if covered {
			o.Status = StatusSuperseded
			if err := store.PutJSON(ctx, s.st, KindSet, wsID, o.ID, o); err != nil {
				return err
			}
		}
	}
	return nil
}

// current returns a file's text, or exists=false when it is not there.
func (s *Service) current(ctx context.Context, wsID, p string) (string, bool, error) {
	t, err := s.fs.Read(ctx, wsID, p)
	switch {
	case err == nil:
		return t.Content, true, nil
	case err == wsfs.ErrNotFound:
		return "", false, nil
	case err == wsfs.ErrBinary:
		return "", false, verr("path", "binary_file", p+" is binary or too large to stage.")
	}
	return "", false, err
}

// FileView is a file with its hunks, for review.
type FileView struct {
	Path    string `json:"path"`
	Op      string `json:"op"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Hunks   []Hunk `json:"hunks"`
}

// View is a set ready for the UI. Contents are left out.
type View struct {
	*Set
	Files []FileView `json:"files"`
	// Gate is the gate state of this exact content, for agent change sets that gates apply to.
	Gate *GateVerdict `json:"gate,omitempty"`
}

func (s *Service) viewOf(ctx context.Context, set *Set, hunks bool) *View {
	v := viewOf(set, hunks)
	if s.gater != nil && set.Actor == domain.ActorAgent && set.Status == StatusProposed {
		g := s.gater.Verdict(ctx, set.WorkspaceID, set.ID, set.Hash)
		v.Gate = &g
	}
	return v
}

func viewOf(set *Set, hunks bool) *View {
	v := &View{Set: set}
	for _, f := range set.Files {
		fv := FileView{Path: f.Path, Op: f.Op, Status: f.Status, Added: f.Added, Removed: f.Removed}
		if hunks {
			fv.Hunks = Hunks(f.Before, f.After)
		}
		v.Files = append(v.Files, fv)
	}
	return v
}

func (s *Service) load(ctx context.Context, owner, wsID, id string) (*Set, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	set, err := store.GetJSON[Set](ctx, s.st, KindSet, id)
	if err != nil || set.WorkspaceID != wsID {
		return nil, store.ErrNotFound
	}
	return set, nil
}

func (s *Service) Get(ctx context.Context, owner, wsID, id string) (*View, error) {
	set, err := s.load(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	return s.viewOf(ctx, set, true), nil
}

// List returns sets newest first, without hunks.
func (s *Service) List(ctx context.Context, owner, wsID string) ([]*View, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	all, err := store.ListJSON[Set](ctx, s.st, KindSet, wsID)
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	out := make([]*View, 0, len(all))
	for _, set := range all {
		out = append(out, viewOf(set, false))
	}
	return out, nil
}

// FileDecision accepts a file. With no Hunks the whole file is accepted.
type FileDecision struct {
	Path  string `json:"path"`
	Hunks []int  `json:"hunks"`
}

// Decision says what to accept; everything else is rejected. All accepts
// every file in full.
type Decision struct {
	All   bool           `json:"all"`
	Files []FileDecision `json:"files"`
	// OverrideGates is the reason for accepting an agent change set whose
	// required gates have not passed. It is recorded (PRD F23).
	OverrideGates string `json:"overrideGates,omitempty"`
}

// Reject discards a set without touching any file.
func (s *Service) Reject(ctx context.Context, owner, wsID, id string) (*View, error) {
	set, err := s.load(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	defer s.lock(wsID)()
	set, _ = s.load(ctx, owner, wsID, id)
	if set.Status != StatusProposed {
		return nil, verr("status", "already_decided", "This change set has already been decided.")
	}
	now := s.now()
	set.Status, set.DecidedAt = StatusRejected, &now
	for i := range set.Files {
		set.Files[i].Status = "rejected"
	}
	if err := store.PutJSON(ctx, s.st, KindSet, wsID, set.ID, set); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "changeset.rejected", set.ID, map[string]any{"title": set.Title})
	return viewOf(set, true), nil
}

// Accept applies the chosen files or hunks. It first checks that every file is
// still exactly as it was when proposed, records a checkpoint, and then writes.
// If a write fails, the files already written are restored.
func (s *Service) Accept(ctx context.Context, owner, wsID, id string, d Decision) (*View, error) {
	if _, err := s.load(ctx, owner, wsID, id); err != nil {
		return nil, err
	}
	defer s.lock(wsID)()
	set, err := s.load(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	if set.Status != StatusProposed {
		return nil, verr("status", "already_decided", "This change set has already been decided.")
	}
	if err := s.busy(ctx, wsID); err != nil {
		return nil, err
	}
	var override *GateOverride
	if set.Actor == domain.ActorAgent && s.gater != nil {
		v := s.gater.Verdict(ctx, wsID, set.ID, set.Hash)
		if v.Configured && !(v.Ran && v.Passed) {
			reason := strings.TrimSpace(d.OverrideGates)
			if reason == "" {
				if !v.Ran {
					return nil, verr("gates", "gates_not_run", "Required quality gates have not run on this change. Run them first, or override with a reason.")
				}
				return nil, verr("gates", "gates_failed", "Required quality gates failed ("+strings.Join(v.Failing, ", ")+"). Fix the change, or override with a reason.")
			}
			if len(reason) < 3 || len(reason) > 500 {
				return nil, verr("overrideGates", "invalid_reason", "Give a short reason for the override (3 to 500 characters).")
			}
			override = &GateOverride{Reason: reason, By: owner, At: s.now(), Failing: v.Failing}
		}
	}
	byPath := map[string]int{}
	for i, f := range set.Files {
		byPath[f.Path] = i
	}
	type plan struct {
		idx     int
		content string
		exists  bool
		partial bool
	}
	var plans []plan
	if d.All {
		for i := range set.Files {
			d.Files = append(d.Files, FileDecision{Path: set.Files[i].Path})
		}
	}
	chosen := map[string]bool{}
	for _, fd := range d.Files {
		i, ok := byPath[fd.Path]
		if !ok {
			return nil, verr("path", "unknown_file", fd.Path+" is not part of this change set.")
		}
		if chosen[fd.Path] {
			return nil, verr("path", "duplicate_path", fd.Path+" was chosen twice.")
		}
		chosen[fd.Path] = true
		f := set.Files[i]
		hs := Hunks(f.Before, f.After)
		var pick map[int]bool
		partial := false
		if len(fd.Hunks) > 0 {
			pick = map[int]bool{}
			for _, h := range fd.Hunks {
				if h < 0 || h >= len(hs) {
					return nil, verr("hunks", "unknown_hunk", "That part of the change does not exist.")
				}
				pick[h] = true
			}
			partial = len(pick) < len(hs)
		}
		text := Apply(f.Before, f.After, pick)
		exists := !(f.Op == OpDelete && text == "")
		if text == f.Before && f.Op != OpCreate {
			continue // nothing chosen that changes the file
		}
		plans = append(plans, plan{i, text, exists, partial})
	}
	if len(plans) == 0 {
		return nil, verr("files", "nothing_selected", "Choose at least one change to accept, or reject the set.")
	}
	// Nothing may have changed underneath the proposal.
	var stale []string
	for _, p := range plans {
		f := set.Files[p.idx]
		cur, exists, err := s.current(ctx, wsID, f.Path)
		if err != nil {
			return nil, err
		}
		if cur != f.Before || exists != (f.Op != OpCreate) {
			stale = append(stale, f.Path)
		}
	}
	if len(stale) > 0 {
		return nil, verr("files", "files_changed", "These files changed after the proposal, so it cannot be applied safely: "+strings.Join(stale, ", ")+". Ask for a new proposal.")
	}
	cp := &Checkpoint{ID: store.NewID("cp"), WorkspaceID: wsID, SetID: set.ID, Label: set.Title, CreatedAt: s.now()}
	for _, p := range plans {
		f := set.Files[p.idx]
		cp.Files = append(cp.Files, CheckpointFile{Path: f.Path, Existed: f.Op != OpCreate, Content: f.Before, Written: p.content, WrittenExists: p.exists})
	}
	if err := store.PutJSON(ctx, s.st, KindCheckpoint, wsID, cp.ID, cp); err != nil {
		return nil, err
	}
	var done []CheckpointFile
	for _, p := range plans {
		f := set.Files[p.idx]
		var werr error
		if p.exists {
			werr = s.write(ctx, wsID, f.Path, p.content)
		} else {
			werr = s.fs.Delete(ctx, wsID, f.Path, false)
		}
		if werr != nil {
			s.restore(ctx, wsID, done)
			_ = s.st.DeleteDoc(ctx, KindCheckpoint, cp.ID)
			s.aud.User(ctx, owner, wsID, "changeset.apply_failed", set.ID, map[string]any{"path": f.Path})
			return nil, verr("files", "write_failed", "Writing "+f.Path+" failed, so nothing was changed.")
		}
		done = append(done, cp.Files[len(done)])
	}
	now := s.now()
	all := true
	for i := range set.Files {
		set.Files[i].Status = "rejected"
	}
	for _, p := range plans {
		set.Files[p.idx].Status = "applied"
		set.Files[p.idx].Applied = p.content
		if p.partial {
			set.Files[p.idx].Status = "partial"
		}
	}
	for _, f := range set.Files {
		if f.Status != "applied" {
			all = false
		}
	}
	set.Status = StatusPartial
	if all {
		set.Status = StatusApplied
	}
	set.CheckpointID, set.DecidedAt, set.GateOverride = cp.ID, &now, override
	if err := store.PutJSON(ctx, s.st, KindSet, wsID, set.ID, set); err != nil {
		return nil, err
	}
	s.prune(ctx, wsID)
	s.aud.Record(ctx, set.Actor, set.ActorID, wsID, "changeset.applied", set.ID, map[string]any{"title": set.Title, "files": len(plans), "checkpoint": cp.ID, "acceptedBy": owner})
	if override != nil {
		s.aud.User(ctx, owner, wsID, "gates.overridden", set.ID, map[string]any{"reason": override.Reason, "failing": override.Failing})
	}
	return viewOf(set, true), nil
}

// write creates missing parent folders, then writes the file.
func (s *Service) write(ctx context.Context, wsID, p, content string) error {
	if dir := path.Dir(p); dir != "." {
		if err := s.fs.Mkdir(ctx, wsID, dir); err != nil {
			return err
		}
	}
	return s.fs.Write(ctx, wsID, p, content, time.Time{})
}

func (s *Service) restore(ctx context.Context, wsID string, files []CheckpointFile) {
	for _, f := range files {
		if f.Existed {
			_ = s.write(ctx, wsID, f.Path, f.Content)
		} else {
			_ = s.fs.Delete(ctx, wsID, f.Path, false)
		}
	}
}

func (s *Service) prune(ctx context.Context, wsID string) {
	cps, err := store.ListJSON[Checkpoint](ctx, s.st, KindCheckpoint, wsID)
	if err != nil || len(cps) <= KeepCheckpoints {
		return
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].CreatedAt.Before(cps[j].CreatedAt) })
	for _, c := range cps[:len(cps)-KeepCheckpoints] {
		_ = s.st.DeleteDoc(ctx, KindCheckpoint, c.ID)
	}
}

// CheckpointView lists a checkpoint without file contents.
type CheckpointView struct {
	ID         string     `json:"id"`
	SetID      string     `json:"setId,omitempty"`
	Label      string     `json:"label"`
	Files      []string   `json:"files"`
	CreatedAt  time.Time  `json:"createdAt"`
	RevertedAt *time.Time `json:"revertedAt,omitempty"`
}

func (s *Service) Checkpoints(ctx context.Context, owner, wsID string) ([]CheckpointView, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	cps, err := store.ListJSON[Checkpoint](ctx, s.st, KindCheckpoint, wsID)
	if err != nil {
		return nil, err
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].CreatedAt.After(cps[j].CreatedAt) })
	out := []CheckpointView{}
	for _, c := range cps {
		v := CheckpointView{ID: c.ID, SetID: c.SetID, Label: c.Label, CreatedAt: c.CreatedAt, RevertedAt: c.RevertedAt}
		for _, f := range c.Files {
			v.Files = append(v.Files, f.Path)
		}
		out = append(out, v)
	}
	return out, nil
}

// Revert restores the files a checkpoint captured. If any file has been edited
// since the apply, the revert refuses unless force is set, so later work is
// never lost by accident.
func (s *Service) Revert(ctx context.Context, owner, wsID, cpID string, force bool) (*CheckpointView, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	defer s.lock(wsID)()
	if err := s.busy(ctx, wsID); err != nil {
		return nil, err
	}
	cp, err := store.GetJSON[Checkpoint](ctx, s.st, KindCheckpoint, cpID)
	if err != nil || cp.WorkspaceID != wsID {
		return nil, store.ErrNotFound
	}
	if cp.RevertedAt != nil {
		return nil, verr("checkpoint", "already_reverted", "This checkpoint was already restored.")
	}
	if !force {
		var edited []string
		for _, f := range cp.Files {
			cur, exists, err := s.current(ctx, wsID, f.Path)
			if err != nil {
				return nil, err
			}
			if cur != f.Written || exists != f.WrittenExists {
				edited = append(edited, f.Path)
			}
		}
		if len(edited) > 0 {
			return nil, verr("files", "edited_since", "These files were edited after the change was applied: "+strings.Join(edited, ", ")+". Restoring would overwrite that work.")
		}
	}
	s.restore(ctx, wsID, cp.Files)
	now := s.now()
	cp.RevertedAt = &now
	if err := store.PutJSON(ctx, s.st, KindCheckpoint, wsID, cp.ID, cp); err != nil {
		return nil, err
	}
	if cp.SetID != "" {
		if set, err := store.GetJSON[Set](ctx, s.st, KindSet, cp.SetID); err == nil {
			set.Status = StatusReverted
			_ = store.PutJSON(ctx, s.st, KindSet, wsID, set.ID, set)
		}
	}
	s.aud.User(ctx, owner, wsID, "changeset.reverted", cp.SetID, map[string]any{"checkpoint": cp.ID, "forced": force})
	v := CheckpointView{ID: cp.ID, SetID: cp.SetID, Label: cp.Label, CreatedAt: cp.CreatedAt, RevertedAt: cp.RevertedAt}
	for _, f := range cp.Files {
		v.Files = append(v.Files, f.Path)
	}
	return &v, nil
}
