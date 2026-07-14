package codex

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

	"github.com/zbss/airoute/internal/application"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPreviewApplyBackupDeleteAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("model = \"old-model\"\nmodel_provider = \"old-provider\"\nsandbox_mode = \"workspace-write\"\n\n[mcp_servers.local]\ncommand = \"keep-me\"\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ConfigPath = path
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:12666", APIKey: "local-key", Model: "mimo-v2.5", Models: []string{"mimo-v2.5", "coding-model"}})
	preview, err := a.Preview(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	var next string
	if err = json.Unmarshal(preview.Content, &next); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, `wire_api = "responses"`) || !strings.Contains(next, `model_catalog_json = `) || !strings.Contains(next, `model_context_window = 1048576`) || !strings.Contains(next, `command = "keep-me"`) || !strings.Contains(preview.Diff, "experimental_bearer_token") {
		t.Fatalf("unexpected preview:\n%s\n%s", next, preview.Diff)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(unchanged, original) {
		t.Fatal("preview modified the Codex configuration")
	}
	first, err := a.Apply(context.Background(), desired)
	if err != nil || first.Backup == "" {
		t.Fatalf("apply=%#v err=%v", first, err)
	}
	catalogPath := filepath.Join(filepath.Dir(path), catalogName)
	catalogRaw, err := os.ReadFile(catalogPath)
	if err != nil || !bytes.Contains(catalogRaw, []byte(`"slug": "mimo-v2.5"`)) || !bytes.Contains(catalogRaw, []byte(`"slug": "coding-model"`)) {
		t.Fatalf("catalog=%s err=%v", catalogRaw, err)
	}
	desired2, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:12666", APIKey: "local-key", Model: "fast"})
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
	if _, err = a.Rollback(context.Background(), first.Backup); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(restored, original) {
		t.Fatalf("rollback did not restore original config:\n%s", restored)
	}
	if _, err = os.Stat(catalogPath); !os.IsNotExist(err) {
		t.Fatalf("rollback should remove catalog that did not originally exist: %v", err)
	}
}

func TestReadAndVerifyResponsesEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	raw := []byte("model = \"mimo\"\nmodel_provider = \"airoute\"\n\n[model_providers.airoute]\nbase_url = \"http://router/v1\"\nexperimental_bearer_token = \"secret\"\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ConfigPath = path
	a.LookPath = func(string) (string, error) { return "/bin/echo", nil }
	a.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://router/v1/responses" || request.Header.Get("authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %#v", request.URL, request.Header)
		}
		requestBody, _ := io.ReadAll(request.Body)
		if !bytes.Contains(requestBody, []byte(`"stream":true`)) {
			t.Fatalf("verification must exercise the streaming path: %s", requestBody)
		}
		stream := "event: response.output_item.added\ndata: {}\n\nevent: response.output_text.delta\ndata: {}\n\nevent: response.completed\ndata: {}\n\n"
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(stream)), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}, nil
	})}
	state, err := a.Read(context.Background())
	if err != nil || !state.Synced || state.Managed["api_key"] != "secret" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://router", APIKey: "secret", Model: "mimo"})
	result, err := a.Verify(context.Background(), application.VerifyOptions{Config: desired})
	if err != nil || !result.OK || len(result.Stages) != 2 {
		t.Fatalf("verify=%#v err=%v", result, err)
	}
}

func TestFirstApplyDoesNotReturnDotAsBackupName(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "config.toml")
	a := New()
	a.ConfigPath = path
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:12666", APIKey: "local-key", Model: "mimo-v2.5-pro"})
	result, err := a.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if result.Backup != "" {
		t.Fatalf("first apply should not report a backup, got %q", result.Backup)
	}
}

func TestDetectRequiresWorkingExecutable(t *testing.T) {
	a := New()
	a.LookPath = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "missing-codex"), nil
	}

	detection, err := a.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if detection.Installed || detection.Executable == "" {
		t.Fatalf("broken executable must not be reported as installed: %#v", detection)
	}
	if detection.Version != "" || strings.Contains(detection.Message, "spawn") {
		t.Fatalf("process errors must not leak into the version or UI message: %#v", detection)
	}
}

func TestDesktopAndCLIHaveDistinctDetection(t *testing.T) {
	desktop := NewDesktop()
	desktop.DesktopExecutables = []string{"/bin/echo"}
	detection, err := desktop.Detect(context.Background())
	if err != nil || !detection.Installed {
		t.Fatalf("desktop detection=%#v err=%v", detection, err)
	}
	if manifest := desktop.Manifest(); manifest.ID != "chatgpt-app" || manifest.Name != "ChatGPT App" {
		t.Fatalf("unexpected desktop manifest: %#v", manifest)
	}

	cli := New()
	cli.LookPath = func(string) (string, error) {
		return "/Applications/ChatGPT.app/Contents/Resources/codex", nil
	}
	detection, err = cli.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if detection.Installed || detection.Executable != "" || detection.Message != "未检测到 Codex CLI 命令" {
		t.Fatalf("desktop bundle must not count as a CLI installation: %#v", detection)
	}
}

func TestApplyRawAndCleanupValidateTOMLAndPreserveOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw := "model = \"old\"\nmodel_provider = \"airoute\"\nsandbox_mode = \"workspace-write\"\n\n[model_providers.airoute]\nbase_url = \"http://old/v1\"\nexperimental_bearer_token = \"secret\"\n\n[mcp_servers.keep]\ncommand = \"keep\"\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ConfigPath = path
	if _, err := a.ApplyRaw(context.Background(), application.RawConfig{Content: raw + "personality = \"friendly\"\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyRaw(context.Background(), application.RawConfig{Content: "broken = ["}); err == nil {
		t.Fatal("invalid TOML was accepted")
	}
	if _, err := a.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(path)
	if !bytes.Contains(written, []byte(`sandbox_mode = "workspace-write"`)) || !bytes.Contains(written, []byte(`command = "keep"`)) || bytes.Contains(written, []byte("model_provider")) || bytes.Contains(written, []byte("model_providers.airoute")) {
		t.Fatalf("unexpected cleaned config:\n%s", written)
	}
}
