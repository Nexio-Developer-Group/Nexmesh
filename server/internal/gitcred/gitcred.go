// Package gitcred stores Git access tokens for a workspace as encrypted
// secrets (PRD F10). Tokens are write-only: they can be replaced or revoked
// but never read back through the API. The control plane hands one to the
// workspace agent only for the duration of a single Git command.
package gitcred

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const MaxCredentials = 10

var (
	hostRE     = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:[0-9]{1,5})?$`)
	usernameRE = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,100}$`)
)

type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func bad(field, format string, a ...any) error {
	return &ValidationError{field, fmt.Sprintf(format, a...)}
}

type provider struct{ host, defaultUser string }

var providers = map[string]provider{
	"github":    {"github.com", "x-access-token"},
	"gitlab":    {"gitlab.com", "oauth2"},
	"bitbucket": {"bitbucket.org", ""},
	"custom":    {"", ""},
}

type Service struct {
	st  store.Store
	sec *secrets.Service
	aud *audit.Recorder
}

func NewService(st store.Store, sec *secrets.Service, aud *audit.Recorder) *Service {
	return &Service{st: st, sec: sec, aud: aud}
}

func secretID(wsID, id string) string { return "gitcred_" + wsID + "_" + id }

func (s *Service) workspace(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil || w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

func (s *Service) List(ctx context.Context, owner, wsID string) ([]domain.GitCredential, error) {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	out := append([]domain.GitCredential{}, w.GitCredentials...)
	return out, nil
}

type AddInput struct {
	Provider string
	Host     string
	Username string
	Token    string
	Label    string
}

func validToken(t string) error {
	if len(t) < 8 || len(t) > 4096 {
		return bad("token", "Paste a personal access token (8 to 4096 characters).")
	}
	for _, r := range t {
		if r <= ' ' || r == 0x7f {
			return bad("token", "The token must not contain spaces or control characters.")
		}
	}
	return nil
}

func normalizeHost(h string) string { return strings.ToLower(strings.TrimSpace(h)) }

func (s *Service) Add(ctx context.Context, owner, wsID string, in AddInput) (*domain.GitCredential, error) {
	p, ok := providers[in.Provider]
	if !ok {
		return nil, bad("provider", "Choose GitHub, GitLab, Bitbucket or a custom host.")
	}
	host := normalizeHost(in.Host)
	if host == "" {
		host = p.host
	}
	if host == "" || !hostRE.MatchString(host) {
		return nil, bad("host", "Enter a host name such as git.example.com (no https:// or path).")
	}
	user := strings.TrimSpace(in.Username)
	if user == "" {
		user = p.defaultUser
	}
	if !usernameRE.MatchString(user) {
		return nil, bad("username", "Enter the account username for this host.")
	}
	if err := validToken(in.Token); err != nil {
		return nil, err
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = host
	}
	if len(label) > 80 {
		return nil, bad("label", "Label must be under 80 characters.")
	}
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	if len(w.GitCredentials) >= MaxCredentials {
		return nil, bad("host", "A workspace can hold at most %d Git credentials.", MaxCredentials)
	}
	for _, c := range w.GitCredentials {
		if c.Host == host && c.Username == user {
			return nil, bad("host", "A credential for %s as %s already exists. Replace its token instead.", host, user)
		}
	}
	now := time.Now().UTC()
	c := domain.GitCredential{ID: store.NewID("gc"), Label: label, Provider: in.Provider, Host: host, Username: user, CreatedAt: now, UpdatedAt: now}
	if err := s.sec.Put(ctx, secretID(wsID, c.ID), map[string]string{"token": in.Token}); err != nil {
		return nil, err
	}
	if _, err := s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		w.GitCredentials = append(w.GitCredentials, c)
		return nil
	}); err != nil {
		_ = s.sec.Delete(ctx, secretID(wsID, c.ID))
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "git.credential_added", c.Host, map[string]any{"provider": c.Provider, "username": c.Username})
	return &c, nil
}

// Update changes the label and/or replaces the token.
func (s *Service) Update(ctx context.Context, owner, wsID, id string, label, token *string) (*domain.GitCredential, error) {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, c := range w.GitCredentials {
		found = found || c.ID == id
	}
	if !found {
		return nil, store.ErrNotFound
	}
	if token != nil {
		if err := validToken(*token); err != nil {
			return nil, err
		}
	}
	if label != nil && (strings.TrimSpace(*label) == "" || len(*label) > 80) {
		return nil, bad("label", "Label must be 1 to 80 characters.")
	}
	if token != nil {
		if err := s.sec.Put(ctx, secretID(wsID, id), map[string]string{"token": *token}); err != nil {
			return nil, err
		}
	}
	var out domain.GitCredential
	if _, err := s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		for i := range w.GitCredentials {
			if w.GitCredentials[i].ID == id {
				if label != nil {
					w.GitCredentials[i].Label = strings.TrimSpace(*label)
				}
				w.GitCredentials[i].UpdatedAt = time.Now().UTC()
				out = w.GitCredentials[i]
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "git.credential_updated", out.Host, map[string]any{"tokenReplaced": token != nil})
	return &out, nil
}

// Revoke deletes the credential and its token. Running Git commands keep the
// copy they were started with; new ones cannot authenticate.
func (s *Service) Revoke(ctx context.Context, owner, wsID, id string) error {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return err
	}
	var gone *domain.GitCredential
	for i := range w.GitCredentials {
		if w.GitCredentials[i].ID == id {
			c := w.GitCredentials[i]
			gone = &c
		}
	}
	if gone == nil {
		return store.ErrNotFound
	}
	if err := s.sec.Delete(ctx, secretID(wsID, id)); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if _, err := s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		out := w.GitCredentials[:0]
		for _, c := range w.GitCredentials {
			if c.ID != id {
				out = append(out, c)
			}
		}
		w.GitCredentials = out
		return nil
	}); err != nil {
		return err
	}
	s.aud.User(ctx, owner, wsID, "git.credential_revoked", gone.Host, map[string]any{"provider": gone.Provider})
	return nil
}

// Secret is a resolved credential for one command. It must not be logged,
// stored or returned by any API.
type Secret struct {
	Host     string
	Username string
	Token    string
}

// Resolve finds the credential for a host (and optionally a specific id).
// It returns (nil, nil) when none is configured, so public repositories work.
func (s *Service) Resolve(ctx context.Context, wsID, host, credentialID string) (*Secret, error) {
	w, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	host = normalizeHost(host)
	for _, c := range w.GitCredentials {
		if (credentialID != "" && c.ID != credentialID) || (credentialID == "" && c.Host != host) {
			continue
		}
		m, err := s.sec.Get(ctx, secretID(wsID, c.ID))
		if err != nil {
			return nil, err
		}
		return &Secret{Host: c.Host, Username: c.Username, Token: m["token"]}, nil
	}
	if credentialID != "" {
		return nil, store.ErrNotFound
	}
	return nil, nil
}

// DeleteAll removes every Git secret of a workspace (workspace deletion).
func (s *Service) DeleteAll(ctx context.Context, w *domain.Workspace) {
	for _, c := range w.GitCredentials {
		_ = s.sec.Delete(ctx, secretID(w.ID, c.ID))
	}
}
