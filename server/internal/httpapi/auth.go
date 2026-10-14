package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/auth"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

const sessionCookie = "nexmesh_session"

type ctxKey struct{}

type authCtx struct {
	user *domain.User
}

func init() {
	extraErrors = append(extraErrors, func(err error) (int, string, bool) {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUnauthenticated):
			return http.StatusUnauthorized, "unauthenticated", true
		case errors.Is(err, auth.ErrSignupClosed):
			return http.StatusForbidden, "signup_closed", true
		case errors.Is(err, auth.ErrEmailTaken):
			return http.StatusConflict, "email_taken", true
		case errors.Is(err, auth.ErrTooManyAttempts):
			return http.StatusTooManyRequests, "rate_limited", true
		}
		return 0, "", false
	})
}

// CurrentUser returns the signed-in user, or nil when auth is disabled or the
// request is unauthenticated.
func CurrentUser(ctx context.Context) *domain.User {
	if a, ok := ctx.Value(ctxKey{}).(*authCtx); ok {
		return a.user
	}
	return nil
}

type userDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

func toUser(u *domain.User) userDTO { return userDTO{u.ID, u.Email, u.Name, u.Role} }

// sameOrigin rejects cross-site writes: an Origin header, when present, must
// name this host or the configured public URL.
func sameOrigin(r *http.Request, publicURL string) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if publicURL != "" {
		if p, err := url.Parse(publicURL); err == nil && strings.EqualFold(p.Host, u.Host) {
			return true
		}
	}
	return false
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// publicPath lists routes reachable without a session.
func publicPath(p string) bool {
	switch p {
	case "/healthz", "/api/auth/login", "/api/auth/register", "/api/auth/status":
		return true
	}
	return !strings.HasPrefix(p, "/api/") // agent API has its own token auth; UI assets
}

// RegisterAuth wires account endpoints and, when enabled, session enforcement
// for every /api route. With auth disabled the API acts as one local user.
func (s *Server) RegisterAuth(svc *auth.Service, enabled bool, publicURL string) {
	s.authEnabled = enabled
	s.publicURL = publicURL
	secure := strings.HasPrefix(publicURL, "https://")

	setCookie := func(w http.ResponseWriter, r *http.Request, tok string, exp time.Time) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: secure || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
	}
	s.Handle("GET /api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "signupOpen": svc.SignupOpen(r.Context())})
	})
	s.Handle("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if u := CurrentUser(r.Context()); u != nil {
			WriteJSON(w, http.StatusOK, map[string]any{"user": toUser(u)})
			return
		}
		if !enabled {
			WriteJSON(w, http.StatusOK, map[string]any{"user": userDTO{ID: "local", Email: "local@nexmesh", Name: "Local user", Role: domain.RoleOwner}})
			return
		}
		s.HandleError(w, auth.ErrUnauthenticated)
	})
	type creds struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	start := func(w http.ResponseWriter, r *http.Request, u *domain.User) {
		tok, exp, err := svc.NewSession(r.Context(), u.ID)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		setCookie(w, r, tok, exp)
		WriteJSON(w, http.StatusOK, map[string]any{"user": toUser(u)})
	}
	s.Handle("POST /api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var in creds
		if !DecodeJSON(w, r, &in) {
			return
		}
		u, err := svc.Register(r.Context(), in.Email, in.Name, in.Password)
		if err != nil {
			s.authError(w, err)
			return
		}
		start(w, r, u)
	})
	s.Handle("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in creds
		if !DecodeJSON(w, r, &in) {
			return
		}
		u, err := svc.Login(r.Context(), in.Email, in.Password, clientIP(r))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		start(w, r, u)
	})
	s.Handle("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil {
			svc.Logout(r.Context(), c.Value)
		}
		setCookie(w, r, "", time.Unix(0, 0))
		w.WriteHeader(http.StatusNoContent)
	})
	s.authSvc = svc
}

func (s *Server) authError(w http.ResponseWriter, err error) {
	var fe *auth.FieldError
	if errors.As(err, &fe) {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: fe.Msg, Field: fe.Field}})
		return
	}
	s.HandleError(w, err)
}

// authenticate is the session middleware.
func (s *Server) authenticate(next http.Handler) http.Handler {
	if !s.authEnabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u *domain.User
		if c, err := r.Cookie(sessionCookie); err == nil {
			u, _ = s.authSvc.UserFor(r.Context(), c.Value)
		}
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if (unsafe || r.Header.Get("Upgrade") != "") && !sameOrigin(r, s.publicURL) {
			WriteError(w, http.StatusForbidden, "bad_origin", "Cross-site request blocked.")
			return
		}
		if u == nil && !publicPath(r.URL.Path) {
			s.HandleError(w, auth.ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &authCtx{user: u})))
	})
}
