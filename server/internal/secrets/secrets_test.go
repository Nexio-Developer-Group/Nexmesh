package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func newBackend(t *testing.T) (*StoreBackend, store.Store, []byte) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	st := store.NewMemory()
	b, err := NewStoreBackend(st, key)
	if err != nil {
		t.Fatal(err)
	}
	return b, st, key
}

func TestRoundTripAndNoPlaintextAtRest(t *testing.T) {
	b, st, _ := newBackend(t)
	ctx := context.Background()
	if err := b.Put(ctx, "sec_1", map[string]string{"password": "hunter2-correct-horse"}); err != nil {
		t.Fatal(err)
	}
	rec, _ := st.GetSecret(ctx, "sec_1")
	if bytes.Contains(rec.Ciphertext, []byte("hunter2")) || bytes.Contains(rec.WrappedKey, []byte("hunter2")) {
		t.Fatal("plaintext found in stored record")
	}
	got, err := b.Get(ctx, "sec_1")
	if err != nil || got["password"] != "hunter2-correct-horse" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestEachEncryptionIsUnique(t *testing.T) {
	b, st, _ := newBackend(t)
	ctx := context.Background()
	_ = b.Put(ctx, "a", map[string]string{"k": "same-value"})
	_ = b.Put(ctx, "b", map[string]string{"k": "same-value"})
	ra, _ := st.GetSecret(ctx, "a")
	rb, _ := st.GetSecret(ctx, "b")
	if bytes.Equal(ra.Ciphertext, rb.Ciphertext) || bytes.Equal(ra.WrappedKey, rb.WrappedKey) {
		t.Fatal("identical plaintexts must not produce identical records")
	}
}

func TestTamperingAndSwappingAreDetected(t *testing.T) {
	b, st, _ := newBackend(t)
	ctx := context.Background()
	_ = b.Put(ctx, "a", map[string]string{"k": "value-a-1234"})
	_ = b.Put(ctx, "b", map[string]string{"k": "value-b-1234"})
	ra, _ := st.GetSecret(ctx, "a")
	ra.Ciphertext[len(ra.Ciphertext)-1] ^= 0xff
	_ = st.PutSecret(ctx, ra)
	if _, err := b.Get(ctx, "a"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered ciphertext: %v", err)
	}
	// Move record b under id a: must fail because the id is authenticated.
	rb, _ := st.GetSecret(ctx, "b")
	rb.ID = "a"
	_ = st.PutSecret(ctx, rb)
	if _, err := b.Get(ctx, "a"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("swapped record: %v", err)
	}
}

func TestWrongMasterKeyFails(t *testing.T) {
	b, st, _ := newBackend(t)
	ctx := context.Background()
	_ = b.Put(ctx, "a", map[string]string{"k": "value-1234"})
	other := make([]byte, 32)
	_, _ = rand.Read(other)
	b2, _ := NewStoreBackend(st, other)
	if _, err := b2.Get(ctx, "a"); !errors.Is(err, ErrDecrypt) {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestScrubRemovesEverySecretValue(t *testing.T) {
	b, _, _ := newBackend(t)
	s := New(b)
	ctx := context.Background()
	_ = s.Put(ctx, "a", map[string]string{"password": "p@ssw0rd-long", "token": "tok_abcdef"})
	s.Register("adhoc-secret-9")
	msg := "dial failed for user:p@ssw0rd-long@host token=tok_abcdef also adhoc-secret-9 end"
	got := s.Scrub(msg)
	for _, bad := range []string{"p@ssw0rd-long", "tok_abcdef", "adhoc-secret-9"} {
		if strings.Contains(got, bad) {
			t.Fatalf("leaked %q in %q", bad, got)
		}
	}
	if !strings.Contains(got, "dial failed") {
		t.Fatal("scrub removed too much")
	}
}

func TestMasterKeyHandling(t *testing.T) {
	if _, err := ParseMasterKey("not base64!!"); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if _, err := ParseMasterKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("short key accepted")
	}
	path := filepath.Join(t.TempDir(), "master.key")
	k1, created, err := LoadOrCreateMasterKey("", path)
	if err != nil || !created || len(k1) != 32 {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", fi.Mode().Perm())
	}
	k2, created, _ := LoadOrCreateMasterKey("", path)
	if created || !bytes.Equal(k1, k2) {
		t.Fatal("key must be stable across restarts")
	}
	envKey := base64.StdEncoding.EncodeToString(k1)
	k3, _, _ := LoadOrCreateMasterKey(envKey, "/nonexistent/x")
	if !bytes.Equal(k1, k3) {
		t.Fatal("env key not used")
	}
	if _, err := NewStoreBackend(store.NewMemory(), []byte("short")); err == nil {
		t.Fatal("short key accepted by backend")
	}
}
