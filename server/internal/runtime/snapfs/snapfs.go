// Package snapfs creates and restores tar.gz snapshots of a directory tree.
// Restores treat the archive as untrusted: absolute paths, ".." segments,
// device nodes and symlinks that leave the target directory are rejected.
package snapfs

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Create writes dir's contents to a gzip-compressed tar at archive (via a temp
// file, so a failed snapshot never leaves a partial archive) and returns its size.
func Create(dir, archive string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(archive), ".snap-*.tmp")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // files that vanish mid-walk are skipped, like tar does
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			link, _ = os.Readlink(p)
		} else if !info.Mode().IsRegular() && !info.IsDir() {
			return nil // sockets, fifos, devices
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return nil
		}
		h.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			h.Name += "/"
		}
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := root.Open(h.Name)
			if err != nil {
				return nil
			}
			defer f.Close()
			if _, err := io.CopyN(tw, f, h.Size); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
		}
		return nil
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), archive); err != nil {
		return 0, err
	}
	fi, err := os.Stat(archive)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// Restore replaces dir's contents with the archive's.
func Restore(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("snapshot is not a valid archive: %w", err)
	}
	// Extract into a sibling directory first so a corrupt archive cannot destroy the workspace.
	stage, err := os.MkdirTemp(filepath.Dir(filepath.Clean(dir)), ".restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extract(tar.NewReader(gz), stage); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	ents, _ = os.ReadDir(stage)
	for _, e := range ents {
		if err := os.Rename(filepath.Join(stage, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func extract(tr *tar.Reader, dst string) error {
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("snapshot is corrupt: %w", err)
		}
		name := path.Clean(strings.TrimSuffix(h.Name, "/"))
		if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("snapshot contains an unsafe path %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := mkdirAll(root, name); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := mkdirAll(root, path.Dir(name)); err != nil {
				return err
			}
			out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fs.FileMode(h.Mode)&0o777|0o200)
			if err != nil {
				return err
			}
			_, cerr := io.Copy(out, tr)
			if err := errors.Join(cerr, out.Close()); err != nil {
				return err
			}
			_ = root.Chmod(name, fs.FileMode(h.Mode)&0o777)
		case tar.TypeSymlink:
			// Only links that stay inside the tree are restored.
			target := h.Linkname
			resolved := target
			if !path.IsAbs(target) {
				resolved = path.Join(path.Dir(name), target)
			}
			if path.IsAbs(target) || resolved == ".." || strings.HasPrefix(resolved, "../") {
				continue
			}
			if err := mkdirAll(root, path.Dir(name)); err != nil {
				return err
			}
			if err := root.Symlink(target, name); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		default:
			// hard links, devices, fifos: not restored
		}
	}
}

func mkdirAll(root *os.Root, p string) error {
	if p == "." || p == "" {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(p, "/") {
		cur = path.Join(cur, part)
		if err := root.Mkdir(cur, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}
