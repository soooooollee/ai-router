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
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soooooollee/ai-router/internal/clientauth"
	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/observe"
	"github.com/soooooollee/ai-router/internal/protocol"
	"github.com/soooooollee/ai-router/internal/protocol/common"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
	providerprofile "github.com/soooooollee/ai-router/internal/provider"
	"github.com/soooooollee/ai-router/internal/routing"
	"github.com/soooooollee/ai-router/internal/secure"
	"github.com/soooooollee/ai-router/internal/streaming"
	"github.com/soooooollee/ai-router/internal/tokencount"
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
	ClientAccess     *clientauth.Manager
	enabled          atomic.Bool
	breakers         breakerRegistry
}

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

func (g *Gateway) SetEnabled(enabled bool)                    { g.enabled.Store(enabled) }
func (g *Gateway) IsEnabled() bool                            { return g.enabled.Load() }
func (g *Gateway) SetClientAccess(access *clientauth.Manager) { g.ClientAccess = access }

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
	traceparent := traceparentFromRequest(r.Header.Get(traceparentHeader))
	r = r.WithContext(withTraceparent(r.Context(), traceparent))
	w.Header().Set("x-airoute-request-id", id)
	w.Header().Set("x-airoute-trace-id", traceID(traceparent))
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
	principal := clientauth.Principal{TenantID: "tenant_default", ProjectID: "project_default", ClientID: "anonymous", ClientName: "Anonymous", Anonymous: true}
	if g.ClientAccess != nil {
		var accessErr *clientauth.AccessError
		principal, accessErr = g.ClientAccess.Authenticate(r, c)
		if accessErr != nil {
			g.logRejection(c, requestProtocol(r.URL.Path), id, principal, accessErr.Status, accessErr.Code)
			errorOut(w, requestProtocol(r.URL.Path), accessErr.Status, accessErr.Code, accessErr.Message, id)
			return
		}
	} else if c.Auth.Enabled {
		// Tests and embedders that do not install the managed authenticator keep
		// the legacy static-key path through an ephemeral manager.
		principal, _ = (&clientauth.Manager{}).Authenticate(r, c)
		if principal.ClientID == "" {
			g.logRejection(c, requestProtocol(r.URL.Path), id, principal, http.StatusUnauthorized, "authentication_error")
			errorOut(w, requestProtocol(r.URL.Path), 401, "authentication_error", "invalid or missing API key", id)
			return
		}
	}
	if r.URL.Path == "/v1/models" {
		g.serveModels(w, principal)
		return
	}
	if r.URL.Path == "/v1/embeddings" || r.URL.Path == "/embeddings" {
		if g.ClientAccess != nil {
			if accessErr := g.ClientAccess.Precheck(r.Context(), principal, ir.OpenAIChat, r.RemoteAddr); accessErr != nil {
				g.logRejection(c, ir.OpenAIChat, id, principal, accessErr.Status, accessErr.Code)
				errorOut(w, ir.OpenAIChat, accessErr.Status, accessErr.Code, accessErr.Message, id)
				return
			}
		}
		g.serveEmbeddings(w, r, c, id, principal)
		return
	}
	if r.URL.Path == "/v1/messages/count_tokens" || r.URL.Path == "/messages/count_tokens" {
		if g.ClientAccess != nil {
			if accessErr := g.ClientAccess.Precheck(r.Context(), principal, ir.Anthropic, r.RemoteAddr); accessErr != nil {
				g.logRejection(c, ir.Anthropic, id, principal, accessErr.Status, accessErr.Code)
				errorOut(w, ir.Anthropic, accessErr.Status, accessErr.Code, accessErr.Message, id)
				return
			}
		}
		g.countTokens(w, r, c, id, principal)
		return
	}
	p, model, ok := protocolFromPath(r.URL.Path)
	if !ok {
		errorOut(w, ir.OpenAIChat, 404, "not_found", "unknown gateway endpoint", id)
		return
	}
	if g.ClientAccess != nil {
		if accessErr := g.ClientAccess.Precheck(r.Context(), principal, p, r.RemoteAddr); accessErr != nil {
			g.logRejection(c, p, id, principal, accessErr.Status, accessErr.Code)
			errorOut(w, p, accessErr.Status, accessErr.Code, accessErr.Message, id)
			return
		}
	}
	g.semMu.RLock()
	sem := g.sem
	g.semMu.RUnlock()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default:
		g.logRejection(c, p, id, principal, http.StatusTooManyRequests, "gateway_concurrency_limited")
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
	g.handle(w, r, c, p, model, id, principal)
}

