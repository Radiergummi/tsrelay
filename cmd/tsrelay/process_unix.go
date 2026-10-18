//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// detach makes cmd a background tunnel: it starts in a new session, without
// a controlling terminal, so closing the terminal does not end it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// runLock is the background tunnel's lock on run/<id>.lock, held until it
// exits. Kept here so the file is never closed.
var runLock *os.File

// markRunning locks run/<id>.lock for the life of this process, so
// runAlive can tell the tunnel from a process that got its pid since.
func markRunning(dir, id string) error {
	f, err := os.OpenFile(lockPath(dir, id), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return err
	}
	runLock = f
	return nil
}

// runAlive reports whether the background tunnel r is still running: its
// lock is held. A pid alone can't tell, since the system reuses them.
func runAlive(dir string, r runRecord) bool {
	f, err := os.Open(lockPath(dir, r.ID))
	if err != nil {
		return false
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	return errors.Is(err, syscall.EWOULDBLOCK)
}

// terminate asks the background tunnel id, running as pid, to stop. On
// Unix that is SIGTERM, which main turns into a clean detach.
func terminate(pid int, _ string) error { return syscall.Kill(pid, syscall.SIGTERM) }

// watchStop calls stop when terminate asks the background tunnel id to
// stop. On Unix, SIGTERM already ends main's context, so there is nothing
// to watch.
func watchStop(string, func()) error { return nil }
