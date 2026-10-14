package codeagent

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/modelprovider"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

const (
	KindSession  = "agentsession"
	KindSettings = "agentsettings"

	MaxSteps       = 16
	MaxRunTime     = 10 * time.Minute
	ApprovalWait   = 30 * time.Minute
	MaxToolOutput  = 16 << 10
	MaxFileToModel = 64 << 10
	MaxTurns       = 200
	// MaxConcurrentRuns bounds model spend and load per workspace.
	MaxConcurrentRuns = 3
)

// Session statuses.
const (
	StatusIdle        = "idle"
	StatusRunning     = "running"
	StatusAwaiting    = "awaiting_approval"
	StatusError       = "error"
	StatusInterrupted = "interrupted"
)

// Call statuses.
const (
	CallOK       = "ok"
	CallError    = "error"
	CallDenied   = "denied"
	CallPending  = "needs_approval"
	CallRejected = "rejected"
)

// Model is one configured model. *modelprovider.Client satisfies it.
type Model interface {
	Chat(ctx context.Context, req modelprovider.ChatRequest) (*modelprovider.ChatResponse, *modelprovider.Error)
}

// ModelFunc resolves the model for a workspace (the default configuration when configID is empty).
type ModelFunc func(ctx context.Context, owner, wsID, configID string) (Model, error)

// FS is the slice of the workspace the agent reads and runs commands in.
type FS interface {
	List(ctx context.Context, ws, path string) ([]wsfs.Entry, error)
	Read(ctx context.Context, ws, path string) (*wsfs.Text, error)
	Run(ctx context.Context, ws, command string, timeoutSec int) (*wsfs.RunResult, error)
}

type Service struct {
	st      store.Store
	fs      FS
	changes *changeset.Service
	aud     *audit.Recorder
	model   ModelFunc
	arch    ArchSource
	vars    VarSource
	rules   func(ctx context.Context, wsID string) ([]string, error)
	secrets func(ctx context.Context, wsID string) []string
	canvas  CanvasOps
	cards   CardSource
	book    *previewBook
	now     func() time.Time

	mu   sync.Mutex
	runs map[string]*run
}

type run struct {
	ws       string
	cancel   context.CancelFunc
	approval chan bool
	done     chan struct{}
}

func New(st store.Store, fs FS, changes *changeset.Service, aud *audit.Recorder, model ModelFunc) *Service {
	return &Service{st: st, fs: fs, changes: changes, aud: aud, model: model, now: func() time.Time { return time.Now().UTC() }, runs: map[string]*run{}}
}

// WithContext sets where the canvas context comes from.
func (s *Service) WithContext(a ArchSource, v VarSource) *Service { s.arch, s.vars = a, v; return s }

// WithRules sets the source of the workspace's rule texts (PRD F21).
func (s *Service) WithRules(f func(ctx context.Context, wsID string) ([]string, error)) *Service {
	s.rules = f
	return s
}

// WithSecretValues sets a source of values that must be masked in anything
// shown to the model, as a second line of defense behind the path guard.
func (s *Service) WithSecretValues(f func(ctx context.Context, wsID string) []string) *Service {
	s.secrets = f
	return s
}

func verr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

func (s *Service) workspace(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil || w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

// Settings are the per-workspace agent controls.
type Settings struct {
	Level string `json:"level"`
	// AutoAccept writes the agent's file edits without asking. It is off by
	// default and never skips checkpoints or required quality gates (PRD F18).
	AutoAccept bool   `json:"autoAccept"`
	ConfigID   string `json:"configId,omitempty"` // empty: the workspace's default code agent configuration
}

func (s *Service) GetSettings(ctx context.Context, owner, wsID string) (*Settings, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	return s.settings(ctx, wsID), nil
}

func (s *Service) settings(ctx context.Context, wsID string) *Settings {
	if st, err := store.GetJSON[Settings](ctx, s.st, KindSettings, wsID); err == nil && ValidLevel(st.Level) {
		return st
	}
	return &Settings{Level: LevelPropose}
}

func (s *Service) SetSettings(ctx context.Context, owner, wsID string, in Settings) (*Settings, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	if !ValidLevel(in.Level) {
		return nil, verr("level", "invalid_level", "Choose read, propose or execute.")
	}
	old := s.settings(ctx, wsID)
	if err := store.PutJSON(ctx, s.st, KindSettings, wsID, wsID, &in); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "agent.permissions_changed", "", map[string]any{"from": old.Level, "to": in.Level, "autoAccept": in.AutoAccept, "wasAutoAccept": old.AutoAccept})
	return &in, nil
}

