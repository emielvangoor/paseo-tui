//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// ownTerminal makes the pty the pane program's controlling terminal, in a session
// of its own: resizes reach it as SIGWINCH, and /dev/tty is the pane, not ours.
func ownTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
}
