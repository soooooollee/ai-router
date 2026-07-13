package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/zbss/airoute/internal/streaming"
	"github.com/zbss/airoute/internal/tokencount"
)

type Gateway struct {
	Config           *config.Store
	Registry         *protocol.Registry
	Logs             *observe.Store
	Metrics          *observe.Metrics
	Client           *http.Client
	RestrictedClient *http.Client
	Logger           *slog.Logger
	sem              chan struct{}
	semMu            sync.RWMutex
	clientMu         sync.RWMutex
	levelController  *slog.LevelVar
	enabled          atomic.Bool
}

var errLossyConversion = errors.New("request requires a lossy protocol conversion")

func New(c *config.Store, r *protocol.Registry, logs *observe.Store, metrics *observe.Metrics, logger *slog.Logger) *Gateway {
	current := c.Get()
	maxConcurrent := current.Server.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 256
	}
	standard, restricted := runtimeClients(maxConcurrent)
	g := &Gateway{Config: c, Registry: r, Logs: logs, Metrics: metrics, Client: standard, RestrictedClient: restricted, Logger: logger, sem: make(chan struct{}, maxConcurrent)}
	g.Logs.Configure(current.Logging.RequestHistory, effectiveLogFile(current))
	g.enabled.Store(true)
	return g
}

func (g *Gateway) SetEnabled(enabled bool) { g.enabled.Store(enabled) }
func (g *Gateway) IsEnabled() bool         { return g.enabled.Load() }

func runtimeClients(maxConcurrent int) (*http.Client, *http.Client) {
	redirectPolicy := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	maxIdle := max(100, maxConcurrent*2)
	standardTransport := &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: maxIdle, MaxIdleConnsPerHost: maxConcurrent, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 10 * time.Minute, ForceAttemptHTTP2: true}
	restrictedTransport := &http.Transport{Proxy: nil, DialContext: secure.PublicDialContext, MaxIdleConns: maxIdle, MaxIdleConnsPerHost: maxConcurrent, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 10 * time.Minute, ForceAttemptHTTP2: true}
	return &http.Client{Transport: standardTransport, CheckRedirect: redirectPolicy}, &http.Client{Transport: restrictedTransport, CheckRedirect: redirectPolicy}
}

func (g *Gateway) ApplyRuntimeConfig(previous, next *config.Config) {
	if previous == nil || next == nil {
		return
	}
	g.Logs.Configure(next.Logging.RequestHistory, effectiveLogFile(next))
	if g.levelController != nil && previous.Logging.Level != next.Logging.Level {
		g.levelController.Set(logLevel(next.Logging.Level))
	}
	if previous.Server.MaxConcurrent == next.Server.MaxConcurrent {
		return
	}
	limit := next.Server.MaxConcurrent
	if limit < 1 {
		limit = 256
	}
	standard, restricted := runtimeClients(limit)
	g.semMu.Lock()
	g.sem = make(chan struct{}, limit)
	g.semMu.Unlock()
	g.clientMu.Lock()
	oldStandard, oldRestricted := g.Client, g.RestrictedClient
	g.Client, g.RestrictedClient = standard, restricted
	g.clientMu.Unlock()
	oldStandard.CloseIdleConnections()
	oldRestricted.CloseIdleConnections()
}

func effectiveLogFile(c *config.Config) string {
	if c == nil || !c.Logging.Persist {
		return ""
	}
	if strings.TrimSpace(c.Logging.File) != "" {
		return c.Logging.File
	}
	base := "."
	if strings.TrimSpace(c.SourcePath) != "" {
		base = filepath.Dir(c.SourcePath)
	}
	return filepath.Join(base, "airoute-requests.jsonl")
}

func (g *Gateway) SetLogLevelController(controller *slog.LevelVar) {
	g.levelController = controller
}

