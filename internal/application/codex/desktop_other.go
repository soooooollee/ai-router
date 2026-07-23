//go:build !windows

package codex

import "context"

func detectPlatformDesktopApplication(_ context.Context) executableDetection {
	return executableDetection{}
}
