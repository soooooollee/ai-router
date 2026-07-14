package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/application"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestApplyPreviewBackupAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{"Stop":[{"command":"keep-me"}]},"env":{"EXISTING":"yes"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.SettingsPath = path
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:8080", APIKey: "local-key", Model: "mimo", SonnetModel: "mimo"})
	preview, err := a.Preview(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(preview.Current, []byte("keep-me")) || !bytes.Contains(preview.Content, []byte("keep-me")) || !strings.Contains(preview.Diff, "ANTHROPIC_MODEL") {
		t.Fatalf("unexpected preview: %s\n%s", preview.Content, preview.Diff)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, []byte(`{"hooks":{"Stop":[{"command":"keep-me"}]},"env":{"EXISTING":"yes"}}`)) {
		t.Fatalf("preview modified the settings file: %s (%v)", unchanged, err)
	}
	first, err := a.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if first.Backup == "" {
		t.Fatal("missing backup")
	}
	desired2, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:8080", APIKey: "local-key", Model: "qwen3"})
	if _, err = a.Apply(context.Background(), desired2); err != nil {
		t.Fatal(err)
	}
	backups, err := a.Backups(context.Background())
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
	for _, backup := range backups {
		if backup.Name != first.Backup {
			if err = a.DeleteBackup(context.Background(), backup.Name); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	backups, err = a.Backups(context.Background())
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup delete failed: backups=%v err=%v", backups, err)
	}
	if _, err = a.Rollback(context.Background(), first.Backup); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(`"EXISTING":"yes"`)) && !bytes.Contains(raw, []byte(`"EXISTING": "yes"`)) {
		t.Fatalf("rollback did not restore original config: %s", raw)
	}
}

func TestManifestAndMissingOrMalformedConfiguration(t *testing.T) {
	a := New()
	a.SettingsPath = filepath.Join(t.TempDir(), ".claude", "settings.json")
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	manifest := a.Manifest()
	wantCapabilities := []application.Capability{
		application.CapabilityDetect,
		application.CapabilityConfigure,
		application.CapabilityPreview,
		application.CapabilityVerify,
		application.CapabilityRollback,
		application.CapabilityCleanup,
		application.CapabilityEdit,
	}
	if manifest.ID != "claude-code" || manifest.ConfigFormat != "json" || len(manifest.Capabilities) != len(wantCapabilities) {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	for i, capability := range wantCapabilities {
		if manifest.Capabilities[i] != capability {
			t.Fatalf("capability %d=%q want %q", i, manifest.Capabilities[i], capability)
		}
	}
	state, err := a.Read(context.Background())
	if err != nil || state.Exists || state.Synced {
		t.Fatalf("missing configuration state=%#v err=%v", state, err)
	}
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:8080", Model: "mimo"})
	preview, err := a.Preview(context.Background(), desired)
	if err != nil || preview.WillCreateBackup {
		t.Fatalf("missing-file preview=%#v err=%v", preview, err)
	}

	if err = os.MkdirAll(filepath.Dir(a.SettingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(a.SettingsPath, []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "有效 JSON") {
		t.Fatalf("malformed configuration was accepted: %v", err)
	}
}

func TestApplyRawAndCleanupPreserveUnmanagedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"theme":"dark","env":{"EXISTING":"yes","ANTHROPIC_BASE_URL":"http://old","ANTHROPIC_MODEL":"old"}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.SettingsPath = path
	if _, err := a.ApplyRaw(context.Background(), application.RawConfig{Content: `{"theme":"light","env":{"EXISTING":"yes","ANTHROPIC_MODEL":"edited"}}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyRaw(context.Background(), application.RawConfig{Content: `{"broken":`}); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
	if _, err := a.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(path)
	if !bytes.Contains(written, []byte(`"theme": "light"`)) || !bytes.Contains(written, []byte(`"EXISTING": "yes"`)) || bytes.Contains(written, []byte("ANTHROPIC_")) {
		t.Fatalf("cleanup removed unmanaged settings or kept managed settings: %s", written)
	}
}

func TestReadShowsLocalAPIKeyAndVerifyGateway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"env":{"ANTHROPIC_BASE_URL":"http://router","ANTHROPIC_API_KEY":"secret","ANTHROPIC_MODEL":"mimo"}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.SettingsPath = path
	a.LookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	a.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://router/v1/messages" || r.Header.Get("authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %#v", r.URL, r.Header)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"content":[{"type":"text","text":"AI_ROUTER_READY"}]}`)), Header: http.Header{}}, nil
	})}
	state, err := a.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(state)
	if !bytes.Contains(encoded, []byte("secret")) || state.Managed["ANTHROPIC_API_KEY"] != "secret" {
		t.Fatalf("local API key is not available to the configuration UI: %s", encoded)
	}
	result, err := a.Verify(context.Background(), application.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || len(result.Stages) != 3 || result.Stages[2].ID != "gateway" {
		t.Fatalf("unexpected verify result: %#v", result)
	}
}

