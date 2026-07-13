package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zbss/airoute/internal/auth"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
	providerprofile "github.com/zbss/airoute/internal/provider"
	"github.com/zbss/airoute/internal/routing"
	"github.com/zbss/airoute/internal/secure"
)

//go:embed webdist/*
var assets embed.FS

type Server struct {
	Config           *config.Store
	Registry         *protocol.Registry
	Logs             *observe.Store
	Metrics          *observe.Metrics
	Started          time.Time
	Version          string
	GatewayURL       string
	Client           *http.Client
	RestrictedClient *http.Client
	failureMu        sync.Mutex
	failures         map[string][]time.Time
	healthMu         sync.RWMutex
	health           map[string]map[string]any
	GatewayControl   interface {
		SetEnabled(bool)
		IsEnabled() bool
	}
}

func New(c *config.Store, r *protocol.Registry, l *observe.Store, m *observe.Metrics, version, gatewayURL string) *Server {
	restrictedTransport := &http.Transport{Proxy: nil, DialContext: secure.PublicDialContext, ResponseHeaderTimeout: 30 * time.Second}
	return &Server{Config: c, Registry: r, Logs: l, Metrics: m, Started: time.Now(), Version: version, GatewayURL: gatewayURL, Client: &http.Client{Timeout: 30 * time.Second}, RestrictedClient: &http.Client{Transport: restrictedTransport, Timeout: 30 * time.Second}, failures: map[string][]time.Time{}, health: map[string]map[string]any{}}
}
func (s *Server) CloseIdleConnections() {
	s.Client.CloseIdleConnections()
	s.RestrictedClient.CloseIdleConnections()
}

