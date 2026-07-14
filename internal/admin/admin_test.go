package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/anthropic"
	"github.com/zbss/airoute/internal/protocol/gemini"
	"github.com/zbss/airoute/internal/protocol/ir"
	"github.com/zbss/airoute/internal/protocol/openaichat"
	"github.com/zbss/airoute/internal/protocol/openairesponses"
)

func TestSummaryIncludesCurrentProcessFirstTokenAverage(t *testing.T) {
	metrics := &observe.Metrics{}
	metrics.FirstTokenMSTotal.Add(360)
	metrics.FirstTokenBuckets[len(metrics.FirstTokenBuckets)-1].Add(2)
	logs := observe.NewStore(2)
	logs.Add(observe.Record{ID: "previous-process", DurationMS: 900})
	result := summary(metrics, logs)
	if result["average_first_token_ms"] != uint64(180) {
		t.Fatalf("average_first_token_ms = %#v", result["average_first_token_ms"])
	}
	if result["p95_latency_ms"] != int64(0) {
		t.Fatalf("previous-process latency leaked into summary: %#v", result)
	}
	logs.Add(observe.Record{ID: "current-process", DurationMS: 120})
	metrics.Completed.Add(1)
	result = summary(metrics, logs)
	if result["p95_latency_ms"] != int64(120) {
		t.Fatalf("p95_latency_ms = %#v", result["p95_latency_ms"])
	}
}

