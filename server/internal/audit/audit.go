// Package audit records state-changing actions (PRD 18.4 rule 4). Detail maps
// are scrubbed so a secret can never be written by accident.
package audit

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type Recorder struct {
	st  store.Store
	log *slog.Logger
}

func New(st store.Store, log *slog.Logger) *Recorder { return &Recorder{st: st, log: log} }

var sensitive = []string{"password", "secret", "apikey", "api_key", "token", "key", "credential", "header"}

// Scrub replaces values of sensitive-looking keys, recursively.
func Scrub(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		redact := false
		for _, s := range sensitive {
			if strings.Contains(lk, s) && !strings.HasSuffix(lk, "ref") && !strings.HasSuffix(lk, "id") && lk != "keyrotated" {
				redact = true
			}
		}
		switch {
		case redact:
			out[k] = "[redacted]"
		default:
			if sub, ok := v.(map[string]any); ok {
				out[k] = Scrub(sub)
			} else {
				out[k] = v
			}
		}
	}
	return out
}

// Record appends an event. Failures are logged, never returned to callers:
// auditing must not break the user's action.
func (r *Recorder) Record(ctx context.Context, actorType, actorID, workspaceID, action, target string, detail map[string]any) {
	e := &domain.AuditEvent{ID: store.NewID("aud"), WorkspaceID: workspaceID, ActorType: actorType, ActorID: actorID,
		Action: action, Target: target, Detail: Scrub(detail), At: time.Now().UTC()}
	if err := r.st.AppendAudit(ctx, e); err != nil {
		r.log.Error("audit append failed", "action", action, "err", err)
	}
}

// User is shorthand for the common case.
func (r *Recorder) User(ctx context.Context, userID, workspaceID, action, target string, detail map[string]any) {
	r.Record(ctx, domain.ActorUser, userID, workspaceID, action, target, detail)
}
