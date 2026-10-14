//go:build windows

// Package procgroup isolates the platform-specific parts of running child
// processes: putting them in their own group and killing the whole group.
package procgroup

import (
	"os/exec"
	"strconv"
)

// Isolate is a no-op on Windows.
func Isolate(cmd *exec.Cmd) {}

// Kill force-kills cmd and its children.
func Kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		_ = cmd.Process.Kill()
	}
}

// Hangup ends cmd; Windows has no SIGHUP.
func Hangup(cmd *exec.Cmd) { Kill(cmd) }

// Shell returns a command that runs line through cmd.exe.
func Shell(line string) *exec.Cmd { return exec.Command("cmd", "/C", line) }