func requestProtocol(requestPath string) ir.Protocol {
	if requestPath == "/v1/messages/count_tokens" || requestPath == "/messages/count_tokens" {
		return ir.Anthropic
	}
	if protocol, _, ok := protocolFromPath(requestPath); ok {
		return protocol
	}
	return ir.OpenAIChat
}

func (g *Gateway) logRejection(c *config.Config, protocol ir.Protocol, id string, principal clientauth.Principal, status int, reason string) {
	start := time.Now().UTC()
	authSource := "unknown"
	if principal.Legacy {
		authSource = "legacy"
	} else if principal.Anonymous {
		authSource = "anonymous"
	} else if principal.CredentialID != "" {
		authSource = "managed"
	}
	record := observe.Record{
		ID: id, StartedAt: start, ClientProtocol: protocol, ClientKeyID: principal.ClientID,
		ClientID: principal.ClientID, ClientName: principal.ClientName, CredentialID: principal.CredentialID,
		CredentialPrefix: principal.CredentialPrefix, AuthSource: authSource, ConfigVersion: c.Hash,
		Status: status, ErrorCode: reason, RejectionReason: reason,
	}
	g.Metrics.Requests.Add(1)
	g.Metrics.Errors.Add(1)
	g.Metrics.Completed.Add(1)
	g.Metrics.Record(protocol, "", "", status, 0, 0)
	g.Logs.Add(record)
	g.Logger.Info("request rejected", "request_id", id, "client_id", principal.ClientID, "client_protocol", protocol, "status_code", status, "rejection_reason", reason)
}

