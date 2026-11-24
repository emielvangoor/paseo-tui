package main

import "os/exec"

// ownTerminal is a no-op: ConPTY attaches the console itself.
func ownTerminal(*exec.Cmd) {}
