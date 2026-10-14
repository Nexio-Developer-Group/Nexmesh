// Package wsfs is the control plane's typed view of a workspace's files, over
// the workspace-agent link. Everything the agent can do to files is already
// confined to the workspace root by the agent; this adds only convenience and
// the same error codes. Features that act for a user or an agent (exports,
// change sets, rules, quality gates) go through it so every file access takes
// one audited, confined path.
package wsfs

import (
	"context"
	"errors"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

// Caller is the slice of the agent registry needed here.
type Caller interface {
	Call(ctx context.Context, wsID, method string, params, out any) error
}

type FS struct{ c Caller }

func New(c Caller) *FS { return &FS{c: c} }

type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Type    string    `json:"type"` // file | dir | symlink
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// ErrNotFound is returned when a path does not exist.
var ErrNotFound = errors.New("not found")

// ErrBinary is returned when a file is not text or is too large to edit.
var ErrBinary = errors.New("the file is binary or too large")

func mapErr(err error) error {
	var ae *agentproto.Error
	if errors.As(err, &ae) && ae.Code == "not_found" {
		return ErrNotFound
	}
	return err
}

func (f *FS) List(ctx context.Context, ws, path string) ([]Entry, error) {
	var out struct {
		Entries []Entry `json:"entries"`
	}
	if err := f.c.Call(ctx, ws, "fs.list", map[string]string{"path": path}, &out); err != nil {
		return nil, mapErr(err)
	}
	return out.Entries, nil
}

// Exists reports whether a file or directory is there.
func (f *FS) Exists(ctx context.Context, ws, path string) (bool, error) {
	var out struct{}
	err := f.c.Call(ctx, ws, "fs.stat", map[string]string{"path": path}, &out)
	if err == nil {
		return true, nil
	}
	if errors.Is(mapErr(err), ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Text is a file's content and the modification time it had when read.
type Text struct {
	Content string
	ModTime time.Time
}

// Read returns a text file. Binary or oversized files return ErrBinary.
func (f *FS) Read(ctx context.Context, ws, path string) (*Text, error) {
	var out struct {
		Content string    `json:"content"`
		ModTime time.Time `json:"modTime"`
		Binary  bool      `json:"binary"`
		TooBig  bool      `json:"tooBig"`
	}
	if err := f.c.Call(ctx, ws, "fs.read", map[string]string{"path": path}, &out); err != nil {
		return nil, mapErr(err)
	}
	if out.Binary || out.TooBig {
		return nil, ErrBinary
	}
	return &Text{Content: out.Content, ModTime: out.ModTime}, nil
}

// Write replaces (or creates) a text file. When expected is non-zero the write
// fails with a conflict if the file changed since it was read.
func (f *FS) Write(ctx context.Context, ws, path, content string, expected time.Time) error {
	p := map[string]any{"path": path, "content": content}
	if !expected.IsZero() {
		p["expectedModTime"] = expected
	}
	return mapErr(f.c.Call(ctx, ws, "fs.write", p, nil))
}

func (f *FS) Mkdir(ctx context.Context, ws, path string) error {
	return mapErr(f.c.Call(ctx, ws, "fs.mkdir", map[string]string{"path": path}, nil))
}

func (f *FS) Delete(ctx context.Context, ws, path string, recursive bool) error {
	return mapErr(f.c.Call(ctx, ws, "fs.delete", map[string]any{"path": path, "recursive": recursive}, nil))
}

// Run executes a command in the workspace and returns its output.
type RunResult struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timedOut"`
	Truncated bool   `json:"truncated"`
}

func (f *FS) Run(ctx context.Context, ws, command string, timeoutSec int) (*RunResult, error) {
	var out RunResult
	if err := f.c.Call(ctx, ws, "exec.run", map[string]any{"command": command, "timeoutSec": timeoutSec}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
