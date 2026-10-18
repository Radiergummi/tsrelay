//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach makes cmd a background tunnel: a process without a console, in a
// process group of its own, so closing the console does not end it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}

// markRunning has nothing to do on Windows: watchStop's stop event exists
// exactly as long as the tunnel runs.
func markRunning(string, string) error { return nil }

// runAlive reports whether the background tunnel r is still running: its
// stop event exists. A pid alone can't tell, since the system reuses them.
func runAlive(_ string, r runRecord) bool {
	h, err := windows.OpenEvent(windows.SYNCHRONIZE, false, stopEvent(r.ID))
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// stopEvent names the event a background tunnel waits on. A detached
// process has no console, so no console control event can reach it;
// terminate sets this event instead.
func stopEvent(id string) *uint16 {
	name, _ := windows.UTF16PtrFromString(`Local\tsrelay-stop-` + id)
	return name
}

// terminate asks the background tunnel id to stop by setting its stop
// event, which watchStop turns into a clean detach.
func terminate(_ int, id string) error {
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, stopEvent(id))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h)
}

// watchStop calls stop once terminate sets the stop event of the
// background tunnel id.
func watchStop(id string, stop func()) error {
	h, err := windows.CreateEvent(nil, 1, 0, stopEvent(id))
	if err != nil {
		return err
	}
	go func() {
		windows.WaitForSingleObject(h, windows.INFINITE)
		stop()
	}()
	return nil
}
