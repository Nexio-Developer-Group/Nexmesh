package workfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

func setup(t *testing.T) (*FS, string) {
	t.Helper()
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, dir
}

func code(err error) string {
	var e *agentproto.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "raw:" + err.Error()
}

func TestWriteReadListRoundTrip(t *testing.T) {
	f, _ := setup(t)
	if err := f.Mkdir("src/app"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write("src/app/main.go", "package main\n", time.Time{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write("README.md", "# hi", time.Time{}, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := f.Read("/src/app/main.go")
	if err != nil || r.Content != "package main\n" {
		t.Fatalf("read: %v %+v", err, r)
	}
	es, _ := f.List("")
	if len(es) != 2 || es[0].Name != "src" || es[0].Type != "dir" || es[1].Name != "README.md" {
		t.Fatalf("list order: %+v", es)
	}
	// No temp files are left behind.
	es, _ = f.List("src/app")
	if len(es) != 1 {
		t.Fatalf("temp file leaked: %+v", es)
	}
}

func TestEscapesAreRejected(t *testing.T) {
	f, dir := setup(t)
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top secret"), 0o644)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "file-link")); err != nil {
		t.Fatal(err)
	}
	// Symlinks to outside are rejected by os.Root.
	for _, p := range []string{"link/secret.txt", "file-link"} {
		if r, err := f.Read(p); err == nil {
			t.Fatalf("read %q escaped the root: %+v", p, r)
		} else if code(err) != "outside_workspace" {
			t.Fatalf("read %q: %v", p, code(err))
		}
	}
	if _, err := f.Write("link/new.txt", "x", time.Time{}, 0o644); err == nil {
		t.Fatal("write through escaping symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Fatal("file created outside the root")
	}
	// ".." cannot climb out: it collapses to the root.
	if es, err := f.List("../../.."); err != nil {
		t.Fatal(err)
	} else {
		for _, e := range es {
			if e.Name == "secret.txt" {
				t.Fatal("listed outside content")
			}
		}
	}
	if _, err := f.Read("../" + filepath.Base(outside) + "/secret.txt"); err == nil {
		t.Fatal("traversal read succeeded")
	}
	// Inside-pointing symlinks work.
	_ = os.WriteFile(filepath.Join(dir, "real.txt"), []byte("ok"), 0o644)
	_ = os.Symlink("real.txt", filepath.Join(dir, "inner"))
	if r, err := f.Read("inner"); err != nil || r.Content != "ok" {
		t.Fatalf("inner symlink: %v", err)
	}
	// The link itself can be deleted without touching the target.
	if err := f.Delete("link", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatal("delete followed the symlink")
	}
}

func TestConflictAndBinaryAndLimits(t *testing.T) {
	f, dir := setup(t)
	w, _ := f.Write("a.txt", "one", time.Time{}, 0o644)
	if _, err := f.Write("a.txt", "two", w.ModTime, 0o644); err != nil {
		t.Fatalf("matching mtime must write: %v", err)
	}
	if _, err := f.Write("a.txt", "three", w.ModTime.Add(-time.Hour), 0o644); code(err) != "conflict" {
		t.Fatalf("stale write: %v", code(err))
	}
	if r, _ := f.Read("a.txt"); r.Content != "two" {
		t.Fatal("conflicting write modified the file")
	}
	_ = os.WriteFile(filepath.Join(dir, "bin"), []byte{0xff, 0xfe, 0, 1}, 0o644)
	if r, _ := f.Read("bin"); !r.Binary || r.Content != "" {
		t.Fatalf("binary: %+v", r)
	}
	if _, err := f.Write("big", strings.Repeat("x", MaxWriteBytes+1), time.Time{}, 0o644); code(err) != "too_large" {
		t.Fatal("oversize write allowed")
	}
	if _, err := f.Read("missing"); code(err) != "not_found" {
		t.Fatalf("missing: %v", code(err))
	}
	if _, err := f.Read(""); code(err) != "is_directory" {
		t.Fatalf("dir read: %v", code(err))
	}
}

func TestRenameDeleteMkdir(t *testing.T) {
	f, _ := setup(t)
	_ = f.Mkdir("d/e")
	_, _ = f.Write("d/e/x.txt", "x", time.Time{}, 0o644)
	if err := f.Rename("d", "d/e/inside"); code(err) != "bad_path" {
		t.Fatalf("move into self: %v", code(err))
	}
	if err := f.Rename("d/e/x.txt", "d/y.txt"); err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write("z.txt", "z", time.Time{}, 0o644)
	if err := f.Rename("z.txt", "d/y.txt"); code(err) != "exists" {
		t.Fatalf("rename over existing: %v", code(err))
	}
	if err := f.Delete("d", false); err == nil {
		t.Fatal("non-recursive delete of non-empty dir must fail")
	}
	if err := f.Delete("d", true); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete("", true); code(err) != "bad_path" {
		t.Fatalf("root delete: %v", code(err))
	}
	if err := f.Mkdir("z.txt"); code(err) != "exists" {
		t.Fatalf("mkdir over file: %v", code(err))
	}
}

func TestSearch(t *testing.T) {
	f, _ := setup(t)
	_ = f.Mkdir("node_modules/pkg")
	_ = f.Mkdir("src")
	_, _ = f.Write("src/server.go", "package main\nfunc handleOrders() {}\n", time.Time{}, 0o644)
	_, _ = f.Write("node_modules/pkg/orders.js", "orders", time.Time{}, 0o644)
	hits, trunc, err := f.Search("orders", true, 0)
	if err != nil || trunc {
		t.Fatal(err, trunc)
	}
	if len(hits) != 1 || hits[0].Path != "src/server.go" || hits[0].Line != 2 {
		t.Fatalf("hits: %+v", hits)
	}
	if hits, _, _ = f.Search("server", false, 0); len(hits) != 1 || hits[0].Line != 0 {
		t.Fatalf("name hits: %+v", hits)
	}
	if _, _, err := f.Search("  ", true, 0); code(err) != "bad_query" {
		t.Fatal("empty query accepted")
	}
}
