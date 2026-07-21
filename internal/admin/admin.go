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

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/application/claudeapp"
	"github.com/zbss/airoute/internal/application/claudecode"
	"github.com/zbss/airoute/internal/application/codex"
	"github.com/zbss/airoute/internal/application/mimocode"
	"github.com/zbss/airoute/internal/auth"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/ir"
	providerprofile "github.com/zbss/airoute/internal/provider"
	"github.com/zbss/airoute/internal/routing"
	"github.com/zbss/airoute/internal/safefile"
	"github.com/zbss/airoute/internal/secure"
	"gopkg.in/yaml.v3"
)

const webRedactionMask = "••••••••"

//go:embed webdist/*
var assets embed.FS

type Server struct {
	Config            *config.Store
	Registry          *protocol.Registry
	Logs              *observe.Store
	Metrics           *observe.Metrics
	Started           time.Time
	Version           string
	GatewayURL        string
	ReleaseURL        string
	Client            *http.Client
	RestrictedClient  *http.Client
	Applications      *application.Registry
	failureMu         sync.Mutex
	failures          map[string][]time.Time
	healthMu          sync.RWMutex
	health            map[string]map[string]any
	updateMu          sync.Mutex
	updateCached      UpdateInfo
	updateCachedUntil time.Time
	probeMu           sync.Mutex
	probeCache        map[string]providerProbeCacheEntry
	probeInFlight     map[string]*providerProbeCall
	GatewayControl    interface {
		SetEnabled(bool)
		IsEnabled() bool
		ApplyRuntimeConfig(*config.Config, *config.Config)
	}
}

func New(c *config.Store, r *protocol.Registry, l *observe.Store, m *observe.Metrics, version, gatewayURL string, configPaths ...string) *Server {
	restrictedTransport := &http.Transport{Proxy: nil, DialContext: secure.PublicDialContext, ResponseHeaderTimeout: 30 * time.Second}
	releaseURL := os.Getenv("AIROUTE_RELEASE_API_URL")
	if releaseURL == "" {
		releaseURL = defaultReleaseAPIURL
	}
	codexAdapter := codex.New()
	if len(configPaths) > 0 {
		codexAdapter.RouterConfigPath = configPaths[0]
	}
	return &Server{Config: c, Registry: r, Logs: l, Metrics: m, Started: time.Now(), Version: version, GatewayURL: gatewayURL, ReleaseURL: releaseURL, Client: &http.Client{Timeout: 30 * time.Second}, RestrictedClient: &http.Client{Transport: restrictedTransport, Timeout: 30 * time.Second}, Applications: application.NewRegistry(claudeapp.New(), claudecode.New(), codexAdapter, mimocode.New()), failures: map[string][]time.Time{}, health: map[string]map[string]any{}, probeCache: map[string]providerProbeCacheEntry{}, probeInFlight: map[string]*providerProbeCall{}}
}
func (s *Server) CloseIdleConnections() {
	s.Client.CloseIdleConnections()
	s.RestrictedClient.CloseIdleConnections()
}