func (s *Server) SetGatewayControl(control interface {
	SetEnabled(bool)
	IsEnabled() bool
}) {
	s.GatewayControl = control
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("x-airoute-request-id", adminRequestID())
	if !allowedHost(r, s.Config.Get()) {
		http.Error(w, "forbidden host", 403)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !s.loginAllowed(r.RemoteAddr) {
			jsonOut(w, 429, map[string]any{"error": "too many failed authentication attempts"})
			return
		}
		if !allowedOrigin(r) || !auth.AdminOK(r, s.Config.Get()) {
			s.recordFailure(r.RemoteAddr)
			jsonOut(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		s.clearFailures(r.RemoteAddr)
		s.api(w, r)
		return
	}
	s.static(w, r)
}

func adminRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "adm_" + hex.EncodeToString(b)
}

func (s *Server) loginAllowed(addr string) bool {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	key := remoteHost(addr)
	cut := time.Now().Add(-time.Minute)
	recent := s.failures[key][:0]
	for _, at := range s.failures[key] {
		if at.After(cut) {
			recent = append(recent, at)
		}
	}
	s.failures[key] = recent
	return len(recent) < 10
}
func (s *Server) recordFailure(addr string) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	key := remoteHost(addr)
	s.failures[key] = append(s.failures[key], time.Now())
}
func (s *Server) clearFailures(addr string) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	delete(s.failures, remoteHost(addr))
}
func remoteHost(addr string) string {
	h, _, e := net.SplitHostPort(addr)
	if e == nil {
		return h
	}
	return addr
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/status" && r.Method == "GET":
		c := s.Config.Get()
		status := "running"
		if s.GatewayControl != nil && !s.GatewayControl.IsEnabled() {
			status = "stopped"
		}
		jsonOut(w, 200, map[string]any{"status": status, "version": s.Version, "uptime_seconds": int(time.Since(s.Started).Seconds()), "config_version": c.Hash, "config_error": s.Config.LastError(), "gateway_url": s.GatewayURL, "providers": len(c.Providers), "routes": len(c.Routes), "provider_health": s.providerHealth(), "metrics": summary(s.Metrics, s.Logs)})
	case r.URL.Path == "/api/runtime" && r.Method == "PUT":
		if s.GatewayControl == nil {
			jsonOut(w, 501, map[string]any{"error": "gateway runtime control is unavailable"})
			return
		}
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in) != nil || in.Enabled == nil {
			jsonOut(w, 400, map[string]any{"error": "enabled must be a boolean"})
			return
		}
		s.GatewayControl.SetEnabled(*in.Enabled)
		jsonOut(w, 200, map[string]any{"ok": true, "status": map[bool]string{true: "running", false: "stopped"}[*in.Enabled]})
	case r.URL.Path == "/api/config" && r.Method == "GET":
		current := s.Config.Get()
		raw, e := os.ReadFile(current.SourcePath)
		if e != nil {
			apiError(w, 500, e)
			return
		}
		jsonOut(w, 200, map[string]any{"yaml": string(raw), "hash": current.Hash, "config": current})
	case r.URL.Path == "/api/config/validate" && r.Method == "POST":
		s.validate(w, r, false)
	case r.URL.Path == "/api/config" && r.Method == "PUT":
		s.validate(w, r, true)
	case r.URL.Path == "/api/config/reload" && r.Method == "POST":
		c, e := config.Load(s.Config.Get().SourcePath)
		if e != nil {
			s.Config.SetError(e)
			apiError(w, 422, e)
			return
		}
		s.Config.Replace(c)
		jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash})
	case r.URL.Path == "/api/config/backups" && r.Method == "GET":
		s.backups(w)
	case r.URL.Path == "/api/config/rollback" && r.Method == "POST":
		s.rollback(w, r)
	case r.URL.Path == "/api/providers" && r.Method == "GET":
		jsonOut(w, 200, map[string]any{"providers": publicProviders(s.Config.Get().Providers, s.providerHealth())})
	case r.URL.Path == "/api/providers/detect" && r.Method == "POST":
		s.detectProvider(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/providers/") && strings.HasSuffix(r.URL.Path, "/probe") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/probe")
		s.probe(w, r, id)
	case r.URL.Path == "/api/routes/explain" && (r.Method == "GET" || r.Method == "POST"):
		s.explain(w, r)
	case r.URL.Path == "/api/claude-code/config" && r.Method == "GET":
		s.claudeCodeConfig(w)
	case r.URL.Path == "/api/claude-code/config" && r.Method == "PUT":
		s.saveClaudeCodeConfig(w, r)
	case r.URL.Path == "/api/playground/preview" && r.Method == "POST":
		s.preview(w, r)
	case r.URL.Path == "/api/logs" && r.Method == "GET":
		limit := 100
		fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
		jsonOut(w, 200, map[string]any{"logs": s.Logs.List(limit)})
	case strings.HasPrefix(r.URL.Path, "/api/logs/") && r.Method == "GET":
		id := strings.TrimPrefix(r.URL.Path, "/api/logs/")
		v, ok := s.Logs.Get(id)
		if !ok {
			jsonOut(w, 404, map[string]any{"error": "not found"})
			return
		}
		jsonOut(w, 200, v)
	case r.URL.Path == "/api/metrics/summary" && r.Method == "GET":
		jsonOut(w, 200, summary(s.Metrics, s.Logs))
	case r.URL.Path == "/api/diagnostics" && r.Method == "GET":
		s.diagnostics(w)
	case r.URL.Path == "/api/playground/request" && r.Method == "POST":
		s.playground(w, r)
	default:
		jsonOut(w, 404, map[string]any{"error": "not found"})
	}
}

