// Package secrets stores credentials with envelope encryption (PRD 11.8,
// 20.3.6). Each secret gets its own random data key; the data key encrypts the
// payload with AES-256-GCM and is itself wrapped by the master key. The record
// id is bound in as additional authenticated data, so ciphertext cannot be
// swapped between secrets. Plaintext exists only in memory, only when used.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

var (
	ErrNotFound = errors.New("secret not found")
	ErrDecrypt  = errors.New("secret could not be decrypted")
)

// Backend is the pluggable storage-and-crypto boundary. External vaults or
// KMS-backed master keys can implement it later (PRD 20.3.6).
type Backend interface {
	Put(ctx context.Context, id string, values map[string]string) error
	Get(ctx context.Context, id string) (map[string]string, error)
	Delete(ctx context.Context, id string) error
}

// Service wraps a Backend with log redaction of every value it has handled.
type Service struct {
	b    Backend
	mu   sync.RWMutex
	seen map[string]struct{}
}

func New(b Backend) *Service { return &Service{b: b, seen: map[string]struct{}{}} }

func (s *Service) remember(values map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range values {
		if len(v) >= 4 {
			s.seen[v] = struct{}{}
		}
	}
}

// Put stores the values under id and registers them for redaction.
func (s *Service) Put(ctx context.Context, id string, values map[string]string) error {
	s.remember(values)
	return s.b.Put(ctx, id, values)
}

func (s *Service) Get(ctx context.Context, id string) (map[string]string, error) {
	v, err := s.b.Get(ctx, id)
	if err == nil {
		s.remember(v)
	}
	return v, err
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.b.Delete(ctx, id) }

// Register adds ad-hoc values (for example a password that was just tested and
// not stored) so Scrub removes them from error messages.
func (s *Service) Register(values ...string) {
	m := map[string]string{}
	for i, v := range values {
		m[fmt.Sprint(i)] = v
	}
	s.remember(m)
}

// Scrub replaces every known secret value in text. Longest values first so a
// secret that contains another is fully removed.
func (s *Service) Scrub(text string) string {
	s.mu.RLock()
	vals := make([]string, 0, len(s.seen))
	for v := range s.seen {
		vals = append(vals, v)
	}
	s.mu.RUnlock()
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		text = strings.ReplaceAll(text, v, "[redacted]")
	}
	return text
}

// ---- envelope-encrypted store backend

type StoreBackend struct {
	st     store.Store
	master cipher.AEAD
}

// ParseMasterKey decodes a base64 32-byte key.
func ParseMasterKey(b64 string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("master key is not valid base64: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes (got %d)", len(k))
	}
	return k, nil
}

// LoadOrCreateMasterKey returns the key from env (base64), else from file,
// creating the file (0600) with a fresh random key when it does not exist.
// Production should always supply NEXMESH_MASTER_KEY or a mounted key file.
func LoadOrCreateMasterKey(envValue, path string) (key []byte, created bool, err error) {
	if envValue != "" {
		k, err := ParseMasterKey(envValue)
		return k, false, err
	}
	if b, err := os.ReadFile(path); err == nil {
		k, err := ParseMasterKey(string(b))
		return k, false, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, false, fmt.Errorf("write master key file: %w", err)
	}
	return k, true, nil
}

func aead(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func NewStoreBackend(st store.Store, masterKey []byte) (*StoreBackend, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	m, err := aead(masterKey)
	if err != nil {
		return nil, err
	}
	return &StoreBackend{st: st, master: m}, nil
}

func seal(a cipher.AEAD, plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plaintext, aad), nil
}

func open(a cipher.AEAD, blob, aad []byte) ([]byte, error) {
	if len(blob) < a.NonceSize() {
		return nil, ErrDecrypt
	}
	pt, err := a.Open(nil, blob[:a.NonceSize()], blob[a.NonceSize():], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func (b *StoreBackend) Put(ctx context.Context, id string, values map[string]string) error {
	payload, err := json.Marshal(values)
	if err != nil {
		return err
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return err
	}
	dek, err := aead(dataKey)
	if err != nil {
		return err
	}
	ct, err := seal(dek, payload, []byte(id))
	if err != nil {
		return err
	}
	wrapped, err := seal(b.master, dataKey, []byte(id))
	if err != nil {
		return err
	}
	return b.st.PutSecret(ctx, &domain.SecretRecord{ID: id, Ciphertext: ct, WrappedKey: wrapped, CreatedAt: time.Now().UTC()})
}

func (b *StoreBackend) Get(ctx context.Context, id string) (map[string]string, error) {
	rec, err := b.st.GetSecret(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	dataKey, err := open(b.master, rec.WrappedKey, []byte(id))
	if err != nil {
		return nil, err
	}
	dek, err := aead(dataKey)
	if err != nil {
		return nil, err
	}
	pt, err := open(dek, rec.Ciphertext, []byte(id))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if err := json.Unmarshal(pt, &out); err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

func (b *StoreBackend) Delete(ctx context.Context, id string) error {
	return b.st.DeleteSecret(ctx, id)
}
