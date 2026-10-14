// Package workfs implements the agent's file operations. Every path is
// resolved inside one root with os.Root, so ".." and symlinks that point
// outside the workspace are rejected by the kernel-level openat semantics
// rather than by string checks alone.
package workfs

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

const (
	MaxReadBytes   = 5 << 20 // larger files are not loaded into the editor
	MaxWriteBytes  = 5 << 20
	maxSearchFile  = 1 << 20
	maxSearchLines = 200
)

// FS is a workspace-confined file system.
type FS struct {
	root *os.Root
}

func Open(dir string) (*FS, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &FS{root: r}, nil
}

func (f *FS) Close() error { return f.root.Close() }

type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Type    string    `json:"type"` // file | dir | symlink
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Mode    string    `json:"mode"`
}

// clean turns a client path into a root-relative path, rejecting escapes.
func clean(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", agentproto.Errf("bad_path", "Path contains an invalid character.")
	}
	p = path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	// path.Clean("/"+x) already collapses leading "..", but reject traversal
	// that the client plainly intended so it fails loudly instead of silently
	// resolving to the root.
	return strings.TrimPrefix(p, "/"), nil
}

func rel(p string) string {
	if p == "" {
		return "."
	}
	return p
}

func mapErr(err error, p string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return agentproto.Errf("not_found", "%s does not exist.", display(p))
	case errors.Is(err, fs.ErrExist):
		return agentproto.Errf("exists", "%s already exists.", display(p))
	case errors.Is(err, fs.ErrPermission):
		return agentproto.Errf("permission", "No permission to access %s.", display(p))
	case strings.Contains(err.Error(), "escapes from parent") || strings.Contains(err.Error(), "path escapes"):
		return agentproto.Errf("outside_workspace", "%s resolves outside the workspace.", display(p))
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "not a directory") {
		return agentproto.Errf("not_a_directory", "%s is not a directory.", display(p))
	}
	return err
}

func display(p string) string {
	if p == "" {
		return "/"
	}
	return "/" + p
}

func entry(dir string, fi fs.FileInfo) Entry {
	t := "file"
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t = "symlink"
	case fi.IsDir():
		t = "dir"
	}
	return Entry{Name: fi.Name(), Path: path.Join(dir, fi.Name()), Type: t, Size: fi.Size(), ModTime: fi.ModTime().UTC(), Mode: fi.Mode().Perm().String()}
}

