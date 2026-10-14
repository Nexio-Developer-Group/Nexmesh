package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCapturesAndPropagatesEnv(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{Root: dir, Env: func(context.Context) (map[string]string, error) { return map[string]string{"FOO": "bar"}, nil }}
	res, err := r.Run(context.Background(), Params{Command: "echo out-$FOO; echo err >&2; pwd; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || !strings.Contains(res.Stdout, "out-bar") || !strings.Contains(res.Stdout, dir) || strings.TrimSpace(res.Stderr) != "err" {
		t.Fatalf("%+v", res)
	}
	res, _ = r.Run(context.Background(), Params{Command: "cat", Stdin: "piped"})
	if res.Stdout != "piped" {
		t.Fatalf("stdin: %+v", res)
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	start := time.Now()
	res, err := r.Run(context.Background(), Params{Command: "sleep 30 & sleep 30", TimeoutSec: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout not enforced: %+v after %v", res, time.Since(start))
	}
}

func TestOutputIsCapped(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	res, _ := r.Run(context.Background(), Params{Command: "head -c 3000000 /dev/zero | tr '\\0' x"})
	if !res.Truncated || len(res.Stdout) != MaxOutput {
		t.Fatalf("truncated=%v len=%d", res.Truncated, len(res.Stdout))
	}
	if _, err := r.Run(context.Background(), Params{}); err == nil {
		t.Fatal("empty command accepted")
	}
}

func TestCancelIsNotATimeout(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	res, _ := r.Run(ctx, Params{Command: "sleep 20"})
	if res.TimedOut {
		t.Fatal("caller cancel reported as timeout")
	}
}