// Call is one tool use inside an assistant turn, with its outcome.
type Call struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Args        map[string]any `json:"args"`
	Result      string         `json:"result,omitempty"`
	Status      string         `json:"status"`
	ChangeSetID string         `json:"changeSetId,omitempty"`
	ProposalID  string         `json:"proposalId,omitempty"`
}

type Turn struct {
	ID      string    `json:"id"`
	Role    string    `json:"role"` // user | assistant
	Content string    `json:"content,omitempty"`
	Calls   []Call    `json:"calls,omitempty"`
	At      time.Time `json:"at"`
}

// Approval is a command waiting for the user.
type Approval struct {
	CallID  string `json:"callId"`
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

type Session struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspaceId"`
	OwnerID      string    `json:"-"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Turns        []Turn    `json:"turns"`
	Pending      *Approval `json:"pending,omitempty"`
	InputTokens  int       `json:"inputTokens"`
	OutputTokens int       `json:"outputTokens"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func (s *Service) save(ctx context.Context, sess *Session) {
	sess.UpdatedAt = s.now()
	_ = store.PutJSON(ctx, s.st, KindSession, sess.WorkspaceID, sess.ID, sess)
}

func (s *Service) active(id string) *run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

func (s *Service) load(ctx context.Context, owner, wsID, id string) (*Session, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	sess, err := store.GetJSON[Session](ctx, s.st, KindSession, id)
	if err != nil || sess.WorkspaceID != wsID {
		return nil, store.ErrNotFound
	}
	sess.OwnerID = owner
	if (sess.Status == StatusRunning || sess.Status == StatusAwaiting) && s.active(id) == nil {
		// The server restarted mid-run: nothing is working on it any more.
		sess.Status, sess.Pending = StatusInterrupted, nil
	}
	return sess, nil
}

func (s *Service) CreateSession(ctx context.Context, owner, wsID, title string) (*Session, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	title = strings.TrimSpace(title)
	if len(title) > 80 {
		title = title[:80]
	}
	now := s.now()
	sess := &Session{ID: store.NewID("ags"), WorkspaceID: wsID, OwnerID: owner, Title: title, Status: StatusIdle, Turns: []Turn{}, CreatedAt: now, UpdatedAt: now}
	s.save(ctx, sess)
	return sess, nil
}

func (s *Service) GetSession(ctx context.Context, owner, wsID, id string) (*Session, error) {
	return s.load(ctx, owner, wsID, id)
}

// SessionSummary lists a session without its turns.
type SessionSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
	Turns     int       `json:"turns"`
}

func (s *Service) ListSessions(ctx context.Context, owner, wsID string) ([]SessionSummary, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	all, err := store.ListJSON[Session](ctx, s.st, KindSession, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(all))
	for _, x := range all {
		st := x.Status
		if (st == StatusRunning || st == StatusAwaiting) && s.active(x.ID) == nil {
			st = StatusInterrupted
		}
		out = append(out, SessionSummary{ID: x.ID, Title: x.Title, Status: st, UpdatedAt: x.UpdatedAt, Turns: len(x.Turns)})
	}
	sortSummaries(out)
	return out, nil
}