func logLevel(value string) slog.Level {
	switch value {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// RuntimeLimits exposes the active runtime object limits for diagnostics and
// acceptance tests; values come from live objects, not the stored config.
func (g *Gateway) RuntimeLimits() (concurrent, idlePerHost int) {
	g.semMu.RLock()
	concurrent = cap(g.sem)
	g.semMu.RUnlock()
	g.clientMu.RLock()
	if transport, ok := g.Client.Transport.(*http.Transport); ok {
		idlePerHost = transport.MaxIdleConnsPerHost
	}
	g.clientMu.RUnlock()
	return concurrent, idlePerHost
}

func (g *Gateway) CloseIdleConnections() {
	g.clientMu.RLock()
	standard, restricted := g.Client, g.RestrictedClient
	g.clientMu.RUnlock()
	standard.CloseIdleConnections()
	restricted.CloseIdleConnections()
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := requestID()
	w.Header().Set("x-airoute-request-id", id)
	if r.URL.Path == "/healthz" {
		jsonOut(w, 200, map[string]any{"status": "ok"})
		return
	}
	if r.URL.Path == "/readyz" {
		c := g.Config.Get()
		if !g.IsEnabled() {
			jsonOut(w, http.StatusServiceUnavailable, map[string]any{"status": "stopped", "config_version": c.Hash})
			return
		}
		jsonOut(w, 200, map[string]any{"status": "ready", "config_version": c.Hash})
		return
	}
	if r.URL.Path == g.Config.Get().Metrics.Path && g.Config.Get().Metrics.Enabled {
		g.serveMetrics(w)
		return
	}
	if !g.IsEnabled() {
		errorOut(w, ir.OpenAIChat, http.StatusServiceUnavailable, "service_unavailable", "AI Router gateway is paused", id)
		return
	}
	c := g.Config.Get()
	if c.Server.RequestTimeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), c.Server.RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	maxHeaders := c.Server.MaxHeaders
	if maxHeaders < 1 {
		maxHeaders = 100
	}
	if len(r.Header) > maxHeaders {
		errorOut(w, ir.OpenAIChat, http.StatusRequestHeaderFieldsTooLarge, "invalid_request", "too many request headers", id)
		return
	}
	clientKeyID, ok := auth.ClientKey(r, c)
	if !ok {
		errorOut(w, ir.OpenAIChat, 401, "authentication_error", "invalid or missing API key", id)
		return
	}
	if r.URL.Path == "/v1/models" {
		g.serveModels(w)
		return
	}
	if r.URL.Path == "/v1/messages/count_tokens" || r.URL.Path == "/messages/count_tokens" {
		g.countTokens(w, r, c, id)
		return
	}
	p, model, ok := protocolFromPath(r.URL.Path)
	if !ok {
		errorOut(w, ir.OpenAIChat, 404, "not_found", "unknown gateway endpoint", id)
		return
	}
	g.semMu.RLock()
	sem := g.sem
	g.semMu.RUnlock()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default:
		errorOut(w, p, http.StatusTooManyRequests, "rate_limited", "gateway concurrency limit reached", id)
		return
	}
	if r.Method != "POST" {
		errorOut(w, p, 405, "invalid_request", "method not allowed", id)
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		errorOut(w, p, http.StatusUnsupportedMediaType, "invalid_request", "content-type must be application/json", id)
		return
	}
	g.handle(w, r, c, p, model, id, clientKeyID)
}

func (g *Gateway) countTokens(w http.ResponseWriter, r *http.Request, c *config.Config, id string) {
	if r.Method != "POST" {
		errorOut(w, ir.Anthropic, 405, "invalid_request", "method not allowed", id)
		return
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		errorOut(w, ir.Anthropic, http.StatusUnsupportedMediaType, "invalid_request", "content-type must be application/json", id)
		return
	}
	body, err := readRequestBody(r, c.Server.MaxBodySize)
	if err != nil {
		errorOut(w, ir.Anthropic, 400, "invalid_request", err.Error(), id)
		return
	}
	a, _ := g.Registry.Get(ir.Anthropic)
	req, _, err := a.DecodeRequest(r.Context(), body)
	if err != nil {
		errorOut(w, ir.Anthropic, 400, "invalid_request", err.Error(), id)
		return
	}
	req.Model = decodeClaudeAppRouteModel(req.Model)
	headers := map[string]string{}
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	decision, routeErr := routing.Resolve(c, routing.Input{Request: req, Protocol: ir.Anthropic, Headers: headers})
	if routeErr == nil && len(decision.Targets) > 0 {
		target := decision.Targets[0]
		if providerprofile.ProviderCapabilities(target.Provider, target.Model).NativeTokenCount {
			if tokens, nativeErr := g.nativeTokenCount(r.Context(), req, target); nativeErr == nil {
				jsonOut(w, 200, map[string]any{"input_tokens": tokens, "estimated": false, "strategy": "provider-native", "provider": target.Provider.ID, "model": target.Model})
				return
			}
		}
	}
	result := (tokencount.Heuristic{}).Count(req)
	jsonOut(w, 200, result)
}

func (g *Gateway) nativeTokenCount(ctx context.Context, request *ir.Request, target routing.ResolvedTarget) (int, error) {
	canonical := *request
	canonical.Model = target.Model
	adapter, err := g.Registry.Get(ir.Anthropic)
	if err != nil {
		return 0, err
	}
	payload, _, err := adapter.EncodeRequest(ctx, &canonical)
	if err != nil {
		return 0, err
	}
	var document map[string]any
	if err = json.Unmarshal(payload, &document); err != nil {
		return 0, err
	}
	for _, key := range []string{"max_tokens", "stream", "temperature", "top_p", "top_k", "stop_sequences"} {
		delete(document, key)
	}
	payload, err = json.Marshal(document)
	if err != nil {
		return 0, err
	}
	endpoint := appendEndpoint(strings.TrimRight(target.Provider.BaseURL, "/"), "/messages/count_tokens")
	if err = secure.ValidatePublicTarget(ctx, endpoint, target.Provider.AllowPrivateURL); err != nil {
		return 0, err
	}
	timeout := target.Provider.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", target.Provider.APIKey)
	for key, value := range target.Provider.Headers {
		if allowedProviderHeader(key) {
			req.Header.Set(key, value)
		}
	}
	g.clientMu.RLock()
	client := g.RestrictedClient
	if target.Provider.AllowPrivateURL {
		client = g.Client
	}
	g.clientMu.RUnlock()
	response, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return 0, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("native token count returned HTTP %d", response.StatusCode)
	}
	var result struct {
		InputTokens int `json:"input_tokens"`
	}
	if err = json.Unmarshal(responseBody, &result); err != nil || result.InputTokens < 1 {
		return 0, errors.New("native token count returned an invalid response")
	}
	return result.InputTokens, nil
}

