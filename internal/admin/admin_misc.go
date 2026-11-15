package admin

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/observe"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
	providerprofile "github.com/soooooollee/ai-router/internal/provider"
	"github.com/soooooollee/ai-router/internal/routing"
)

//go:embed webdist/*
var assets embed.FS

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
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	b, e := assets.ReadFile("webdist/" + name)
	if e != nil {
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
		b, e = assets.ReadFile("webdist/index.html")
	}
	if e != nil {
		http.Error(w, "web console not built", 503)
		return
	}
	if typ := mime.TypeByExtension(path.Ext(name)); typ != "" {
		w.Header().Set("content-type", typ)
	}
	w.Header().Set("cache-control", "no-cache")
	_, _ = w.Write(b)
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
