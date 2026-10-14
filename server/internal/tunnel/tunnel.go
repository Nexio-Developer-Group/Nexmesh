// Package tunnel (control-plane side) dials TCP ports inside a workspace
// through the agent link and presents them as net.Conn, so an ordinary
// http.Transport / httputil.ReverseProxy (including WebSocket upgrades) can
// reach servers running in the workspace.
package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Link is the agent connection the dialer uses.
type Link interface {
	Call(ctx context.Context, wsID, method string, params, out any) error
	Emit(ctx context.Context, wsID, method string, params any) error
	Subscribe(wsID string, fn func(method string, params json.RawMessage)) (cancel func())
}

const chunk = 32 << 10

type Dialer struct {
	link Link
	next atomic.Uint64

	mu      sync.Mutex
	routers map[string]*router
}

func NewDialer(l Link) *Dialer { return &Dialer{link: l, routers: map[string]*router{}} }

type router struct {
	mu    sync.Mutex
	conns map[uint64]*Conn
}

func (d *Dialer) route(ws string) *router {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.routers[ws]
	if r != nil {
		return r
	}
	r = &router{conns: map[uint64]*Conn{}}
	d.routers[ws] = r
	d.link.Subscribe(ws, func(method string, params json.RawMessage) {
		switch method {
		case "tun.data":
			var p struct {
				SID uint64 `json:"sid"`
				D   string `json:"d"`
			}
			if json.Unmarshal(params, &p) != nil {
				return
			}
			if c := r.get(p.SID); c != nil {
				if b, err := base64.StdEncoding.DecodeString(p.D); err == nil {
					c.deliver(b)
				}
			}
		case "tun.close":
			var p struct {
				SID uint64 `json:"sid"`
			}
			if json.Unmarshal(params, &p) == nil {
				if c := r.get(p.SID); c != nil {
					c.remoteClosed()
				}
			}
		case "link.closed":
			r.mu.Lock()
			all := make([]*Conn, 0, len(r.conns))
			for _, c := range r.conns {
				all = append(all, c)
			}
			r.mu.Unlock()
			for _, c := range all {
				c.remoteClosed()
			}
		}
	})
	return r
}

func (r *router) get(sid uint64) *Conn { r.mu.Lock(); defer r.mu.Unlock(); return r.conns[sid] }

// DialContext connects to 127.0.0.1:port inside the workspace.
func (d *Dialer) DialContext(ctx context.Context, ws string, port int) (net.Conn, error) {
	r := d.route(ws)
	sid := d.next.Add(1)
	c := &Conn{d: d, ws: ws, sid: sid, r: r, in: make(chan []byte, 256), done: make(chan struct{})}
	r.mu.Lock()
	r.conns[sid] = c
	r.mu.Unlock()
	if err := d.link.Call(ctx, ws, "tun.open", map[string]any{"sid": sid, "port": port}, nil); err != nil {
		r.mu.Lock()
		delete(r.conns, sid)
		r.mu.Unlock()
		return nil, err
	}
	return c, nil
}

type addr string

func (addr) Network() string  { return "nexmesh-tunnel" }
func (a addr) String() string { return string(a) }

// Conn is one tunnelled TCP connection.
type Conn struct {
	d   *Dialer
	ws  string
	sid uint64
	r   *router

	in   chan []byte
	rest []byte

	done      chan struct{}
	closeOnce sync.Once
	rdl       atomic.Value // time.Time
}

func (c *Conn) deliver(b []byte) {
	select {
	case c.in <- b:
	case <-c.done:
	default:
		c.Close() // reader far behind: shed the connection rather than stall the link
	}
}

func (c *Conn) remoteClosed() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.r.mu.Lock()
		delete(c.r.conns, c.sid)
		c.r.mu.Unlock()
	})
}

func (c *Conn) Read(p []byte) (int, error) {
	if len(c.rest) > 0 {
		n := copy(p, c.rest)
		c.rest = c.rest[n:]
		return n, nil
	}
	var timeout <-chan time.Time
	if dl, _ := c.rdl.Load().(time.Time); !dl.IsZero() {
		t := time.NewTimer(time.Until(dl))
		defer t.Stop()
		timeout = t.C
	}
	select {
	case b := <-c.in:
		n := copy(p, b)
		c.rest = b[n:]
		return n, nil
	case <-c.done:
		// Drain anything that arrived before the close.
		select {
		case b := <-c.in:
			n := copy(p, b)
			c.rest = b[n:]
			return n, nil
		default:
		}
		return 0, io.EOF
	case <-timeout:
		return 0, errors.New("tunnel read timeout")
	}
}

func (c *Conn) Write(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, io.ErrClosedPipe
	default:
	}
	total := 0
	for len(p) > 0 {
		n := min(len(p), chunk)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := c.d.link.Emit(ctx, c.ws, "tun.data", map[string]any{"sid": c.sid, "d": base64.StdEncoding.EncodeToString(p[:n])})
		cancel()
		if err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (c *Conn) Close() error {
	first := false
	c.closeOnce.Do(func() {
		first = true
		close(c.done)
		c.r.mu.Lock()
		delete(c.r.conns, c.sid)
		c.r.mu.Unlock()
	})
	if first {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.d.link.Emit(ctx, c.ws, "tun.close", map[string]any{"sid": c.sid})
	}
	return nil
}

func (c *Conn) LocalAddr() net.Addr  { return addr("control-plane") }
func (c *Conn) RemoteAddr() net.Addr { return addr("workspace") }
func (c *Conn) SetDeadline(t time.Time) error {
	c.rdl.Store(t)
	return nil
}
func (c *Conn) SetReadDeadline(t time.Time) error { c.rdl.Store(t); return nil }
func (c *Conn) SetWriteDeadline(time.Time) error  { return nil }