func TestAdminConfigSaveAndSecurity(t *testing.T) {
	t.Setenv("TEST_ADMIN_TOKEN", "12345678901234567890123456789012")
	t.Setenv("TEST_PROVIDER_KEY", "provider-secret")
	dir := t.TempDir()
	path := filepath.Join(dir, "airoute.yaml")
	raw := `version: 1
server:
  admin_listen: 127.0.0.1:8081
admin:
  enabled: true
  token: ${TEST_ADMIN_TOKEN}
providers:
  - id: p
    protocol: openai-chat
    base_url: https://example.com/v1
    api_key: ${TEST_PROVIDER_KEY}
    models: [m]
default_route:
  targets: [{provider: p, model: m}]
logging:
  level: info
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "http://127.0.0.1:8080")
	ts := httptest.NewServer(s)
	defer ts.Close()
	request := func(method, path string, body []byte, origin string) *http.Response {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
		if origin != "" {
			req.Header.Set("origin", origin)
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		return resp
	}
	resp := request("GET", "/api/status", nil, "")
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	resp.Body.Close()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	resp = request("GET", "/api/config", nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("insecure config permissions should block raw read, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	resp = request("GET", "/api/config", nil, "")
	configBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(configBody, []byte("provider-secret")) {
		t.Fatal("resolved provider secret leaked from config API")
	}
	resp = request("GET", "/api/providers", nil, "")
	providerBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(providerBody, []byte(`"api_key":"provider-secret"`)) {
		t.Fatalf("provider API key was not returned to the local management UI: %s", providerBody)
	}
	resp = request("GET", "/api/diagnostics", nil, "")
	diagnosticBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(diagnosticBody, []byte(`"manifest"`)) || bytes.Contains(diagnosticBody, []byte("provider-secret")) || bytes.Contains(diagnosticBody, []byte("12345678901234567890123456789012")) {
		t.Fatalf("diagnostic bundle missing manifest or leaked a secret: %s", diagnosticBody)
	}
	resp = request("GET", "/api/status", nil, "https://evil.example")
	if resp.StatusCode != 401 {
		t.Fatalf("expected origin rejection, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	hostReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
	hostReq.Host = "evil.example"
	hostReq.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
	resp, err = http.DefaultClient.Do(hostReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected Host rejection, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	next := bytes.ReplaceAll([]byte(raw), []byte("level: info"), []byte("level: debug"))
	payload, _ := json.Marshal(map[string]any{"yaml": string(next), "expected_hash": c.Hash})
	resp = request("PUT", "/api/config", payload, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("save failed %d: %s", resp.StatusCode, body)
	}
	var saveResult struct {
		HotReloaded []string `json:"hot_reloaded"`
	}
	if err := json.Unmarshal(body, &saveResult); err != nil || !slices.Contains(saveResult.HotReloaded, "logging.level") {
		t.Fatalf("save response did not explain hot reload semantics: %s (%v)", body, err)
	}
	files, _ := filepath.Glob(path + ".bak.*")
	if len(files) != 1 {
		t.Fatalf("expected backup, got %v", files)
	}
	if info, statErr := os.Stat(files[0]); statErr != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup permission is not 0600: %v (%v)", info, statErr)
	}
	saved, _ := os.ReadFile(path)
	if !bytes.Contains(saved, []byte("level: debug")) {
		t.Fatal("new config not saved")
	}
	rollbackPayload, _ := json.Marshal(map[string]any{"name": filepath.Base(files[0])})
	resp = request("POST", "/api/config/rollback", rollbackPayload, "")
	rollbackBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("rollback failed: %s", rollbackBody)
	}
	var rollbackResult struct {
		HotReloaded []string `json:"hot_reloaded"`
	}
	if err = json.Unmarshal(rollbackBody, &rollbackResult); err != nil || !slices.Contains(rollbackResult.HotReloaded, "logging.level") {
		t.Fatalf("rollback response did not explain hot reload semantics: %s (%v)", rollbackBody, err)
	}
	rolledBack, _ := os.ReadFile(path)
	if !bytes.Contains(rolledBack, []byte("level: info")) {
		t.Fatal("backup was not restored")
	}
	resp, _ = http.Get(ts.URL + "/")
	if resp.StatusCode != 200 {
		t.Fatalf("static UI unavailable: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRedactLogBodyForWebDisplay(t *testing.T) {
	raw := `{"api_key":"visible-on-disk","messages":[{"role":"user","content":"hello"}],"nested":{"access_token":"token-value","input_tokens":42}}`
	redacted := redactLogBody(raw)
	if strings.Contains(redacted, "visible-on-disk") || strings.Contains(redacted, "token-value") || !strings.Contains(redacted, "hello") || !strings.Contains(redacted, "••••••••") || !strings.Contains(redacted, `"input_tokens":42`) {
		t.Fatalf("unexpected web redaction: %s", redacted)
	}
}

func TestWebRedactionCoversProvidersAndPreservesSecretsOnSave(t *testing.T) {
	t.Setenv("WEB_REDACTION_ADMIN_TOKEN", "12345678901234567890123456789012")
	dir := t.TempDir()
	path := filepath.Join(dir, "airoute.yaml")
	raw := `version: 1
server:
  admin_listen: 127.0.0.1:8081
admin:
  enabled: true
  token: ${WEB_REDACTION_ADMIN_TOKEN}
providers:
  - id: private-provider
    name: Private Provider
    protocol: openai-chat
    base_url: https://example.com/v1
    api_key: plaintext-provider-secret
    models: [model]
default_route:
  targets: [{provider: private-provider, model: model}]
logging:
  level: info
  web_redaction: true
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(config.NewStore(current), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "http://127.0.0.1:8080"))
	defer server.Close()

	get := func(endpoint string) []byte {
		request, _ := http.NewRequest(http.MethodGet, server.URL+endpoint, nil)
		request.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d %s", endpoint, response.StatusCode, body)
		}
		return body
	}
	providerBody := get("/api/providers")
	if bytes.Contains(providerBody, []byte("plaintext-provider-secret")) || !bytes.Contains(providerBody, []byte(webRedactionMask)) {
		t.Fatalf("provider key was not redacted: %s", providerBody)
	}
	configBody := get("/api/config")
	if bytes.Contains(configBody, []byte("plaintext-provider-secret")) || !bytes.Contains(configBody, []byte(webRedactionMask)) {
		t.Fatalf("complete config was not redacted: %s", configBody)
	}
	var response struct {
		YAML string `json:"yaml"`
		Hash string `json:"hash"`
	}
	if err = json.Unmarshal(configBody, &response); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(response.YAML, "level: info", "level: debug", 1)
	payload, _ := json.Marshal(map[string]any{"yaml": edited, "expected_hash": response.Hash})
	request, _ := http.NewRequest(http.MethodPut, server.URL+"/api/config", bytes.NewReader(payload))
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
	saveResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	saveBody, _ := io.ReadAll(saveResponse.Body)
	_ = saveResponse.Body.Close()
	if saveResponse.StatusCode != http.StatusOK {
		t.Fatalf("save failed: %d %s", saveResponse.StatusCode, saveBody)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(written, []byte("plaintext-provider-secret")) || bytes.Contains(written, []byte(webRedactionMask)) || !bytes.Contains(written, []byte("level: debug")) {
		t.Fatalf("secret was not preserved during masked save: %s", written)
	}
}

