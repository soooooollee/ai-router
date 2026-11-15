package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
	providerprofile "github.com/zbss/airoute/internal/provider"
)

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

func providerModelsURL(p config.Provider) string {
	if providerprofile.IsOpenCodeGo(p.BaseURL) && strings.Contains(p.BaseURL, "/inference/go/") {
		u, _ := url.Parse(p.BaseURL)
		u.Path = "/inference/go/v1/models"
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	}
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
