package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	resp = request("GET", "/api/config", nil, "")
	configBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(configBody, []byte("provider-secret")) {
		t.Fatal("resolved provider secret leaked from config API")
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
	files, _ := filepath.Glob(path + ".bak.*")
	if len(files) != 1 {
		t.Fatalf("expected backup, got %v", files)
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
