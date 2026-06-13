//go:build windows

package main

import (
	"os"
	"os/exec"
)

func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func configureHeadlessCommand(cmd *exec.Cmd) {
}

func terminateHeadlessProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