func (s *Server) SetGatewayControl(control interface {
	SetEnabled(bool)
	IsEnabled() bool
	ApplyRuntimeConfig(*config.Config, *config.Config)
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
		configuredApps, totalApps := applicationConfigurationCounts(r.Context(), s.Applications)
		jsonOut(w, 200, map[string]any{"status": status, "runtime_state_persistent": false, "version": s.Version, "uptime_seconds": int(time.Since(s.Started).Seconds()), "config_version": c.Hash, "config_error": s.Config.LastError(), "gateway_url": s.GatewayURL, "providers": len(c.Providers), "models": configuredModelCount(c), "routes": len(c.Routes), "applications_configured": configuredApps, "applications_total": totalApps, "logs": len(s.Logs.List(0)), "logs_capacity": c.Logging.RequestHistory, "logging_persist": c.Logging.Persist, "logging_capture_bodies": c.Logging.CaptureBodies, "provider_health": s.providerHealth(), "metrics": summary(s.Metrics, s.Logs)})
	case r.URL.Path == "/api/update" && r.Method == "GET":
		jsonOut(w, 200, s.checkUpdate(r.Context()))
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
		jsonOut(w, 200, map[string]any{"ok": true, "status": map[bool]string{true: "running", false: "stopped"}[*in.Enabled], "persistent": false, "message": "运行状态只对当前进程有效，进程重启后恢复运行"})
	case r.URL.Path == "/api/config" && r.Method == "GET":
		current := s.Config.Get()
		info, statErr := os.Stat(current.SourcePath)
		if statErr != nil {
			apiError(w, 500, statErr)
			return
		}
		if info.Mode().Perm()&0077 != 0 {
			jsonOut(w, http.StatusForbidden, map[string]any{"error": "configuration file permissions are too broad; require 0600"})
			return
		}
		raw, e := os.ReadFile(current.SourcePath)
		if e != nil {
			apiError(w, 500, e)
			return
		}
		yamlText := string(raw)
		if current.Logging.WebRedaction {
			yamlText = redactConfigYAML(yamlText)
		}
		jsonOut(w, 200, map[string]any{"yaml": yamlText, "hash": current.Hash, "config": current})
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
		current := s.Config.Get()
		jsonOut(w, 200, map[string]any{"providers": publicProviders(current, s.providerHealth(), current.Logging.WebRedaction)})
	case r.URL.Path == "/api/providers/detect" && r.Method == "POST":
		s.detectProvider(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/providers/") && strings.HasSuffix(r.URL.Path, "/probe") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/probe")
		s.probe(w, r, id)
	case r.URL.Path == "/api/routes/explain" && (r.Method == "GET" || r.Method == "POST"):
		s.explain(w, r)
	case r.URL.Path == "/api/apps" || strings.HasPrefix(r.URL.Path, "/api/apps/"):
		s.applicationAPI(w, r)
	case r.URL.Path == "/api/claude-code/config" && r.Method == "GET":
		s.legacyClaudeCodeConfig(w, r)
	case r.URL.Path == "/api/claude-code/config" && r.Method == "PUT":
		s.legacyClaudeCodeConfig(w, r)
	case r.URL.Path == "/api/playground/preview" && r.Method == "POST":
		s.preview(w, r)
	case r.URL.Path == "/api/logs" && r.Method == "GET":
		limit := 100
		fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
		logs := s.Logs.List(limit)
		for i := range logs {
			logs[i].RequestBody = ""
			logs[i].ResponseBody = ""
		}
		jsonOut(w, 200, map[string]any{"logs": logs})
	case strings.HasPrefix(r.URL.Path, "/api/logs/") && r.Method == "GET":
		id := strings.TrimPrefix(r.URL.Path, "/api/logs/")
		v, ok := s.Logs.Get(id)
		if !ok {
			jsonOut(w, 404, map[string]any{"error": "not found"})
			return
		}
		if s.Config.Get().Logging.WebRedaction {
			v.RequestBody = redactLogBody(v.RequestBody)
			v.ResponseBody = redactLogBody(v.ResponseBody)
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

func configuredModelCount(c *config.Config) int {
	count := 0
	for _, provider := range c.Providers {
		seen := make(map[string]struct{}, len(provider.Models))
		for _, model := range provider.Models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, exists := seen[model]; exists {
				continue
			}
			seen[model] = struct{}{}
			count++
		}
	}
	return count
}

func applicationConfigurationCounts(ctx context.Context, registry *application.Registry) (configured, total int) {
	if registry == nil {
		return 0, 0
	}
	for _, adapter := range registry.List() {
		total++
		reader, ok := adapter.(application.ConfigurationStatusReader)
		if !ok {
			continue
		}
		synced, err := reader.ConfigurationSynced(ctx)
		if err == nil && synced {
			configured++
		}
	}
	return configured, total
}

func redactLogBody(raw string) string {
	if raw == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	redactWebValue(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(redacted)
}

func sensitiveWebKey(key string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
	compact := strings.ReplaceAll(normalized, "_", "")
	return normalized == "key" || normalized == "token" || strings.HasSuffix(normalized, "_key") || strings.HasSuffix(normalized, "_token") || strings.HasSuffix(compact, "apikey") || strings.HasSuffix(compact, "accesstoken") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "authorization") || strings.Contains(normalized, "cookie") || strings.Contains(normalized, "password")
}

func collectSensitiveWebValues(value any, parentKey string, values map[string]struct{}) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			collectSensitiveWebValues(child, key, values)
		}
	case []any:
		for _, child := range item {
			collectSensitiveWebValues(child, parentKey, values)
		}
	case string:
		if sensitiveWebKey(parentKey) && item != "" && item != webRedactionMask {
			values[item] = struct{}{}
		}
	}
}

