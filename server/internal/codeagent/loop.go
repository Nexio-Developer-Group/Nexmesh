package codeagent

import (
	"context"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/modelprovider"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type runCtx struct {
	owner string
	sess  *Session
	level string
	run   *run
}

const systemPrompt = `You are the Code Agent inside Nexmesh, a developer workspace. You help the user read, understand and change the code in their workspace.

How you work:
- Look before you change. Use get_canvas_context, list_files, read_file and search_files to understand the project.
- To change code, call propose_changes with the full new content of every file you want to change. The user reviews and accepts or rejects it; nothing is written until then. Never claim a change is applied; say it is proposed.
- Use run_command only to run tests, builds and read-only inspection. Do not edit files with shell commands.
- Secrets are never visible to you and you must not ask for them. Code should read secrets from environment variables by the names listed in the context. If a task needs a secret, say which variable name the user should add in Variables.
- Do not try to change the canvas, install components or manage the workspace; you do not have tools for that. Tell the user what to do instead.
- Be concise. Explain what you found and what you changed in plain language.`

// Send adds a user message and starts a run in the background. Poll the
// session (or wait for events) to follow it.
func (s *Service) Send(ctx context.Context, owner, wsID, id, text string) (*Session, error) {
	sess, err := s.load(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, verr("message", "empty_message", "Write a message first.")
	}
	if len(text) > 20_000 {
		return nil, verr("message", "message_too_long", "That message is too long. Keep it under 20,000 characters.")
	}
	if len(sess.Turns) >= MaxTurns {
		return nil, verr("session", "session_full", "This conversation is long. Start a new one.")
	}
	st := s.settings(ctx, wsID)
	model, err := s.model(ctx, owner, wsID, st.ConfigID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if _, busy := s.runs[id]; busy {
		s.mu.Unlock()
		return nil, verr("session", "busy", "The agent is still working on the last message.")
	}
	rctx, cancel := context.WithTimeout(context.Background(), MaxRunTime)
	r := &run{cancel: cancel, approval: make(chan bool, 1), done: make(chan struct{})}
	s.runs[id] = r
	s.mu.Unlock()

	if sess.Title == "" {
		sess.Title = truncate(text, 60)
	}
	sess.Turns = append(sess.Turns, Turn{ID: store.NewID("trn"), Role: "user", Content: text, At: s.now()})
	sess.Status, sess.Error, sess.Pending = StatusRunning, "", nil
	s.save(ctx, sess)
	s.aud.User(ctx, owner, wsID, "agent.message_sent", id, map[string]any{"level": st.Level, "chars": len(text)})
	snapshot := *sess
	snapshot.Turns = append([]Turn(nil), sess.Turns...)
	rc := &runCtx{owner: owner, sess: sess, level: st.Level, run: r}
	go func() {
		defer close(r.done)
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.runs, id)
			s.mu.Unlock()
		}()
		s.loop(rctx, rc, model)
	}()
	return &snapshot, nil
}

func (s *Service) awaitApproval(ctx context.Context, rc *runCtx, callID, cmd, why string) bool {
	rc.sess.Status = StatusAwaiting
	rc.sess.Pending = &Approval{CallID: callID, Command: cmd, Reason: why}
	s.save(context.Background(), rc.sess)
	defer func() {
		rc.sess.Status, rc.sess.Pending = StatusRunning, nil
		s.save(context.Background(), rc.sess)
	}()
	select {
	case ok := <-rc.run.approval:
		return ok
	case <-ctx.Done():
		return false
	case <-time.After(ApprovalWait):
		return false
	}
}