func (g *Gateway) countTokens(w http.ResponseWriter, r *http.Request, c *config.Config, id string, principal clientauth.Principal) {
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
	var lease *clientauth.RequestLease
	countUsage := ir.Usage{}
	if g.ClientAccess != nil {
		var accessErr *clientauth.AccessError
		tokenPrincipal := principal
		tokenPrincipal.Policy.DailyOutputTokens = 0
		tokenPrincipal.Policy.MaxOutputTokens = 0
		lease, accessErr = g.ClientAccess.BeginRequest(r.Context(), tokenPrincipal, req, id)
		if accessErr != nil {
			errorOut(w, ir.Anthropic, accessErr.Status, accessErr.Code, accessErr.Message, id)
			return
		}
		defer func() { _ = lease.Finish(context.Background(), countUsage, 0, false) }()
	}
	headers := map[string]string{}
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	decision, routeErr := routing.Resolve(c, routing.Input{Request: req, Protocol: ir.Anthropic, Headers: headers})
	if routeErr == nil && len(decision.Targets) > 0 {
		target := decision.Targets[0]
		if providerprofile.IsOpenCodeGo(target.Provider.BaseURL) {
			r = r.WithContext(providerprofile.WithConversation(r.Context(), r.Header, req, principal.ClientID+":"+principal.CredentialID, body))
		}
		if providerprofile.ProviderCapabilities(target.Provider, target.Model).NativeTokenCount {
			if tokens, nativeErr := g.nativeTokenCount(r.Context(), req, target); nativeErr == nil {
				countUsage.InputTokens = tokens
				jsonOut(w, 200, map[string]any{"input_tokens": tokens, "estimated": false, "strategy": "provider-native", "provider": target.Provider.ID, "model": target.Model})
				return
			}
		}
	}
	result := (tokencount.Heuristic{}).Count(req)
	countUsage.InputTokens = result.InputTokens
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
	providerprofile.ApplyAuthenticationHeaders(req.Header, target.Provider)
	for key, value := range target.Provider.Headers {
		if allowedProviderHeader(key) {
			req.Header.Set(key, value)
		}
	}
	providerprofile.ApplyOpenCodeHeaders(req, target.Provider)
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

func (g *Gateway) handle(w http.ResponseWriter, r *http.Request, c *config.Config, clientProtocol ir.Protocol, pathModel, id string, principal clientauth.Principal) {
	start := time.Now()
	var clientLease *clientauth.RequestLease
	g.Logs.Configure(c.Logging.RequestHistory, effectiveLogFile(c))
	g.Metrics.Requests.Add(1)
	g.Metrics.InFlight.Add(1)
	authSource := "managed"
	if principal.Legacy {
		authSource = "legacy"
	} else if principal.Anonymous {
		authSource = "anonymous"
	}
	record := observe.Record{ID: id, StartedAt: start, TraceID: traceID(traceparentFromContext(r.Context())), ClientProtocol: clientProtocol, ClientKeyID: principal.ClientID, ClientID: principal.ClientID, ClientName: principal.ClientName, CredentialID: principal.CredentialID, CredentialPrefix: principal.CredentialPrefix, AuthSource: authSource, ConfigVersion: c.Hash}
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
		record.CostMicros = routeCostMicros(c, record.ProviderID, record.Usage)
		g.Metrics.CostMicros.Add(uint64(max(0, record.CostMicros)))
		if clientLease != nil {
			if err := clientLease.Finish(context.Background(), record.Usage, record.CostMicros, record.ErrorCode != ""); err != nil {
				g.Logger.Error("client usage settlement failed", "request_id", id, "client_id", principal.ClientID, "error", err)
			}
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
	if g.ClientAccess != nil {
		var accessErr *clientauth.AccessError
		clientLease, accessErr = g.ClientAccess.BeginRequest(r.Context(), principal, canonical, id)
		if accessErr != nil {
			record.Status = accessErr.Status
			record.ErrorCode = accessErr.Code
			record.RejectionReason = accessErr.Code
			g.Metrics.Errors.Add(1)
			errorOut(w, clientProtocol, accessErr.Status, accessErr.Code, accessErr.Message, id)
			return
		}
	}
	customTools := customToolNames(canonical)
	record.Diagnostics = append(record.Diagnostics, diagnostics...)
	if c.Conversion.RemoteImagePolicy == "reject" && common.HasType(canonical, "image_url") {
		record.Status = 422
		record.ErrorCode = "protocol_conversion_error"
		errorOut(w, clientProtocol, 422, "protocol_conversion_error", "remote image URLs are rejected by configuration", id)
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
	for _, target := range decision.Targets {
		if providerprofile.IsOpenCodeGo(target.Provider.BaseURL) {
			r = r.WithContext(providerprofile.WithConversation(r.Context(), r.Header, canonical, principal.ClientID+":"+principal.CredentialID, body))
			break
		}
	}
	record.RouteID = decision.RouteID
	estimatedInput := tokencount.Heuristic{}.Count(canonical).InputTokens
	requestedOutput := 0
	if canonical.Sampling.MaxOutputTokens != nil {
		requestedOutput = *canonical.Sampling.MaxOutputTokens
	}
	var upstream *http.Response
	var target routing.ResolvedTarget
	var finalErr error
	var finalStatus int
	var finalBody []byte
	var streamDecoder *streaming.Decoder
	var prefetched []streaming.Event
	for ti, t := range g.orderTargets(c, decision.Targets) {
		finalStatus = 0
		finalBody = nil
		finalErr = nil
		target = t
		if ti > 0 {
			g.Metrics.Fallbacks.Add(1)
		}
		canonical.Model = t.Model
		if t.Provider.ContextWindow > 0 && estimatedInput+requestedOutput > t.Provider.ContextWindow {
			finalErr = fmt.Errorf("%w: provider %q accepts %d tokens, this request needs about %d", errContextWindow, t.Provider.ID, t.Provider.ContextWindow, estimatedInput+requestedOutput)
			continue
		}
		if lossy := strictConversionLoss(c, clientProtocol, t.Provider.Protocol, diagnostics); len(lossy) > 0 {
			finalErr = lossyConversionError(lossy)
			continue
		}
		outAdapter, e := g.Registry.Get(t.Provider.Protocol)
		if e != nil {
			finalErr = e
			continue
		}
		var payload []byte
		var d []ir.Diagnostic
		forceCodexResponsesBridge := clientProtocol == ir.OpenAIResponses && t.Provider.Protocol == ir.OpenAIResponses && t.Provider.CompatibilityMode == "codex-responses"
		if clientProtocol == t.Provider.Protocol && !forceCodexResponsesBridge {
			payload, e = rewriteNativeRequest(body, t.Model, t.Provider.Protocol)
		} else {
			if forceCodexResponsesBridge {
				bridgeCodexCustomTools(canonical)
			}
			payload, d, e = outAdapter.EncodeRequest(r.Context(), canonical)
			if e == nil {
				payload = sanitizeProviderPayload(payload, t.Provider.Protocol)
			}
		}
		if e == nil {
			var policyDiagnostics []ir.Diagnostic
			payload, policyDiagnostics, e = providerprofile.PrepareRequest(payload, t.Provider, canonical)
			d = append(d, policyDiagnostics...)
		}
		record.Diagnostics = append(record.Diagnostics, d...)
		if e != nil {
			finalErr = e
			continue
		}
		// Source-side loss was already rejected above, so this only has to
		// judge what the target encoder itself would lose.
		if lossy := strictConversionLoss(c, clientProtocol, t.Provider.Protocol, d); len(lossy) > 0 {
			finalErr = lossyConversionError(lossy)
			continue
		}
		// Route-level retry/fallback/timeout replace the global policy when set.
		retry := effectiveRetry(c, decision.RouteID)
		provider := t.Provider
		provider.Timeout = providerTimeout(c, decision.RouteID, provider)
		for attempt := 1; attempt <= retry.MaxAttempts; attempt++ {
			aStart := time.Now()
			upstream, e = g.do(r.Context(), provider, payload, canonical.Stream, t.Model)
			a := observe.Attempt{Number: attempt, ProviderID: t.Provider.ID, Model: t.Model, DurationMS: time.Since(aStart).Milliseconds()}
			if e != nil {
				a.Error = e.Error()
				record.Attempts = append(record.Attempts, a)
				finalErr = e
				g.noteProviderResult(c, t.Provider.ID, false)
			} else {
				a.Status = upstream.StatusCode
				record.Attempts = append(record.Attempts, a)
				if upstream.StatusCode >= 200 && upstream.StatusCode < 300 {
					g.noteProviderResult(c, t.Provider.ID, true)
					finalErr = nil
					break
				}
				g.noteProviderResult(c, t.Provider.ID, false)
				errBody, _ := io.ReadAll(io.LimitReader(upstream.Body, 1<<20))
				upstream.Body.Close()
				finalErr = fmt.Errorf("upstream returned %d: %s", upstream.StatusCode, redactSnippet(errBody))
				finalStatus = upstream.StatusCode
				finalBody = errBody
				if !retryStatus(retry.OnStatus, upstream.StatusCode) {
					break
				}
			}
			if attempt < retry.MaxAttempts {
				g.Metrics.Retries.Add(1)
				if !sleepContext(r.Context(), backoff(retry, attempt, upstream)) {
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
		if ti+1 < len(decision.Targets) && !fallbackAllowed(effectiveFallback(c, decision.RouteID), finalStatus, finalBody, finalErr) {
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
		if errors.Is(finalErr, errContextWindow) {
			record.Status = http.StatusRequestEntityTooLarge
			record.ErrorCode = "context_window_exceeded"
			g.Metrics.Errors.Add(1)
			errorOut(w, clientProtocol, http.StatusRequestEntityTooLarge, "invalid_request", finalErr.Error(), id)
			return
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
		g.stream(w, r.Context(), upstream, streamDecoder, prefetched, clientProtocol, target.Provider.Protocol, target.Model, id, customTools, c.Logging.CaptureBodies, &record)
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
	markCustomToolCalls(canonicalResp, customTools)
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

func requestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
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
func (g *Gateway) serveModels(w http.ResponseWriter, principal clientauth.Principal) {
	c := g.Config.Get()
	seen := map[string]bool{}
	models := make([]string, 0)
	for _, p := range c.Providers {
		for _, m := range p.Models {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
	}
	for _, r := range c.Routes {
		if r.Match.Model != "" && !strings.ContainsAny(r.Match.Model, "*?[") && !seen[r.Match.Model] {
			seen[r.Match.Model] = true
			models = append(models, r.Match.Model)
		}
	}
	for _, allowed := range principal.Policy.AllowedModels {
		if allowed != "" && !strings.ContainsAny(allowed, "*?[") && !seen[allowed] {
			seen[allowed] = true
			models = append(models, allowed)
		}
	}
	if g.ClientAccess != nil {
		models = g.ClientAccess.FilterModels(principal, models)
	}
	data := make([]any, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]any{"id": model, "object": "model", "owned_by": "airoute"})
	}
	jsonOut(w, 200, map[string]any{"object": "list", "data": data})
}
func (g *Gateway) serveMetrics(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/plain; version=0.0.4")
	inputTokens, outputTokens := g.Metrics.InputTokens.Load(), g.Metrics.OutputTokens.Load()
	fmt.Fprintf(w, "airoute_requests_total %d\nairoute_errors_total %d\nairoute_inflight %d\nairoute_retries_total %d\nairoute_fallbacks_total %d\nairoute_timeouts_total %d\nairoute_cancellations_total %d\nairoute_conversion_diagnostics_total %d\nairoute_input_tokens_total %d\nairoute_output_tokens_total %d\nairoute_tokens_total %d\n", g.Metrics.Requests.Load(), g.Metrics.Errors.Load(), g.Metrics.InFlight.Load(), g.Metrics.Retries.Load(), g.Metrics.Fallbacks.Load(), g.Metrics.Timeouts.Load(), g.Metrics.Cancellations.Load(), g.Metrics.Diagnostics.Load(), inputTokens, outputTokens, inputTokens+outputTokens)
	fmt.Fprintf(w, "airoute_cost_micros_total %d\n", g.Metrics.CostMicros.Load())
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