func currentApplicationKey(state application.State) string {
	for _, key := range []string{"api_key", "ANTHROPIC_API_KEY", "inferenceGatewayApiKey"} {
		if value, ok := state.Managed[key].(string); ok && value != "" && value != webRedactionMask {
			return value
		}
	}
	values := map[string]struct{}{}
	collectSensitiveWebValues(state.Managed, "", values)
	for value := range values {
		return value
	}
	return ""
}

func restoreApplicationMaskedKey(ctx context.Context, adapter application.Adapter, raw json.RawMessage) json.RawMessage {
	var desired map[string]any
	if json.Unmarshal(raw, &desired) != nil || desired["api_key"] != webRedactionMask {
		return raw
	}
	state, err := adapter.Read(ctx)
	if err != nil {
		return raw
	}
	if key := currentApplicationKey(state); key != "" {
		desired["api_key"] = key
		restored, marshalErr := json.Marshal(desired)
		if marshalErr == nil {
			return restored
		}
	}
	return raw
}

func redactApplicationPreview(ctx context.Context, adapter application.Adapter, preview *application.Preview) {
	values := map[string]struct{}{}
	type textPreview struct {
		target *json.RawMessage
		value  string
	}
	textPreviews := make([]textPreview, 0, 2)
	for _, target := range []*json.RawMessage{&preview.Current, &preview.Content} {
		var content any
		if json.Unmarshal(*target, &content) == nil {
			if text, ok := content.(string); ok {
				textPreviews = append(textPreviews, textPreview{target: target, value: text})
				continue
			}
			collectSensitiveWebValues(content, "", values)
			redactWebValue(content)
			if redacted, err := json.Marshal(content); err == nil {
				*target = redacted
			}
		}
	}
	if state, err := adapter.Read(ctx); err == nil {
		collectSensitiveWebValues(state.Managed, "", values)
	}
	for _, item := range textPreviews {
		lines := strings.Split(item.value, "\n")
		for index, line := range lines {
			position := strings.Index(line, "=")
			if position >= 0 && sensitiveWebKey(strings.TrimSpace(line[:position])) {
				lines[index] = line[:position+1] + " \"" + webRedactionMask + "\""
			}
		}
		redacted := strings.Join(lines, "\n")
		for value := range values {
			redacted = strings.ReplaceAll(redacted, value, webRedactionMask)
		}
		if raw, err := json.Marshal(redacted); err == nil {
			*item.target = raw
		}
	}
	for value := range values {
		preview.Diff = strings.ReplaceAll(preview.Diff, value, webRedactionMask)
	}
}

func redactWebValue(value any) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if sensitiveWebKey(key) {
				item[key] = webRedactionMask
			} else {
				redactWebValue(child)
			}
		}
	case []any:
		for _, child := range item {
			redactWebValue(child)
		}
	}
}

func redactConfigYAML(raw string) string {
	var document any
	if yaml.Unmarshal([]byte(raw), &document) != nil {
		return raw
	}
	redactWebValue(document)
	redacted, err := yaml.Marshal(document)
	if err != nil {
		return raw
	}
	return string(redacted)
}

func restoreMaskedConfigYAML(masked, original string) string {
	var edited, source any
	if yaml.Unmarshal([]byte(masked), &edited) != nil || yaml.Unmarshal([]byte(original), &source) != nil {
		return masked
	}
	restoreMaskedValue(edited, source)
	restored, err := yaml.Marshal(edited)
	if err != nil {
		return masked
	}
	return string(restored)
}

