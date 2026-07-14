//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func prepareBackgroundCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminateProcess(pid int, force bool) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if force {
		return process.Kill()
	}
	return process.Signal(syscall.SIGTERM)
}
