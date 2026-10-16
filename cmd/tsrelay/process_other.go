//go:build !unix && !windows

package main

import (
	"os"
	"os/exec"
)

// On other systems a background tunnel is not detached, no process counts
// as alive, and stopping one kills it.

func detach(*exec.Cmd) {}

func processAlive(int) bool { return false }

func terminate(pid int, _ string) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func watchStop(string, func()) error { return nil }