func TestDetectProviderBeforeSave(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("authorization") != "Bearer test-key" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`)
	}))
	defer upstream.Close()
	c := &config.Config{Admin: config.Admin{Enabled: true, Token: "test-admin-token-1234567890"}}
	s := New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "")
	ts := httptest.NewServer(s)
	defer ts.Close()
	payload, _ := json.Marshal(map[string]any{"base_url": upstream.URL + "/v1", "api_key": "test-key", "models": []string{"Qwen/Qwen3-Test"}, "allow_private_url": true})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/providers/detect", bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer test-admin-token-1234567890")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["ok"] != true || result["protocol"] != string(ir.OpenAIChat) || result["profile"] != "qwen3" {
		t.Fatalf("unexpected detection result: %#v", result)
	}
}

func TestClaudeCodeConfigPreservesExistingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	t.Setenv("AIROUTE_CLAUDE_SETTINGS_PATH", path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{"Stop":[{"command":"keep-me"}]},"env":{"EXISTING":"yes"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Admin: config.Admin{Enabled: true, Token: "test-admin-token-1234567890"}}
	s := New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "http://127.0.0.1:8080")
	ts := httptest.NewServer(s)
	defer ts.Close()
	payload := `{"base_url":"http://127.0.0.1:8080","api_key":"local-key","model":"mimo","opus_model":"qwen3","sonnet_model":"mimo","haiku_model":"mimo"}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/claude-code/config", strings.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer test-admin-token-1234567890")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("save returned %d", resp.StatusCode)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte("keep-me")) || !bytes.Contains(raw, []byte(`"ANTHROPIC_MODEL": "mimo"`)) || !bytes.Contains(raw, []byte(`"EXISTING": "yes"`)) {
		t.Fatalf("settings not merged: %s", raw)
	}
	backups, _ := filepath.Glob(path + ".airoute.bak.*")
	if len(backups) != 1 {
		t.Fatalf("expected one backup, got %v", backups)
	}
	if info, err := os.Stat(backups[0]); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("application backup permission is not 0600: %v (%v)", info, err)
	}
}

func TestApplicationAPIClaudeCodeLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	t.Setenv("AIROUTE_CLAUDE_SETTINGS_PATH", path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"EXISTING":"yes","ANTHROPIC_API_KEY":"must-not-leak"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Admin: config.Admin{Enabled: true, Token: "test-admin-token-1234567890"}}
	s := New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "http://127.0.0.1:8080")
	ts := httptest.NewServer(s)
	defer ts.Close()

	request := func(method, endpoint, payload string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+endpoint, strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer test-admin-token-1234567890")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, body
	}

	resp, body := request(http.MethodGet, "/api/apps?detect=false", "")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"id":"claude-code"`)) || !bytes.Contains(body, []byte(`"id":"claude-app"`)) || !bytes.Contains(body, []byte(`"id":"chatgpt-app"`)) || !bytes.Contains(body, []byte(`"id":"codex"`)) || !bytes.Contains(body, []byte(`"id":"mimo-code"`)) {
		t.Fatalf("application list failed (%d): %s", resp.StatusCode, body)
	}
	resp, body = request(http.MethodGet, "/api/apps/claude-code", "")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ANTHROPIC_API_KEY":"must-not-leak"`)) {
		t.Fatalf("application state does not expose the local API key (%d): %s", resp.StatusCode, body)
	}

	payload := `{"base_url":"http://127.0.0.1:8080","api_key":"local-key","model":"mimo","sonnet_model":"mimo"}`
	resp, body = request(http.MethodPost, "/api/apps/claude-code/preview", payload)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"will_create_backup":true`)) || !bytes.Contains(body, []byte(`"theme"`)) || !bytes.Contains(body, []byte("local-key")) {
		t.Fatalf("application preview failed (%d): %s", resp.StatusCode, body)
	}
	var preview struct {
		Current map[string]any `json:"current"`
	}
	if err := json.Unmarshal(body, &preview); err != nil || preview.Current["theme"] != "dark" {
		t.Fatalf("application preview omitted current config: %s (%v)", body, err)
	}
	resp, body = request(http.MethodPut, "/api/apps/claude-code/config", payload)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("application apply failed (%d): %s", resp.StatusCode, body)
	}
	var applied struct {
		Backup string `json:"backup"`
	}
	if err := json.Unmarshal(body, &applied); err != nil || applied.Backup == "" {
		t.Fatalf("application apply did not return a backup: %s", body)
	}
	resp, body = request(http.MethodGet, "/api/apps/claude-code/backups", "")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(applied.Backup)) {
		t.Fatalf("application backups failed (%d): %s", resp.StatusCode, body)
	}
	rollbackPayload, _ := json.Marshal(map[string]string{"name": applied.Backup})
	resp, body = request(http.MethodPost, "/api/apps/claude-code/rollback", string(rollbackPayload))
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("application rollback failed (%d): %s", resp.StatusCode, body)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte(`"theme":"dark"`)) || !bytes.Contains(raw, []byte("must-not-leak")) {
		t.Fatalf("application rollback did not restore the original file: %s (%v)", raw, err)
	}
	deletePayload, _ := json.Marshal(map[string]string{"name": applied.Backup})
	resp, body = request(http.MethodDelete, "/api/apps/claude-code/backups", string(deletePayload))
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("application backup delete failed (%d): %s", resp.StatusCode, body)
	}
	resp, body = request(http.MethodGet, "/api/apps/claude-code/backups", "")
	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte(applied.Backup)) {
		t.Fatalf("deleted application backup still listed (%d): %s", resp.StatusCode, body)
	}

	rawPayload, _ := json.Marshal(map[string]any{"content": `{"theme":"edited","env":{"EXISTING":"yes","ANTHROPIC_MODEL":"manual"}}`})
	resp, body = request(http.MethodPut, "/api/apps/claude-code/raw-config", string(rawPayload))
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("editable preview write failed (%d): %s", resp.StatusCode, body)
	}
	resp, body = request(http.MethodPost, "/api/apps/claude-code/cleanup", "")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("application cleanup failed (%d): %s", resp.StatusCode, body)
	}
	cleaned, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(cleaned, []byte(`"theme": "edited"`)) || bytes.Contains(cleaned, []byte("ANTHROPIC_")) {
		t.Fatalf("application cleanup did not preserve unrelated settings: %s (%v)", cleaned, err)
	}

	resp, body = request(http.MethodGet, "/api/apps/unknown", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown application should return 404, got %d: %s", resp.StatusCode, body)
	}
}

func TestApplicationWebRedactionFollowsSettingAndPreservesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	codexPath := filepath.Join(t.TempDir(), ".codex", "config.toml")
	t.Setenv("AIROUTE_CLAUDE_SETTINGS_PATH", path)
	t.Setenv("AIROUTE_CODEX_CONFIG_PATH", codexPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"ANTHROPIC_API_KEY":"real-local-key","ANTHROPIC_MODEL":"old-model"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte("model = \"old-model\"\nmodel_provider = \"airoute\"\n\n[model_providers.airoute]\nbase_url = \"http://127.0.0.1:12666/v1\"\nexperimental_bearer_token = \"real-codex-key\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{
		Admin:   config.Admin{Enabled: true, Token: "test-admin-token-1234567890"},
		Logging: config.Logging{WebRedaction: true},
	}
	ts := httptest.NewServer(New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "http://127.0.0.1:8080"))
	defer ts.Close()

	request := func(method, endpoint, payload string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+endpoint, strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer test-admin-token-1234567890")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, body
	}

	resp, body := request(http.MethodGet, "/api/apps/claude-code", "")
	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte("real-local-key")) || !bytes.Contains(body, []byte(webRedactionMask)) {
		t.Fatalf("application state did not follow web redaction (%d): %s", resp.StatusCode, body)
	}
	payload := `{"base_url":"http://127.0.0.1:8080","api_key":"••••••••","model":"new-model"}`
	resp, body = request(http.MethodPost, "/api/apps/claude-code/preview", payload)
	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte("real-local-key")) || !bytes.Contains(body, []byte(webRedactionMask)) {
		t.Fatalf("application preview did not follow web redaction (%d): %s", resp.StatusCode, body)
	}
	resp, body = request(http.MethodPut, "/api/apps/claude-code/config", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("application save failed (%d): %s", resp.StatusCode, body)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(written, []byte("real-local-key")) || !bytes.Contains(written, []byte("new-model")) || bytes.Contains(written, []byte(webRedactionMask)) {
		t.Fatalf("masked application save did not preserve the real key: %s", written)
	}

	resp, body = request(http.MethodGet, "/api/apps/codex", "")
	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte("real-codex-key")) || !bytes.Contains(body, []byte(webRedactionMask)) {
		t.Fatalf("Codex state did not follow web redaction (%d): %s", resp.StatusCode, body)
	}
	codexPayload := `{"base_url":"http://127.0.0.1:12666","api_key":"••••••••","model":"new-model"}`
	resp, body = request(http.MethodPost, "/api/apps/codex/preview", codexPayload)
	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte("real-codex-key")) || !bytes.Contains(body, []byte(webRedactionMask)) || !bytes.Contains(body, []byte("experimental_bearer_token")) {
		t.Fatalf("Codex TOML preview did not follow web redaction (%d): %s", resp.StatusCode, body)
	}
}

func TestPlaygroundStreamsSSE(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("x-airoute-request-id", "r1")
		_, _ = io.WriteString(w, "data: hello\n\n")
	}))
	defer gateway.Close()
	c := &config.Config{Server: config.Server{RequestTimeout: time.Second}, Admin: config.Admin{Enabled: true, Token: "12345678901234567890123456789012"}}
	s := New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", gateway.URL)
	ts := httptest.NewServer(s)
	defer ts.Close()
	payload := `{"protocol":"openai-chat","body":{"model":"m","messages":[],"stream":true}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/playground/request", strings.NewReader(payload))
	req.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(resp.Header.Get("content-type"), "text/event-stream") || !strings.Contains(string(raw), "hello") {
		t.Fatalf("stream not proxied: %s %s", resp.Header.Get("content-type"), raw)
	}
}

