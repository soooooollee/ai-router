package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/config"
)

func TestRuntimeStateRoundTripUsesPrivateFiles(t *testing.T) {
	t.Setenv("AIROUTE_RUNTIME_DIR", t.TempDir())
	statePath, logPath, err := runtimePaths()
	if err != nil {
		t.Fatal(err)
	}
	want := runtimeState{
		PID:        1234,
		ConfigPath: "/tmp/airoute.yaml",
		GatewayURL: "http://127.0.0.1:12666",
		AdminURL:   "http://127.0.0.1:12667",
		LogFile:    logPath,
		StartedAt:  time.Unix(10, 0),
	}
	if err = saveRuntimeState(statePath, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadRuntimeState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != want.PID || got.ConfigPath != want.ConfigPath || got.LogFile != want.LogFile {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
	removeRuntimeState(statePath, want.PID+1)
	if _, err = os.Stat(statePath); err != nil {
		t.Fatalf("state removed by the wrong PID: %v", err)
	}
	removeRuntimeState(statePath, want.PID)
	if !os.IsNotExist(statError(statePath)) {
		t.Fatal("state was not removed by its owner PID")
	}
}

func TestDefaultConfigPathSharesManagedRuntimeDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("AIROUTE_RUNTIME_DIR", directory)
	t.Setenv("AIROUTE_CONFIG", "")
	path, err := defaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(directory, configFileName) {
		t.Fatalf("default config path = %q", path)
	}
	override := filepath.Join(t.TempDir(), "custom.yaml")
	t.Setenv("AIROUTE_CONFIG", override)
	path, err = defaultConfigPath()
	if err != nil || path != override {
		t.Fatalf("environment config path = %q, err=%v", path, err)
	}
}

func TestResolveConfigPathMigratesLegacyWorkingDirectoryFile(t *testing.T) {
	runtimeDirectory := t.TempDir()
	workingDirectory := t.TempDir()
	t.Setenv("AIROUTE_RUNTIME_DIR", runtimeDirectory)
	t.Setenv("AIROUTE_CONFIG", "")
	t.Chdir(workingDirectory)
	legacy := filepath.Join(workingDirectory, configFileName)
	if err := os.WriteFile(legacy, []byte(minimalConfig), 0600); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(runtimeDirectory, configFileName)
	if resolved != want {
		t.Fatalf("resolved config path = %q, want %q", resolved, want)
	}
	if _, err = config.Load(want); err != nil {
		t.Fatalf("migrated configuration is invalid: %v", err)
	}
	if _, err = os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy working-directory configuration remains: %v", err)
	}
}

func TestRotateRuntimeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "airoute.log")
	if err := os.WriteFile(path, make([]byte, (10<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rotateRuntimeLog(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureListenAvailableRejectsOccupiedAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = ensureListenAvailable(listener.Addr().String(), "gateway"); err == nil {
		t.Fatal("occupied address was accepted")
	}
}

func statError(path string) error {
	_, err := os.Stat(path)
	return err
}
