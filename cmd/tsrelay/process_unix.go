//go:build unix

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

// detach makes cmd a background tunnel: it starts in a new session, without
// a controlling terminal, so closing the terminal does not end it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// processAlive reports whether a process with this pid exists. A pid the
// system has given to another process since counts as alive.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// terminate asks the background tunnel id, running as pid, to stop. On
// Unix that is SIGTERM, which main turns into a clean detach.
func terminate(pid int, _ string) error { return syscall.Kill(pid, syscall.SIGTERM) }

// watchStop calls stop when terminate asks the background tunnel id to
// stop. On Unix, SIGTERM already ends main's context, so there is nothing
// to watch.
func watchStop(string, func()) error { return nil }
