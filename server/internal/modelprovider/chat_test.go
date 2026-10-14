package modelprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
)

func chatClient(t *testing.T, proto string, h http.HandlerFunc, key string) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, e := NewClient(Config{Protocol: proto, BaseURL: srv.URL, APIKey: key, ModelID: "m1"}, loop, nil)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

var lookupTool = ToolDef{Name: "read_file", Description: "Read a file", Parameters: map[string]any{
	"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}

func convo() ChatRequest {
	return ChatRequest{
		System: "You are an agent.",
		Tools:  []ToolDef{lookupTool},
		Messages: []Message{
			{Role: RoleUser, Content: "show main.go"},
			{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Arguments: map[string]any{"path": "main.go"}}, {ID: "t2", Name: "read_file", Arguments: map[string]any{"path": "go.mod"}}}},
			{Role: RoleTool, ToolCallID: "t1", ToolName: "read_file", Content: "package main"},
			{Role: RoleTool, ToolCallID: "t2", ToolName: "read_file", Content: "module x"},
		},
	}
}

func readBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var b map[string]any
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestChatAnthropicToolRoundTrip(t *testing.T) {
	var got map[string]any
	c := chatClient(t, ProtoAnthropic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != goodKey {
			jsonOut(w, 401, errBody(401, "bad"))
			return
		}
		got = readBody(t, r)
		jsonOut(w, 200, map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "reading"}, {"type": "tool_use", "id": "tu_9", "name": "read_file", "input": map[string]any{"path": "a.go"}}},
			"stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 11, "output_tokens": 7},
		})
	}, goodKey)
	res, e := c.Chat(context.Background(), convo())
	if e != nil {
		t.Fatal(e)
	}
	if res.Text != "reading" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "tu_9" || res.ToolCalls[0].Arguments["path"] != "a.go" || res.InputTokens != 11 || res.OutputTokens != 7 {
		t.Fatalf("bad parse: %+v", res)
	}
	if got["system"] != "You are an agent." || got["max_tokens"].(float64) != 4096 {
		t.Fatalf("request: %v", got)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 { // user, assistant, ONE user turn carrying both results
		t.Fatalf("expected results merged into one user turn, got %d messages", len(msgs))
	}
	results := msgs[2].(map[string]any)["content"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["type"] != "tool_result" || results[1].(map[string]any)["tool_use_id"] != "t2" {
		t.Fatalf("tool results: %v", results)
	}
	if got["tools"].([]any)[0].(map[string]any)["input_schema"] == nil {
		t.Fatal("tool schema missing")
	}
}

func TestChatOpenAIToolCallsAndMaxCompletionRetry(t *testing.T) {
	calls := 0
	var last map[string]any
	c := chatClient(t, ProtoOpenAI, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer "+goodKey {
			jsonOut(w, 401, errBody(401, "bad"))
			return
		}
		calls++
		b := readBody(t, r)
		last = b
		if _, ok := b["max_tokens"]; ok {
			jsonOut(w, 400, errBody(400, "Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead."))
			return
		}
		jsonOut(w, 200, map[string]any{
			"choices": []map[string]any{{"finish_reason": "tool_calls", "message": map[string]any{"content": "",
				"tool_calls": []map[string]any{{"id": "c1", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"x.go"}`}}}}}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}, goodKey)
	res, e := c.Chat(context.Background(), convo())
	if e != nil {
		t.Fatal(e)
	}
	if calls != 2 || last["max_completion_tokens"] == nil {
		t.Fatalf("expected a retry with max_completion_tokens (calls=%d)", calls)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments["path"] != "x.go" || res.StopReason != "tool_calls" {
		t.Fatalf("parse: %+v", res)
	}
	msgs := last["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatal("system prompt must lead")
	}
	var toolMsgs int
	for _, m := range msgs {
		if m.(map[string]any)["role"] == "tool" {
			toolMsgs++
		}
	}
	if toolMsgs != 2 {
		t.Fatalf("tool messages: %d", toolMsgs)
	}
}

func TestChatOpenAIMalformedArgumentsDoNotCrash(t *testing.T) {
	c := chatClient(t, ProtoOpenAI, func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]any{"choices": []map[string]any{{"message": map[string]any{
			"tool_calls": []map[string]any{{"function": map[string]any{"name": "read_file", "arguments": `{"path":`}}}}}}})
	}, goodKey)
	res, e := c.Chat(context.Background(), convo())
	if e != nil || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID == "" || res.ToolCalls[0].Arguments["_raw"] == nil {
		t.Fatalf("%+v %v", res, e)
	}
}

func TestChatGeminiFunctionCalls(t *testing.T) {
	var got map[string]any
	var path string
	c := chatClient(t, ProtoGemini, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		got = readBody(t, r)
		jsonOut(w, 200, map[string]any{
			"candidates":    []map[string]any{{"finishReason": "STOP", "content": map[string]any{"parts": []map[string]any{{"text": "hi "}, {"functionCall": map[string]any{"name": "read_file", "args": map[string]any{"path": "z"}}}}}}},
			"usageMetadata": map[string]any{"promptTokenCount": 4, "candidatesTokenCount": 2},
		})
	}, goodKey)
	res, e := c.Chat(context.Background(), convo())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(path, ":generateContent") || got["systemInstruction"] == nil {
		t.Fatalf("path %q body %v", path, got)
	}
	contents := got["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("results must be one user turn, got %d", len(contents))
	}
	parts := contents[2].(map[string]any)["parts"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["functionResponse"].(map[string]any)["name"] != "read_file" {
		t.Fatalf("parts: %v", parts)
	}
	if res.Text != "hi " || len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "read_file" || res.ToolCalls[0].ID == "" {
		t.Fatalf("%+v", res)
	}
}

func TestChatOllama(t *testing.T) {
	var got map[string]any
	c := chatClient(t, ProtoOllama, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			jsonOut(w, 404, errBody(404, "no"))
			return
		}
		got = readBody(t, r)
		jsonOut(w, 200, map[string]any{"message": map[string]any{"content": "done", "tool_calls": []map[string]any{{"function": map[string]any{"name": "read_file", "arguments": map[string]any{"path": "q"}}}}},
			"done_reason": "stop", "prompt_eval_count": 8, "eval_count": 2})
	}, "")
	res, e := c.Chat(context.Background(), convo())
	if e != nil {
		t.Fatal(e)
	}
	if got["stream"] != false {
		t.Fatalf("must not stream: %v", got["stream"])
	}
	if res.Text != "done" || len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments["path"] != "q" || res.InputTokens != 8 || res.OutputTokens != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestChatErrorsAreClassifiedAndNeverLeakKeyOrConversation(t *testing.T) {
	for _, proto := range []string{ProtoAnthropic, ProtoOpenAI, ProtoGemini, ProtoOllama} {
		c := chatClient(t, proto, func(w http.ResponseWriter, r *http.Request) {
			jsonOut(w, 401, errBody(401, "invalid key "+goodKey+" for show main.go"))
		}, goodKey)
		_, e := c.Chat(context.Background(), convo())
		if e == nil || e.Kind != KindUnauthorized {
			t.Fatalf("%s: want unauthorized, got %v", proto, e)
		}
		if strings.Contains(e.Msg, goodKey) || strings.Contains(e.Msg, "main.go") {
			t.Fatalf("%s: leaked: %s", proto, e.Msg)
		}
	}
	c := chatClient(t, ProtoOpenAI, func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]any{"choices": []any{}}) }, goodKey)
	if _, e := c.Chat(context.Background(), convo()); e == nil || e.Kind != KindProtocol {
		t.Fatalf("empty choices: %v", e)
	}
}

func TestChatRespectsSSRFGuard(t *testing.T) {
	g := netguard.New(netguard.Policy{})
	c, _ := NewClient(Config{Protocol: ProtoOpenAI, BaseURL: "http://127.0.0.1:1", APIKey: goodKey, ModelID: "m"}, g, nil)
	if _, e := c.Chat(context.Background(), convo()); e == nil || e.Kind != KindBlocked {
		t.Fatalf("want blocked, got %v", e)
	}
}
