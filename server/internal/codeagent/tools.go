package codeagent

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/modelprovider"
	"github.com/nexio-developer-group/nexmesh/server/internal/secretscan"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

// Tool names.
const (
	ToolContext = "get_canvas_context"
	ToolList    = "list_files"
	ToolRead    = "read_file"
	ToolSearch  = "search_files"
	ToolPropose = "propose_changes"
	ToolRun     = "run_command"
	ToolCard    = "propose_card"
	ToolWire    = "propose_connection"
)

func obj(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// toolDefs lists the tools a permission level may use.
func (s *Service) toolDefs(level string) []modelprovider.ToolDef {
	defs := []modelprovider.ToolDef{
		{Name: ToolContext, Description: "Get the current canvas: cards, connection lines, problems and variable names. Values are never included.", Parameters: obj(map[string]any{})},
		{Name: ToolList, Description: "List the files and folders in a workspace folder (default: the workspace root).", Parameters: obj(map[string]any{"path": str("Folder path relative to the workspace root")})},
		{Name: ToolRead, Description: "Read a text file. Use start_line and end_line (1-based) for part of a large file.", Parameters: obj(map[string]any{
			"path": str("File path relative to the workspace root"), "start_line": map[string]any{"type": "integer"}, "end_line": map[string]any{"type": "integer"}}, "path")},
		{Name: ToolSearch, Description: "Find lines containing a text (case-insensitive) across the workspace's text files.", Parameters: obj(map[string]any{
			"query": str("Text to look for"), "path": str("Folder to search in (default: the workspace root)")}, "query")},
	}
	if Allows(level, LevelPropose) {
		defs = append(defs, modelprovider.ToolDef{Name: ToolPropose, Description: "Propose file changes for the user to review. Nothing is written until the user accepts. Give the full new content of each file; set content to null to delete a file. Propose all related edits in one call.",
			Parameters: obj(map[string]any{
				"title":   str("Short title for the change"),
				"summary": str("One or two sentences on what and why"),
				"changes": map[string]any{"type": "array", "items": obj(map[string]any{
					"path": str("File path relative to the workspace root"), "content": map[string]any{"type": []string{"string", "null"}, "description": "Full new file content, or null to delete"}}, "path")},
			}, "title", "changes")})
	}
	if Allows(level, LevelPropose) && s.canvas != nil {
		defs = append(defs,
			modelprovider.ToolDef{Name: ToolCard, Description: "Suggest adding a technology to the canvas as a Draft card. The user decides; nothing is installed or started.",
				Parameters: obj(map[string]any{"catalog_id": str("Catalog id of the technology, for example redis"), "reason": str("Why it is needed")}, "catalog_id", "reason")},
			modelprovider.ToolDef{Name: ToolWire, Description: "Suggest a connection line from one canvas card to another (for example a backend that uses a database). The user reviews the variables it creates and decides.",
				Parameters: obj(map[string]any{"from": str("Card that depends on the other (name or id)"), "to": str("Card it connects to (name or id)"), "reason": str("Why")}, "from", "to", "reason")})
	}
	if Allows(level, LevelExecute) {
		defs = append(defs, modelprovider.ToolDef{Name: ToolRun, Description: "Run a shell command in the workspace (for tests, builds and inspection). Read-only, build and test commands run immediately; anything else waits for the user's approval. Do not use commands to edit files: use propose_changes.",
			Parameters: obj(map[string]any{"command": str("The command line")}, "command")})
	}
	return defs
}

// exec runs one tool call and returns its result text and status. It never
// returns an error: every failure becomes a result the model can read.
func (s *Service) exec(ctx context.Context, rc *runCtx, call modelprovider.ToolCall) (result, status, csID string) {
	denied := func(reason, target string) (string, string, string) {
		s.aud.Record(ctx, domain.ActorAgent, rc.sess.ID, rc.sess.WorkspaceID, "agent.action_denied", call.Name, map[string]any{"reason": reason, "target": truncate(target, 200)})
		return "Not allowed: " + reason, CallDenied, ""
	}
	argStr := func(k string) string { v, _ := call.Arguments[k].(string); return v }
	switch call.Name {
	case ToolContext:
		c, err := s.BuildContext(ctx, rc.owner, rc.sess.WorkspaceID)
		if err != nil {
			return "The canvas could not be read.", CallError, ""
		}
		return c.Render(), CallOK, ""

	case ToolList:
		p := argStr("path")
		if p == "" {
			p = "."
		}
		if p != "." {
			cp, err := changeset.CleanPath(p)
			if err != nil {
				return denied("that folder is outside the workspace", p)
			}
			p = cp
		}
		if SecretPath(p) {
			return denied("that folder can hold secrets, which are kept in Variables", p)
		}
		es, err := s.fs.List(ctx, rc.sess.WorkspaceID, p)
		if err != nil {
			return failText(err), CallError, ""
		}
		var b strings.Builder
		hidden := 0
		for _, e := range es {
			if e.Name == ".git" {
				continue
			}
			if SecretPath(e.Path) {
				hidden++
				continue
			}
			if e.Type == "dir" {
				b.WriteString(e.Name + "/\n")
			} else {
				fmt.Fprintf(&b, "%s (%d bytes)\n", e.Name, e.Size)
			}
		}
		if hidden > 0 {
			fmt.Fprintf(&b, "(%d file(s) that can hold secrets are hidden)\n", hidden)
		}
		if b.Len() == 0 {
			return "(empty)", CallOK, ""
		}
		return b.String(), CallOK, ""

	case ToolRead:
		p, err := changeset.CleanPath(argStr("path"))
		if err != nil {
			return denied("that path is outside the workspace", argStr("path"))
		}
		if SecretPath(p) {
			return denied("that file can hold secrets, which are kept in Variables and never shown to the agent", p)
		}
		t, err := s.fs.Read(ctx, rc.sess.WorkspaceID, p)
		if err == wsfs.ErrNotFound {
			return p + " does not exist.", CallError, ""
		}
		if err == wsfs.ErrBinary {
			return p + " is binary or too large to read.", CallError, ""
		}
		if err != nil {
			return failText(err), CallError, ""
		}
		text := t.Content
		start, end := intArg(call.Arguments["start_line"]), intArg(call.Arguments["end_line"])
		if start > 0 || end > 0 {
			lines := strings.SplitAfter(text, "\n")
			if start < 1 {
				start = 1
			}
			if end < 1 || end > len(lines) {
				end = len(lines)
			}
			if start > end {
				return "start_line is after end_line.", CallError, ""
			}
			text = strings.Join(lines[start-1:end], "")
		}
		if len(text) > MaxFileToModel {
			text = text[:MaxFileToModel] + "\n[truncated: use start_line and end_line to read further]"
		}
		return text, CallOK, ""

	case ToolSearch:
		q := argStr("query")
		if strings.TrimSpace(q) == "" {
			return "Give some text to search for.", CallError, ""
		}
		root := argStr("path")
		if root == "" {
			root = "."
		} else if cp, err := changeset.CleanPath(root); err != nil {
			return denied("that folder is outside the workspace", root)
		} else {
			root = cp
		}
		return s.search(ctx, rc.sess.WorkspaceID, root, q), CallOK, ""

	case ToolPropose:
		if !Allows(rc.level, LevelPropose) {
			return denied("this workspace lets the agent read but not propose changes (Workspace Settings, Code Agent)", "")
		}
		changes, title, summary, err := parseChanges(call.Arguments)
		if err != nil {
			return err.Error(), CallError, ""
		}
		for _, c := range changes {
			if SecretPath(c.Path) {
				return denied("changes to "+c.Path+" are not allowed: files that can hold secrets are managed in Variables", c.Path)
			}
		}
		set, err2 := s.changes.Propose(ctx, rc.owner, rc.sess.WorkspaceID, domain.ActorAgent, rc.sess.ID, changeset.ProposeInput{Title: title, Summary: summary, Changes: changes})
		if err2 != nil {
			return "The change could not be staged: " + err2.Error(), CallError, ""
		}
		if rc.autoAccept {
			if _, aerr := s.changes.Accept(ctx, rc.owner, rc.sess.WorkspaceID, set.ID, changeset.Decision{All: true}); aerr == nil {
				s.aud.User(ctx, rc.owner, rc.sess.WorkspaceID, "agent.auto_accepted", set.ID, map[string]any{"files": len(set.Files)})
				return fmt.Sprintf("Applied %d file(s) (auto-accept is on). A checkpoint was recorded so the user can revert.", len(set.Files)), CallOK, set.ID
			} else {
				return fmt.Sprintf("Staged as a change set with %d file(s), but it was not applied automatically: %s The user will review it.", len(set.Files), aerr.Error()), CallOK, set.ID
			}
		}
		return fmt.Sprintf("Staged as a change set with %d file(s). Nothing has been written; the user will review it and accept or reject it.", len(set.Files)), CallOK, set.ID

	case ToolCard, ToolWire:
		if !Allows(rc.level, LevelPropose) || s.canvas == nil {
			return denied("this workspace does not let the agent propose canvas changes", "")
		}
		if call.Name == ToolCard {
			r, st, id := s.proposeCard(ctx, rc, call.Arguments)
			return r, st, "p:" + id
		}
		r, st, id := s.proposeConnection(ctx, rc, call.Arguments)
		return r, st, "p:" + id

	case ToolRun:
		if !Allows(rc.level, LevelExecute) {
			return denied("this workspace does not let the agent run commands (Workspace Settings, Code Agent)", argStr("command"))
		}
		cmd := argStr("command")
		verdict, why := ClassifyCommand(cmd)
		if verdict == CmdForbidden {
			return denied(why, cmd)
		}
		if verdict == CmdRisky {
			ok := s.awaitApproval(ctx, rc, call.ID, cmd, why)
			if !ok {
				if ctx.Err() != nil {
					return "The run was stopped.", CallRejected, ""
				}
				s.aud.Record(ctx, domain.ActorAgent, rc.sess.ID, rc.sess.WorkspaceID, "agent.command_not_run", ToolRun, map[string]any{"command": truncate(cmd, 300), "reason": "declined"})
				return "The user declined to run this command. Do not retry it; suggest another approach or ask what they prefer.", CallRejected, ""
			}
		}
		s.aud.Record(ctx, domain.ActorAgent, rc.sess.ID, rc.sess.WorkspaceID, "agent.command_run", ToolRun, map[string]any{"command": truncate(cmd, 300), "approved": verdict == CmdRisky})
		res, err := s.fs.Run(ctx, rc.sess.WorkspaceID, cmd, 60)
		if err != nil {
			return failText(err), CallError, ""
		}
		out := res.Stdout
		if res.Stderr != "" {
			out += "\n[stderr]\n" + res.Stderr
		}
		note := ""
		if res.TimedOut {
			note = "\n[timed out after 60 seconds]"
		}
		if res.Truncated {
			note += "\n[output truncated]"
		}
		// Whatever a command printed may include credentials from the environment or a file.
		out, _ = secretscan.Scrub(out)
		return fmt.Sprintf("exit code %d\n%s%s", res.ExitCode, out, note), CallOK, ""
	}
	return denied("there is no tool called "+call.Name+"; the agent can only use the tools it was given", call.Name)
}

func failText(err error) string { return "That did not work: " + truncate(err.Error(), 200) }

func intArg(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func parseChanges(args map[string]any) ([]changeset.Change, string, string, error) {
	title, _ := args["title"].(string)
	summary, _ := args["summary"].(string)
	raw, ok := args["changes"].([]any)
	if !ok || len(raw) == 0 {
		return nil, "", "", fmt.Errorf("changes must be a list with at least one file")
	}
	var out []changeset.Change
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, "", "", fmt.Errorf("each change needs a path and content")
		}
		p, _ := m["path"].(string)
		c := changeset.Change{Path: p}
		switch v := m["content"].(type) {
		case string:
			c.Content = &v
		case nil:
		default:
			return nil, "", "", fmt.Errorf("content must be a string, or null to delete")
		}
		out = append(out, c)
	}
	return out, title, summary, nil
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".venv": true, "target": true, "__pycache__": true, ".next": true}

