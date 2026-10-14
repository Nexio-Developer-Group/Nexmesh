package changeset

import (
	"context"
	"sort"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// Files written by accepted agent changes that are not committed yet. Git
// commits use this to mark agent-authored work as such (PRD F18, F10).

const kindTouched = "agenttouched"

const maxTouched = 500

type touchedEntry struct {
	Path  string    `json:"path"`
	SetID string    `json:"setId"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
}

type touchedDoc struct {
	WorkspaceID string         `json:"workspaceId"`
	Entries     []touchedEntry `json:"entries"`
}

func (s *Service) loadTouched(ctx context.Context, wsID string) *touchedDoc {
	d, err := store.GetJSON[touchedDoc](ctx, s.st, kindTouched, wsID)
	if err != nil {
		return &touchedDoc{WorkspaceID: wsID}
	}
	return d
}

// recordTouched notes files an accepted agent change set wrote.
func (s *Service) recordTouched(ctx context.Context, set *Set, paths []string) {
	d := s.loadTouched(ctx, set.WorkspaceID)
	byPath := map[string]touchedEntry{}
	for _, e := range d.Entries {
		byPath[e.Path] = e
	}
	now := s.now()
	for _, p := range paths {
		byPath[p] = touchedEntry{Path: p, SetID: set.ID, Title: set.Title, At: now}
	}
	d.Entries = d.Entries[:0]
	for _, e := range byPath {
		d.Entries = append(d.Entries, e)
	}
	sort.Slice(d.Entries, func(i, j int) bool { return d.Entries[i].At.After(d.Entries[j].At) })
	if len(d.Entries) > maxTouched {
		d.Entries = d.Entries[:maxTouched]
	}
	_ = store.PutJSON(ctx, s.st, kindTouched, set.WorkspaceID, set.WorkspaceID, d)
}

// AgentFiles returns the workspace-relative paths written by accepted agent
// changes and not yet committed, with the change set title that wrote each.
func (s *Service) AgentFiles(ctx context.Context, wsID string) map[string]string {
	out := map[string]string{}
	for _, e := range s.loadTouched(ctx, wsID).Entries {
		out[e.Path] = e.Title
	}
	return out
}

// ConsumeAgentFiles forgets paths that have been committed.
func (s *Service) ConsumeAgentFiles(ctx context.Context, wsID string, paths []string) {
	gone := map[string]bool{}
	for _, p := range paths {
		gone[p] = true
	}
	d := s.loadTouched(ctx, wsID)
	kept := d.Entries[:0]
	for _, e := range d.Entries {
		if !gone[e.Path] {
			kept = append(kept, e)
		}
	}
	d.Entries = kept
	_ = store.PutJSON(ctx, s.st, kindTouched, wsID, wsID, d)
}
