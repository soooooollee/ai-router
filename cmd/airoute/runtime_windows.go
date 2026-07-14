//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func prepareBackgroundCommand(cmd *exec.Cmd) {
	const (
		createNewProcessGroup = 0x00000200
		detachedProcess       = 0x00000008
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
		HideWindow:    true,
	}
}

func processAlive(pid int) bool {
	output, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	return err == nil && strings.Contains(string(output), fmt.Sprintf("\"%d\"", pid))
}

func terminateProcess(pid int, force bool) error {
	args := []string{"/PID", fmt.Sprint(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return exec.Command("taskkill", args...).Run()
}
