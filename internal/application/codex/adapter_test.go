package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
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
	original := []byte("model = \"old-model\"\nmodel_provider = \"old-provider\"\nsandbox_mode = \"workspace-write\"\n\n[mcp_servers.local]\ncommand = \"keep-me\"\n\n[features]\ncodex_hooks = true\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ConfigPath = path
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	a.DesktopExecutables = []string{filepath.Join(t.TempDir(), "missing-desktop-codex")}
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:12666", APIKey: "local-key", Model: "mimo-v2.5", Models: []string{"mimo-v2.5", "coding-model"}})
	preview, err := a.Preview(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	var next string
	if err = json.Unmarshal(preview.Content, &next); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, `wire_api = "responses"`) || !strings.Contains(next, `model_catalog_json = `) || !strings.Contains(next, `model_context_window = 1048576`) || !strings.Contains(next, `command = "keep-me"`) || strings.Contains(next, "[mcp_servers.codex_apps]") || !strings.Contains(next, "[features]\nhooks = true") || strings.Contains(next, "codex_hooks") || !strings.Contains(preview.Diff, "experimental_bearer_token") {
		t.Fatalf("unexpected preview:\n%s\n%s", next, preview.Diff)
	}
	if strings.Count(next, managedBlockStart) != 2 || strings.Count(next, managedBlockEnd) != 2 {
		t.Fatalf("managed configuration ownership markers are missing:\n%s", next)
	}
	var previewDocument map[string]any
	if err = toml.Unmarshal([]byte(next), &previewDocument); err != nil {
		t.Fatalf("preview is invalid TOML: %v\n%s", err, next)
	}
	providers, _ := previewDocument["model_providers"].(map[string]any)
	airoute, _ := providers["airoute"].(map[string]any)
	if previewDocument["sandbox_mode"] != "workspace-write" || airoute["wire_api"] != "responses" {
		t.Fatalf("root settings or managed provider moved to the wrong TOML scope: %#v", previewDocument)
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

func TestMergeCodexRuntimeSettingsIsIdempotentAndPreservesExplicitValues(t *testing.T) {
	current := []byte(`[mcp_servers.codex_apps]
command = "/usr/local/bin/codex-apps"
startup_timeout_sec = 75

[features]
hooks = false
codex_hooks = true
`)
	desired := DesiredConfig{BaseURL: "http://127.0.0.1:12666/v1", APIKey: "local-key", Model: "gpt-test"}
	first := merge(current, desired, "/tmp/models.json")
	second := merge(first, desired, "/tmp/models.json")
	if !bytes.Equal(first, second) {
		t.Fatalf("Codex runtime normalization is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if bytes.Count(first, []byte("[mcp_servers.codex_apps]")) != 1 || !bytes.Contains(first, []byte(`command = "/usr/local/bin/codex-apps"`)) || !bytes.Contains(first, []byte("startup_timeout_sec = 75")) || bytes.Contains(first, []byte("startup_timeout_sec = 120")) {
		t.Fatalf("explicit codex_apps timeout was not preserved:\n%s", first)
	}
	if bytes.Count(first, []byte("hooks = false")) != 1 || bytes.Contains(first, []byte("codex_hooks")) {
		t.Fatalf("stable hooks setting was not preserved:\n%s", first)
	}
}

func TestMergeRemovesGeneratedOrphanCodexAppsSection(t *testing.T) {
	current := []byte(`[mcp_servers.codex_apps]
startup_timeout_sec = 120

[features]
codex_hooks = true
`)
	desired := DesiredConfig{BaseURL: "http://127.0.0.1:12666/v1", APIKey: "local-key", Model: "gpt-test"}
	result := merge(current, desired, "/tmp/models.json")
	if bytes.Contains(result, []byte("[mcp_servers.codex_apps]")) || bytes.Contains(result, []byte("startup_timeout_sec = 120")) {
		t.Fatalf("generated orphan codex_apps section was not removed:\n%s", result)
	}
	if !bytes.Contains(result, []byte("[features]\nhooks = true")) || bytes.Contains(result, []byte("codex_hooks")) {
		t.Fatalf("hooks migration was not preserved:\n%s", result)
	}
}

func TestApplyRawRejectsMCPServerWithoutTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "config.toml")
	a := New()
	a.ConfigPath = path
	result, err := a.ApplyRaw(context.Background(), application.RawConfig{Content: `[mcp_servers.codex_apps]
startup_timeout_sec = 120
`})
	if err == nil || !strings.Contains(err.Error(), "必须设置 command 或 url") {
		t.Fatalf("invalid MCP transport was accepted: result=%#v err=%v", result, err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("invalid config was written: %v", statErr)
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

func TestOfficialDirectUsesCommandAuthWithoutCopyingProviderSecret(t *testing.T) {
	root := t.TempDir()
	codexPath := filepath.Join(root, ".codex", "config.toml")
	routerPath := filepath.Join(root, "airoute.yaml")
	routerConfig := `version: 1
server:
  listen: 127.0.0.1:12666
  admin_listen: 127.0.0.1:12667
providers:
  - id: native
    name: Native Responses
    protocol: openai-responses
    codex_integration: direct
    base_url: https://provider.example/v1
    api_key: upstream-secret
    models: [gpt-native]
routes: []
conversion:
  unsupported_fields: warn
logging:
  level: info
metrics:
  path: /metrics
`
	if err := os.WriteFile(routerPath, []byte(routerConfig), 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ConfigPath = codexPath
	a.RouterConfigPath = routerPath
	a.AirExecutable = "/usr/local/bin/air"
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	a.DesktopExecutables = []string{filepath.Join(root, "missing-codex")}
	desired, _ := json.Marshal(DesiredConfig{
		IntegrationMode: "direct", ProviderID: "native", ProviderName: "Native Responses",
		ProviderBaseURL: "https://provider.example/v1", ProviderModel: "gpt-native",
	})
	preview, err := a.Preview(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	var generated string
	if err = json.Unmarshal(preview.Content, &generated); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`model = "gpt-native"`,
		`model_provider = "airoute-direct"`,
		`[model_providers.airoute-direct]`,
		`[model_providers.airoute-direct.auth]`,
		`command = "/usr/local/bin/air"`,
		`"provider-token"`,
		`"native"`,
	} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("direct config is missing %q:\n%s", expected, generated)
		}
	}
	if strings.Contains(generated, "upstream-secret") || strings.Contains(generated, "experimental_bearer_token") {
		t.Fatalf("direct config copied the upstream secret:\n%s", generated)
	}

	a.LookPath = func(string) (string, error) { return "/bin/echo", nil }
	a.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://provider.example/v1/responses" || request.Header.Get("authorization") != "Bearer upstream-secret" {
			t.Fatalf("unexpected direct request: %s %#v", request.URL, request.Header)
		}
		stream := "event: response.output_item.added\ndata: {}\n\nevent: response.output_text.delta\ndata: {}\n\nevent: response.completed\ndata: {}\n\n"
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(stream)), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}, nil
	})}
	result, err := a.Verify(context.Background(), application.VerifyOptions{Config: desired})
	if err != nil || !result.OK || result.Stages[1].ID != "provider" {
		t.Fatalf("direct verify=%#v err=%v", result, err)
	}
}

