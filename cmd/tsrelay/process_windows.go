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

// stillActive is the exit code GetExitCodeProcess reports for a process
// that is still running.
const stillActive = 259

// processAlive reports whether a process with this pid is running. A pid
// the system has given to another process since counts as alive.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
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
