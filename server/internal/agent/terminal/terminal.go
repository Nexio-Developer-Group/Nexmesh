// Package terminal runs PTY sessions inside the workspace. Sessions belong to
// the agent, not to a browser tab: closing the tab leaves the shell running
// and a later attach replays the scrollback.
package terminal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/procgroup"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

const (
	ScrollbackBytes = 64 << 10
	MaxSessions     = 8
)

// Emitter delivers events to the control plane.
type Emitter func(method string, params any)

// EnvFunc supplies extra environment (variables, connection strings) for each
// new process; it is called per start so rotated values apply immediately.
type EnvFunc func(ctx context.Context) (map[string]string, error)

type Manager struct {
	root  string
	emit  Emitter
	env   EnvFunc
	shell string

	mu       sync.Mutex
	sessions map[string]*session
	next     int
}

func NewManager(root string, emit Emitter, env EnvFunc) *Manager {
	shell := os.Getenv("SHELL")
	if _, err := exec.LookPath(shell); shell == "" || err != nil {
		shell = "/bin/bash"
		if _, err := os.Stat(shell); err != nil {
			shell = "/bin/sh"
		}
	}
	return &Manager{root: root, emit: emit, env: env, shell: shell, sessions: map[string]*session{}}
}

type session struct {
	id      string
	title   string
	cmd     *exec.Cmd
	pty     *os.File
	created time.Time

	mu     sync.Mutex
	buf    []byte // last ScrollbackBytes
	total  int64  // bytes ever produced
	cols   uint16
	rows   uint16
	exited bool
	code   int
}

type Info struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Cols    uint16    `json:"cols"`
	Rows    uint16    `json:"rows"`
	Exited  bool      `json:"exited"`
	Code    int       `json:"exitCode"`
}

func (s *session) info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Info{ID: s.id, Title: s.title, Created: s.created, Cols: s.cols, Rows: s.rows, Exited: s.exited, Code: s.code}
}

type OpenParams struct {
	Title string `json:"title"`
	Cols  uint16 `json:"cols"`
	Rows  uint16 `json:"rows"`
	Cwd   string `json:"cwd"` // root-relative; the shell never starts outside the root
}

func (m *Manager) Open(ctx context.Context, p OpenParams) (*Info, error) {
	m.mu.Lock()
	live := 0
	for _, s := range m.sessions {
		if !s.info().Exited {
			live++
		}
	}
	if live >= MaxSessions {
		m.mu.Unlock()
		return nil, agentproto.Errf("too_many", "This workspace already has %d open terminals. Close one first.", MaxSessions)
	}
	m.next++
	id := "t" + itoa(m.next)
	m.mu.Unlock()

	if p.Cols == 0 {
		p.Cols = 80
	}
	if p.Rows == 0 {
		p.Rows = 24
	}
	if p.Title == "" {
		p.Title = "Terminal " + itoa(m.next)
	}
	env := append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	if m.env != nil {
		extra, err := m.env(ctx)
		if err != nil {
			return nil, agentproto.Errf("env_failed", "Could not load environment variables: %v", err)
		}
		for k, v := range extra {
			env = append(env, k+"="+v)
		}
	}
	cmd := exec.Command(m.shell)
	cmd.Dir = m.root
	cmd.Env = env
	if runtime.GOOS == "windows" {
		return nil, agentproto.Errf("unsupported", "Terminals are not available when the workspace runs natively on Windows. Use WSL or Docker (docs/INSTALL.md).")
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: p.Cols, Rows: p.Rows})
	if err != nil {
		return nil, agentproto.Errf("start_failed", "Could not start a shell: %v", err)
	}
	s := &session{id: id, title: p.Title, cmd: cmd, pty: f, created: time.Now().UTC(), cols: p.Cols, rows: p.Rows}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	go m.pump(s)
	in := s.info()
	return &in, nil
}

func itoa(n int) string { return string(appendInt(nil, n)) }

func appendInt(b []byte, n int) []byte {
	if n >= 10 {
		b = appendInt(b, n/10)
	}
	return append(b, byte('0'+n%10))
}

type outputEvent struct {
	ID   string `json:"id"`
	Off  int64  `json:"off"` // byte offset of the first byte of this chunk
	Data string `json:"data"`
}

