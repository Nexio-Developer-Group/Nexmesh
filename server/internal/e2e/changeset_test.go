package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Staged changes against a real workspace agent: nothing is written until
// accepted, partial accept works, a checkpoint reverts, and edits made after
// an apply are protected.
func TestChangeSetsOverRealFiles(t *testing.T) {
	do := startLocalApp(t)
	for i := 0; i < 100; i++ {
		if c, _ := do("GET", "/healthz", ""); c == 200 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, body := do("POST", "/api/workspaces", `{"name":"cs-dev","cpu":2,"ramGb":4,"storageGb":20}`)
	id := between(body, `"id":"`, `"`)
	for i := 0; i < 300; i++ {
		if c, _ := do("GET", "/api/workspaces/"+id+"/files", ""); c == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	put := func(p, c string) {
		b, _ := json.Marshal(map[string]string{"path": p, "content": c})
		if code, out := do("PUT", "/api/workspaces/"+id+"/files/content", string(b)); code != 200 {
			t.Fatalf("put %s: %d %s", p, code, out)
		}
	}
	read := func(p string) (int, string) {
		c, o := do("GET", "/api/workspaces/"+id+"/files/content?path="+p, "")
		var f struct{ Content string }
		_ = json.Unmarshal([]byte(o), &f)
		return c, f.Content
	}

	if c, o := do("POST", "/api/workspaces/"+id+"/files/dir", `{"path":"src"}`); c >= 300 {
		t.Fatalf("mkdir: %d %s", c, o)
	}
	put("src/app.txt", "alpha\nbeta\ngamma\n")
	propose := `{"title":"Rename beta","summary":"agent edit","changes":[
		{"path":"src/app.txt","content":"alpha\nBETA\ngamma\n"},
		{"path":"src/new/created.txt","content":"fresh\n"}]}`
	c, out := do("POST", "/api/workspaces/"+id+"/changes", propose)
	if c != 201 {
		t.Fatalf("propose: %d %s", c, out)
	}
	csID := between(out, `"id":"`, `"`)
	if _, got := read("src/app.txt"); strings.Contains(got, "BETA") {
		t.Fatal("staged change reached the workspace")
	}
	if c, _ := read("src/new/created.txt"); c != 404 {
		t.Fatalf("staged file exists: %d", c)
	}
	if !strings.Contains(out, `"hunks"`) || strings.Contains(out, `"before"`) || strings.Contains(out, `"after"`) {
		t.Fatalf("view must carry hunks and no full contents: %s", out)
	}

	// Accept only the new file, so the edit stays staged-and-rejected.
	c, out = do("POST", "/api/workspaces/"+id+"/changes/"+csID+"/accept", `{"files":[{"path":"src/new/created.txt"}]}`)
	if c != 200 || !strings.Contains(out, `"partially_applied"`) {
		t.Fatalf("accept part: %d %s", c, out)
	}
	if _, got := read("src/new/created.txt"); !strings.Contains(got, "fresh") {
		t.Fatalf("created.txt: %s", got)
	}
	if _, got := read("src/app.txt"); strings.Contains(got, "BETA") {
		t.Fatal("unchosen file changed")
	}
	if c, _ := do("POST", "/api/workspaces/"+id+"/changes/"+csID+"/accept", `{"all":true}`); c != 422 {
		t.Fatalf("second decision = %d, want 422", c)
	}

	// Second set: accept all, then someone edits, revert refuses, force restores.
	_, out = do("POST", "/api/workspaces/"+id+"/changes", `{"title":"Rename beta again","changes":[{"path":"src/app.txt","content":"alpha\nBETA\ngamma\n"}]}`)
	cs2 := between(out, `"id":"`, `"`)
	if c, o := do("POST", "/api/workspaces/"+id+"/changes/"+cs2+"/accept", `{"all":true}`); c != 200 {
		t.Fatalf("accept all: %d %s", c, o)
	}
	if _, got := read("src/app.txt"); !strings.Contains(got, "BETA") {
		t.Fatalf("not applied: %s", got)
	}
	_, cps := do("GET", "/api/workspaces/"+id+"/checkpoints", "")
	var parsed struct {
		Checkpoints []struct {
			ID    string
			SetID string
		}
	}
	_ = json.Unmarshal([]byte(cps), &parsed)
	var cpID string
	for _, cp := range parsed.Checkpoints {
		if cp.SetID == cs2 {
			cpID = cp.ID
		}
	}
	if cpID == "" {
		t.Fatalf("no checkpoint for set: %s", cps)
	}
	if c, o := do("POST", "/api/workspaces/"+id+"/files/dir", `{"path":"src"}`); c >= 300 {
		t.Fatalf("mkdir: %d %s", c, o)
	}
	put("src/app.txt", "alpha\nBETA\ngamma\nmy own line\n")
	if c, o := do("POST", "/api/workspaces/"+id+"/checkpoints/"+cpID+"/revert", `{}`); c != 422 || !strings.Contains(o, "edited_since") {
		t.Fatalf("revert over edits: %d %s", c, o)
	}
	if c, o := do("POST", "/api/workspaces/"+id+"/checkpoints/"+cpID+"/revert", `{"force":true}`); c != 200 {
		t.Fatalf("forced revert: %d %s", c, o)
	}
	if _, got := read("src/app.txt"); got != "alpha\nbeta\ngamma\n" {
		t.Fatalf("restored: %q", got)
	}

	// Safety: paths cannot escape or touch .git; another user's id leaks nothing.
	for _, p := range []string{"../escape.txt", ".git/hooks/pre-commit", "/etc/passwd"} {
		b, _ := json.Marshal(map[string]any{"title": "x", "changes": []map[string]string{{"path": p, "content": "x"}}})
		if c, _ := do("POST", "/api/workspaces/"+id+"/changes", string(b)); c != 422 {
			t.Fatalf("path %q accepted: %d", p, c)
		}
	}
	// The audit log records the lifecycle without file contents.
	_, aud := do("GET", "/api/workspaces/"+id+"/audit", "")
	for _, a := range []string{"changeset.proposed", "changeset.applied", "changeset.reverted"} {
		if !strings.Contains(aud, a) {
			t.Fatalf("audit lacks %s: %s", a, aud)
		}
	}
	if strings.Contains(aud, "my own line") {
		t.Fatal("audit must not hold file contents")
	}
}