func restoreMaskedValue(edited, source any) {
	switch current := edited.(type) {
	case map[string]any:
		original, _ := source.(map[string]any)
		for key, value := range current {
			originalValue := original[key]
			text, isText := value.(string)
			if sensitiveWebKey(key) && isText && text == webRedactionMask {
				if originalValue != nil {
					current[key] = originalValue
				}
				continue
			}
			restoreMaskedValue(value, originalValue)
		}
	case []any:
		original, _ := source.([]any)
		for index, value := range current {
			var originalValue any
			if item, ok := value.(map[string]any); ok {
				if id, ok := item["id"].(string); ok {
					for _, candidate := range original {
						if candidateMap, ok := candidate.(map[string]any); ok && candidateMap["id"] == id {
							originalValue = candidate
							break
						}
					}
				}
			}
			if originalValue == nil && index < len(original) {
				originalValue = original[index]
			}
			restoreMaskedValue(value, originalValue)
		}
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
	previousConfig := s.Config.Get()
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
	if previousConfig.Logging.WebRedaction && strings.Contains(in.YAML, webRedactionMask) {
		original, readErr := os.ReadFile(previousConfig.SourcePath)
		if readErr != nil {
			apiError(w, 500, readErr)
			return
		}
		in.YAML = restoreMaskedConfigYAML(in.YAML, string(original))
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
	backup, e := safefile.Backup(target, ".bak.")
	if e != nil {
		apiError(w, 500, e)
		return
	}
	if e = safefile.AtomicWrite(target, []byte(in.YAML), 0600); e != nil {
		apiError(w, 500, e)
		return
	}
	c, e = config.Load(target)
	if e != nil {
		if backup != "" {
			if previous, readErr := os.ReadFile(backup); readErr == nil {
				_ = safefile.AtomicWrite(target, previous, 0600)
			}
		}
		apiError(w, 500, fmt.Errorf("saved configuration failed verification and was restored: %w", e))
		return
	}
	c.SourcePath = target
	effects := config.CompareEffects(previousConfig, c)
	if s.GatewayControl != nil {
		s.GatewayControl.ApplyRuntimeConfig(previousConfig, c)
	}
	if len(effects.RuntimeRebuilt) > 0 {
		if s.GatewayControl == nil {
			effects.RestartNeeded = append(effects.RestartNeeded, effects.RuntimeRebuilt...)
			effects.RuntimeRebuilt = nil
		}
	}
	s.Config.Replace(c)
	_ = safefile.Prune(target, ".bak.", 10)
	jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash, "backup": filepath.Base(backup), "hot_reloaded": effects.HotReloaded, "runtime_rebuilt": effects.RuntimeRebuilt, "restart_required": effects.RestartNeeded})
}

func (s *Server) backups(w http.ResponseWriter) {
	target := s.Config.Get().SourcePath
	entries, _ := safefile.List(target, ".bak.")
	var out []any
	for _, entry := range entries {
		out = append(out, map[string]any{"name": entry.Name, "size": entry.Size, "modified": entry.ModifiedAt, "contains_sensitive_config": true})
	}
	jsonOut(w, 200, map[string]any{"backups": out})
}
func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	previousConfig := s.Config.Get()
	var in struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.Contains(in.Name, "/") {
		jsonOut(w, 400, map[string]any{"error": "invalid backup name"})
		return
	}
	target := s.Config.Get().SourcePath
	source, ok := safefile.ResolveBackup(target, ".bak.", in.Name)
	if !ok {
		jsonOut(w, 400, map[string]any{"error": "invalid backup name"})
		return
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		apiError(w, 404, e)
		return
	}
	c, e := config.Load(source)
	if e != nil {
		apiError(w, 422, e)
		return
	}
	if _, e = safefile.Backup(target, ".bak."); e != nil {
		apiError(w, 500, e)
		return
	}
	if e = safefile.AtomicWrite(target, raw, 0600); e != nil {
		apiError(w, 500, e)
		return
	}
	c.SourcePath = target
	effects := config.CompareEffects(previousConfig, c)
	if s.GatewayControl != nil {
		s.GatewayControl.ApplyRuntimeConfig(previousConfig, c)
	} else if len(effects.RuntimeRebuilt) > 0 {
		effects.RestartNeeded = append(effects.RestartNeeded, effects.RuntimeRebuilt...)
		effects.RuntimeRebuilt = nil
	}
	s.Config.Replace(c)
	_ = safefile.Prune(target, ".bak.", 10)
	jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash, "hot_reloaded": effects.HotReloaded, "runtime_rebuilt": effects.RuntimeRebuilt, "restart_required": effects.RestartNeeded})
}