func (m *Manager) pump(s *session) {
	chunk := make([]byte, 16<<10)
	for {
		n, err := s.pty.Read(chunk)
		if n > 0 {
			b := append([]byte(nil), chunk[:n]...)
			s.mu.Lock()
			off := s.total
			s.total += int64(n)
			s.buf = append(s.buf, b...)
			if len(s.buf) > ScrollbackBytes {
				s.buf = append([]byte(nil), s.buf[len(s.buf)-ScrollbackBytes:]...)
			}
			s.mu.Unlock()
			m.emit("pty.output", outputEvent{ID: s.id, Off: off, Data: base64.StdEncoding.EncodeToString(b)})
		}
		if err != nil {
			break
		}
	}
	err := s.cmd.Wait()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	s.mu.Lock()
	s.exited, s.code = true, code
	s.mu.Unlock()
	_ = s.pty.Close()
	m.emit("pty.exit", map[string]any{"id": s.id, "exitCode": code})
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, agentproto.Errf("not_found", "That terminal no longer exists.")
	}
	return s, nil
}

func (m *Manager) Write(id, data string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.info().Exited {
		return agentproto.Errf("exited", "That terminal has exited.")
	}
	_, err = s.pty.WriteString(data)
	return err
}

func (m *Manager) Resize(id string, cols, rows uint16) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if cols == 0 || rows == 0 || s.info().Exited {
		return nil
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
	return pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close ends a session (SIGHUP to the process group, SIGKILL after a grace
// period) and forgets it.
func (m *Manager) Close(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if !s.info().Exited && s.cmd.Process != nil {
		procgroup.Hangup(s.cmd)
		go func() {
			time.Sleep(2 * time.Second)
			if !s.info().Exited {
				_ = s.cmd.Process.Kill()
			}
		}()
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

// CloseAll terminates every session (agent shutdown).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Close(id)
	}
}

func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

type AttachResult struct {
	Info
	Scrollback string `json:"scrollback"` // base64
	End        int64  `json:"end"`        // offset just after the snapshot; drop live chunks at or below it
}

// Attach returns the scrollback so a new browser connection can repaint.
func (m *Manager) Attach(id string) (*AttachResult, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	snap := append([]byte(nil), s.buf...)
	end := s.total
	s.mu.Unlock()
	return &AttachResult{Info: s.info(), Scrollback: base64.StdEncoding.EncodeToString(snap), End: end}, nil
}

type installer interface {
	Handle(method string, h agentproto.Handler)
}

func bind[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) > 0 && json.Unmarshal(raw, &v) != nil {
		return v, agentproto.Errf("bad_params", "Invalid request parameters.")
	}
	return v, nil
}

// Install registers the pty.* methods.
func (m *Manager) Install(a installer) {
	type idP struct {
		ID string `json:"id"`
	}
	a.Handle("pty.open", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bind[OpenParams](raw)
		if err != nil {
			return nil, err
		}
		return m.Open(ctx, p)
	})
	a.Handle("pty.list", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"sessions": m.List()}, nil
	})
	a.Handle("pty.attach", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := bind[idP](raw)
		if err != nil {
			return nil, err
		}
		return m.Attach(p.ID)
	})
	a.Handle("pty.write", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := bind[struct {
			ID   string `json:"id"`
			Data string `json:"data"`
		}](raw)
		if err != nil {
			return nil, err
		}
		return struct{}{}, m.Write(p.ID, p.Data)
	})
	a.Handle("pty.resize", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := bind[struct {
			ID   string `json:"id"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}](raw)
		if err != nil {
			return nil, err
		}
		return struct{}{}, m.Resize(p.ID, p.Cols, p.Rows)
	})
	a.Handle("pty.close", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := bind[idP](raw)
		if err != nil {
			return nil, err
		}
		return struct{}{}, m.Close(p.ID)
	})
}

// Busy names a terminal whose shell has a running child process (a build, a
// dev server, an editor), or "" when every shell is sitting at a prompt.
func (m *Manager) Busy() string {
	m.mu.Lock()
	list := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()
	for _, s := range list {
		if s.info().Exited || s.cmd.Process == nil {
			continue
		}
		if hasChild("/proc", s.cmd.Process.Pid) {
			return "a process is running in " + s.title
		}
	}
	return ""
}

// hasChild reports whether any process has ppid as its parent, by scanning proc.
func hasChild(proc string, ppid int) bool {
	ents, err := os.ReadDir(proc)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() || e.Name()[0] < '0' || e.Name()[0] > '9' {
			continue
		}
		b, err := os.ReadFile(proc + "/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// /proc/<pid>/stat: pid (comm) state ppid ...; comm may contain spaces/parens.
		str := string(b)
		i := strings.LastIndex(str, ")")
		if i < 0 || i+4 > len(str) {
			continue
		}
		f := strings.Fields(str[i+1:])
		if len(f) >= 2 && f[1] == strconv.Itoa(ppid) {
			return true
		}
	}
	return false
}
