// Package catalogdata carries a copy of the catalog inside the binary so a
// downloaded executable needs no files beside it. The build copies ../catalog
// into data/ (see the Makefile and scripts/build-release.sh); a source
// checkout leaves it empty and reads the catalog directory instead.
package catalogdata

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:data
var files embed.FS

// Available reports whether a catalog was embedded at build time.
func Available() bool {
	ents, err := fs.ReadDir(files, "data")
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.Name() != ".gitkeep" {
			return true
		}
	}
	return false
}

// Extract writes the embedded catalog to dir, replacing what is there so the
// catalog always matches the binary.
func Extract(dir string) error {
	_ = os.RemoveAll(dir)
	return fs.WalkDir(files, "data", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("data", p)
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if d.Name() == ".gitkeep" {
			return nil
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}
