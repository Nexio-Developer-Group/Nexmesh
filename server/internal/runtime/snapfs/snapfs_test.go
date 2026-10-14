package snapfs

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRoundTripReplacesContents(t *testing.T) {
	src := t.TempDir()
	write(t, src, "a.txt", "alpha")
	write(t, src, "src/deep/b.go", "package b")
	_ = os.Chmod(filepath.Join(src, "a.txt"), 0o755)
	_ = os.Symlink("a.txt", filepath.Join(src, "link"))
	_ = os.Mkdir(filepath.Join(src, "empty"), 0o755)

	arc := filepath.Join(t.TempDir(), "s", "one.tgz")
	n, err := Create(src, arc)
	if err != nil || n <= 0 {
		t.Fatalf("create: %v %d", err, n)
	}
	// The workspace changes afterwards: a file edited, one added, one removed.
	write(t, src, "a.txt", "CHANGED")
	write(t, src, "new.txt", "new")
	_ = os.RemoveAll(filepath.Join(src, "src"))

	if err := Restore(arc, src); err != nil {
		t.Fatal(err)
	}
	if read(t, src, "a.txt") != "alpha" || read(t, src, "src/deep/b.go") != "package b" {
		t.Fatal("contents not restored")
	}
	if _, err := os.Stat(filepath.Join(src, "new.txt")); err == nil {
		t.Fatal("files created after the snapshot must be gone")
	}
	if fi, _ := os.Stat(filepath.Join(src, "a.txt")); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode not restored: %v", fi.Mode())
	}
	if l, err := os.Readlink(filepath.Join(src, "link")); err != nil || l != "a.txt" {
		t.Fatalf("symlink: %v %v", l, err)
	}
	if fi, err := os.Stat(filepath.Join(src, "empty")); err != nil || !fi.IsDir() {
		t.Fatal("empty dir lost")
	}
	// No temp artifacts left behind next to the snapshot or the workspace.
	if ents, _ := os.ReadDir(filepath.Dir(arc)); len(ents) != 1 {
		t.Fatalf("leftovers beside archive: %v", ents)
	}
	for _, e := range mustRead(t, filepath.Dir(src)) {
		if strings.HasPrefix(e, ".restore-") {
			t.Fatalf("leftover restore staging: %s", e)
		}
	}
}

func malicious(t *testing.T, mutate func(tw *tar.Writer)) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bad.tgz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	mutate(tw)
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func TestRestoreRejectsUnsafeArchives(t *testing.T) {
	for name, mut := range map[string]func(*tar.Writer){
		"dotdot": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "../evil.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("x"))
		},
		"absolute": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "/etc/evil", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("x"))
		},
	} {
		parent := t.TempDir()
		dir := filepath.Join(parent, "ws")
		_ = os.MkdirAll(dir, 0o755)
		write(t, dir, "keep.txt", "precious")
		if err := Restore(malicious(t, mut), dir); err == nil || !strings.Contains(err.Error(), "unsafe path") {
			t.Fatalf("%s accepted: %v", name, err)
		}
		if read(t, dir, "keep.txt") != "precious" {
			t.Fatalf("%s: a rejected restore must leave the workspace untouched", name)
		}
		if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
			t.Fatalf("%s: file escaped the workspace", name)
		}
	}
	// Escaping symlinks are dropped rather than recreated.
	dir := filepath.Join(t.TempDir(), "ws")
	_ = os.MkdirAll(dir, 0o755)
	arc := malicious(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(&tar.Header{Name: "esc", Linkname: "../../etc/passwd", Typeflag: tar.TypeSymlink, Mode: 0o777})
		_ = tw.WriteHeader(&tar.Header{Name: "abs", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink, Mode: 0o777})
		_ = tw.WriteHeader(&tar.Header{Name: "ok.txt", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("ok"))
	})
	if err := Restore(arc, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "esc")); err == nil {
		t.Fatal("escaping symlink restored")
	}
	if _, err := os.Lstat(filepath.Join(dir, "abs")); err == nil {
		t.Fatal("absolute symlink restored")
	}
	if read(t, dir, "ok.txt") != "ok" {
		t.Fatal("good entries skipped")
	}
	// A corrupt archive fails cleanly.
	bad := filepath.Join(t.TempDir(), "junk.tgz")
	_ = os.WriteFile(bad, []byte("not gzip"), 0o644)
	if err := Restore(bad, dir); err == nil {
		t.Fatal("junk accepted")
	}
}

func mustRead(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
