// Package agentapi is the control-plane side of the workspace agent link.
// Agents always dial out (PRD 20.3.4), so workspaces need no inbound ports.
// The Phase 1 transport is HTTPS request/response with heartbeats; the same
// protocol messages move onto a bidirectional gRPC stream in Phase 2, when
// terminals and command execution need server-initiated traffic.
package agentapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const (
	HeartbeatEvery = 10 * time.Second
	StaleAfter     = 35 * time.Second
)

type Hello struct {
	Hostname     string `json:"hostname"`
	AgentVersion string `json:"agentVersion"`
	OS           string `json:"os"`
}

type HelloResponse struct {
	WorkspaceID    string `json:"workspaceId"`
	HeartbeatEvery int    `json:"heartbeatEverySeconds"`
}

type Heartbeat struct {
	Runtimes map[string][]string `json:"runtimes,omitempty"`
}

type Registry struct {
	st  store.Store
	bus *events.Bus
	mu  sync.Mutex
	now func() time.Time
}

func NewRegistry(st store.Store, bus *events.Bus) *Registry {
	return &Registry{st: st, bus: bus, now: time.Now}
}

func tokenKey(wsID string) string { return "agenttoken_" + wsID }

func hash(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }

// IssueToken creates a registration token for a workspace. Only its SHA-256
// hash is stored; the plaintext goes to the container environment once.
func (r *Registry) IssueToken(ctx context.Context, wsID string) (string, error) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	err := r.st.PutSecret(ctx, &domain.SecretRecord{ID: tokenKey(wsID), Ciphertext: hash(tok), CreatedAt: r.now().UTC()})
	return tok, err
}

func (r *Registry) authenticate(req *http.Request, wsID string) bool {
	h := req.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || wsID == "" {
		return false
	}
	rec, err := r.st.GetSecret(req.Context(), tokenKey(wsID))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(rec.Ciphertext, hash(tok)) == 1
}

func (r *Registry) touch(ctx context.Context, wsID string, connected bool) {
	now := r.now().UTC()
	w, err := r.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		w.AgentConnected = connected
		if connected {
			w.AgentLastSeen = &now
		}
		return nil
	})
	if err == nil {
		r.bus.Publish(wsID, events.Event{Type: "workspace", Data: w})
	}
}

// MarkStale flips agents that stopped heartbeating to disconnected.
func (r *Registry) MarkStale(ctx context.Context) {
	list, err := r.st.ListWorkspaces(ctx, "")
	if err != nil {
		return
	}
	for _, w := range list {
		if w.AgentConnected && w.AgentLastSeen != nil && r.now().Sub(*w.AgentLastSeen) > StaleAfter {
			r.touch(ctx, w.ID, false)
		}
	}
}

func (r *Registry) Run(ctx context.Context) {
	t := time.NewTicker(HeartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.MarkStale(ctx)
		}
	}
}

// Register mounts the agent endpoints on the API server.
func (r *Registry) Register(s *httpapi.Server) {
	s.Handle("POST /agent/v1/workspaces/{id}/hello", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		if !r.authenticate(req, id) {
			httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid agent token")
			return
		}
		var h Hello
		if !httpapi.DecodeJSON(w, req, &h) {
			return
		}
		r.touch(req.Context(), id, true)
		httpapi.WriteJSON(w, http.StatusOK, HelloResponse{WorkspaceID: id, HeartbeatEvery: int(HeartbeatEvery.Seconds())})
	})
	s.Handle("POST /agent/v1/workspaces/{id}/heartbeat", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		if !r.authenticate(req, id) {
			httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid agent token")
			return
		}
		var hb Heartbeat
		if !httpapi.DecodeJSON(w, req, &hb) {
			return
		}
		r.touch(req.Context(), id, true)
		w.WriteHeader(http.StatusNoContent)
	})
}

// TokenHashHex is exposed for tests.
func TokenHashHex(tok string) string { return hex.EncodeToString(hash(tok)) }