func (s *Service) DeleteSession(ctx context.Context, owner, wsID, id string) error {
	if _, err := s.load(ctx, owner, wsID, id); err != nil {
		return err
	}
	if r := s.active(id); r != nil {
		r.cancel()
		<-r.done
	}
	return s.st.DeleteDoc(ctx, KindSession, id)
}

// Close stops every running session.
func (s *Service) Close() {
	s.mu.Lock()
	var rs []*run
	for _, r := range s.runs {
		rs = append(rs, r)
	}
	s.mu.Unlock()
	for _, r := range rs {
		r.cancel()
	}
	for _, r := range rs {
		<-r.done
	}
}

// Wait blocks until the session's current run ends (tests and shutdown).
func (s *Service) Wait(id string) {
	if r := s.active(id); r != nil {
		<-r.done
	}
}

// Stop cancels the current run. The conversation is kept.
func (s *Service) Stop(ctx context.Context, owner, wsID, id string) error {
	if _, err := s.load(ctx, owner, wsID, id); err != nil {
		return err
	}
	if r := s.active(id); r != nil {
		r.cancel()
	}
	return nil
}

// Approve answers the command waiting for approval.
func (s *Service) Approve(ctx context.Context, owner, wsID, id, callID string, approve bool) error {
	sess, err := s.load(ctx, owner, wsID, id)
	if err != nil {
		return err
	}
	r := s.active(id)
	if sess.Status != StatusAwaiting || sess.Pending == nil || r == nil {
		return verr("approval", "nothing_to_approve", "Nothing is waiting for approval.")
	}
	if sess.Pending.CallID != callID {
		return verr("approval", "stale_approval", "That request is no longer the one waiting.")
	}
	select {
	case r.approval <- approve:
	default:
		return verr("approval", "already_answered", "That request was already answered.")
	}
	s.aud.User(ctx, owner, wsID, "agent.command_"+map[bool]string{true: "approved", false: "declined"}[approve], id, map[string]any{"command": truncate(sess.Pending.Command, 300)})
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func sortSummaries(l []SessionSummary) {
	for i := 1; i < len(l); i++ {
		for j := i; j > 0 && l[j].UpdatedAt.After(l[j-1].UpdatedAt); j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
}

// Public returns the session as shown in the UI: large file contents the agent
// proposed are replaced by a size note (the change set holds the real text).
func (s *Session) Public() *Session {
	out := *s
	out.Turns = make([]Turn, len(s.Turns))
	for i, t := range s.Turns {
		nt := t
		nt.Calls = make([]Call, len(t.Calls))
		for j, c := range t.Calls {
			nc := c
			nc.Args = slimArgs(c.Args)
			nt.Calls[j] = nc
		}
		out.Turns[i] = nt
	}
	return &out
}

func slimArgs(a map[string]any) map[string]any {
	if a == nil {
		return nil
	}
	out := make(map[string]any, len(a))
	for k, v := range a {
		switch x := v.(type) {
		case string:
			out[k] = truncate(x, 300)
		case []any:
			var l []any
			for _, e := range x {
				if m, ok := e.(map[string]any); ok {
					nm := map[string]any{}
					for mk, mv := range m {
						if ms, ok := mv.(string); ok && mk == "content" {
							nm[mk] = "(" + itoa(len(ms)) + " characters)"
						} else {
							nm[mk] = mv
						}
					}
					l = append(l, nm)
				} else {
					l = append(l, e)
				}
			}
			out[k] = l
		default:
			out[k] = v
		}
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

// ForgetWorkspace stops runs for a workspace that is being deleted, so nothing
// writes records for it afterwards.
func (s *Service) ForgetWorkspace(wsID string) {
	s.mu.Lock()
	var rs []*run
	for _, r := range s.runs {
		if r.ws == wsID {
			rs = append(rs, r)
		}
	}
	s.mu.Unlock()
	for _, r := range rs {
		r.cancel()
	}
	for _, r := range rs {
		<-r.done
	}
}
