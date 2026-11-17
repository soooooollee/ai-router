package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/protocol/common"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
	providerprofile "github.com/soooooollee/ai-router/internal/provider"
	"github.com/soooooollee/ai-router/internal/secure"
)

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
	if traceparent := traceparentFromContext(ctx); traceparent != "" {
		req.Header.Set(traceparentHeader, traceparent)
	}
	providerprofile.ApplyAuthenticationHeaders(req.Header, p)
	if p.Protocol == ir.Anthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	for k, v := range p.Headers {
		if allowedProviderHeader(k) {
			req.Header.Set(k, v)
		}
	}
	providerprofile.ApplyOpenCodeHeaders(req, p)
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

func redactSnippet(b []byte) string {
	s := string(b)
	if len(s) > 512 {
		s = s[:512]
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
}