func (s *Server) probe(w http.ResponseWriter, r *http.Request, id string) {
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
	basic := s.runProviderCheck(r.Context(), *p, probeBasic)
	models := s.runProviderCheck(r.Context(), *p, probeModels)
	report := map[string]any{
		"ok":                basic.OK,
		"status":            basic.Status,
		"latency_ms":        basic.LatencyMS,
		"checked_at":        time.Now(),
		"detected_protocol": p.Protocol,
		"test_ok":           basic.OK,
		"test_status":       basic.Status,
		"models_ok":         models.OK,
		"models_status":     models.Status,
		"models_latency_ms": models.LatencyMS,
		"models":            discoverModels(models.body),
		"capabilities":      map[string]any{"basic": basic},
	}
	if !basic.OK {
		report["error"] = basic.Error
		report["error_code"] = basic.ErrorCode
	}
	if !models.OK {
		report["models_error"] = models.Error
		report["models_error_code"] = models.ErrorCode
	}
	if len(models.body) > 0 {
		report["models_response"] = json.RawMessage(validJSON(models.body))
	}
	s.setProviderHealth(id, report)
	jsonOut(w, 200, report)
}

func (s *Server) detectProvider(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL         string   `json:"base_url"`
		APIKey          string   `json:"api_key"`
		Models          []string `json:"models"`
		AllowPrivateURL bool     `json:"allow_private_url"`
		ForceRefresh    bool     `json:"force_refresh"`
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
	resolvedAPIKey, resolveErr := config.ResolveSecretInput(in.APIKey)
	if resolveErr != nil {
		apiError(w, http.StatusUnprocessableEntity, resolveErr)
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		s.streamProviderDetection(w, r, in.BaseURL, resolvedAPIKey, in.Models, in.AllowPrivateURL, in.ForceRefresh)
		return
	}
	result := s.detectProviderCapabilitiesWithOptions(r.Context(), in.BaseURL, resolvedAPIKey, in.Models, in.AllowPrivateURL, in.ForceRefresh)
	jsonOut(w, 200, result)
}

