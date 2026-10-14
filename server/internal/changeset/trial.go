package changeset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const kindTrial = "cstrial"

func contentHash(set *Set) string {
	h := sha256.New()
	for _, f := range set.Files {
		h.Write([]byte(f.Path + "\x00" + f.Op + "\x00" + f.After + "\x01"))
	}
	return hex.EncodeToString(h.Sum(nil)[:10])
}

// trial records files as they were before a trial apply so a crash cannot
// leave the proposed content behind.
type trial struct {
	WorkspaceID string           `json:"workspaceId"`
	SetID       string           `json:"setId"`
	Files       []CheckpointFile `json:"files"`
}

// busy refuses work that would read or write files while gates are running on
// a trial apply, because the files on disk are temporarily the proposed ones.
func (s *Service) busy(ctx context.Context, wsID string) error {
	s.mu.Lock()
	active := s.trials[wsID]
	s.mu.Unlock()
	if active {
		return verr("gates", "gates_running", "Quality gates are running on a proposed change. Wait for them to finish.")
	}
	s.recoverTrial(ctx, wsID)
	return nil
}

// recoverTrial restores files from a trial that never finished (a crash).
func (s *Service) recoverTrial(ctx context.Context, wsID string) {
	t, err := store.GetJSON[trial](ctx, s.st, kindTrial, wsID)
	if err != nil || t.WorkspaceID != wsID {
		return
	}
	s.restore(ctx, wsID, t.Files)
	_ = s.st.DeleteDoc(ctx, kindTrial, wsID)
}

// ContentHash returns the identity of a change set's proposed content.
func (s *Service) ContentHash(ctx context.Context, owner, wsID, setID string) (string, error) {
	set, err := s.load(ctx, owner, wsID, setID)
	if err != nil {
		return "", err
	}
	return set.Hash, nil
}

// WithTrial writes every file of a proposed set to the workspace, runs fn, and
// always puts the files back as they were. Nothing else may change files
// while it runs. fn receives the content hash so results can be tied to it.
func (s *Service) WithTrial(ctx context.Context, owner, wsID, setID string, fn func(ctx context.Context, hash string, files []string) error) error {
	set, err := s.load(ctx, owner, wsID, setID)
	if err != nil {
		return err
	}
	unlock := s.lock(wsID)
	s.mu.Lock()
	if s.trials[wsID] {
		s.mu.Unlock()
		unlock()
		return verr("gates", "gates_running", "Quality gates are already running for this workspace.")
	}
	s.trials[wsID] = true
	s.mu.Unlock()
	unlock() // the flag, not the lock, excludes other work for the duration

	defer func() {
		s.mu.Lock()
		delete(s.trials, wsID)
		s.mu.Unlock()
	}()
	s.recoverTrial(ctx, wsID)
	if set.Status != StatusProposed {
		return verr("status", "already_decided", "This change set has already been decided.")
	}
	tr := &trial{WorkspaceID: wsID, SetID: setID}
	paths := make([]string, 0, len(set.Files))
	for _, f := range set.Files {
		cur, exists, err := s.current(ctx, wsID, f.Path)
		if err != nil {
			return err
		}
		if cur != f.Before || exists != (f.Op != OpCreate) {
			return verr("files", "files_changed", f.Path+" changed after the proposal, so it cannot be checked. Ask for a new proposal.")
		}
		tr.Files = append(tr.Files, CheckpointFile{Path: f.Path, Existed: f.Op != OpCreate, Content: f.Before})
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	if err := store.PutJSON(ctx, s.st, kindTrial, wsID, wsID, tr); err != nil {
		return err
	}
	// Restore with a context that survives cancellation of the request.
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		s.restore(rctx, wsID, tr.Files)
		_ = s.st.DeleteDoc(rctx, kindTrial, wsID)
	}()
	for _, f := range set.Files {
		var werr error
		if f.Op == OpDelete {
			werr = s.fs.Delete(ctx, wsID, f.Path, false)
		} else {
			werr = s.write(ctx, wsID, f.Path, f.After)
		}
		if werr != nil {
			return verr("files", "write_failed", "Could not stage "+f.Path+" for the checks.")
		}
	}
	s.aud.User(ctx, owner, wsID, "changeset.trial", setID, map[string]any{"files": len(set.Files)})
	return fn(ctx, set.Hash, paths)
}
