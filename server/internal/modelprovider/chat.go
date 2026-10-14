package modelprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Chat with tool calls (PRD F17). The agent loop talks to every provider
// through these types; each adapter translates them to its own wire format.
// Like the rest of this package, requests go through the SSRF guard and errors
// are user-safe: they never contain the key or the conversation.

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall is a request from the model to run a tool.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Message is one turn. An assistant turn may carry tool calls; a tool turn
// answers one call (ToolCallID) with text.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	ToolName   string     `json:"toolName,omitempty"`
}

// ToolDef describes a tool the model may call. Parameters is a JSON Schema object.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	System      string
	Messages    []Message
	Tools       []ToolDef
	MaxTokens   int
	Temperature *float64
}

type ChatResponse struct {
	Text         string     `json:"text"`
	ToolCalls    []ToolCall `json:"toolCalls,omitempty"`
	StopReason   string     `json:"stopReason,omitempty"`
	InputTokens  int        `json:"inputTokens,omitempty"`
	OutputTokens int        `json:"outputTokens,omitempty"`
}

// Chatter is implemented by every adapter that can run a conversation.
type Chatter interface {
	Chat(ctx context.Context, c *Client, req ChatRequest) (*ChatResponse, *Error)
}

// Chat sends one request and returns the model's next turn.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, *Error) {
	ad, ok := ForProtocol(c.cfg.Protocol)
	if !ok {
		return nil, errf(KindInvalidInput, "Choose how to call this model.")
	}
	ch, ok := ad.(Chatter)
	if !ok {
		return nil, errf(KindUnsupported, "This protocol cannot run a conversation.")
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 4096
	}
	return ch.Chat(ctx, c, req)
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func parseArgs(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return map[string]any{"_raw": raw}
	}
	return out
}

func toolSchema(t ToolDef) map[string]any {
	if t.Parameters == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return t.Parameters
}

// ---- Anthropic Messages API

func (a anthropicAdapter) Chat(ctx context.Context, c *Client, req ChatRequest) (*ChatResponse, *Error) {
	var msgs []map[string]any
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": m.Content}}})
		case RoleAssistant:
			var blocks []map[string]any
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if args == nil {
					args = map[string]any{}
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": args})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]any{"type": "text", "text": "(no output)"})
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": blocks})
		case RoleTool:
			block := map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content}
			// Consecutive results belong to one user turn.
			if n := len(msgs); n > 0 && msgs[n-1]["role"] == "user" {
				if prev, ok := msgs[n-1]["content"].([]map[string]any); ok && len(prev) > 0 && prev[0]["type"] == "tool_result" {
					msgs[n-1]["content"] = append(prev, block)
					continue
				}
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": []map[string]any{block}})
		}
	}
	body := map[string]any{"model": c.cfg.ModelID, "max_tokens": req.MaxTokens, "messages": msgs}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		var ts []map[string]any
		for _, t := range req.Tools {
			ts = append(ts, map[string]any{"name": t.Name, "description": t.Description, "input_schema": toolSchema(t)})
		}
		body["tools"] = ts
	}
	raw, e := c.do(ctx, http.MethodPost, "/v1/messages", body, a.auth(c))
	if e != nil {
		return nil, e
	}
	var out struct {
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}
	if e := decode(raw, &out); e != nil {
		return nil, e
	}
	res := &ChatResponse{StopReason: out.StopReason, InputTokens: out.Usage.In, OutputTokens: out.Usage.Out}
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			res.Text += b.Text
		case "tool_use":
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input})
		}
	}
	return res, nil
}

// ---- OpenAI Chat Completions (and compatible servers)

func (a openaiAdapter) Chat(ctx context.Context, c *Client, req ChatRequest) (*ChatResponse, *Error) {
	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Content})
		case RoleAssistant:
			am := map[string]any{"role": "assistant", "content": m.Content}
			if m.Content == "" {
				am["content"] = nil
			}
			var tcs []map[string]any
			for _, tc := range m.ToolCalls {
				tcs = append(tcs, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": jsonString(tc.Arguments)}})
			}
			if len(tcs) > 0 {
				am["tool_calls"] = tcs
			}
			msgs = append(msgs, am)
		case RoleTool:
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Content})
		}
	}
	body := map[string]any{"model": c.cfg.ModelID, "messages": msgs, "max_tokens": req.MaxTokens}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		var ts []map[string]any
		for _, t := range req.Tools {
			ts = append(ts, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": toolSchema(t)}})
		}
		body["tools"] = ts
	}
	raw, e := c.do(ctx, http.MethodPost, "/chat/completions", body, a.auth(c))
	if e != nil && e.Kind == KindProtocol && strings.Contains(e.Msg, "max_completion_tokens") {
		delete(body, "max_tokens")
		body["max_completion_tokens"] = req.MaxTokens
		raw, e = c.do(ctx, http.MethodPost, "/chat/completions", body, a.auth(c))
	}
	if e != nil {
		return nil, e
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			In  int `json:"prompt_tokens"`
			Out int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if e := decode(raw, &out); e != nil {
		return nil, e
	}
	if len(out.Choices) == 0 {
		return nil, errf(KindProtocol, "The model returned no answer.")
	}
	ch := out.Choices[0]
	res := &ChatResponse{Text: ch.Message.Content, StopReason: ch.FinishReason, InputTokens: out.Usage.In, OutputTokens: out.Usage.Out}
	for i, tc := range ch.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{ID: id, Name: tc.Function.Name, Arguments: parseArgs(tc.Function.Arguments)})
	}
	return res, nil
}

