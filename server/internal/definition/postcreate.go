package definition

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
)

// Agent is the link to the workspace agent, used to run post-create commands.
type Agent interface {
	Call(ctx context.Context, wsID, method string, params, out any) error
	Online(wsID string) bool
}

// WithAgent enables running post-create commands.
func (s *Service) WithAgent(a Agent) *Service { s.agent = a; return s }

type execResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timedOut"`
}

// postCreateHandler runs commands one after another in the workspace root. A
// failure stops the sequence and is reported with the command's output.
func (s *Service) postCreateHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	if s.agent == nil {
		return errors.New("running commands in the workspace is not available")
	}
	raw, _ := op.Params["commands"].([]any)
	owner, _ := op.Params["owner"].(string)
	// The agent connects shortly after the workspace starts; give it a moment.
	for i := 0; i < 60 && !s.agent.Online(op.WorkspaceID); i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if !s.agent.Online(op.WorkspaceID) {
		return errors.New("the workspace agent is not connected, so the post-create commands did not run")
	}
	for i, c := range raw {
		cmd := fmt.Sprint(c)
		p.Logf("[%d/%d] $ %s", i+1, len(raw), cmd)
		var res execResult
		cctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
		err := s.agent.Call(cctx, op.WorkspaceID, "exec.run", map[string]any{"command": cmd, "timeoutSec": 600}, &res)
		cancel()
		if err != nil {
			return fmt.Errorf("command %d could not be run: %w", i+1, err)
		}
		if out := strings.TrimSpace(res.Stdout + res.Stderr); out != "" {
			if len(out) > 4000 {
				out = "…" + out[len(out)-4000:]
			}
			p.Logf("%s", out)
		}
		if res.TimedOut {
			return fmt.Errorf("command %d timed out after 10 minutes: %s", i+1, cmd)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("command %d failed with exit code %d: %s", i+1, res.ExitCode, cmd)
		}
	}
	s.aud.User(ctx, owner, op.WorkspaceID, "devcontainer.post_create", "", map[string]any{"commands": len(raw)})
	p.Logf("post-create commands finished")
	return nil
}