func (g *Gateway) handle(w http.ResponseWriter, r *http.Request, c *config.Config, clientProtocol ir.Protocol, pathModel, id, clientKeyID string) {
	start := time.Now()
	g.Logs.Configure(c.Logging.RequestHistory, effectiveLogFile(c))
	g.Metrics.Requests.Add(1)
	g.Metrics.InFlight.Add(1)
	record := observe.Record{ID: id, StartedAt: start, ClientProtocol: clientProtocol, ClientKeyID: clientKeyID, ConfigVersion: c.Hash}
	defer func() {
		if errors.Is(r.Context().Err(), context.Canceled) {
			record.ErrorCode = "client_cancelled"
			g.Metrics.Cancellations.Add(1)
		} else if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
			if record.ErrorCode == "" {
				record.ErrorCode = "upstream_timeout"
			}
			g.Metrics.Timeouts.Add(1)
		}
		g.Metrics.InFlight.Add(-1)
		record.DurationMS = time.Since(start).Milliseconds()
		g.Metrics.Completed.Add(1)
		g.Metrics.LatencyMSTotal.Add(uint64(max(int64(0), record.DurationMS)))
		g.Metrics.FirstTokenMSTotal.Add(uint64(max(int64(0), record.FirstTokenMS)))
		g.Metrics.InputTokens.Add(uint64(max(0, record.Usage.InputTokens)))
		g.Metrics.OutputTokens.Add(uint64(max(0, record.Usage.OutputTokens)))
		g.Metrics.Diagnostics.Add(uint64(len(record.Diagnostics)))
		for _, diagnostic := range record.Diagnostics {
			record.DiagnosticCodes = append(record.DiagnosticCodes, diagnostic.Code)
		}
		metricModel := ""
		if c.Metrics.ModelLabels {
			metricModel = record.ResolvedModel
		}
		g.Metrics.Record(clientProtocol, record.ProviderID, metricModel, record.Status, record.DurationMS, record.FirstTokenMS)
		g.Logs.Add(record)
		g.Logger.Info("request completed", "request_id", id, "config_version", record.ConfigVersion, "client_protocol", clientProtocol, "requested_model", record.RequestedModel, "route_id", record.RouteID, "provider_id", record.ProviderID, "upstream_protocol", record.UpstreamProtocol, "resolved_model", record.ResolvedModel, "status_code", record.Status, "latency_ms", record.DurationMS, "first_token_ms", record.FirstTokenMS, "input_tokens", record.Usage.InputTokens, "output_tokens", record.Usage.OutputTokens, "error_code", record.ErrorCode, "diagnostic_codes", record.DiagnosticCodes)
	}()
	body, err := readRequestBody(r, c.Server.MaxBodySize)
	if err != nil {
		record.Status = 400
		record.ErrorCode = "invalid_request"
		g.Metrics.Errors.Add(1)
		errorOut(w, clientProtocol, 400, "invalid_request", err.Error(), id)
		return
	}
	if clientProtocol == ir.Gemini {
		body = injectGemini(body, pathModel, strings.Contains(r.URL.Path, "streamGenerateContent"))
	}
	if c.Logging.CaptureBodies {
		record.RequestBody = string(body)
	}
	inAdapter, err := g.Registry.Get(clientProtocol)
	if err != nil {
		record.Status = 400
		errorOut(w, clientProtocol, 400, "invalid_request", err.Error(), id)
		return
	}
	canonical, diagnostics, err := inAdapter.DecodeRequest(r.Context(), body)
	if err != nil {
		record.Status = 400
		record.ErrorCode = "protocol_conversion_error"
		g.Metrics.Errors.Add(1)
		errorOut(w, clientProtocol, 400, "protocol_conversion_error", err.Error(), id)
		return
	}
	canonical.ID = id
	record.RequestedModel = canonical.Model
	canonical.Model = decodeClaudeAppRouteModel(canonical.Model)
	record.Diagnostics = append(record.Diagnostics, diagnostics...)
	if c.Conversion.RemoteImagePolicy == "reject" && common.HasType(canonical, "image_url") {
		record.Status = 422
		record.ErrorCode = "protocol_conversion_error"
		errorOut(w, clientProtocol, 422, "protocol_conversion_error", "remote image URLs are rejected by configuration", id)
		return
	}
	if hasErrorDiagnostic(diagnostics) {
		record.Status = 422
		errorOut(w, clientProtocol, 422, "protocol_conversion_error", "request requires a lossy protocol conversion", id)
		return
	}
	headers := map[string]string{}
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	decision, err := routing.Resolve(c, routing.Input{Request: canonical, Protocol: clientProtocol, Headers: headers})
	if err != nil {
		record.Status = 404
		record.ErrorCode = "not_found"
		errorOut(w, clientProtocol, 404, "not_found", err.Error(), id)
		return
	}
	record.RouteID = decision.RouteID
	var upstream *http.Response
	var target routing.ResolvedTarget
	var finalErr error
	var finalStatus int
	var finalBody []byte
	var streamDecoder *streaming.Decoder
	var prefetched []streaming.Event
	for ti, t := range decision.Targets {
		finalStatus = 0
		finalBody = nil
		finalErr = nil
		target = t
		if ti > 0 {
			g.Metrics.Fallbacks.Add(1)
		}
		canonical.Model = t.Model
		if clientProtocol != t.Provider.Protocol && c.Conversion.UnsupportedFields == "strict" && common.IsLossy(diagnostics) {
			finalErr = errLossyConversion
			continue
		}
		outAdapter, e := g.Registry.Get(t.Provider.Protocol)
		if e != nil {
			finalErr = e
			continue
		}
		var payload []byte
		var d []ir.Diagnostic
		if clientProtocol == t.Provider.Protocol {
			payload, e = rewriteNativeRequest(body, t.Model, t.Provider.Protocol)
		} else {
			payload, d, e = outAdapter.EncodeRequest(r.Context(), canonical)
			if e == nil {
				payload = sanitizeProviderPayload(payload, t.Provider.Protocol)
			}
		}
		if e == nil {
			payload, e = providerprofile.PrepareRequest(payload, t.Provider, canonical)
		}
		record.Diagnostics = append(record.Diagnostics, d...)
		if e != nil {
			finalErr = e
			continue
		}
		if clientProtocol != t.Provider.Protocol && c.Conversion.UnsupportedFields == "strict" && common.IsLossy(d) {
			finalErr = errLossyConversion
			continue
		}
		for attempt := 1; attempt <= c.Retry.MaxAttempts; attempt++ {
			aStart := time.Now()
			upstream, e = g.do(r.Context(), t.Provider, payload, canonical.Stream, t.Model)
			a := observe.Attempt{Number: attempt, ProviderID: t.Provider.ID, Model: t.Model, DurationMS: time.Since(aStart).Milliseconds()}
			if e != nil {
				a.Error = e.Error()
				record.Attempts = append(record.Attempts, a)
				finalErr = e
			} else {
				a.Status = upstream.StatusCode
				record.Attempts = append(record.Attempts, a)
				if upstream.StatusCode >= 200 && upstream.StatusCode < 300 {
					finalErr = nil
					break
				}
				errBody, _ := io.ReadAll(io.LimitReader(upstream.Body, 1<<20))
				upstream.Body.Close()
				finalErr = fmt.Errorf("upstream returned %d: %s", upstream.StatusCode, redactSnippet(errBody))
				finalStatus = upstream.StatusCode
				finalBody = errBody
				if !retryStatus(c.Retry.OnStatus, upstream.StatusCode) {
					break
				}
			}
			if attempt < c.Retry.MaxAttempts {
				g.Metrics.Retries.Add(1)
				if !sleepContext(r.Context(), backoff(c.Retry, attempt, upstream)) {
					finalErr = r.Context().Err()
					break
				}
			}
		}
		if finalErr == nil && canonical.Stream {
			streamDecoder = streaming.NewDecoder(upstream.Body, 8<<20)
			prefetched = nil
			for {
				firstEvent, streamErr := streamDecoder.Next()
				if streamErr != nil {
					_ = upstream.Body.Close()
					finalErr = fmt.Errorf("upstream stream failed before first event: %w", streamErr)
					finalStatus = 0
					if len(record.Attempts) > 0 {
						record.Attempts[len(record.Attempts)-1].Error = finalErr.Error()
					}
					break
				}
				prefetched = append(prefetched, firstEvent)
				events, _, streamErr := outAdapter.DecodeStreamEvent(r.Context(), firstEvent.Name, firstEvent.Data)
				if streamErr != nil {
					_ = upstream.Body.Close()
					finalErr = fmt.Errorf("upstream stream was invalid before first event: %w", streamErr)
					finalStatus = 0
					if len(record.Attempts) > 0 {
						record.Attempts[len(record.Attempts)-1].Error = finalErr.Error()
					}
					break
				}
				if len(events) > 0 {
					break
				}
			}
		}
		if finalErr == nil {
			break
		}
		if ti+1 < len(decision.Targets) && !fallbackAllowed(c.Fallback, finalStatus, finalBody, finalErr) {
			break
		}
	}
	if finalErr != nil || upstream == nil {
		if target.Provider.ID != "" {
			record.ProviderID = target.Provider.ID
			record.UpstreamProtocol = target.Provider.Protocol
			record.ResolvedModel = target.Model
		}
		if c.Logging.CaptureBodies && len(finalBody) > 0 {
			record.ResponseBody = string(finalBody)
		}
		if errors.Is(finalErr, errLossyConversion) {
			record.Status = 422
			record.ErrorCode = "protocol_conversion_error"
			g.Metrics.Errors.Add(1)
			errorOut(w, clientProtocol, 422, "protocol_conversion_error", finalErr.Error(), id)
			return
		}
		status, typ, message := normalizeUpstreamError(finalStatus, finalBody, finalErr)
		record.Status = status
		record.ErrorCode = typ
		g.Metrics.Errors.Add(1)
		errorOut(w, clientProtocol, status, typ, message, id)
		return
	}
	record.ProviderID = target.Provider.ID
	record.UpstreamProtocol = target.Provider.Protocol
	record.ResolvedModel = target.Model
	w.Header().Set("x-airoute-provider-id", target.Provider.ID)
	w.Header().Set("x-airoute-model", target.Model)
	record.Status = upstream.StatusCode
	if clientProtocol == target.Provider.Protocol {
		record.Diagnostics = nil
	}
	for _, d := range record.Diagnostics {
		w.Header().Add("x-airoute-diagnostic", d.Code)
	}
	if canonical.Stream {
		g.stream(w, r.Context(), upstream, streamDecoder, prefetched, clientProtocol, target.Provider.Protocol, target.Model, id, c.Logging.CaptureBodies, &record)
		return
	}
	rawResp, err := io.ReadAll(io.LimitReader(upstream.Body, c.Server.MaxBodySize))
	upstream.Body.Close()
	if c.Logging.CaptureBodies {
		record.ResponseBody = string(rawResp)
	}
	if err != nil {
		record.Status = 502
		record.ErrorCode = "upstream_unavailable"
		g.Metrics.Errors.Add(1)
		errorOut(w, clientProtocol, 502, "upstream_unavailable", err.Error(), id)
		return
	}
	outAdapter, _ := g.Registry.Get(target.Provider.Protocol)
	canonicalResp, d, err := outAdapter.DecodeResponse(r.Context(), rawResp)
	record.Diagnostics = append(record.Diagnostics, d...)
	if err != nil {
		record.Status = 502
		record.ErrorCode = "protocol_conversion_error"
		g.Metrics.Errors.Add(1)
		errorOut(w, clientProtocol, 502, "protocol_conversion_error", err.Error(), id)
		return
	}
	canonicalResp, err = ir.AggregateEvents(ir.EventsFromResponse(canonicalResp))
	if err != nil {
		record.Status = 502
		record.ErrorCode = "protocol_conversion_error"
		g.Metrics.Errors.Add(1)
		errorOut(w, clientProtocol, 502, "protocol_conversion_error", err.Error(), id)
		return
	}
	canonicalResp.Model = target.Model
	encoded, d, err := inAdapter.EncodeResponse(r.Context(), canonicalResp)
	record.Diagnostics = append(record.Diagnostics, d...)
	if err != nil {
		record.Status = 500
		record.ErrorCode = "protocol_conversion_error"
		errorOut(w, clientProtocol, 500, "protocol_conversion_error", err.Error(), id)
		return
	}
	record.Usage = canonicalResp.Usage
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(upstream.StatusCode)
	_, _ = w.Write(encoded)
}