func TestFirstApplyDoesNotReturnDotAsBackupName(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "config.toml")
	a := New()
	a.ConfigPath = path
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	a.DesktopExecutables = []string{filepath.Join(t.TempDir(), "missing-desktop-codex")}
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
	a.DesktopExecutables = []string{filepath.Join(t.TempDir(), "missing-desktop-codex")}

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

func TestCodexCombinesDesktopAndCLIDetection(t *testing.T) {
	combined := New()
	combined.LookPath = func(string) (string, error) { return "/bin/echo", nil }
	combined.DesktopExecutables = []string{"/bin/echo"}
	detection, err := combined.Detect(context.Background())
	if err != nil || !detection.Installed || detection.Message != "Codex CLI 与 ChatGPT App 已安装，共享同一配置" {
		t.Fatalf("combined detection=%#v err=%v", detection, err)
	}
	if manifest := combined.Manifest(); manifest.ID != "codex" || manifest.Name != "Codex CLI / ChatGPT App" {
		t.Fatalf("unexpected combined manifest: %#v", manifest)
	}

	desktopOnly := New()
	desktopOnly.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	desktopOnly.DesktopExecutables = []string{"/bin/echo"}
	detection, err = desktopOnly.Detect(context.Background())
	if err != nil || !detection.Installed || detection.Message != "ChatGPT App 已安装；Codex CLI 未检测到，仍使用同一配置入口" {
		t.Fatalf("desktop-only detection=%#v err=%v", detection, err)
	}
}

func TestLooksLikeChatGPTWindowsPackage(t *testing.T) {
	for _, name := range []string{
		"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0",
		"OpenAI.ChatGPT_abcdefghijk",
		"ChatGPT_abcdefghijk",
		"OpenAI.Codex_2p2nqsd0c76g0",
	} {
		if !looksLikeChatGPTWindowsPackage(name) {
			t.Fatalf("ChatGPT package %q was not recognized", name)
		}
	}
	for _, name := range []string{"OpenAI.Codex_abcdefghijk", "Other.Codex_2p2nqsd0c76g0", "Microsoft.WindowsTerminal_8wekyb3d8bbwe", "ChatApp_abcdefghijk"} {
		if looksLikeChatGPTWindowsPackage(name) {
			t.Fatalf("unrelated package %q was recognized as ChatGPT", name)
		}
	}
}

func TestLooksLikeOfficialChatGPTWindowsAppID(t *testing.T) {
	for _, appID := range []string{
		"OpenAI.Codex_2p2nqsd0c76g0!App",
		"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0!App",
	} {
		if !looksLikeOfficialChatGPTWindowsAppID(appID) {
			t.Fatalf("official ChatGPT AppID %q was not recognized", appID)
		}
	}
	for _, appID := range []string{
		"Other.Codex_2p2nqsd0c76g0!App",
		"OpenAI.Codex_otherpublisher!App",
		"OpenAI.ChatGPT-Desktop_otherpublisher!BackgroundTask",
	} {
		if looksLikeOfficialChatGPTWindowsAppID(appID) {
			t.Fatalf("unrelated AppID %q was recognized as ChatGPT", appID)
		}
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
