//go:build windows

package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// detectPlatformDesktopApplication detects the Microsoft Store/MSIX ChatGPT
// application without trying to execute its GUI binary with CLI arguments.
func detectPlatformDesktopApplication(ctx context.Context) executableDetection {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return executableDetection{}
	}

	// Execution aliases and unpackaged installers, when present.
	for _, candidate := range []string{
		filepath.Join(localAppData, "Microsoft", "WindowsApps", "ChatGPT.exe"),
		filepath.Join(localAppData, "Programs", "ChatGPT", "ChatGPT.exe"),
		filepath.Join(localAppData, "ChatGPT", "ChatGPT.exe"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return executableDetection{path: candidate, installed: true}
		}
	}

	// Microsoft Store packages create a per-user package data directory. This
	// remains readable even though the application payload under WindowsApps is
	// protected by ACLs and cannot be inspected reliably by a normal process.
	if path := findChatGPTPackage(filepath.Join(localAppData, "Packages")); path != "" {
		return executableDetection{path: path, installed: true}
	}
	if path := findChatGPTPackage(filepath.Join(localAppData, "Microsoft", "WindowsApps")); path != "" {
		return executableDetection{path: path, installed: true}
	}
	if appID := findChatGPTStartApp(ctx); appID != "" {
		return executableDetection{path: appID, installed: true}
	}
	return executableDetection{}
}

func findChatGPTPackage(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if looksLikeChatGPTWindowsPackage(entry.Name()) {
			return filepath.Join(root, entry.Name())
		}
	}
	return ""
}

func findChatGPTStartApp(ctx context.Context) string {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	script := `Get-StartApps | Where-Object { $_.Name -eq 'ChatGPT' } | ForEach-Object { $_.AppID }`
	command := exec.CommandContext(checkCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		if looksLikeOfficialChatGPTWindowsAppID(line) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