func (s *Server) diagnostics(w http.ResponseWriter) {
	c := s.Config.Get()
	logs := s.Logs.List(100)
	for i := range logs {
		logs[i].RequestBody = ""
		logs[i].ResponseBody = ""
	}
	ok, failed := s.Config.LoadCounts()
	bundle := map[string]any{
		"manifest":                []string{"runtime", "configuration_summary", "provider_health", "metrics", "recent_request_metadata"},
		"generated_at":            time.Now().UTC(),
		"runtime":                 map[string]any{"version": s.Version, "uptime_seconds": int(time.Since(s.Started).Seconds()), "config_version": c.Hash, "config_error": s.Config.LastError(), "config_load_success": ok, "config_load_failure": failed},
		"configuration_summary":   c,
		"provider_health":         s.providerHealth(),
		"metrics":                 summary(s.Metrics, s.Logs),
		"recent_request_metadata": logs,
	}
	w.Header().Set("content-disposition", `attachment; filename="airoute-diagnostics.json"`)
	jsonOut(w, 200, bundle)
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request, save bool) {
	var in struct {
		YAML         string `json:"yaml"`
		ExpectedHash string `json:"expected_hash"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in) != nil {
		jsonOut(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if save && in.ExpectedHash != "" && in.ExpectedHash != s.Config.Get().Hash {
		jsonOut(w, 409, map[string]any{"error": "configuration changed since it was loaded"})
		return
	}
	dir := filepath.Dir(s.Config.Get().SourcePath)
	tmp, e := os.CreateTemp(dir, ".airoute-validate-*.yaml")
	if e != nil {
		apiError(w, 500, e)
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if e = tmp.Chmod(0600); e == nil {
		_, e = tmp.WriteString(in.YAML)
	}
	if e == nil {
		e = tmp.Sync()
	}
	if closeErr := tmp.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		apiError(w, 500, e)
		return
	}
	c, e := config.Load(name)
	if e != nil {
		jsonOut(w, 422, map[string]any{"valid": false, "error": e.Error()})
		return
	}
	if !save {
		jsonOut(w, 200, map[string]any{"valid": true})
		return
	}
	target := s.Config.Get().SourcePath
	backup := target + ".bak." + time.Now().UTC().Format("20060102T150405.000000000Z")
	if raw, e := os.ReadFile(target); e == nil {
		if e = os.WriteFile(backup, raw, 0600); e != nil {
			apiError(w, 500, e)
			return
		}
	}
	if e = os.Rename(name, target); e != nil {
		apiError(w, 500, e)
		return
	}
	_ = syncDirectory(dir)
	c.SourcePath = target
	s.Config.Replace(c)
	pruneBackups(target, 10)
	jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash, "backup": filepath.Base(backup)})
}

func (s *Server) backups(w http.ResponseWriter) {
	target := s.Config.Get().SourcePath
	files, _ := filepath.Glob(target + ".bak.*")
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	var out []any
	for _, f := range files {
		st, _ := os.Stat(f)
		if st != nil {
			out = append(out, map[string]any{"name": filepath.Base(f), "size": st.Size(), "modified": st.ModTime()})
		}
	}
	jsonOut(w, 200, map[string]any{"backups": out})
}
func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.Contains(in.Name, "/") {
		jsonOut(w, 400, map[string]any{"error": "invalid backup name"})
		return
	}
	target := s.Config.Get().SourcePath
	source := filepath.Join(filepath.Dir(target), in.Name)
	if !strings.HasPrefix(in.Name, filepath.Base(target)+".bak.") {
		jsonOut(w, 400, map[string]any{"error": "invalid backup name"})
		return
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		apiError(w, 404, e)
		return
	}
	tmp := target + ".rollback.tmp"
	if e = writeSynced(tmp, raw); e != nil {
		apiError(w, 500, e)
		return
	}
	c, e := config.Load(tmp)
	if e != nil {
		os.Remove(tmp)
		apiError(w, 422, e)
		return
	}
	if e = os.Rename(tmp, target); e != nil {
		apiError(w, 500, e)
		return
	}
	_ = syncDirectory(filepath.Dir(target))
	c.SourcePath = target
	s.Config.Replace(c)
	jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash})
}

func (s *Server) probe(w http.ResponseWriter, r *http.Request, id string) {
	var options struct {
		TestRequest bool `json:"test_request"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&options)
	var p *config.Provider
	for i := range s.Config.Get().Providers {
		if s.Config.Get().Providers[i].ID == id {
			p = &s.Config.Get().Providers[i]
			break
		}
	}
	if p == nil {
		jsonOut(w, 404, map[string]any{"error": "provider not found"})
		return
	}
	start := time.Now()
	u := providerModelsURL(*p)
	if e := secure.ValidatePublicTarget(r.Context(), u, p.AllowPrivateURL); e != nil {
		report := map[string]any{"ok": false, "error": e.Error(), "latency_ms": time.Since(start).Milliseconds(), "checked_at": time.Now()}
		s.setProviderHealth(id, report)
		jsonOut(w, 200, report)
		return
	}
	req, e := http.NewRequestWithContext(r.Context(), http.MethodGet, u, nil)
	if e != nil {
		apiError(w, 400, e)
		return
	}
	switch p.Protocol {
	case ir.Anthropic:
		req.Header.Set("x-api-key", p.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case ir.Gemini:
		q := req.URL.Query()
		q.Set("key", p.APIKey)
		req.URL.RawQuery = q.Encode()
	default:
		req.Header.Set("authorization", "Bearer "+p.APIKey)
	}
	client := s.RestrictedClient
	if p.AllowPrivateURL {
		client = s.Client
	}
	resp, e := client.Do(req)
	if e != nil {
		report := map[string]any{"ok": false, "error": e.Error(), "latency_ms": time.Since(start).Milliseconds(), "checked_at": time.Now()}
		s.setProviderHealth(id, report)
		jsonOut(w, 200, report)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	report := map[string]any{"ok": resp.StatusCode >= 200 && resp.StatusCode < 300, "status": resp.StatusCode, "latency_ms": time.Since(start).Milliseconds(), "checked_at": time.Now(), "detected_protocol": detectProtocol(*p, body), "models": discoverModels(body), "response": json.RawMessage(validJSON(body))}
	if options.TestRequest {
		testStatus, testError := s.minimalProviderTest(r.Context(), *p)
		report["test_status"] = testStatus
		report["test_ok"] = testError == nil && testStatus >= 200 && testStatus < 300
		if testError != nil {
			report["test_error"] = testError.Error()
		}
	}
	s.setProviderHealth(id, report)
	jsonOut(w, 200, report)
}

func (s *Server) minimalProviderTest(ctx context.Context, p config.Provider) (int, error) {
	model := ""
	if len(p.Models) > 0 {
		model = p.Models[0]
	}
	var endpoint string
	var payload []byte
	switch p.Protocol {
	case ir.OpenAIChat:
		endpoint = strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
		payload = common.Raw(map[string]any{"model": model, "max_completion_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "Reply OK"}}})
	case ir.OpenAIResponses:
		endpoint = strings.TrimRight(p.BaseURL, "/") + "/responses"
		payload = common.Raw(map[string]any{"model": model, "max_output_tokens": 8, "input": "Reply OK"})
	case ir.Anthropic:
		endpoint = strings.TrimRight(p.BaseURL, "/") + "/v1/messages"
		payload = common.Raw(map[string]any{"model": model, "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "Reply OK"}}})
	case ir.Gemini:
		base := strings.TrimRight(p.BaseURL, "/")
		if !strings.Contains(base, "/v1beta") {
			base += "/v1beta"
		}
		endpoint = base + "/models/" + url.PathEscape(model) + ":generateContent"
		payload = common.Raw(map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Reply OK"}}}}})
	}
	if err := secure.ValidatePublicTarget(ctx, endpoint, p.AllowPrivateURL); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("content-type", "application/json")
	switch p.Protocol {
	case ir.Anthropic:
		req.Header.Set("x-api-key", p.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case ir.Gemini:
		req.Header.Set("x-goog-api-key", p.APIKey)
	default:
		req.Header.Set("authorization", "Bearer "+p.APIKey)
	}
	client := s.RestrictedClient
	if p.AllowPrivateURL {
		client = s.Client
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 256<<10))
	return resp.StatusCode, nil
}

func (s *Server) detectProvider(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL         string   `json:"base_url"`
		APIKey          string   `json:"api_key"`
		Models          []string `json:"models"`
		AllowPrivateURL bool     `json:"allow_private_url"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&in) != nil {
		jsonOut(w, 400, map[string]any{"error": "invalid request"})
		return
	}
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.BaseURL == "" || strings.TrimSpace(in.APIKey) == "" || len(in.Models) == 0 || strings.TrimSpace(in.Models[0]) == "" {
		jsonOut(w, 422, map[string]any{"error": "API 地址、密钥和至少一个模型名为必填项"})
		return
	}
	profile := detectProviderProfile(in.BaseURL, in.Models)
	candidates := []ir.Protocol{ir.OpenAIChat, ir.Anthropic, ir.OpenAIResponses, ir.Gemini}
	lowerBase := strings.ToLower(in.BaseURL)
	if strings.Contains(lowerBase, "anthropic") {
		candidates = []ir.Protocol{ir.Anthropic, ir.OpenAIChat, ir.OpenAIResponses, ir.Gemini}
	} else if strings.Contains(lowerBase, "generativelanguage") || strings.Contains(lowerBase, "gemini") {
		candidates = []ir.Protocol{ir.Gemini, ir.OpenAIChat, ir.Anthropic, ir.OpenAIResponses}
	}
	started := time.Now()
	attempts := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		p := config.Provider{Protocol: candidate, BaseURL: in.BaseURL, APIKey: in.APIKey, Models: in.Models, AllowPrivateURL: in.AllowPrivateURL, Timeout: 30 * time.Second}
		status, err := s.minimalProviderTest(r.Context(), p)
		attempt := map[string]any{"protocol": candidate, "status": status}
		if err != nil {
			attempt["error"] = err.Error()
		}
		attempts = append(attempts, attempt)
		if err == nil && status >= 200 && status < 300 {
			jsonOut(w, 200, map[string]any{
				"ok": true, "protocol": candidate, "profile": profile,
				"label": detectedProviderLabel(candidate, profile), "models": in.Models,
				"latency_ms": time.Since(started).Milliseconds(), "attempts": attempts,
			})
			return
		}
	}
	jsonOut(w, 200, map[string]any{"ok": false, "profile": profile, "attempts": attempts, "latency_ms": time.Since(started).Milliseconds()})
}

func detectProviderProfile(baseURL string, models []string) string {
	value := strings.ToLower(baseURL + " " + strings.Join(models, " "))
	switch {
	case strings.Contains(value, "qwen"):
		return "qwen3"
	case strings.Contains(value, "mimo") || strings.Contains(value, "xiaomi"):
		return "xiaomi-mimo"
	default:
		return "generic"
	}
}

func detectedProviderLabel(protocolName ir.Protocol, profile string) string {
	if profile == "qwen3" {
		return "Qwen 3.x（OpenAI 兼容）"
	}
	if profile == "xiaomi-mimo" {
		return "Xiaomi MiMo（" + string(protocolName) + "）"
	}
	switch protocolName {
	case ir.Anthropic:
		return "Anthropic / Claude 协议"
	case ir.Gemini:
		return "Google Gemini 协议"
	case ir.OpenAIResponses:
		return "OpenAI Responses 协议"
	default:
		return "OpenAI Chat Completions 协议"
	}
}

var claudeRouterEnvKeys = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

func claudeSettingsPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AIROUTE_CLAUDE_SETTINGS_PATH")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func readClaudeSettings(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, fmt.Errorf("Claude Code 配置不是有效 JSON: %w", err)
		}
	}
	return settings, nil
}

func (s *Server) claudeCodeConfig(w http.ResponseWriter) {
	path, err := claudeSettingsPath()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	settings, err := readClaudeSettings(path)
	if err != nil {
		apiError(w, 422, err)
		return
	}
	env, _ := settings["env"].(map[string]any)
	result := map[string]any{}
	for _, key := range claudeRouterEnvKeys {
		if value, ok := env[key].(string); ok && value != "" {
			if key == "ANTHROPIC_API_KEY" {
				result["api_key_set"] = true
			} else {
				result[key] = value
			}
		}
	}
	jsonOut(w, 200, map[string]any{"path": path, "exists": len(settings) > 0, "router": result, "preserved_fields": len(settings)})
}

func (s *Server) saveClaudeCodeConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL     string `json:"base_url"`
		APIKey      string `json:"api_key"`
		Model       string `json:"model"`
		OpusModel   string `json:"opus_model"`
		SonnetModel string `json:"sonnet_model"`
		HaikuModel  string `json:"haiku_model"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&in) != nil || strings.TrimSpace(in.BaseURL) == "" || strings.TrimSpace(in.Model) == "" {
		jsonOut(w, 422, map[string]any{"error": "网关地址和主模型为必填项"})
		return
	}
	path, err := claudeSettingsPath()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	settings, err := readClaudeSettings(path)
	if err != nil {
		apiError(w, 422, err)
		return
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["ANTHROPIC_BASE_URL"] = strings.TrimRight(in.BaseURL, "/")
	if in.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = in.APIKey
	} else if _, ok := env["ANTHROPIC_API_KEY"]; !ok {
		env["ANTHROPIC_API_KEY"] = "airoute-local"
	}
	env["ANTHROPIC_MODEL"] = in.Model
	roleModels := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   in.OpusModel,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": in.SonnetModel,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  in.HaikuModel,
	}
	for key, value := range roleModels {
		if value == "" {
			delete(env, key)
		} else {
			env[key] = value
		}
	}
	settings["env"] = env
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		apiError(w, 500, err)
		return
	}
	if previous, readErr := os.ReadFile(path); readErr == nil {
		backup := path + ".airoute.bak." + time.Now().Format("20060102-150405")
		if err = os.WriteFile(backup, previous, 0600); err != nil {
			apiError(w, 500, err)
			return
		}
	}
	tmp := path + ".airoute.tmp"
	if err = os.WriteFile(tmp, append(raw, '\n'), 0600); err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		apiError(w, 500, err)
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "path": path, "model": in.Model})
}

func (s *Server) setProviderHealth(id string, report map[string]any) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.health[id] = report
}

func (s *Server) providerHealth() map[string]map[string]any {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	out := make(map[string]map[string]any, len(s.health))
	for id, report := range s.health {
		copy := make(map[string]any, len(report))
		for key, value := range report {
			if key != "response" {
				copy[key] = value
			}
		}
		out[id] = copy
	}
	return out
}

func (s *Server) explain(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		protocolName := ir.Protocol(r.URL.Query().Get("protocol"))
		if _, err := s.Registry.Get(protocolName); err != nil {
			apiError(w, 400, err)
			return
		}
		request := ir.Request{Model: r.URL.Query().Get("model"), Stream: queryBool(r, "stream")}
		if queryBool(r, "tools") {
			request.Tools = []ir.Tool{{Name: "route-explain-tool"}}
		}
		if queryBool(r, "image") {
			request.Messages = []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "image_url", URL: "https://example.invalid/image.png"}}}}
		}
		decision, err := routing.Resolve(s.Config.Get(), routing.Input{Request: &request, Protocol: protocolName, Headers: lower(queryHeaders(r))})
		if err != nil {
			apiError(w, 404, err)
			return
		}
		jsonOut(w, 200, map[string]any{"decision": decision, "diagnostics": []ir.Diagnostic{}, "canonical_request": request})
		return
	}
	var in struct {
		Protocol ir.Protocol       `json:"protocol"`
		Request  json.RawMessage   `json:"request"`
		Headers  map[string]string `json:"headers"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		jsonOut(w, 400, map[string]any{"error": "invalid request"})
		return
	}
	a, e := s.Registry.Get(in.Protocol)
	if e != nil {
		apiError(w, 400, e)
		return
	}
	req, d, e := a.DecodeRequest(r.Context(), in.Request)
	if e != nil {
		apiError(w, 422, e)
		return
	}
	decision, e := routing.Resolve(s.Config.Get(), routing.Input{Request: req, Protocol: in.Protocol, Headers: lower(in.Headers)})
	if e != nil {
		apiError(w, 404, e)
		return
	}
	jsonOut(w, 200, map[string]any{"decision": decision, "diagnostics": d, "canonical_request": req})
}

func queryBool(r *http.Request, name string) bool {
	value := strings.ToLower(r.URL.Query().Get(name))
	return value == "1" || value == "true" || value == "yes"
}

func queryHeaders(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, raw := range r.URL.Query()["header"] {
		name, value, ok := strings.Cut(raw, ":")
		if ok && strings.TrimSpace(name) != "" {
			out[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return out
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Protocol ir.Protocol       `json:"protocol"`
		Request  json.RawMessage   `json:"request"`
		Headers  map[string]string `json:"headers"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&in) != nil {
		jsonOut(w, 400, map[string]any{"error": "invalid request"})
		return
	}
	inAdapter, err := s.Registry.Get(in.Protocol)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	request, diagnostics, err := inAdapter.DecodeRequest(r.Context(), in.Request)
	if err != nil {
		apiError(w, 422, err)
		return
	}
	decision, err := routing.Resolve(s.Config.Get(), routing.Input{Request: request, Protocol: in.Protocol, Headers: lower(in.Headers)})
	if err != nil {
		apiError(w, 404, err)
		return
	}
	var upstream json.RawMessage
	if len(decision.Targets) > 0 {
		target := decision.Targets[0]
		request.Model = target.Model
		adapter, _ := s.Registry.Get(target.Provider.Protocol)
		var encodedDiagnostics []ir.Diagnostic
		upstream, encodedDiagnostics, err = adapter.EncodeRequest(r.Context(), request)
		diagnostics = append(diagnostics, encodedDiagnostics...)
		if err != nil {
			apiError(w, 422, err)
			return
		}
		upstream, err = providerprofile.PrepareRequest(upstream, target.Provider, request)
		if err != nil {
			apiError(w, 422, err)
			return
		}
	}
	jsonOut(w, 200, map[string]any{"canonical_request": request, "decision": decision, "upstream_request": json.RawMessage(upstream), "diagnostics": diagnostics})
}