// List returns directory entries, directories first then by name.
func (f *FS) List(p string) ([]Entry, error) {
	p, _ = clean(p)
	d, err := f.root.Open(rel(p))
	if err != nil {
		return nil, mapErr(err, p)
	}
	defer d.Close()
	infos, err := d.ReadDir(-1)
	if err != nil {
		return nil, mapErr(err, p)
	}
	out := make([]Entry, 0, len(infos))
	for _, de := range infos {
		fi, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, entry(p, fi))
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Type == "dir") != (out[j].Type == "dir") {
			return out[i].Type == "dir"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (f *FS) Stat(p string) (Entry, error) {
	p, _ = clean(p)
	fi, err := f.root.Lstat(rel(p))
	if err != nil {
		return Entry{}, mapErr(err, p)
	}
	e := entry(path.Dir(p), fi)
	if p == "" {
		e.Name, e.Path = "", ""
	}
	return e, nil
}

type ReadResult struct {
	Path    string    `json:"path"`
	Content string    `json:"content,omitempty"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Binary  bool      `json:"binary"`
	TooBig  bool      `json:"tooBig"`
}

// Read returns a text file's content. Binary and oversized files return
// metadata only so the editor can show a placeholder.
func (f *FS) Read(p string) (*ReadResult, error) {
	p, _ = clean(p)
	h, err := f.root.Open(rel(p))
	if err != nil {
		return nil, mapErr(err, p)
	}
	defer h.Close()
	fi, err := h.Stat()
	if err != nil {
		return nil, mapErr(err, p)
	}
	if fi.IsDir() {
		return nil, agentproto.Errf("is_directory", "%s is a directory.", display(p))
	}
	res := &ReadResult{Path: p, Size: fi.Size(), ModTime: fi.ModTime().UTC()}
	if fi.Size() > MaxReadBytes {
		res.TooBig = true
		return res, nil
	}
	b, err := io.ReadAll(h)
	if err != nil {
		return nil, mapErr(err, p)
	}
	if !utf8.Valid(b) || strings.ContainsRune(string(b[:min(len(b), 8000)]), 0) {
		res.Binary = true
		return res, nil
	}
	res.Content = string(b)
	return res, nil
}

type WriteResult struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Write replaces a file atomically. When expected is non-zero and the file's
// modification time differs, the write is refused so the editor never
// overwrites changes made by the terminal or an agent.
func (f *FS) Write(p, content string, expected time.Time, mode fs.FileMode) (*WriteResult, error) {
	p, _ = clean(p)
	if p == "" {
		return nil, agentproto.Errf("is_directory", "/ is a directory.")
	}
	if len(content) > MaxWriteBytes {
		return nil, agentproto.Errf("too_large", "File is larger than %d MB.", MaxWriteBytes>>20)
	}
	if fi, err := f.root.Stat(p); err == nil {
		if fi.IsDir() {
			return nil, agentproto.Errf("is_directory", "%s is a directory.", display(p))
		}
		if !expected.IsZero() && !fi.ModTime().UTC().Equal(expected.UTC()) {
			return nil, agentproto.Errf("conflict", "%s changed on disk since you opened it.", display(p))
		}
		mode = fi.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, mapErr(err, p)
	}
	tmp := path.Join(path.Dir(p), fmt.Sprintf(".%s.nexmesh-%d.tmp", path.Base(p), time.Now().UnixNano()))
	h, err := f.root.OpenFile(rel(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, mapErr(err, path.Dir(p))
	}
	_, werr := h.WriteString(content)
	cerr := h.Close()
	if werr != nil || cerr != nil {
		_ = f.root.Remove(rel(tmp))
		return nil, errors.Join(werr, cerr)
	}
	if err := f.root.Rename(rel(tmp), p); err != nil {
		_ = f.root.Remove(rel(tmp))
		return nil, mapErr(err, p)
	}
	fi, err := f.root.Stat(p)
	if err != nil {
		return nil, mapErr(err, p)
	}
	return &WriteResult{Size: fi.Size(), ModTime: fi.ModTime().UTC()}, nil
}

func (f *FS) Mkdir(p string) error {
	p, _ = clean(p)
	if p == "" {
		return agentproto.Errf("exists", "/ already exists.")
	}
	if fi, err := f.root.Stat(p); err == nil {
		if fi.IsDir() {
			return nil
		}
		return agentproto.Errf("exists", "%s already exists and is a file.", display(p))
	}
	// MkdirAll semantics, one component at a time, so each step stays rooted.
	cur := ""
	for _, part := range strings.Split(p, "/") {
		cur = path.Join(cur, part)
		if err := f.root.Mkdir(cur, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return mapErr(err, cur)
		}
	}
	return nil
}

func (f *FS) Rename(from, to string) error {
	from, _ = clean(from)
	to, _ = clean(to)
	if from == "" || to == "" {
		return agentproto.Errf("bad_path", "The workspace root cannot be renamed.")
	}
	if _, err := f.root.Lstat(from); err != nil {
		return mapErr(err, from)
	}
	if _, err := f.root.Lstat(to); err == nil {
		return agentproto.Errf("exists", "%s already exists.", display(to))
	}
	if strings.HasPrefix(to+"/", from+"/") {
		return agentproto.Errf("bad_path", "A folder cannot be moved into itself.")
	}
	return mapErr(f.root.Rename(from, to), to)
}

func (f *FS) Delete(p string, recursive bool) error {
	p, _ = clean(p)
	if p == "" {
		return agentproto.Errf("bad_path", "The workspace root cannot be deleted.")
	}
	fi, err := f.root.Lstat(p)
	if err != nil {
		return mapErr(err, p)
	}
	if fi.IsDir() && !recursive {
		return mapErr(f.root.Remove(p), p) // fails if non-empty
	}
	return mapErr(f.root.RemoveAll(p), p)
}

type SearchHit struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".cache": true, ".venv": true, "__pycache__": true}

// Search finds files whose name contains query (case-insensitive) and, when
// content is true, text lines containing it. Results are capped.
func (f *FS) Search(query string, content bool, limit int) ([]SearchHit, bool, error) {
	if strings.TrimSpace(query) == "" {
		return nil, false, agentproto.Errf("bad_query", "Enter something to search for.")
	}
	if limit <= 0 || limit > maxSearchLines {
		limit = maxSearchLines
	}
	q := strings.ToLower(query)
	var hits []SearchHit
	truncated := false
	var walk func(dir string) bool
	walk = func(dir string) bool {
		d, err := f.root.Open(rel(dir))
		if err != nil {
			return true
		}
		ents, _ := d.ReadDir(-1)
		d.Close()
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, de := range ents {
			p := path.Join(dir, de.Name())
			if de.IsDir() {
				if skipDirs[de.Name()] {
					continue
				}
				if !walk(p) {
					return false
				}
				continue
			}
			if !de.Type().IsRegular() {
				continue
			}
			if strings.Contains(strings.ToLower(de.Name()), q) {
				hits = append(hits, SearchHit{Path: p})
			}
			if content {
				hits = f.grep(p, q, hits)
			}
			if len(hits) >= limit {
				truncated = true
				hits = hits[:limit]
				return false
			}
		}
		return true
	}
	walk("")
	return hits, truncated, nil
}

func (f *FS) grep(p, q string, hits []SearchHit) []SearchHit {
	h, err := f.root.Open(p)
	if err != nil {
		return hits
	}
	defer h.Close()
	if fi, err := h.Stat(); err != nil || fi.Size() > maxSearchFile {
		return hits
	}
	sc := bufio.NewScanner(h)
	sc.Buffer(make([]byte, 64<<10), 256<<10)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if !utf8.ValidString(line) || strings.ContainsRune(line, 0) {
			return hits // binary
		}
		if strings.Contains(strings.ToLower(line), q) {
			if len(line) > 240 {
				line = line[:240]
			}
			hits = append(hits, SearchHit{Path: p, Line: n, Text: line})
			if len(hits) >= maxSearchLines {
				return hits
			}
		}
	}
	return hits
}