// Claude App only accepts Claude-shaped model identifiers in its discovery
// profile. The application adapter encodes the real AI Router alias as hex so
// the gateway can restore it before normal route matching.
func decodeClaudeAppRouteModel(model string) string {
	const prefix = "anthropic/claude-ccr-h"
	if !strings.HasPrefix(strings.ToLower(model), prefix) {
		return model
	}
	decoded, err := hex.DecodeString(model[len(prefix):])
	if err != nil || len(decoded) == 0 {
		return model
	}
	return string(decoded)
}

func (g *Gateway) do(ctx context.Context, p config.Provider, payload []byte, stream bool, model string) (*http.Response, error) {
	endpoint, err := providerEndpoint(p, stream, model)
	if err != nil {
		return nil, err
	}
	if err := secure.ValidatePublicTarget(ctx, endpoint, p.AllowPrivateURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer func() {
		if ctx.Err() != nil {
			cancel()
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	switch p.Protocol {
	case ir.Anthropic:
		req.Header.Set("x-api-key", p.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case ir.Gemini:
		req.Header.Set("x-goog-api-key", p.APIKey)
	default:
		req.Header.Set("authorization", "Bearer "+p.APIKey)
	}
	for k, v := range p.Headers {
		if allowedProviderHeader(k) {
			req.Header.Set(k, v)
		}
	}
	g.clientMu.RLock()
	client := g.RestrictedClient
	if p.AllowPrivateURL {
		client = g.Client
	}
	g.clientMu.RUnlock()
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelBody) Close() error { e := c.ReadCloser.Close(); c.cancel(); return e }

func (g *Gateway) stream(w http.ResponseWriter, ctx context.Context, resp *http.Response, dec *streaming.Decoder, prefetched []streaming.Event, clientProtocol, providerProtocol ir.Protocol, model, id string, captureBody bool, record *observe.Record) {
	defer resp.Body.Close()
	decoderAdapter, _ := g.Registry.Get(providerProtocol)
	encoderAdapter, _ := g.Registry.Get(clientProtocol)
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	if dec == nil {
		dec = streaming.NewDecoder(resp.Body, 8<<20)
	}
	first := true
	responseID := id
	pendingGeminiTools := map[int]ir.ContentBlock{}
	pendingGeminiArgs := map[int]string{}
	sequence := 0
	normalizer := ir.NewStreamNormalizer()
	var lastUsage *ir.Usage
	capturedEvents := make([]ir.Event, 0, 64)
	capturedBytes := 0
	captureTruncated := false
	if captureBody {
		defer func() {
			payload, _ := json.Marshal(map[string]any{"stream": true, "truncated": captureTruncated, "events": capturedEvents})
			record.ResponseBody = string(payload)
		}()
	}
	for eventIndex := 0; ; eventIndex++ {
		var se streaming.Event
		var err error
		if eventIndex < len(prefetched) {
			se = prefetched[eventIndex]
		} else {
			se, err = dec.Next()
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				record.ErrorCode = "upstream_stream_error"
				g.Metrics.Errors.Add(1)
				writeEncodedStreamError(w, flusher, encoderAdapter, ctx, responseID, "upstream_unavailable", err.Error())
			}
			return
		}
		events, d, err := decoderAdapter.DecodeStreamEvent(ctx, se.Name, se.Data)
		record.Diagnostics = append(record.Diagnostics, d...)
		if err != nil {
			record.ErrorCode = "protocol_conversion_error"
			g.Metrics.Errors.Add(1)
			writeEncodedStreamError(w, flusher, encoderAdapter, ctx, responseID, "protocol_conversion_error", err.Error())
			return
		}
		for _, event := range events {
			if event.Model == "" {
				event.Model = model
			}
			if event.ResponseID != "" {
				responseID = event.ResponseID
			} else {
				event.ResponseID = responseID
			}
			if first && (event.Type == "text.delta" || event.Type == "reasoning.delta" || event.Type == "tool_call.start") {
				first = false
				record.FirstTokenMS = time.Since(record.StartedAt).Milliseconds()
			}
			if event.Usage != nil {
				u := *event.Usage
				lastUsage = &u
				record.Usage = *event.Usage
			}
			for _, normalized := range normalizer.Push(event) {
				sequence++
				normalized.Sequence = sequence
				if captureBody {
					eventSize := len(normalized.Delta) + len(normalized.Arguments) + len(normalized.Raw) + 128
					if capturedBytes+eventSize <= 1<<20 {
						capturedEvents = append(capturedEvents, normalized)
						capturedBytes += eventSize
					} else {
						captureTruncated = true
					}
				}
				if normalized.Type == "response.end" && normalized.Usage == nil {
					normalized.Usage = lastUsage
				}
				clientEvents := []ir.Event{normalized}
				if clientProtocol == ir.Gemini {
					clientEvents = bufferGeminiToolEvents(normalized, pendingGeminiTools, pendingGeminiArgs)
				}
				for _, clientEvent := range clientEvents {
					chunks, d, err := encoderAdapter.EncodeStreamEvent(ctx, clientEvent)
					record.Diagnostics = append(record.Diagnostics, d...)
					if err != nil {
						record.ErrorCode = "protocol_conversion_error"
						return
					}
					for _, chunk := range chunks {
						if err := streaming.Write(w, chunk.Event, chunk.Data); err != nil {
							return
						}
						if flusher != nil {
							flusher.Flush()
						}
					}
				}
			}
		}
	}
}

func writeEncodedStreamError(w io.Writer, flusher http.Flusher, adapter protocol.Adapter, ctx context.Context, responseID, typ, message string) {
	chunks, _, err := adapter.EncodeStreamEvent(ctx, ir.Event{Type: "error", ResponseID: responseID, Error: &ir.Error{Type: typ, Message: redactSnippet([]byte(message))}})
	if err != nil {
		return
	}
	for _, chunk := range chunks {
		if streaming.Write(w, chunk.Event, chunk.Data) != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func bufferGeminiToolEvents(e ir.Event, tools map[int]ir.ContentBlock, args map[int]string) []ir.Event {
	switch e.Type {
	case "tool_call.start":
		if e.Block != nil {
			tools[e.Index] = *e.Block
		}
		return nil
	case "tool_call.arguments.delta":
		args[e.Index] += e.Arguments
		return nil
	case "content.end":
		if tool, ok := tools[e.Index]; ok {
			tool.Arguments = json.RawMessage(args[e.Index])
			delete(tools, e.Index)
			delete(args, e.Index)
			return []ir.Event{{Type: "tool_call.start", Index: e.Index, ResponseID: e.ResponseID, Block: &tool}}
		}
	case "message.end", "response.end":
		out := make([]ir.Event, 0, len(tools)+1)
		for index, tool := range tools {
			tool.Arguments = json.RawMessage(args[index])
			out = append(out, ir.Event{Type: "tool_call.start", Index: index, ResponseID: e.ResponseID, Block: &tool})
			delete(tools, index)
			delete(args, index)
		}
		out = append(out, e)
		return out
	}
	return []ir.Event{e}
}

func providerEndpoint(p config.Provider, stream bool, model string) (string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	switch p.Protocol {
	case ir.OpenAIChat:
		return appendEndpoint(base, "/chat/completions"), nil
	case ir.OpenAIResponses:
		return appendEndpoint(base, "/responses"), nil
	case ir.Anthropic:
		return appendEndpoint(base, "/messages"), nil
	case ir.Gemini:
		if !strings.Contains(base, "/v1beta") {
			base += "/v1beta"
		}
		action := "generateContent"
		if stream {
			action = "streamGenerateContent"
		}
		return base + "/models/" + url.PathEscape(model) + ":" + action + "?alt=sse", nil
	default:
		return "", fmt.Errorf("unsupported provider protocol %q", p.Protocol)
	}
}
func appendEndpoint(base, suffix string) string {
	if strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/v1beta") {
		return base + suffix
	}
	return base + "/v1" + suffix
}
func protocolFromPath(p string) (ir.Protocol, string, bool) {
	switch {
	case p == "/v1/chat/completions" || p == "/chat/completions":
		return ir.OpenAIChat, "", true
	case p == "/v1/responses" || p == "/responses":
		return ir.OpenAIResponses, "", true
	case p == "/v1/messages" || p == "/messages":
		return ir.Anthropic, "", true
	case strings.Contains(p, "/models/") && (strings.HasSuffix(p, ":generateContent") || strings.HasSuffix(p, ":streamGenerateContent")):
		part := p[strings.Index(p, "/models/")+8:]
		return ir.Gemini, strings.Split(part, ":")[0], true
	}
	return "", "", false
}
func injectGemini(raw []byte, model string, stream bool) []byte {
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	v["model"] = model
	v["stream"] = stream
	return common.Raw(v)
}

func rewriteNativeRequest(raw []byte, model string, p ir.Protocol) ([]byte, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	v["model"] = model
	return sanitizeProviderPayload(common.Raw(v), p), nil
}
func sanitizeProviderPayload(raw []byte, p ir.Protocol) []byte {
	if p != ir.Gemini {
		return raw
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	delete(v, "model")
	delete(v, "stream")
	return common.Raw(v)
}

func readBody(r io.Reader, max int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("request body exceeds %d bytes", max)
	}
	return b, nil
}
func readRequestBody(r *http.Request, max int64) ([]byte, error) {
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
		return readBody(r.Body, max)
	case "gzip":
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("invalid gzip request body: %w", err)
		}
		defer reader.Close()
		return readBody(reader, max)
	default:
		return nil, fmt.Errorf("unsupported content-encoding %q", encoding)
	}
}
func retryStatus(statuses []int, s int) bool {
	for _, x := range statuses {
		if x == s {
			return true
		}
	}
	return false
}
func fallbackAllowed(policy config.Fallback, status int, body []byte, err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return policy.OnTimeout == nil || *policy.OnTimeout
	}
	if status == 0 && err != nil {
		return policy.OnNetworkError == nil || *policy.OnNetworkError
	}
	statuses := policy.OnStatus
	if len(statuses) == 0 {
		statuses = []int{429, 500, 502, 503, 504}
	}
	if retryStatus(statuses, status) {
		return true
	}
	_, code, _ := normalizeUpstreamError(status, body, err)
	for _, allowed := range policy.OnErrorCodes {
		if allowed == code {
			return true
		}
	}
	return false
}
func backoff(r config.Retry, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if v := resp.Header.Get("Retry-After"); v != "" {
			if n, e := strconv.Atoi(v); e == nil {
				return min(time.Duration(n)*time.Second, time.Minute)
			}
			if when, e := http.ParseTime(v); e == nil {
				return min(max(time.Duration(0), time.Until(when)), time.Minute)
			}
		}
	}
	d := time.Duration(float64(r.BaseDelay) * math.Pow(2, float64(attempt-1)))
	if d > r.MaxDelay {
		d = r.MaxDelay
	}
	return d + time.Duration(mathrand.Int64N(max(int64(1), int64(d/4))))
}
func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func allowedProviderHeader(k string) bool {
	n := strings.ToLower(k)
	return n != "authorization" && n != "x-api-key" && n != "host" && n != "connection" && n != "content-length"
}