// search walks text files breadth first within small limits.
func (s *Service) search(ctx context.Context, ws, root, q string) string {
	const maxFiles, maxMatches, maxBytes = 300, 60, 200 << 10
	needle := strings.ToLower(q)
	queue := []string{root}
	var matches []string
	files := 0
	for len(queue) > 0 && files < maxFiles && len(matches) < maxMatches {
		dir := queue[0]
		queue = queue[1:]
		es, err := s.fs.List(ctx, ws, dir)
		if err != nil {
			continue
		}
		sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
		for _, e := range es {
			if ctx.Err() != nil || files >= maxFiles || len(matches) >= maxMatches {
				break
			}
			switch {
			case e.Type == "dir":
				if !skipDirs[e.Name] && !SecretPath(e.Path) {
					queue = append(queue, e.Path)
				}
			case e.Type == "file" && e.Size <= maxBytes && !SecretPath(e.Path):
				files++
				t, err := s.fs.Read(ctx, ws, e.Path)
				if err != nil {
					continue
				}
				for i, line := range strings.Split(t.Content, "\n") {
					if strings.Contains(strings.ToLower(line), needle) {
						matches = append(matches, fmt.Sprintf("%s:%d: %s", path.Clean(e.Path), i+1, truncate(strings.TrimSpace(line), 200)))
						if len(matches) >= maxMatches {
							break
						}
					}
				}
			}
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No matches in %d file(s).", files)
	}
	out := strings.Join(matches, "\n")
	if len(matches) >= maxMatches {
		out += "\n[more matches exist; narrow the search]"
	}
	return out
}
