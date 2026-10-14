// Package agentproto is the message layer shared by the control plane and the
// workspace agent. Both ends speak the same JSON frames over one WebSocket:
//
//	{"t":"req","id":7,"m":"fs.list","p":{...}}   request  (either direction)
//	{"t":"res","id":7,"r":{...}}  or  {"t":"res","id":7,"e":{"code":"...","msg":"..."}}
//	{"t":"evt","m":"pty.output","p":{...}}         one-way event
//
// The agent always dials out (PRD 20.3.4); once connected, the control plane
// also issues requests, so terminals and file operations need no inbound port.
package agentproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// MaxFrame bounds a single message (file transfers are chunked below this).
const MaxFrame = 8 << 20

type Frame struct {
	T  string          `json:"t"`
	ID uint64          `json:"id,omitempty"`
	M  string          `json:"m,omitempty"`
	P  json.RawMessage `json:"p,omitempty"`
	R  json.RawMessage `json:"r,omitempty"`
	E  *Error          `json:"e,omitempty"`
}

// Error is a typed remote failure; Code is stable, Msg is user-presentable.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

func (e *Error) Error() string { return e.Msg }

// Errf builds a remote error to return from a handler.
func Errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

var ErrClosed = errors.New("agent connection closed")

// Handler serves one inbound request.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Peer is one end of the link.
type Peer struct {
	conn *websocket.Conn

	wmu sync.Mutex

	handlers map[string]Handler
	onEvent  func(method string, params json.RawMessage)

	next    atomic.Uint64
	pmu     sync.Mutex
	pending map[uint64]chan Frame

	done chan struct{}
	once sync.Once
}

func New(conn *websocket.Conn) *Peer {
	conn.SetReadLimit(MaxFrame)
	return &Peer{conn: conn, handlers: map[string]Handler{}, pending: map[uint64]chan Frame{}, done: make(chan struct{})}
}

// Handle registers a request handler. Call before Serve.
func (p *Peer) Handle(method string, h Handler) { p.handlers[method] = h }

// OnEvent sets the event sink. Call before Serve.
func (p *Peer) OnEvent(fn func(method string, params json.RawMessage)) { p.onEvent = fn }

func (p *Peer) Done() <-chan struct{} { return p.done }

func (p *Peer) Close() {
	p.once.Do(func() {
		close(p.done)
		_ = p.conn.Close(websocket.StatusNormalClosure, "bye")
	})
}

func (p *Peer) write(ctx context.Context, f Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return p.conn.Write(wctx, websocket.MessageText, b)
}

// Emit sends a one-way event.
func (p *Peer) Emit(ctx context.Context, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.write(ctx, Frame{T: "evt", M: method, P: raw})
}

// Call sends a request and waits for its response. Remote failures come back
// as *Error.
func (p *Peer) Call(ctx context.Context, method string, params any, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id := p.next.Add(1)
	ch := make(chan Frame, 1)
	p.pmu.Lock()
	p.pending[id] = ch
	p.pmu.Unlock()
	defer func() { p.pmu.Lock(); delete(p.pending, id); p.pmu.Unlock() }()
	if err := p.write(ctx, Frame{T: "req", ID: id, M: method, P: raw}); err != nil {
		return err
	}
	select {
	case f := <-ch:
		if f.E != nil {
			return f.E
		}
		if out != nil && len(f.R) > 0 {
			return json.Unmarshal(f.R, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrClosed
	}
}

// Serve reads frames until the connection ends. Requests run concurrently.
func (p *Peer) Serve(ctx context.Context) error {
	defer p.Close()
	for {
		_, b, err := p.conn.Read(ctx)
		if err != nil {
			return err
		}
		var f Frame
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		switch f.T {
		case "res":
			p.pmu.Lock()
			ch := p.pending[f.ID]
			p.pmu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "evt":
			if p.onEvent != nil {
				p.onEvent(f.M, f.P)
			}
		case "req":
			go p.serve(ctx, f)
		}
	}
}

func (p *Peer) serve(ctx context.Context, f Frame) {
	res := Frame{T: "res", ID: f.ID}
	h, ok := p.handlers[f.M]
	if !ok {
		res.E = &Error{Code: "unknown_method", Msg: "Unknown method " + f.M}
	} else {
		out, err := safeCall(ctx, h, f.P)
		var re *Error
		switch {
		case errors.As(err, &re):
			res.E = re
		case err != nil:
			res.E = &Error{Code: "internal", Msg: err.Error()}
		default:
			res.R, _ = json.Marshal(out)
		}
	}
	_ = p.write(ctx, res)
}

func safeCall(ctx context.Context, h Handler, params json.RawMessage) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &Error{Code: "internal", Msg: "handler panicked"}
		}
	}()
	return h(ctx, params)
}

// Ping checks liveness of the underlying socket.
func (p *Peer) Ping(ctx context.Context) error { return p.conn.Ping(ctx) }
