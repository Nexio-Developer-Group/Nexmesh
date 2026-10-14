package preview

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const (
	MinShareTTL     = 5 * time.Minute
	MaxShareTTL     = 7 * 24 * time.Hour
	MaxActiveShares = 20
)

var (
	ErrSharingDisabled = errors.New("Sharing is turned off on this Nexmesh instance. An administrator can enable it with NEXMESH_ALLOW_SHARE_LINKS=true.")
	ErrShareLimit      = fmt.Errorf("A workspace can have at most %d active share links. Revoke one first.", MaxActiveShares)
)

// ShareError marks a user-correctable problem.
type ShareError struct{ Field, Msg string }

func (e *ShareError) Error() string { return e.Msg }

// EnableSharing turns share links on (they are off by default) and sets the audit sink.
func (s *Service) EnableSharing(enabled bool, aud *audit.Recorder) {
	s.shareOn = enabled
	s.aud = aud
}

func (s *Service) SharingEnabled() bool { return s.shareOn }

func shaHex(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// Share is the API view of a link; the secret is never part of it.
type Share struct {
	ID        string    `json:"id"`
	Port      int       `json:"port"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Revoked   bool      `json:"revoked"`
	Active    bool      `json:"active"`
	Hits      int       `json:"hits"`
}

func (s *Service) view(l domain.ShareLink) Share {
	return Share{ID: l.ID, Port: l.Port, Label: l.Label, CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt, Revoked: l.Revoked,
		Active: !l.Revoked && s.now().Before(l.ExpiresAt), Hits: l.Hits}
}

func (s *Service) ListShares(ctx context.Context, wsID string) ([]Share, error) {
	w, err := s.workspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]Share, 0, len(w.ShareLinks))
	for _, l := range w.ShareLinks {
		out = append(out, s.view(l))
	}
	return out, nil
}

// CreateShare makes a link for one port. The returned secret is shown to the
// owner once; only its hash is stored.
func (s *Service) CreateShare(ctx context.Context, actor, wsID string, port int, ttl time.Duration, label string) (*Share, string, error) {
	if !s.shareOn {
		return nil, "", ErrSharingDisabled
	}
	if !validPort(port) {
		return nil, "", &ShareError{"port", "Enter a port between 1 and 65535."}
	}
	if ttl < MinShareTTL || ttl > MaxShareTTL {
		return nil, "", &ShareError{"ttl", "Choose a duration between 5 minutes and 7 days."}
	}
	label = strings.TrimSpace(label)
	if len(label) > 60 {
		return nil, "", &ShareError{"label", "Label must be under 60 characters."}
	}
	rnd := make([]byte, 24)
	_, _ = rand.Read(rnd)
	id := store.NewID("shr")
	secret := id + "." + base64.RawURLEncoding.EncodeToString(rnd)
	now := s.now().UTC()
	link := domain.ShareLink{ID: id, Port: port, Label: label, SecretSHA: shaHex(secret), CreatedAt: now, ExpiresAt: now.Add(ttl), CreatedBy: actor}
	if _, err := s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		active := 0
		kept := w.ShareLinks[:0]
		for _, l := range w.ShareLinks {
			if l.Revoked || now.After(l.ExpiresAt) {
				if now.Sub(l.ExpiresAt) < 24*time.Hour { // keep recently ended links visible for a day
					kept = append(kept, l)
				}
				continue
			}
			active++
			kept = append(kept, l)
		}
		if active >= MaxActiveShares {
			return ErrShareLimit
		}
		w.ShareLinks = append(kept, link)
		return nil
	}); err != nil {
		return nil, "", err
	}
	if s.aud != nil {
		s.aud.User(ctx, actor, wsID, "share.created", strconv.Itoa(port), map[string]any{"linkId": id, "expiresAt": link.ExpiresAt, "label": label})
	}
	v := s.view(link)
	return &v, secret, nil
}

func (s *Service) RevokeShare(ctx context.Context, actor, wsID, id string) error {
	found := false
	if _, err := s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		for i := range w.ShareLinks {
			if w.ShareLinks[i].ID == id {
				w.ShareLinks[i].Revoked = true
				found = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return store.ErrNotFound
	}
	if s.aud != nil {
		s.aud.User(ctx, actor, wsID, "share.revoked", id, nil)
	}
	return nil
}

// ShareURL builds the link the owner hands out.
func (s *Service) ShareURL(r *http.Request, wsID string, port int, secret string) string {
	if s.SubdomainMode() {
		return s.origin(r, wsID, port) + "/?nx_share=" + url.QueryEscape(secret)
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/share/%s/%d/?nx_share=%s", scheme, r.Host, wsID, port, url.QueryEscape(secret))
}

func (s *Service) shareFromSecret(ctx context.Context, wsID string, port int, secret string) (*domain.ShareLink, bool) {
	if !s.shareOn {
		return nil, false
	}
	id, _, ok := strings.Cut(secret, ".")
	if !ok {
		return nil, false
	}
	w, err := s.workspace(ctx, wsID)
	if err != nil {
		return nil, false
	}
	want := shaHex(secret)
	for _, l := range w.ShareLinks {
		if l.ID != id {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(l.SecretSHA), []byte(want)) != 1 || l.Revoked || l.Port != port || !s.now().Before(l.ExpiresAt) {
			return nil, false
		}
		cp := l
		return &cp, true
	}
	return nil, false
}

func (s *Service) shareActive(ctx context.Context, wsID, id string, port int) bool {
	if !s.shareOn {
		return false
	}
	w, err := s.workspace(ctx, wsID)
	if err != nil {
		return false
	}
	for _, l := range w.ShareLinks {
		if l.ID == id {
			return !l.Revoked && l.Port == port && s.now().Before(l.ExpiresAt)
		}
	}
	return false
}

func (s *Service) noteShareHit(ctx context.Context, wsID, id string) {
	_, _ = s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		for i := range w.ShareLinks {
			if w.ShareLinks[i].ID == id {
				w.ShareLinks[i].Hits++
			}
		}
		return nil
	})
}

// ServeShare handles /share/{ws}/{port}/... (path mode): anonymous visitors
// holding a share link. Access is a path-scoped cookie bootstrapped by the
// link's secret; every request re-checks that the link is still active.
func (s *Service) ServeShare(w http.ResponseWriter, r *http.Request, wsID string, port int) {
	if !validPort(port) || !s.shareOn {
		s.deny(w)
		return
	}
	prefix := fmt.Sprintf("/share/%s/%d", wsID, port)
	if secret := r.URL.Query().Get("nx_share"); secret != "" {
		link, ok := s.shareFromSecret(r.Context(), wsID, port, secret)
		if !ok {
			s.deny(w)
			return
		}
		c := claims{WS: wsID, Port: port, Kind: "share", Ref: link.ID, Exp: minUnix(s.now().Add(cookieTTL), link.ExpiresAt)}
		http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.sign(c), Path: prefix, Expires: time.Unix(c.Exp, 0), HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
		s.noteShareHit(r.Context(), wsID, link.ID)
		s.cleanRedirect(w, r, "nx_share")
		return
	}
	ck, err := r.Cookie(CookieName)
	if err != nil {
		s.deny(w)
		return
	}
	c, ok := s.verify(ck.Value)
	if !ok || c.Kind != "share" || c.WS != wsID || c.Port != port || !s.shareActive(r.Context(), wsID, c.Ref, port) {
		s.deny(w)
		return
	}
	s.proxy(wsID, port, prefix, true).ServeHTTP(w, r)
}
