package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

// Browser <-> control plane terminal frames (JSON text):
//
//	client: {"t":"in","data":"ls\r"}   {"t":"resize","cols":120,"rows":40}
//	server: {"t":"out","data":"<base64>"}   {"t":"exit","code":0}   {"t":"offline"}
type termFrame struct {
	T    string `json:"t"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Code int    `json:"code,omitempty"`
}

type ptyOutput struct {
	ID   string `json:"id"`
	Off  int64  `json:"off"`
	Data string `json:"data"`
}

func (s *Server) RegisterTerminal(d FilesDeps) {
	s.Handle("GET /api/workspaces/{id}/terminals", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := s.workspaceAgent(d, w, r)
		if !ok {
			return
		}
		var out map[string]any
		if err := d.Agent.Call(r.Context(), ws.ID, "pty.list", struct{}{}, &out); err != nil {
			s.agentFail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, out)
	})
	s.Handle("POST /api/workspaces/{id}/terminals", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := s.workspaceAgent(d, w, r)
		if !ok {
			return
		}
		var in struct {
			Title string `json:"title"`
			Cols  uint16 `json:"cols"`
			Rows  uint16 `json:"rows"`
		}
		if r.ContentLength != 0 && !DecodeJSON(w, r, &in) {
			return
		}
		var out map[string]any
		if err := d.Agent.Call(r.Context(), ws.ID, "pty.open", in, &out); err != nil {
			s.agentFail(w, err)
			return
		}
		if d.Audit != nil {
			d.Audit.User(r.Context(), Owner(r), ws.ID, "workspace.terminal_opened", "", map[string]any{"session": out["id"]})
		}
		WriteJSON(w, http.StatusCreated, out)
	})
	s.Handle("DELETE /api/workspaces/{id}/terminals/{tid}", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := s.workspaceAgent(d, w, r)
		if !ok {
			return
		}
		if err := d.Agent.Call(r.Context(), ws.ID, "pty.close", map[string]string{"id": r.PathValue("tid")}, nil); err != nil {
			s.agentFail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.Handle("GET /api/workspaces/{id}/terminals/{tid}/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, ok := s.workspaceAgent(d, w, r)
		if !ok {
			return
		}
		opts := &websocket.AcceptOptions{}
		if u, err := url.Parse(s.publicURL); err == nil && u.Host != "" {
			opts.OriginPatterns = []string{u.Host}
		}
		c, err := websocket.Accept(w, r, opts)
		if err != nil {
			return
		}
		s.bridge(d, c, ws.ID, r.PathValue("tid"))
	})
}

func (s *Server) bridge(d FilesDeps, c *websocket.Conn, wsID, tid string) {
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	send := func(f termFrame) error {
		b, _ := json.Marshal(f)
		wctx, wc := context.WithTimeout(ctx, 10*time.Second)
		defer wc()
		return c.Write(wctx, websocket.MessageText, b)
	}

	// Subscribe before attaching so no output falls between snapshot and live.
	events := make(chan func(), 256)
	push := func(fn func()) {
		select {
		case events <- fn:
		default:
			cancel() // client too slow: drop it, it can reattach and replay
		}
	}
	var snapEnd int64 = -1
	cancelSub := d.Agent.Subscribe(wsID, func(method string, params json.RawMessage) {
		switch method {
		case "pty.output":
			var o ptyOutput
			if json.Unmarshal(params, &o) != nil || o.ID != tid {
				return
			}
			push(func() {
				b, err := base64.StdEncoding.DecodeString(o.Data)
				if err != nil {
					return
				}
				end := o.Off + int64(len(b))
				if end <= snapEnd {
					return // already in the snapshot
				}
				if o.Off < snapEnd {
					b = b[snapEnd-o.Off:]
				}
				_ = send(termFrame{T: "out", Data: base64.StdEncoding.EncodeToString(b)})
			})
		case "pty.exit":
			var e struct {
				ID   string `json:"id"`
				Code int    `json:"exitCode"`
			}
			if json.Unmarshal(params, &e) == nil && e.ID == tid {
				push(func() { _ = send(termFrame{T: "exit", Code: e.Code}) })
			}
		case "link.closed":
			push(func() { _ = send(termFrame{T: "offline"}); cancel() })
		}
	})
	defer cancelSub()

	var att struct {
		Scrollback string `json:"scrollback"`
		End        int64  `json:"end"`
		Exited     bool   `json:"exited"`
		Code       int    `json:"exitCode"`
	}
	actx, ac := context.WithTimeout(ctx, 10*time.Second)
	err := d.Agent.Call(actx, wsID, "pty.attach", map[string]string{"id": tid}, &att)
	ac()
	if err != nil {
		code := "agent_error"
		msg := "Could not attach to the terminal."
		if ae, ok := err.(*agentproto.Error); ok {
			code, msg = ae.Code, ae.Msg
		}
		_ = c.Close(websocket.StatusPolicyViolation, code+": "+msg)
		return
	}
	// Events queued before snapEnd was set are filtered when they run, below.
	snapEnd = att.End
	if att.Scrollback != "" {
		_ = send(termFrame{T: "out", Data: att.Scrollback})
	}
	if att.Exited {
		_ = send(termFrame{T: "exit", Code: att.Code})
	}

	go func() { // browser -> agent
		defer cancel()
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var f termFrame
			if json.Unmarshal(b, &f) != nil {
				continue
			}
			cctx, cc := context.WithTimeout(ctx, 10*time.Second)
			switch f.T {
			case "in":
				_ = d.Agent.Call(cctx, wsID, "pty.write", map[string]string{"id": tid, "data": f.Data}, nil)
			case "resize":
				_ = d.Agent.Call(cctx, wsID, "pty.resize", map[string]any{"id": tid, "cols": f.Cols, "rows": f.Rows}, nil)
			}
			cc()
		}
	}()

	for {
		select {
		case fn := <-events:
			fn()
		case <-ctx.Done():
			return
		}
	}
}
