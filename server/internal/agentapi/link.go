package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
)

// ErrOffline is returned when a workspace's agent is not connected.
var ErrOffline = errors.New("The workspace agent is not connected. Start the workspace and wait for it to report ready.")

// Subscriber receives events the agent emits (pty output, port changes, ...).
type Subscriber = func(method string, params json.RawMessage)

type link struct {
	peer  *agentproto.Peer
	hello Hello
}

type linkState struct {
	mu    sync.Mutex
	links map[string]*link
	subs  map[string]map[int]Subscriber
	next  int
}

func (r *Registry) ls() *linkState {
	r.lsOnce.Do(func() { r.link = &linkState{links: map[string]*link{}, subs: map[string]map[int]Subscriber{}} })
	return r.link
}

// Call sends a request to the workspace's agent and decodes the result.
func (r *Registry) Call(ctx context.Context, wsID, method string, params, out any) error {
	s := r.ls()
	s.mu.Lock()
	l := s.links[wsID]
	s.mu.Unlock()
	if l == nil {
		return ErrOffline
	}
	return l.peer.Call(ctx, method, params, out)
}

// Online reports whether an agent link is currently open.
func (r *Registry) Online(wsID string) bool {
	s := r.ls()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links[wsID] != nil
}

// Subscribe delivers every agent event for a workspace until cancel is called.
func (r *Registry) Subscribe(wsID string, fn Subscriber) (cancel func()) {
	s := r.ls()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := s.next
	if s.subs[wsID] == nil {
		s.subs[wsID] = map[int]Subscriber{}
	}
	s.subs[wsID][id] = fn
	return func() { s.mu.Lock(); delete(s.subs[wsID], id); s.mu.Unlock() }
}

func (r *Registry) dispatch(wsID, method string, params json.RawMessage) {
	s := r.ls()
	s.mu.Lock()
	fns := make([]Subscriber, 0, len(s.subs[wsID]))
	for _, f := range s.subs[wsID] {
		fns = append(fns, f)
	}
	s.mu.Unlock()
	for _, f := range fns {
		f(method, params)
	}
}

func (r *Registry) serveLink(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if !r.authenticate(req, id) {
		httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid agent token")
		return
	}
	c, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true}) // agents are not browsers; token-authenticated
	if err != nil {
		return
	}
	peer := agentproto.New(c)
	l := &link{peer: peer}
	peer.OnEvent(func(method string, params json.RawMessage) {
		if method == "hello" {
			_ = json.Unmarshal(params, &l.hello)
			return
		}
		r.dispatch(id, method, params)
	})

	s := r.ls()
	s.mu.Lock()
	if old := s.links[id]; old != nil {
		old.peer.Close() // a reconnecting agent supersedes its stale link
	}
	s.links[id] = l
	s.mu.Unlock()
	r.touch(req.Context(), id, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // liveness: ping, refresh last-seen, drop on silence
		t := time.NewTicker(HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pc := context.WithTimeout(ctx, 10*time.Second)
				err := peer.Ping(pctx)
				pc()
				if err != nil {
					peer.Close()
					return
				}
				r.touch(ctx, id, true)
			}
		}
	}()

	_ = peer.Serve(ctx)

	s.mu.Lock()
	current := s.links[id] == l
	if current {
		delete(s.links, id)
	}
	s.mu.Unlock()
	if current {
		r.touch(context.Background(), id, false)
		r.dispatch(id, "link.closed", nil)
	}
}

// Drop closes a workspace's agent link (used when the workspace stops or is
// deleted). A running agent will reconnect with backoff if the workspace is
// still up.
func (r *Registry) Drop(wsID string) {
	s := r.ls()
	s.mu.Lock()
	l := s.links[wsID]
	s.mu.Unlock()
	if l != nil {
		l.peer.Close()
	}
}