func (s *Server) playground(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Protocol ir.Protocol     `json:"protocol"`
		Body     json.RawMessage `json:"body"`
		Stream   bool            `json:"stream"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&in) != nil {
		jsonOut(w, 400, map[string]any{"error": "invalid request"})
		return
	}
	var requestMeta map[string]any
	_ = json.Unmarshal(in.Body, &requestMeta)
	if stream, ok := requestMeta["stream"].(bool); ok {
		in.Stream = stream
	}
	geminiAction := "generateContent"
	if in.Stream {
		geminiAction = "streamGenerateContent"
	}
	model := "playground"
	if value, ok := requestMeta["model"].(string); ok && value != "" {
		model = value
	}
	path := map[ir.Protocol]string{ir.OpenAIChat: "/v1/chat/completions", ir.OpenAIResponses: "/v1/responses", ir.Anthropic: "/v1/messages", ir.Gemini: "/v1beta/models/" + url.PathEscape(model) + ":" + geminiAction}[in.Protocol]
	req, e := http.NewRequestWithContext(r.Context(), http.MethodPost, s.GatewayURL+path, bytes.NewReader(in.Body))
	if e != nil {
		apiError(w, 400, e)
		return
	}
	req.Header.Set("content-type", "application/json")
	if c := s.Config.Get(); c.Auth.Enabled && len(c.Auth.Keys) > 0 {
		req.Header.Set("authorization", "Bearer "+c.Auth.Keys[0].Value)
	}
	client := &http.Client{Timeout: s.Config.Get().Server.RequestTimeout}
	resp, e := client.Do(req)
	if e != nil {
		apiError(w, 502, e)
		return
	}
	defer resp.Body.Close()
	if strings.Contains(resp.Header.Get("content-type"), "text/event-stream") {
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-cache")
		w.Header().Set("x-airoute-playground-status", fmt.Sprint(resp.StatusCode))
		w.Header().Set("x-airoute-request-id", resp.Header.Get("x-airoute-request-id"))
		w.WriteHeader(200)
		buf := make([]byte, 4096)
		flusher, _ := w.(http.Flusher)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr != nil {
				return
			}
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	jsonOut(w, 200, map[string]any{"status": resp.StatusCode, "content_type": resp.Header.Get("content-type"), "body": string(body), "request_id": resp.Header.Get("x-airoute-request-id")})
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	b, e := assets.ReadFile("webdist/" + name)
	if e != nil {
		b, e = assets.ReadFile("webdist/index.html")
	}
	if e != nil {
		http.Error(w, "web console not built", 503)
		return
	}
	if typ := mime.TypeByExtension(filepath.Ext(name)); typ != "" {
		w.Header().Set("content-type", typ)
	}
	w.Header().Set("cache-control", "no-cache")
	_, _ = w.Write(b)
}
func allowedHost(r *http.Request, c *config.Config) bool {
	h := r.Host
	if x, _, e := net.SplitHostPort(h); e == nil {
		h = x
	}
	if h == "localhost" || net.ParseIP(strings.Trim(h, "[]")) != nil {
		return true
	}
	for _, allowed := range c.Admin.AllowedHosts {
		if strings.EqualFold(h, allowed) || strings.EqualFold(r.Host, allowed) {
			return true
		}
	}
	return false
}
func allowedOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, e := url.Parse(o)
	return e == nil && strings.EqualFold(u.Host, r.Host)
}
func summary(m *observe.Metrics, logs *observe.Store) map[string]any {
	completed := m.Completed.Load()
	avg := uint64(0)
	if completed > 0 {
		avg = m.LatencyMSTotal.Load() / completed
	}
	records := logs.List(500)
	latencies := make([]int64, 0, len(records))
	for _, r := range records {
		latencies = append(latencies, r.DurationMS)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := int64(0)
	p50 := int64(0)
	if len(latencies) > 0 {
		p50 = latencies[(len(latencies)-1)*50/100]
		p95 = latencies[(len(latencies)-1)*95/100]
	}
	return map[string]any{"requests": m.Requests.Load(), "errors": m.Errors.Load(), "in_flight": m.InFlight.Load(), "retries": m.Retries.Load(), "fallbacks": m.Fallbacks.Load(), "timeouts": m.Timeouts.Load(), "cancellations": m.Cancellations.Load(), "diagnostics": m.Diagnostics.Load(), "input_tokens": m.InputTokens.Load(), "output_tokens": m.OutputTokens.Load(), "average_latency_ms": avg, "p50_latency_ms": p50, "p95_latency_ms": p95}
}
func publicProviders(in []config.Provider, health map[string]map[string]any) []any {
	out := make([]any, 0, len(in))
	for _, p := range in {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "profile": p.Profile, "protocol": p.Protocol, "base_url": p.BaseURL, "models": p.Models, "api_key_set": p.APIKey != "", "health": health[p.ID]})
	}
	return out
}
func lower(m map[string]string) map[string]string {
	o := map[string]string{}
	for k, v := range m {
		o[strings.ToLower(k)] = v
	}
	return o
}
func validJSON(b []byte) []byte {
	if json.Valid(b) {
		return b
	}
	x, _ := json.Marshal(string(b))
	return x
}
func providerModelsURL(p config.Provider) string {
	base := strings.TrimRight(p.BaseURL, "/")
	if p.Protocol == ir.Gemini {
		if !strings.Contains(base, "/v1beta") {
			base += "/v1beta"
		}
		return base + "/models"
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}
func discoverModels(body []byte) []string {
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	var out []string
	for _, x := range []string{"data", "models"} {
		items, ok := v[x].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if id == "" {
				id, _ = m["name"].(string)
			}
			id = strings.TrimPrefix(id, "models/")
			if id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}
func detectProtocol(p config.Provider, body []byte) ir.Protocol {
	var v map[string]any
	_ = json.Unmarshal(body, &v)
	if _, ok := v["models"]; ok {
		return ir.Gemini
	}
	base := strings.ToLower(p.BaseURL)
	if strings.Contains(base, "anthropic") {
		return ir.Anthropic
	}
	if p.Protocol == ir.OpenAIResponses {
		return ir.OpenAIResponses
	}
	return p.Protocol
}
func pruneBackups(target string, keep int) {
	files, _ := filepath.Glob(target + ".bak.*")
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, f := range files[minimum(keep, len(files)):] {
		_ = os.Remove(f)
	}
}
func minimum(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func writeSynced(path string, data []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	return e
}
func syncDirectory(path string) error {
	d, e := os.Open(path)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func apiError(w http.ResponseWriter, status int, e error) {
	jsonOut(w, status, map[string]any{"error": e.Error()})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