func (s *Service) loop(ctx context.Context, rc *runCtx, model Model) {
	sess := rc.sess
	bg := context.Background()
	fail := func(msg string) {
		sess.Status, sess.Error, sess.Pending = StatusError, msg, nil
		s.save(bg, sess)
	}
	finish := func() {
		sess.Status, sess.Pending = StatusIdle, nil
		s.save(bg, sess)
	}
	start := s.now()
	s.aud.Record(bg, domain.ActorAgent, sess.ID, sess.WorkspaceID, "agent.run_started", sess.ID, map[string]any{"level": rc.level})
	steps := 0
	defer func() {
		s.aud.Record(bg, domain.ActorAgent, sess.ID, sess.WorkspaceID, "agent.run_finished", sess.ID, map[string]any{"steps": steps, "status": sess.Status, "seconds": int(s.now().Sub(start).Seconds())})
	}()
	for steps < MaxSteps {
		if ctx.Err() != nil {
			sess.Turns = append(sess.Turns, Turn{ID: store.NewID("trn"), Role: "assistant", Content: "Stopped.", At: s.now()})
			finish()
			return
		}
		steps++
		cctx, err := s.BuildContext(ctx, rc.owner, sess.WorkspaceID)
		system := systemPrompt
		if err == nil {
			system += "\n\nCurrent workspace:\n" + cctx.Render()
		}
		resp, merr := model.Chat(ctx, modelprovider.ChatRequest{System: system, Messages: s.history(sess), Tools: toolDefs(rc.level)})
		if merr != nil {
			if ctx.Err() != nil {
				sess.Turns = append(sess.Turns, Turn{ID: store.NewID("trn"), Role: "assistant", Content: "Stopped.", At: s.now()})
				finish()
				return
			}
			fail(merr.Msg)
			return
		}
		sess.InputTokens += resp.InputTokens
		sess.OutputTokens += resp.OutputTokens
		turn := Turn{ID: store.NewID("trn"), Role: "assistant", Content: resp.Text, At: s.now()}
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(turn.Content) == "" {
				turn.Content = "(The model returned an empty answer.)"
			}
			sess.Turns = append(sess.Turns, turn)
			finish()
			return
		}
		for _, tc := range resp.ToolCalls {
			id := tc.ID
			if id == "" {
				id = store.NewID("call")
			}
			turn.Calls = append(turn.Calls, Call{ID: id, Name: tc.Name, Args: tc.Arguments, Status: CallPending})
		}
		sess.Turns = append(sess.Turns, turn)
		idx := len(sess.Turns) - 1
		s.save(bg, sess)
		for i := range sess.Turns[idx].Calls {
			c := &sess.Turns[idx].Calls[i]
			res, status, cs := s.exec(ctx, rc, modelprovider.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Args})
			c.Result, c.Status, c.ChangeSetID = truncate(s.mask(bg, sess.WorkspaceID, res), MaxToolOutput), status, cs
			s.save(bg, sess)
		}
	}
	sess.Turns = append(sess.Turns, Turn{ID: store.NewID("trn"), Role: "assistant", At: s.now(),
		Content: "I stopped after the maximum number of steps for one message. Ask me to continue if you want me to carry on."})
	finish()
}

// history converts stored turns to provider messages, shortening old tool output.
func (s *Service) history(sess *Session) []modelprovider.Message {
	var msgs []modelprovider.Message
	totalCalls := 0
	for _, t := range sess.Turns {
		totalCalls += len(t.Calls)
	}
	seen := 0
	for _, t := range sess.Turns {
		if t.Role == "user" {
			msgs = append(msgs, modelprovider.Message{Role: modelprovider.RoleUser, Content: t.Content})
			continue
		}
		m := modelprovider.Message{Role: modelprovider.RoleAssistant, Content: t.Content}
		for _, c := range t.Calls {
			m.ToolCalls = append(m.ToolCalls, modelprovider.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Args})
		}
		msgs = append(msgs, m)
		for _, c := range t.Calls {
			seen++
			res := c.Result
			if res == "" {
				res = "(no result)"
			}
			if totalCalls-seen >= 8 && len(res) > 600 {
				res = res[:600] + "\n[earlier output shortened]"
			}
			msgs = append(msgs, modelprovider.Message{Role: modelprovider.RoleTool, ToolCallID: c.ID, ToolName: c.Name, Content: res})
		}
	}
	return msgs
}

// mask removes known secret values from anything shown to the model or user.
func (s *Service) mask(ctx context.Context, wsID, text string) string {
	if s.secrets == nil || text == "" {
		return text
	}
	for _, v := range s.secrets(ctx, wsID) {
		if len(v) >= 6 {
			text = strings.ReplaceAll(text, v, "[hidden]")
		}
	}
	return text
}
