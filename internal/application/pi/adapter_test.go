package pi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/safefile"
)

func testAdapter(t *testing.T) *Adapter {
	t.Helper()
	return &Adapter{
		AgentDir:   t.TempDir(),
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		LookPath:   func(string) (string, error) { return "", errors.New("missing") },
	}
}

func testConfig(baseURL, protocol, model string) json.RawMessage {
	raw, _ := json.Marshal(DesiredConfig{
		BaseURL: baseURL, APIKey: "sk-test-secret", Model: model,
		Models: []string{model, "second-model"}, WireProtocol: protocol,
	})
	return raw
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = safefile.AtomicWrite(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{}
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func pathsForTest(t *testing.T, a *Adapter) (string, string) {
	t.Helper()
	models, settings, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	return models, settings
}

func TestApplyPreservesDocumentsAndRegistersPiProvider(t *testing.T) {
	a := testAdapter(t)
	modelsPath, settingsPath := pathsForTest(t, a)
	writeJSON(t, modelsPath, map[string]any{
		"providers":  map[string]any{"other": map[string]any{"api": "openai-completions", "apiKey": "other-key"}},
		"editorOnly": true,
	})
	writeJSON(t, settingsPath, map[string]any{"theme": "dark", "defaultProvider": "other", "defaultModel": "other-model"})

	result, err := a.Apply(context.Background(), testConfig("https://router.example", "openai-chat", "coding-model"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Backup == "" {
		t.Fatalf("apply result = %#v", result)
	}
	models := readJSON(t, modelsPath)
	providers := models["providers"].(map[string]any)
	if _, ok := providers["other"]; !ok || models["editorOnly"] != true {
		t.Fatalf("unmanaged models document fields were lost: %#v", models)
	}
	provider := providers[providerID].(map[string]any)
	if provider["api"] != "openai-completions" || provider["baseUrl"] != "https://router.example/v1" || provider["apiKey"] != "sk-test-secret" {
		t.Fatalf("Pi provider = %#v", provider)
	}
	modelList := provider["models"].([]any)
	if len(modelList) != 2 || modelList[0].(map[string]any)["id"] != "coding-model" {
		t.Fatalf("Pi provider models = %#v", modelList)
	}
	settings := readJSON(t, settingsPath)
	if settings["theme"] != "dark" || settings["defaultProvider"] != providerID || settings["defaultModel"] != "coding-model" {
		t.Fatalf("Pi settings = %#v", settings)
	}

	state, err := a.Read(context.Background())
	if err != nil || !state.Synced || state.Managed["api_key"] != "sk-test-secret" || state.Managed["wire_protocol"] != "openai-chat" {
		t.Fatalf("state = %#v, error = %v", state, err)
	}
	synced, err := a.ConfigurationSynced(context.Background())
	if err != nil || !synced {
		t.Fatalf("configuration synced = %v, %v", synced, err)
	}

	previousModels, _ := os.ReadFile(modelsPath)
	if _, err = a.Apply(context.Background(), testConfig("https://router.example", "openai-chat", "coding-model")); err != nil {
		t.Fatal(err)
	}
	currentModels, _ := os.ReadFile(modelsPath)
	if string(previousModels) != string(currentModels) {
		t.Fatal("same Pi configuration was not idempotent")
	}
}

func TestPreviewBundlesBothPiFiles(t *testing.T) {
	a := testAdapter(t)
	preview, err := a.Preview(context.Background(), testConfig("http://127.0.0.1:12666", "anthropic-messages", "claude-route"))
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]json.RawMessage
	if err = json.Unmarshal(preview.Content, &content); err != nil {
		t.Fatal(err)
	}
	if content[modelsFileName] == nil || content[settingsFileName] == nil || !strings.Contains(preview.Diff, modelsFileName) || !strings.Contains(preview.Diff, settingsFileName) {
		t.Fatalf("preview = %#v", preview)
	}
}

func TestJSONCSafetyAndInvalidDocumentsDoNotWrite(t *testing.T) {
	a := testAdapter(t)
	modelsPath, _ := pathsForTest(t, a)
	jsonc := []byte("{\n  // Pi accepts this\n  \"providers\": {},\n}\n")
	if err := safefile.AtomicWrite(modelsPath, jsonc, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Preview(context.Background(), testConfig("https://router.example", "openai-chat", "model")); err == nil || !strings.Contains(err.Error(), "JSONC") {
		t.Fatalf("JSONC preview error = %v", err)
	}
	after, _ := os.ReadFile(modelsPath)
	if string(after) != string(jsonc) {
		t.Fatal("JSONC document was changed despite rejected automatic write")
	}

	invalid := []byte("{ invalid")
	if err := safefile.AtomicWrite(modelsPath, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(context.Background(), testConfig("https://router.example", "openai-chat", "model")); err == nil {
		t.Fatal("invalid models document was accepted")
	}
	after, _ = os.ReadFile(modelsPath)
	if string(after) != string(invalid) {
		t.Fatal("invalid models document was changed")
	}
}

func TestApplyRawAcceptsOnlyStandardTwoFileBundle(t *testing.T) {
	a := testAdapter(t)
	modelsPath, settingsPath := pathsForTest(t, a)
	content := `{
  "models.json": {"providers": {"airoute": {"api": "openai-completions"}}},
  "settings.json": {"defaultProvider": "airoute", "defaultModel": "manual"}
}`
	if _, err := a.ApplyRaw(context.Background(), application.RawConfig{Content: content}); err != nil {
		t.Fatal(err)
	}
	if readJSON(t, modelsPath)["providers"] == nil || readJSON(t, settingsPath)["defaultModel"] != "manual" {
		t.Fatal("manual Pi bundle was not written")
	}
	if _, err := a.ApplyRaw(context.Background(), application.RawConfig{Content: `{"models.json": {}}`}); err == nil {
		t.Fatal("partial manual Pi bundle was accepted")
	}
}

func TestSecondWriteFailureRestoresModels(t *testing.T) {
	a := testAdapter(t)
	modelsPath, settingsPath := pathsForTest(t, a)
	writeJSON(t, modelsPath, map[string]any{"providers": map[string]any{"other": map[string]any{"api": "x"}}})
	writeJSON(t, settingsPath, map[string]any{"defaultProvider": "other", "defaultModel": "old"})
	beforeModels, _ := os.ReadFile(modelsPath)
	beforeSettings, _ := os.ReadFile(settingsPath)
	failOnce := true
	a.WriteFile = func(path string, content []byte) error {
		if path == settingsPath && failOnce {
			failOnce = false
			return errors.New("simulated settings write failure")
		}
		return safefile.AtomicWrite(path, content, 0600)
	}
	if _, err := a.Apply(context.Background(), testConfig("https://router.example", "openai-chat", "model")); err == nil {
		t.Fatal("second write failure was not returned")
	}
	afterModels, _ := os.ReadFile(modelsPath)
	afterSettings, _ := os.ReadFile(settingsPath)
	if string(afterModels) != string(beforeModels) || string(afterSettings) != string(beforeSettings) {
		t.Fatalf("transaction was not compensated: models=%s settings=%s", afterModels, afterSettings)
	}
}

func TestCleanupOnlyRemovesPiProviderAndMatchingDefaults(t *testing.T) {
	a := testAdapter(t)
	modelsPath, settingsPath := pathsForTest(t, a)
	writeJSON(t, modelsPath, map[string]any{"providers": map[string]any{"other": map[string]any{"api": "x"}, providerID: map[string]any{"api": "openai-completions"}}})
	writeJSON(t, settingsPath, map[string]any{"defaultProvider": providerID, "defaultModel": "route", "theme": "dark"})
	if _, err := a.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	providers := readJSON(t, modelsPath)["providers"].(map[string]any)
	if _, ok := providers[providerID]; ok || providers["other"] == nil {
		t.Fatalf("cleanup providers = %#v", providers)
	}
	settings := readJSON(t, settingsPath)
	if settings["defaultProvider"] != nil || settings["defaultModel"] != nil || settings["theme"] != "dark" {
		t.Fatalf("cleanup settings = %#v", settings)
	}

	writeJSON(t, modelsPath, map[string]any{"providers": map[string]any{providerID: map[string]any{"api": "openai-completions"}}})
	writeJSON(t, settingsPath, map[string]any{"defaultProvider": "other", "defaultModel": "other-model"})
	if _, err := a.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	settings = readJSON(t, settingsPath)
	if settings["defaultProvider"] != "other" || settings["defaultModel"] != "other-model" {
		t.Fatalf("cleanup touched another provider defaults: %#v", settings)
	}
}

func TestBundleRollbackAndBackupDeletion(t *testing.T) {
	a := testAdapter(t)
	modelsPath, settingsPath := pathsForTest(t, a)
	writeJSON(t, modelsPath, map[string]any{"providers": map[string]any{"other": map[string]any{"api": "x"}}})
	writeJSON(t, settingsPath, map[string]any{"defaultProvider": "other", "defaultModel": "original"})
	if _, err := a.Apply(context.Background(), testConfig("https://router.example", "openai-chat", "first")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(context.Background(), testConfig("https://router.example", "openai-responses", "second")); err != nil {
		t.Fatal(err)
	}
	backups, err := a.Backups(context.Background())
	if err != nil || len(backups) < 2 {
		t.Fatalf("backups = %#v, %v", backups, err)
	}
	if _, err = a.Rollback(context.Background(), backups[0].Name); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, settingsPath)
	if settings["defaultModel"] != "first" || readJSON(t, modelsPath)["providers"].(map[string]any)[providerID].(map[string]any)["api"] != "openai-completions" {
		t.Fatal("bundle rollback did not restore both Pi files")
	}
	backups, err = a.Backups(context.Background())
	if err != nil || len(backups) == 0 {
		t.Fatalf("backups after rollback = %#v, %v", backups, err)
	}
	if err = a.DeleteBackup(context.Background(), backups[0].Name); err != nil {
		t.Fatal(err)
	}
	if err = a.DeleteBackup(context.Background(), "../not-a-backup"); !errors.Is(err, safefile.ErrInvalidBackupName) {
		t.Fatalf("invalid backup deletion error = %v", err)
	}
}

func TestVerifyUsesEachPiProtocolWithoutExposingSecret(t *testing.T) {
	cases := []struct {
		protocol string
		path     string
		header   string
	}{
		{"openai-chat", "/v1/chat/completions", "authorization"},
		{"openai-responses", "/v1/responses", "authorization"},
		{"anthropic-messages", "/v1/messages", "x-api-key"},
		{"gemini-generate-content", "/v1beta/models/gemini-route:streamGenerateContent", "x-goog-api-key"},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.Header.Get(tc.header) == "" {
					t.Errorf("request path=%q header %q=%q", r.URL.Path, tc.header, r.Header.Get(tc.header))
				}
				if tc.protocol == "anthropic-messages" && r.Header.Get("anthropic-version") == "" {
					t.Error("Anthropic version header is missing")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			a := testAdapter(t)
			a.LookPath = func(string) (string, error) { return "/definitely/not/pi", nil }
			model := "route"
			if tc.protocol == "gemini-generate-content" {
				model = "gemini-route"
			}
			result, err := a.Verify(context.Background(), application.VerifyOptions{Config: testConfig(server.URL, tc.protocol, model)})
			if err != nil || !result.OK || len(result.Stages) != 2 || !result.Stages[1].OK {
				t.Fatalf("verify = %#v, error=%v", result, err)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer server.Close()
	a := testAdapter(t)
	a.LookPath = func(string) (string, error) { return "/definitely/not/pi", nil }
	result, err := a.Verify(context.Background(), application.VerifyOptions{Config: testConfig(server.URL, "openai-chat", "route")})
	if err != nil || result.Stages[1].OK || strings.Contains(result.Stages[1].Detail, "sk-test-secret") {
		t.Fatalf("verification exposed secret or unexpectedly succeeded: %#v, %v", result, err)
	}
}

func TestAgentDirectoryEnvironmentAndPassiveDetection(t *testing.T) {
	t.Setenv("AIROUTE_PI_AGENT_DIR", "")
	configured := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", configured)
	a := New()
	modelsPath, _, err := a.paths()
	if err != nil || filepath.Dir(modelsPath) != configured {
		t.Fatalf("Pi directory = %q, error=%v", modelsPath, err)
	}
	a.LookPath = func(string) (string, error) { return "", errors.New("missing") }
	missing, err := a.Detect(context.Background())
	if err != nil || missing.Installed || !strings.Contains(missing.Message, "未检测到") {
		t.Fatalf("missing detection = %#v, %v", missing, err)
	}

	script := filepath.Join(t.TempDir(), "pi")
	if err = os.WriteFile(script, []byte("#!/bin/sh\ntouch \"$0.invoked\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIROUTE_PI_EXECUTABLE", script)
	detected, err := a.Detect(context.Background())
	if err != nil || !detected.Installed || detected.Executable != script || detected.Version != "" || detected.Message != "Pi Coding Agent 已检测到" {
		t.Fatalf("success detection = %#v, %v", detected, err)
	}
	if _, statErr := os.Stat(script + ".invoked"); !os.IsNotExist(statErr) {
		t.Fatalf("passive detection must not start Pi: stat error = %v", statErr)
	}
}
