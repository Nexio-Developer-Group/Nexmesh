// Package auth implements accounts and browser sessions: argon2id password
// hashing, hashed session tokens and a small login rate limiter.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("Email or password is incorrect.")
	ErrSignupClosed       = errors.New("Sign-up is closed on this instance. Ask the owner for an account.")
	ErrEmailTaken         = errors.New("An account with this email already exists.")
	ErrTooManyAttempts    = errors.New("Too many sign-in attempts. Try again in a few minutes.")
	ErrUnauthenticated    = errors.New("Sign in to continue.")
)

// FieldError marks a validation problem on one field.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

const (
	SessionTTL = 14 * 24 * time.Hour
	minPwLen   = 10
)

type Service struct {
	st          store.Store
	allowSignup bool
	now         func() time.Time

	mu       sync.Mutex
	failures map[string][]time.Time
}

func NewService(st store.Store, allowSignup bool) *Service {
	return &Service{st: st, allowSignup: allowSignup, now: time.Now, failures: map[string][]time.Time{}}
}

// --- passwords ---

const (
	argonTime    = 2
	argonMemory  = 32 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash equalises timing for unknown emails.
var dummyHash, _ = HashPassword("nexmesh-timing-equaliser")

// --- accounts ---

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Register creates an account. The first account on an instance is always
// allowed and becomes the owner; later ones need NEXMESH_ALLOW_SIGNUP.
func (s *Service) Register(ctx context.Context, email, name, password string) (*domain.User, error) {
	email = normEmail(email)
	if _, err := mail.ParseAddress(email); err != nil || strings.ContainsAny(email, " <>") {
		return nil, &FieldError{"email", "Enter a valid email address."}
	}
	if len(password) < minPwLen {
		return nil, &FieldError{"password", fmt.Sprintf("Use at least %d characters.", minPwLen)}
	}
	if len(password) > 256 {
		return nil, &FieldError{"password", "Password is too long."}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	n, err := s.st.CountUsers(ctx)
	if err != nil {
		return nil, err
	}
	if n > 0 && !s.allowSignup {
		return nil, ErrSignupClosed
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	role := "member"
	if n == 0 {
		role = domain.RoleOwner
	}
	u := &domain.User{ID: store.NewID("usr"), Email: email, Name: name, Role: role, PasswordHash: hash, CreatedAt: s.now().UTC()}
	if err := s.st.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return u, nil
}

func (s *Service) throttled(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-15 * time.Minute)
	keep := s.failures[key][:0]
	for _, t := range s.failures[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	s.failures[key] = keep
	return len(keep) >= 8
}

func (s *Service) fail(key string) {
	s.mu.Lock()
	s.failures[key] = append(s.failures[key], s.now())
	s.mu.Unlock()
}

// Login verifies credentials; ip is only used for rate limiting.
func (s *Service) Login(ctx context.Context, email, password, ip string) (*domain.User, error) {
	email = normEmail(email)
	k1, k2 := "e:"+email, "i:"+ip
	if s.throttled(k1) || s.throttled(k2) {
		return nil, ErrTooManyAttempts
	}
	u, err := s.st.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			VerifyPassword(password, dummyHash)
			s.fail(k1)
			s.fail(k2)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !VerifyPassword(password, u.PasswordHash) {
		s.fail(k1)
		s.fail(k2)
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// --- sessions ---

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// NewSession returns the cookie token; only its hash is stored.
func (s *Service) NewSession(ctx context.Context, userID string) (token string, exp time.Time, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	now := s.now().UTC()
	exp = now.Add(SessionTTL)
	err = s.st.PutSession(ctx, &domain.Session{ID: hashToken(token), UserID: userID, CreatedAt: now, ExpiresAt: exp})
	return
}

// UserFor resolves a cookie token to its user.
func (s *Service) UserFor(ctx context.Context, token string) (*domain.User, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	id := hashToken(token)
	sess, err := s.st.GetSession(ctx, id)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	if !sess.ExpiresAt.After(s.now()) {
		_ = s.st.DeleteSession(ctx, id)
		return nil, ErrUnauthenticated
	}
	u, err := s.st.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	return u, nil
}

func (s *Service) Logout(ctx context.Context, token string) {
	if token != "" {
		_ = s.st.DeleteSession(ctx, hashToken(token))
	}
}

// SignupOpen reports whether the sign-up form should be offered.
func (s *Service) SignupOpen(ctx context.Context) bool {
	n, err := s.st.CountUsers(ctx)
	return err == nil && (n == 0 || s.allowSignup)
}
