//go:build windows

package instance

import (
	"os"
	"syscall"
)

func isProcessAlive(pid int) bool {
	const access = 0x1000 | 0x00100000
	handle, err := syscall.OpenProcess(access, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)

	var exitCode uint32
	err = syscall.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return false
	}
	return exitCode == 259
}

func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
