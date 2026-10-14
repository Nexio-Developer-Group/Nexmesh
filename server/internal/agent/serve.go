package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent/ports"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/runner"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/terminal"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/workfs"
)

// FetchEnv asks the control plane for the variables and connection settings
// to inject into the next process. It is called per process start, so rotated
// secrets take effect without restarting the agent.
func (a *Agent) FetchEnv(ctx context.Context) (map[string]string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, a.cfg.ControlURL+"/agent/v1/workspaces/"+a.cfg.WorkspaceID+"/env", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("control plane returned %s", res.Status)
	}
	var body struct {
		Env map[string]string `json:"env"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Env, nil
}

// Serve installs the workspace capabilities (files, terminals, commands,
// port detection) rooted at root. The returned function stops them.
func (a *Agent) Serve(ctx context.Context, root string) (stop func(), err error) {
	fsys, err := workfs.Open(root)
	if err != nil {
		return nil, err
	}
	fsys.Install(a)
	(&runner.Runner{Root: root, Env: a.FetchEnv}).Install(a)
	tm := terminal.NewManager(root, a.Emit, a.FetchEnv)
	tm.Install(a)
	sc := ports.NewScanner()
	sc.Install(a)

	pctx, cancel := context.WithCancel(ctx)
	go sc.Watch(pctx, 2*time.Second, func(p []ports.Port) { a.Emit("ports.changed", map[string]any{"ports": p}) })
	a.OnConnect(func() { a.Emit("ports.changed", map[string]any{"ports": sc.Scan()}) })
	return func() {
		cancel()
		tm.CloseAll()
		_ = fsys.Close()
	}, nil
}
