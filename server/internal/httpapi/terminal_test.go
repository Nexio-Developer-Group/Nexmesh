package httpapi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

type tframe struct {
	T    string `json:"t"`
	Data string `json:"data"`
	Code int    `json:"code"`
}

func readUntil(t *testing.T, c *websocket.Conn, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var acc strings.Builder
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q, got %q: %v", want, acc.String(), err)
		}
		var f tframe
		_ = json.Unmarshal(b, &f)
		if f.T == "out" {
			d, _ := base64.StdEncoding.DecodeString(f.Data)
			acc.Write(d)
			if strings.Contains(acc.String(), want) {
				return acc.String()
			}
		}
	}
}

func send(t *testing.T, c *websocket.Conn, f tframe, extra map[string]any) {
	t.Helper()
	m := map[string]any{"t": f.T, "data": f.Data}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalBridgeSurvivesReconnect(t *testing.T) {
	ts, st, _ := filesRig(t, domain.WSReady)
	base := ts.URL + "/api/workspaces/ws1"
	code, body := req(t, "POST", base+"/terminals", `{"cols":90,"rows":20}`)
	if code != 201 {
		t.Fatalf("open: %d %s", code, body)
	}
	var info struct{ ID string }
	_ = json.Unmarshal([]byte(body), &info)
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/terminals/" + info.ID + "/ws"

	c1, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c1, tframe{T: "in", Data: "echo first-$((20+22))\n"}, nil)
	readUntil(t, c1, "first-42")
	send(t, c1, tframe{T: "resize"}, map[string]any{"cols": 132, "rows": 43})
	send(t, c1, tframe{T: "in", Data: "stty size\n"}, nil)
	readUntil(t, c1, "43 132")
	// Browser tab closes; the shell keeps running and produces output.
	c1.Close(websocket.StatusNormalClosure, "")
	cx, _, _ := websocket.Dial(context.Background(), wsURL, nil)
	send(t, cx, tframe{T: "in", Data: "echo while-away-$((1+1))\n"}, nil)
	readUntil(t, cx, "while-away-2")
	cx.Close(websocket.StatusNormalClosure, "")

	// Reattach: scrollback replays everything, without duplicating live output.
	c2, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	all := readUntil(t, c2, "while-away-2")
	if !strings.Contains(all, "first-42") {
		t.Fatalf("scrollback missing earlier output: %q", all)
	}
	send(t, c2, tframe{T: "in", Data: "exit 5\n"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for {
		_, b, err := c2.Read(ctx)
		if err != nil {
			t.Fatal("no exit frame:", err)
		}
		var f tframe
		_ = json.Unmarshal(b, &f)
		if f.T == "exit" {
			if f.Code != 5 {
				t.Fatalf("exit code %d", f.Code)
			}
			break
		}
	}
	evs, _ := st.ListAudit(context.Background(), "ws1", 20)
	found := false
	for _, e := range evs {
		found = found || e.Action == "workspace.terminal_opened"
	}
	if !found {
		t.Fatal("terminal open not audited")
	}
}

func TestTerminalBridgeRejectsUnknownSessionAndForeignOrigin(t *testing.T) {
	ts, _, _ := filesRig(t, domain.WSReady)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/workspaces/ws1/terminals/nope/ws"
	c, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, _, err = c.Read(context.Background())
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("want policy-violation close, got %v", err)
	}
	_, resp, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: map[string][]string{"Origin": {"https://evil.example"}}})
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("foreign origin must be refused, got err=%v resp=%v", err, resp)
	}
}