func TestAdminAuthenticationRateLimit(t *testing.T) {
	c := &config.Config{Admin: config.Admin{Enabled: true, Token: "correct-admin-token"}}
	s := New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "")
	ts := httptest.NewServer(s)
	defer ts.Close()
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
		req.Header.Set("authorization", "Bearer wrong")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("attempt %d: %d", i, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
	req.Header.Set("authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("expected rate limit, got %d", resp.StatusCode)
	}
}

func TestPlaygroundPreviewAndProviderHealth(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "m"}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "r", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "OK"}}, "stop_reason": "end_turn"})
			return
		}
		http.NotFound(w, r)
	}))
	defer provider.Close()
	c := &config.Config{Admin: config.Admin{Enabled: true, Token: "12345678901234567890123456789012"}, Providers: []config.Provider{{ID: "p", Protocol: ir.Anthropic, BaseURL: provider.URL, APIKey: "x", Models: []string{"m"}, AllowPrivateURL: true}}, DefaultRoute: &config.RouteTargetList{Targets: []config.RouteTarget{{Provider: "p", Model: "m"}}}}
	registry := protocol.NewRegistry(openaichat.New(), openairesponses.New(), anthropic.New(), gemini.New())
	s := New(config.NewStore(c), registry, observe.NewStore(10), &observe.Metrics{}, "test", "http://127.0.0.1:8080")
	ts := httptest.NewServer(s)
	defer ts.Close()
	call := func(path, payload string) map[string]any {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(payload))
		req.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
		req.Header.Set("content-type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		if resp.StatusCode != 200 {
			t.Fatalf("%s status=%d result=%v", path, resp.StatusCode, result)
		}
		return result
	}
	preview := call("/api/playground/preview", `{"protocol":"openai-chat","request":{"model":"alias","messages":[{"role":"user","content":"hello"}]}}`)
	if preview["canonical_request"] == nil || preview["upstream_request"] == nil || preview["decision"] == nil {
		t.Fatalf("incomplete preview: %#v", preview)
	}
	explainReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/routes/explain?protocol=openai-chat&model=alias&stream=true", nil)
	explainReq.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
	explainResp, err := http.DefaultClient.Do(explainReq)
	if err != nil {
		t.Fatal(err)
	}
	var explain map[string]any
	_ = json.NewDecoder(explainResp.Body).Decode(&explain)
	explainResp.Body.Close()
	if explainResp.StatusCode != 200 || explain["decision"] == nil {
		t.Fatalf("GET route explain failed: status=%d body=%#v", explainResp.StatusCode, explain)
	}
	probe := call("/api/providers/p/probe", `{}`)
	if probe["ok"] != true {
		t.Fatalf("probe failed: %#v", probe)
	}
	probe = call("/api/providers/p/probe", `{"test_request":true}`)
	if probe["test_ok"] != true {
		t.Fatalf("minimal test failed: %#v", probe)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
	req.Header.Set("authorization", "Bearer 12345678901234567890123456789012")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&status)
	health := status["provider_health"].(map[string]any)
	if health["p"] == nil {
		t.Fatalf("provider health missing: %#v", status)
	}
}