func (s *Server) streamProviderDetection(w http.ResponseWriter, r *http.Request, baseURL, apiKey string, models []string, allowPrivateURL, forceRefresh bool) {
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("x-accel-buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	writeEvent := func(event string, value any) {
		payload, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	writeEvent("progress", providerDetectionProgress{Stage: "start", Status: "running", Message: "开始自动检测模型服务"})
	result := s.detectProviderCapabilitiesWithProgress(r.Context(), baseURL, apiKey, models, allowPrivateURL, forceRefresh, func(progress providerDetectionProgress) {
		writeEvent("progress", progress)
	})
	writeEvent("result", result)
}

func detectProviderProfile(baseURL string, models []string) string {
	return providerprofile.DetectProfile(baseURL, models)
}

func detectedProviderLabel(protocolName ir.Protocol, profile string) string {
	if profile == "qwen3" {
		return "Qwen 3.x（OpenAI 兼容）"
	}
	if profile == "xiaomi-mimo" {
		name := "OpenAI Chat"
		if protocolName == ir.OpenAIResponses {
			name = "OpenAI Responses"
		}
		return "Xiaomi MiMo（" + name + "）"
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

func (s *Server) applicationAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/apps" {
		if r.Method != http.MethodGet {
			jsonOut(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		detect := r.URL.Query().Get("detect") != "false"
		apps := make([]map[string]any, 0)
		for _, adapter := range s.Applications.List() {
			detection := application.Detection{}
			if detect {
				var err error
				detection, err = adapter.Detect(r.Context())
				if err != nil {
					detection.Message = err.Error()
				}
			}
			apps = append(apps, map[string]any{"manifest": adapter.Manifest(), "detection": detection})
		}
		jsonOut(w, http.StatusOK, map[string]any{"apps": apps})
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "apps" {
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "application endpoint not found"})
		return
	}
	adapter, err := s.Applications.Get(parts[2])
	if err != nil {
		apiError(w, http.StatusNotFound, err)
		return
	}
	action := ""
	if len(parts) > 3 {
		action = parts[3]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		state, readErr := adapter.Read(r.Context())
		if readErr != nil {
			apiError(w, http.StatusUnprocessableEntity, readErr)
			return
		}
		if s.Config.Get().Logging.WebRedaction {
			redactWebValue(state.Managed)
		}
		jsonOut(w, http.StatusOK, state)
	case action == "preview" && r.Method == http.MethodPost:
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if readErr != nil {
			apiError(w, http.StatusBadRequest, readErr)
			return
		}
		raw = restoreApplicationMaskedKey(r.Context(), adapter, raw)
		preview, previewErr := adapter.Preview(r.Context(), raw)
		if previewErr != nil {
			apiError(w, http.StatusUnprocessableEntity, previewErr)
			return
		}
		if s.Config.Get().Logging.WebRedaction {
			redactApplicationPreview(r.Context(), adapter, &preview)
		}
		jsonOut(w, http.StatusOK, preview)
	case action == "config" && r.Method == http.MethodPut:
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if readErr != nil {
			apiError(w, http.StatusBadRequest, readErr)
			return
		}
		raw = restoreApplicationMaskedKey(r.Context(), adapter, raw)
		result, applyErr := adapter.Apply(r.Context(), raw)
		if applyErr != nil {
			apiError(w, http.StatusUnprocessableEntity, applyErr)
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "raw-config" && r.Method == http.MethodPut:
		rawAdapter, ok := adapter.(application.RawConfigAdapter)
		if !ok {
			jsonOut(w, http.StatusNotImplemented, map[string]any{"error": "application does not support editable previews"})
			return
		}
		var input application.RawConfig
		if decodeErr := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&input); decodeErr != nil {
			apiError(w, http.StatusBadRequest, decodeErr)
			return
		}
		if strings.TrimSpace(input.Content) == "" {
			jsonOut(w, http.StatusBadRequest, map[string]any{"error": "configuration content is required"})
			return
		}
		if strings.Contains(input.Content, webRedactionMask) {
			jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"error": "关闭网页脱敏后才能写入手动编辑的配置"})
			return
		}
		input.Config = restoreApplicationMaskedKey(r.Context(), adapter, input.Config)
		result, applyErr := rawAdapter.ApplyRaw(r.Context(), input)
		if applyErr != nil {
			apiError(w, http.StatusUnprocessableEntity, applyErr)
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "cleanup" && r.Method == http.MethodPost:
		cleanupAdapter, ok := adapter.(application.CleanupAdapter)
		if !ok {
			jsonOut(w, http.StatusNotImplemented, map[string]any{"error": "application does not support configuration cleanup"})
			return
		}
		result, cleanupErr := cleanupAdapter.Cleanup(r.Context())
		if cleanupErr != nil {
			apiError(w, http.StatusUnprocessableEntity, cleanupErr)
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "verify" && r.Method == http.MethodPost:
		var options application.VerifyOptions
		decodeErr := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&options)
		if decodeErr != nil && decodeErr != io.EOF {
			apiError(w, http.StatusBadRequest, decodeErr)
			return
		}
		options.Config = restoreApplicationMaskedKey(r.Context(), adapter, options.Config)
		result, verifyErr := adapter.Verify(r.Context(), options)
		if verifyErr != nil {
			apiError(w, http.StatusUnprocessableEntity, verifyErr)
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "backups" && r.Method == http.MethodGet:
		backups, backupErr := adapter.Backups(r.Context())
		if backupErr != nil {
			apiError(w, http.StatusInternalServerError, backupErr)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"backups": backups})
	case action == "backups" && r.Method == http.MethodDelete:
		var input struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input) != nil || input.Name == "" {
			jsonOut(w, http.StatusBadRequest, map[string]any{"error": "backup name is required"})
			return
		}
		if deleteErr := adapter.DeleteBackup(r.Context(), input.Name); deleteErr != nil {
			apiError(w, http.StatusUnprocessableEntity, deleteErr)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"ok": true, "name": input.Name})
	case action == "rollback" && r.Method == http.MethodPost:
		var input struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input) != nil || input.Name == "" {
			jsonOut(w, http.StatusBadRequest, map[string]any{"error": "backup name is required"})
			return
		}
		result, rollbackErr := adapter.Rollback(r.Context(), input.Name)
		if rollbackErr != nil {
			apiError(w, http.StatusUnprocessableEntity, rollbackErr)
			return
		}
		jsonOut(w, http.StatusOK, result)
	default:
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "application endpoint not found"})
	}
}

