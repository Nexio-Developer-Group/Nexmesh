// Package tunnel (agent side) opens TCP connections to ports inside the
// workspace on behalf of the control plane, so previews work without any
// inbound port and regardless of whether a server binds loopback or all
// interfaces. Only localhost can be dialled.
package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

const (
	chunk       = 32 << 10
	maxStreams  = 64
	queueChunks = 256
)

type Emitter func(method string, params any)

type host interface {
	Handle(method string, h agentproto.Handler)
	OnEvent(method string, fn func(params json.RawMessage))
}

type stream struct {
	conn net.Conn
	out  chan []byte
	once sync.Once
}

func (s *stream) close() { s.once.Do(func() { _ = s.conn.Close(); close(s.out) }) }

type Manager struct {
	emit Emitter
	mu   sync.Mutex
	m    map[uint64]*stream
}

func NewManager(emit Emitter) *Manager { return &Manager{emit: emit, m: map[uint64]*stream{}} }

func dial(port int) (net.Conn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		c, err = d.Dial("tcp", net.JoinHostPort("::1", strconv.Itoa(port)))
	}
	return c, err
}

func (m *Manager) Install(a host) {
	a.Handle("tun.open", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SID  uint64 `json:"sid"`
			Port int    `json:"port"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Port < 1 || p.Port > 65535 || p.SID == 0 {
			return nil, agentproto.Errf("bad_params", "Invalid tunnel request.")
		}
		m.mu.Lock()
		if len(m.m) >= maxStreams {
			m.mu.Unlock()
			return nil, agentproto.Errf("too_many", "Too many open connections to this workspace.")
		}
		m.mu.Unlock()
		c, err := dial(p.Port)
		if err != nil {
			return nil, agentproto.Errf("port_closed", "Nothing is listening on port %d inside the workspace.", p.Port)
		}
		s := &stream{conn: c, out: make(chan []byte, queueChunks)}
		m.mu.Lock()
		m.m[p.SID] = s
		m.mu.Unlock()
		go m.writer(p.SID, s)
		go m.reader(p.SID, s)
		return struct{}{}, nil
	})
	a.OnEvent("tun.data", func(raw json.RawMessage) {
		var p struct {
			SID uint64 `json:"sid"`
			D   string `json:"d"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return
		}
		m.mu.Lock()
		s := m.m[p.SID]
		m.mu.Unlock()
		if s == nil {
			return
		}
		b, err := base64.StdEncoding.DecodeString(p.D)
		if err != nil {
			return
		}
		select {
		case s.out <- b:
		default: // peer is not reading: drop the stream rather than block the link
			m.drop(p.SID, true)
		}
	})
	a.OnEvent("tun.close", func(raw json.RawMessage) {
		var p struct {
			SID uint64 `json:"sid"`
		}
		if json.Unmarshal(raw, &p) == nil {
			m.drop(p.SID, false)
		}
	})
}

func (m *Manager) drop(sid uint64, notify bool) {
	m.mu.Lock()
	s := m.m[sid]
	delete(m.m, sid)
	m.mu.Unlock()
	if s != nil {
		s.close()
		if notify {
			m.emit("tun.close", map[string]any{"sid": sid})
		}
	}
}

func (m *Manager) writer(sid uint64, s *stream) {
	for b := range s.out {
		if _, err := s.conn.Write(b); err != nil {
			m.drop(sid, true)
			return
		}
	}
}

func (m *Manager) reader(sid uint64, s *stream) {
	buf := make([]byte, chunk)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			m.emit("tun.data", map[string]any{"sid": sid, "d": base64.StdEncoding.EncodeToString(buf[:n])})
		}
		if err != nil {
			m.drop(sid, true)
			return
		}
	}
}

// CloseAll drops every stream (agent shutdown or reconnect).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]uint64, 0, len(m.m))
	for id := range m.m {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.drop(id, false)
	}
}

// Active reports how many tunnelled connections are open.
func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.m)
}