func TestVerifyRejectsUnsyncedAndInsecureConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"env":{"ANTHROPIC_BASE_URL":"http://router","ANTHROPIC_API_KEY":"secret","ANTHROPIC_MODEL":"mimo"}}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.SettingsPath = path
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	result, err := a.Verify(context.Background(), application.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || len(result.Stages) < 2 || !strings.Contains(result.Stages[1].Message, "权限") {
		t.Fatalf("insecure configuration was accepted: %#v", result)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	different, _ := json.Marshal(DesiredConfig{BaseURL: "http://router", Model: "qwen3"})
	result, err = a.Verify(context.Background(), application.VerifyOptions{Config: different})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || len(result.Stages) < 2 || !strings.Contains(result.Stages[1].Message, "尚未写入") {
		t.Fatalf("unsynced configuration was accepted: %#v", result)
	}
}

func TestCLISmokeUsesTimeoutOutputCapAndSecretRedaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	commandPath := filepath.Join(dir, "claude")
	raw := `{"env":{"ANTHROPIC_BASE_URL":"http://router","ANTHROPIC_API_KEY":"secret-value","ANTHROPIC_MODEL":"mimo"}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo test-version; exit 0; fi\nprintf '%s' \"$ANTHROPIC_API_KEY\"\nyes x | head -c 70000\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.SettingsPath = path
	a.LookPath = func(string) (string, error) { return commandPath, nil }
	a.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}}, nil
	})}
	result, err := a.Verify(context.Background(), application.VerifyOptions{RunCLI: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || len(result.Stages) != 4 || strings.Contains(result.Stages[3].Detail, "secret-value") || len(result.Stages[3].Detail) > 515 {
		t.Fatalf("CLI output was not safely handled: %#v", result)
	}

	if err = os.WriteFile(commandPath, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo test-version; exit 0; fi\nwhile :; do :; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a.CLITimeout = 20 * time.Millisecond
	result, err = a.Verify(context.Background(), application.VerifyOptions{RunCLI: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || len(result.Stages) != 4 || result.Stages[3].OK {
		t.Fatalf("CLI timeout was not reported: %#v", result)
	}
}

func TestCLISmokeHonorsCallerCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	commandPath := filepath.Join(dir, "claude")
	raw := `{"env":{"ANTHROPIC_BASE_URL":"http://router","ANTHROPIC_API_KEY":"secret-value","ANTHROPIC_MODEL":"mimo"}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo test-version; exit 0; fi\nwhile :; do :; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.SettingsPath = path
	a.LookPath = func(string) (string, error) { return commandPath, nil }
	a.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}}, nil
	})}
	a.CLITimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Millisecond, cancel)
	started := time.Now()
	result, err := a.Verify(ctx, application.VerifyOptions{RunCLI: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || len(result.Stages) != 4 || result.Stages[3].OK || time.Since(started) > time.Second {
		t.Fatalf("caller cancellation was not propagated promptly: %#v", result)
	}
}
