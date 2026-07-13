package config

import (
	"slices"
	"testing"
)

func TestCompareEffects(t *testing.T) {
	previous := &Config{}
	next := &Config{}
	next.Server.MaxConcurrent = 12
	next.Server.Listen = "127.0.0.1:9090"
	next.Logging.RequestHistory = 20
	next.Logging.Level = "debug"
	effects := CompareEffects(previous, next)
	if !slices.Contains(effects.RuntimeRebuilt, "server.max_concurrent") {
		t.Fatalf("runtime effects=%v", effects.RuntimeRebuilt)
	}
	if !slices.Contains(effects.RestartNeeded, "server.listen") {
		t.Fatalf("restart effects=%v", effects.RestartNeeded)
	}
	if !slices.Contains(effects.HotReloaded, "logging.request_history") || !slices.Contains(effects.HotReloaded, "logging.level") {
		t.Fatalf("hot effects=%v", effects.HotReloaded)
	}
}