func hasErrorDiagnostic(d []ir.Diagnostic) bool {
	for _, x := range d {
		if x.Severity == "error" {
			return true
		}
	}
	return false
}
func requestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}
func redactSnippet(b []byte) string {
	s := string(b)
	if len(s) > 512 {
		s = s[:512]
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
}

func normalizeUpstreamError(status int, body []byte, e error) (int, string, string) {
	if errors.Is(e, context.Canceled) {
		return 499, "client_cancelled", "client cancelled request"
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "upstream_timeout", "request deadline exceeded"
	}
	message := redactSnippet(body)
	if message == "" && e != nil {
		message = e.Error()
	}
	if message == "" {
		message = "upstream unavailable"
	}
	lower := strings.ToLower(message)
	switch {
	case status == 429:
		return 429, "rate_limited", message
	case status == 400 && strings.Contains(lower, "context"):
		return 400, "context_length_exceeded", message
	case status == 400:
		return 400, "invalid_request", message
	case status == 413:
		return 400, "context_length_exceeded", message
	case status == 401 || status == 403:
		if status == 403 {
			return 502, "permission_denied", "upstream permission denied"
		}
		return 502, "authentication_error", "upstream credentials were rejected"
	case status == 404:
		return 404, "not_found", message
	case strings.Contains(lower, "safety") || strings.Contains(lower, "content filter"):
		return 400, "content_filtered", message
	default:
		return 502, "upstream_unavailable", message
	}
}
func errorOut(w http.ResponseWriter, p ir.Protocol, status int, typ, message, id string) {
	w.Header().Set("content-type", "application/json")
	w.Header().Set("x-airoute-request-id", id)
	w.WriteHeader(status)
	var v any
	switch p {
	case ir.Anthropic:
		v = map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": message}, "request_id": id}
	case ir.Gemini:
		v = map[string]any{"error": map[string]any{"code": status, "message": message, "status": strings.ToUpper(typ), "request_id": id}}
	default:
		v = map[string]any{"error": map[string]any{"message": message, "type": typ, "code": typ, "request_id": id}}
	}
	_ = json.NewEncoder(w).Encode(v)
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (g *Gateway) serveModels(w http.ResponseWriter) {
	c := g.Config.Get()
	seen := map[string]bool{}
	var data []any
	for _, p := range c.Providers {
		for _, m := range p.Models {
			if !seen[m] {
				seen[m] = true
				data = append(data, map[string]any{"id": m, "object": "model", "owned_by": p.ID})
			}
		}
	}
	for _, r := range c.Routes {
		if r.Match.Model != "" && !strings.ContainsAny(r.Match.Model, "*?[") && !seen[r.Match.Model] {
			seen[r.Match.Model] = true
			data = append(data, map[string]any{"id": r.Match.Model, "object": "model", "owned_by": "airoute"})
		}
	}
	jsonOut(w, 200, map[string]any{"object": "list", "data": data})
}
func (g *Gateway) serveMetrics(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/plain; version=0.0.4")
	inputTokens, outputTokens := g.Metrics.InputTokens.Load(), g.Metrics.OutputTokens.Load()
	fmt.Fprintf(w, "airoute_requests_total %d\nairoute_errors_total %d\nairoute_inflight %d\nairoute_retries_total %d\nairoute_fallbacks_total %d\nairoute_timeouts_total %d\nairoute_cancellations_total %d\nairoute_conversion_diagnostics_total %d\nairoute_input_tokens_total %d\nairoute_output_tokens_total %d\nairoute_tokens_total %d\n", g.Metrics.Requests.Load(), g.Metrics.Errors.Load(), g.Metrics.InFlight.Load(), g.Metrics.Retries.Load(), g.Metrics.Fallbacks.Load(), g.Metrics.Timeouts.Load(), g.Metrics.Cancellations.Load(), g.Metrics.Diagnostics.Load(), inputTokens, outputTokens, inputTokens+outputTokens)
	fmt.Fprintf(w, "airoute_request_duration_milliseconds_sum %d\nairoute_request_duration_milliseconds_count %d\nairoute_first_token_milliseconds_sum %d\n", g.Metrics.LatencyMSTotal.Load(), g.Metrics.Completed.Load(), g.Metrics.FirstTokenMSTotal.Load())
	for i, bound := range observe.HistogramBounds {
		fmt.Fprintf(w, "airoute_request_duration_milliseconds_bucket{le=%q} %d\nairoute_first_token_milliseconds_bucket{le=%q} %d\n", fmt.Sprint(bound), g.Metrics.LatencyBuckets[i].Load(), fmt.Sprint(bound), g.Metrics.FirstTokenBuckets[i].Load())
	}
	fmt.Fprintf(w, "airoute_request_duration_milliseconds_bucket{le=\"+Inf\"} %d\nairoute_first_token_milliseconds_bucket{le=\"+Inf\"} %d\n", g.Metrics.LatencyBuckets[6].Load(), g.Metrics.FirstTokenBuckets[6].Load())
	ok, failed := g.Config.LoadCounts()
	fmt.Fprintf(w, "airoute_config_load_success_total %d\nairoute_config_load_failure_total %d\n", ok, failed)
	for _, s := range g.Metrics.Series() {
		labels := fmt.Sprintf("protocol=%q,provider=%q,status=%q", metricLabel(s.Protocol), metricLabel(s.Provider), fmt.Sprint(s.Status))
		if s.Model != "" {
			labels += ",model=" + strconv.Quote(metricLabel(s.Model))
		}
		fmt.Fprintf(w, "airoute_route_requests_total{%s} %d\nairoute_route_errors_total{%s} %d\nairoute_route_duration_milliseconds_sum{%s} %d\n", labels, s.Requests, labels, s.Errors, labels, s.LatencyMS)
	}
}
func metricLabel(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, "\\", "_"), "\n", "_")
}