func (s *Server) legacyClaudeCodeConfig(w http.ResponseWriter, r *http.Request) {
	adapter, err := s.Applications.Get("claude-code")
	if err != nil {
		apiError(w, http.StatusNotFound, err)
		return
	}
	if r.Method == http.MethodGet {
		state, readErr := adapter.Read(r.Context())
		if readErr != nil {
			apiError(w, http.StatusUnprocessableEntity, readErr)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"path": state.Path, "exists": state.Exists, "router": state.Managed, "preserved_fields": state.PreservedFields})
		return
	}
	raw, readErr := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if readErr != nil {
		apiError(w, http.StatusBadRequest, readErr)
		return
	}
	result, applyErr := adapter.Apply(r.Context(), raw)
	if applyErr != nil {
		apiError(w, http.StatusUnprocessableEntity, applyErr)
		return
	}
	jsonOut(w, http.StatusOK, result)
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
		var policyDiagnostics []ir.Diagnostic
		upstream, policyDiagnostics, err = providerprofile.PrepareRequest(upstream, target.Provider, request)
		diagnostics = append(diagnostics, policyDiagnostics...)
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
	firstTokenCount := m.FirstTokenBuckets[len(m.FirstTokenBuckets)-1].Load()
	avgFirstToken := uint64(0)
	if firstTokenCount > 0 {
		avgFirstToken = m.FirstTokenMSTotal.Load() / firstTokenCount
	}
	records := []observe.Record{}
	if completed > 0 {
		sampleLimit := 500
		if completed < uint64(sampleLimit) {
			sampleLimit = int(completed)
		}
		records = logs.List(sampleLimit)
	}
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
	return map[string]any{"requests": m.Requests.Load(), "errors": m.Errors.Load(), "in_flight": m.InFlight.Load(), "retries": m.Retries.Load(), "fallbacks": m.Fallbacks.Load(), "timeouts": m.Timeouts.Load(), "cancellations": m.Cancellations.Load(), "diagnostics": m.Diagnostics.Load(), "input_tokens": m.InputTokens.Load(), "output_tokens": m.OutputTokens.Load(), "average_latency_ms": avg, "average_first_token_ms": avgFirstToken, "p50_latency_ms": p50, "p95_latency_ms": p95}
}
func publicProviders(current *config.Config, health map[string]map[string]any, redact bool) []any {
	out := make([]any, 0, len(current.Providers))
	for _, p := range current.Providers {
		apiKey := p.APIKey
		if redact && apiKey != "" {
			apiKey = webRedactionMask
		}
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "profile": p.Profile, "protocol": p.Protocol, "codex_integration": p.CodexIntegration, "codex_compatibility": p.CodexCompatibility, "compatibility_mode": p.CompatibilityMode, "tool_choice_mode": p.ToolChoiceMode, "reasoning_history": p.ReasoningHistory, "reasoning_with_tools": p.ReasoningWithTools, "base_url": p.BaseURL, "models": p.Models, "api_key_set": p.APIKey != "", "api_key": apiKey, "request_policy": p.RequestPolicy, "health": health[p.ID]})
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
func apiError(w http.ResponseWriter, status int, e error) {
	jsonOut(w, status, map[string]any{"error": e.Error()})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