// ---- Google Gemini API

func (a geminiAdapter) Chat(ctx context.Context, c *Client, req ChatRequest) (*ChatResponse, *Error) {
	var contents []map[string]any
	names := map[string]string{} // tool call id -> function name, for results
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]any{{"text": m.Content}}})
		case RoleAssistant:
			var parts []map[string]any
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				names[tc.ID] = tc.Name
				args := tc.Arguments
				if args == nil {
					args = map[string]any{}
				}
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": tc.Name, "args": args}})
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			}
		case RoleTool:
			name := m.ToolName
			if name == "" {
				name = names[m.ToolCallID]
			}
			part := map[string]any{"functionResponse": map[string]any{"name": name, "response": map[string]any{"result": m.Content}}}
			if n := len(contents); n > 0 && contents[n-1]["role"] == "user" {
				if prev, ok := contents[n-1]["parts"].([]map[string]any); ok && len(prev) > 0 && prev[0]["functionResponse"] != nil {
					contents[n-1]["parts"] = append(prev, part)
					continue
				}
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]any{part}})
		}
	}
	gc := map[string]any{"maxOutputTokens": req.MaxTokens}
	if req.Temperature != nil {
		gc["temperature"] = *req.Temperature
	}
	body := map[string]any{"contents": contents, "generationConfig": gc}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": req.System}}}
	}
	if len(req.Tools) > 0 {
		var ds []map[string]any
		for _, t := range req.Tools {
			ds = append(ds, map[string]any{"name": t.Name, "description": t.Description, "parameters": toolSchema(t)})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": ds}}
	}
	raw, e := c.do(ctx, http.MethodPost, "/v1beta/models/"+c.cfg.ModelID+":generateContent", body, a.auth(c))
	if e != nil {
		return nil, e
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		Usage struct {
			In  int `json:"promptTokenCount"`
			Out int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if e := decode(raw, &out); e != nil {
		return nil, e
	}
	if len(out.Candidates) == 0 {
		return nil, errf(KindProtocol, "The model returned no answer (it may have been blocked by a safety filter).")
	}
	cand := out.Candidates[0]
	res := &ChatResponse{StopReason: cand.FinishReason, InputTokens: out.Usage.In, OutputTokens: out.Usage.Out}
	for _, p := range cand.Content.Parts {
		res.Text += p.Text
		if p.FunctionCall != nil {
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: fmt.Sprintf("call_%d", len(res.ToolCalls)+1), Name: p.FunctionCall.Name, Arguments: p.FunctionCall.Args})
		}
	}
	return res, nil
}

// ---- Ollama native chat API

func (a ollamaAdapter) Chat(ctx context.Context, c *Client, req ChatRequest) (*ChatResponse, *Error) {
	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Content})
		case RoleAssistant:
			am := map[string]any{"role": "assistant", "content": m.Content}
			var tcs []map[string]any
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if args == nil {
					args = map[string]any{}
				}
				tcs = append(tcs, map[string]any{"function": map[string]any{"name": tc.Name, "arguments": args}})
			}
			if len(tcs) > 0 {
				am["tool_calls"] = tcs
			}
			msgs = append(msgs, am)
		case RoleTool:
			msgs = append(msgs, map[string]any{"role": "tool", "content": m.Content, "tool_name": m.ToolName})
		}
	}
	opts := map[string]any{"num_predict": req.MaxTokens}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	body := map[string]any{"model": c.cfg.ModelID, "messages": msgs, "stream": false, "options": opts}
	if len(req.Tools) > 0 {
		var ts []map[string]any
		for _, t := range req.Tools {
			ts = append(ts, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": toolSchema(t)}})
		}
		body["tools"] = ts
	}
	raw, e := c.do(ctx, http.MethodPost, "/api/chat", body, a.auth(c))
	if e != nil {
		return nil, e
	}
	var out struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		DoneReason string `json:"done_reason"`
		In         int    `json:"prompt_eval_count"`
		Out        int    `json:"eval_count"`
	}
	if e := decode(raw, &out); e != nil {
		return nil, e
	}
	res := &ChatResponse{Text: out.Message.Content, StopReason: out.DoneReason, InputTokens: out.In, OutputTokens: out.Out}
	for i, tc := range out.Message.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, ToolCall{ID: fmt.Sprintf("call_%d", i+1), Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return res, nil
}
