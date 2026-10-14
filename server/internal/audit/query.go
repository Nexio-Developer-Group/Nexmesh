package audit

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// Window is how many of the newest events a query considers.
const Window = 10000

// Filter selects audit events. Zero values match everything.
type Filter struct {
	ActorType string    // user | agent | system
	Action    string    // exact action or prefix ending in "."  (e.g. "git.")
	Q         string    // case-insensitive substring of action, target or actor
	From, To  time.Time // inclusive bounds on At
}

func (f Filter) match(e *domain.AuditEvent) bool {
	if f.ActorType != "" && e.ActorType != f.ActorType {
		return false
	}
	if f.Action != "" {
		if strings.HasSuffix(f.Action, ".") {
			if !strings.HasPrefix(e.Action, f.Action) {
				return false
			}
		} else if e.Action != f.Action {
			return false
		}
	}
	if !f.From.IsZero() && e.At.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && e.At.After(f.To) {
		return false
	}
	if f.Q != "" {
		q := strings.ToLower(f.Q)
		if !strings.Contains(strings.ToLower(e.Action), q) && !strings.Contains(strings.ToLower(e.Target), q) && !strings.Contains(strings.ToLower(e.ActorID), q) {
			return false
		}
	}
	return true
}

// Page is one page of newest-first results.
type Page struct {
	Events []*domain.AuditEvent `json:"events"`
	Next   string               `json:"next,omitempty"` // pass as `after` for the following page
	// Truncated is true when older events exist beyond the query window.
	Truncated bool `json:"truncated,omitempty"`
}

func (r *Recorder) newest(ctx context.Context, wsID string) ([]*domain.AuditEvent, error) {
	evs, err := r.st.ListAudit(ctx, wsID, Window)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].At.After(evs[j].At) })
	return evs, nil
}

// Query returns a filtered page. after is the Next cursor of the previous page.
func (r *Recorder) Query(ctx context.Context, wsID string, f Filter, after string, limit int) (*Page, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	evs, err := r.newest(ctx, wsID)
	if err != nil {
		return nil, err
	}
	p := &Page{Events: []*domain.AuditEvent{}, Truncated: len(evs) >= Window}
	skipping := after != ""
	for _, e := range evs {
		if skipping {
			if e.ID == after {
				skipping = false
			}
			continue
		}
		if !f.match(e) {
			continue
		}
		if len(p.Events) == limit {
			p.Next = p.Events[len(p.Events)-1].ID
			break
		}
		p.Events = append(p.Events, e)
	}
	return p, nil
}

// Export streams every matching event (within the window) as csv or json.
func (r *Recorder) Export(ctx context.Context, wsID string, f Filter, format string, w io.Writer) error {
	evs, err := r.newest(ctx, wsID)
	if err != nil {
		return err
	}
	matched := make([]*domain.AuditEvent, 0, len(evs))
	for _, e := range evs {
		if f.match(e) {
			matched = append(matched, e)
		}
	}
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(matched)
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "actor_type", "actor_id", "action", "target", "detail"})
	for _, e := range matched {
		detail := ""
		if len(e.Detail) > 0 {
			b, _ := json.Marshal(e.Detail)
			detail = string(b)
		}
		_ = cw.Write([]string{e.At.UTC().Format(time.RFC3339), e.ActorType, safeCell(e.ActorID), safeCell(e.Action), safeCell(e.Target), safeCell(detail)})
	}
	cw.Flush()
	return cw.Error()
}

// safeCell neutralises spreadsheet formula injection.
func safeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
