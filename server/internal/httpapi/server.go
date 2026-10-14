// Package httpapi exposes the control-plane REST API.
package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"github.com/nexio-developer-group/nexmesh/server/internal/auth"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Server struct {
	log *slog.Logger
	mux *http.ServeMux

	authEnabled bool
	publicURL   string
	authSvc     *auth.Service
	activity    Activity
}

func New(log *slog.Logger) *Server {
	s := &Server{log: log, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return s
}

// Handle registers a route on the underlying mux (Go 1.22 pattern syntax).
func (s *Server) Handle(pattern string, h http.HandlerFunc) { s.mux.HandleFunc(pattern, h) }

func (s *Server) Handler() http.Handler { return s.logging(s.recover(s.authenticate(s.track(s.mux)))) }

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "dur", time.Since(start))
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec)
				WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// Hijack lets WebSocket upgrades through the logging wrapper.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking not supported")
	}
	w.status = http.StatusSwitchingProtocols
	return h.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush keeps SSE working through the logging wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteError(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, map[string]ErrorBody{"error": {Code: code, Message: msg}})
}

// Activity records use of a workspace so idle workspaces can be stopped.
type Activity interface {
	Touch(workspaceID string)
	Open(workspaceID string) (end func())
}

// WithActivity enables activity tracking for every /api/workspaces/{id}/..., preview and share request.
func (s *Server) WithActivity(a Activity) { s.activity = a }

var wsPathRE = regexp.MustCompile(`^/(?:api/workspaces|api/preview|share)/(ws_[a-f0-9]+)(?:/|$)`)

// TrackRequest records a request for the workspace named in its path (if any).
// Long-lived requests (event streams, WebSocket upgrades) keep the workspace
// active for as long as they stay open; the returned function ends that.
func TrackRequest(a Activity, r *http.Request, wsID string) (end func()) {
	if a == nil || wsID == "" {
		return func() {}
	}
	if r.Header.Get("Upgrade") != "" || strings.HasSuffix(r.URL.Path, "/events") {
		return a.Open(wsID)
	}
	a.Touch(wsID)
	return func() {}
}

func (s *Server) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.activity != nil {
			if m := wsPathRE.FindStringSubmatch(r.URL.Path); m != nil {
				defer TrackRequest(s.activity, r, m[1])()
			}
		}
		next.ServeHTTP(w, r)
	})
}
