package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/config"
)

func TestWatchConfigKeepsOldSnapshotOnInvalidFile(t *testing.T) {
	t.Setenv("WATCH_KEY", "secret")
	path := filepath.Join(t.TempDir(), "airoute.yaml")
	valid := `version: 1
providers:
  - id: p
    protocol: openai-chat
    base_url: https://example.com/v1
    api_key: ${WATCH_KEY}
    models: [m]
default_route:
  targets: [{provider: p, model: m}]
logging: {level: info}
`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(c)
	oldHash := c.Hash
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchConfigEvery(ctx, path, store, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Millisecond)
	if err = os.WriteFile(path, []byte("version: broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return store.LastError() != "" })
	if store.Get().Hash != oldHash {
		t.Fatal("invalid file replaced active snapshot")
	}
	next := valid + "metrics: {enabled: true, path: /metrics}\n"
	if err = os.WriteFile(path, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return store.Get().Hash != oldHash })
	if store.LastError() != "" {
		t.Fatalf("load error not cleared: %s", store.LastError())
	}
}

func TestInitConfigCreatesSecureValidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "airoute.yaml")
	if err := initConfig([]string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	if _, err = config.Load(path); err != nil {
		t.Fatalf("generated configuration is invalid: %v", err)
	}
	if err = initConfig([]string{"--config", path}); err == nil {
		t.Fatal("expected existing configuration to be preserved")
	}
}

func TestProviderTokenPrintsOnlySelectedCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "airoute.yaml")
	raw := `version: 1
providers:
  - id: native
    protocol: openai-responses
    codex_integration: direct
    base_url: https://example.com/v1
    api_key: upstream-secret
    models: [gpt-native]
routes: []
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	runErr := providerToken([]string{"--config", path, "--provider", "native"})
	_ = writer.Close()
	os.Stdout = original
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if runErr != nil || readErr != nil || strings.TrimSpace(string(output)) != "upstream-secret" {
		t.Fatalf("provider-token output=%q runErr=%v readErr=%v", output, runErr, readErr)
	}
}
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
