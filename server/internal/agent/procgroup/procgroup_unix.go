//go:build !windows

// Package procgroup isolates the platform-specific parts of running child
// processes: putting them in their own group and killing the whole group.
package procgroup

import (
	"os/exec"
	"syscall"
)

// Isolate starts cmd in its own process group.
func Isolate(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// Kill force-kills cmd and its children.
func Kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Hangup asks cmd and its children to exit.
func Hangup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		_ = cmd.Process.Signal(syscall.SIGHUP)
	}
}

// Shell returns a command that runs line through the platform shell.
func Shell(line string) *exec.Cmd { return exec.Command("/bin/sh", "-c", line) }
