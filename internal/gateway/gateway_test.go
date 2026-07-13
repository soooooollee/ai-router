package gateway_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/gateway"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/anthropic"
	"github.com/zbss/airoute/internal/protocol/gemini"
	"github.com/zbss/airoute/internal/protocol/ir"
	"github.com/zbss/airoute/internal/protocol/openaichat"
	"github.com/zbss/airoute/internal/protocol/openairesponses"
)

func TestGatewayAllNonStreamingDirections(t *testing.T) {
	reg := testRegistry()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p := pathProtocol(r.URL.Path)
		if p == ir.Gemini {
			var v map[string]any
			_ = json.Unmarshal(body, &v)
			v["model"] = "upstream-model"
			body, _ = json.Marshal(v)
		}
		a, e := reg.Get(p)
		if e != nil {
			t.Errorf("unknown path %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if _, _, e = a.DecodeRequest(r.Context(), body); e != nil {
			t.Errorf("invalid %s upstream request: %v\n%s", p, e, body)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(responseFor(p))
	}))
	defer upstream.Close()
	for _, source := range allProtocols() {
		for _, target := range allProtocols() {
			t.Run(string(source)+"_to_"+string(target), func(t *testing.T) {
				c := testConfig(upstream.URL, target)
				g := gateway.New(config.NewStore(c), reg, observe.NewStore(20), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				server := httptest.NewServer(g)
				defer server.Close()
				path, body := clientRequest(source, false)
				req, _ := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
				req.Header.Set("content-type", "application/json")
				resp, e := http.DefaultClient.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != 200 {
					t.Fatalf("status %d: %s", resp.StatusCode, raw)
				}
				a, _ := reg.Get(source)
				decoded, _, e := a.DecodeResponse(req.Context(), raw)
				if e != nil {
					t.Fatalf("invalid client response: %v\n%s", e, raw)
				}
				if len(decoded.Messages) == 0 {
					t.Fatal("missing response message")
				}
			})
		}
	}
}

func TestGatewayCanPauseWithoutStoppingHealthEndpoint(t *testing.T) {
	g := gateway.New(config.NewStore(testConfig("https://example.com", ir.OpenAIChat)), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.SetEnabled(false)
	server := httptest.NewServer(g)
	defer server.Close()

	health, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health endpoint stopped with gateway: %d", health.StatusCode)
	}

	ready, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("paused gateway reported ready: %d", ready.StatusCode)
	}
}

func TestGatewayTranslatesAnthropicStreamToOpenAI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		f := w.(http.Flusher)
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":2}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, e := range events {
			_, _ = io.WriteString(w, e)
			f.Flush()
		}
	}))
	defer upstream.Close()
	reg := testRegistry()
	g := gateway.New(config.NewStore(testConfig(upstream.URL, ir.Anthropic)), reg, observe.NewStore(20), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	_, body := clientRequest(ir.OpenAIChat, true)
	resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)
	if !strings.Contains(text, "hello") || !strings.Contains(text, "[DONE]") {
		t.Fatalf("unexpected stream:\n%s", text)
	}
}

func TestGatewayBuffersToolArgumentsForGeminiStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":2}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"weather\",\"input\":{}}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"Paris\\\"}\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":4}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"}
		for _, e := range events {
			_, _ = io.WriteString(w, e)
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()
	g := gateway.New(config.NewStore(testConfig(upstream.URL, ir.Anthropic)), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	path, body := clientRequest(ir.Gemini, true)
	resp, e := http.Post(server.URL+path, "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)
	if !strings.Contains(text, "functionCall") || !strings.Contains(text, "Paris") {
		t.Fatalf("tool call was not assembled for Gemini:\n%s", text)
	}
}

func TestFallbackAfterRetryableFailure(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(responseFor(ir.OpenAIChat)) }))
	defer good.Close()
	c := testConfig(bad.URL, ir.OpenAIChat)
	c.Providers = append(c.Providers, config.Provider{ID: "good", Protocol: ir.OpenAIChat, BaseURL: good.URL, APIKey: "x", Models: []string{"upstream-model"}, Timeout: 2 * time.Second, AllowPrivateURL: true})
	c.DefaultRoute.Targets = append(c.DefaultRoute.Targets, config.RouteTarget{Provider: "good", Model: "upstream-model"})
	c.Retry.MaxAttempts = 1
	metrics := &observe.Metrics{}
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(20), metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	_, body := clientRequest(ir.OpenAIChat, false)
	resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if metrics.Fallbacks.Load() != 1 {
		t.Fatalf("expected one fallback, got %d", metrics.Fallbacks.Load())
	}
}

func TestFallbackPolicyRejectsAuthenticationAndAllowsConfiguredCode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		policy     config.Fallback
		wantSecond int64
	}{
		{name: "default rejects authentication", wantSecond: 0},
		{name: "explicit code allows authentication", policy: config.Fallback{OnErrorCodes: []string{"authentication_error"}}, wantSecond: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":{"message":"bad upstream key"}}`)
			}))
			defer first.Close()
			secondHits := atomic.Int64{}
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				secondHits.Add(1)
				_, _ = w.Write(responseFor(ir.OpenAIChat))
			}))
			defer second.Close()
			c := testConfig(first.URL, ir.OpenAIChat)
			c.Providers = append(c.Providers, config.Provider{ID: "second", Protocol: ir.OpenAIChat, BaseURL: second.URL, APIKey: "x", Models: []string{"upstream-model"}, Timeout: time.Second, AllowPrivateURL: true})
			c.DefaultRoute.Targets = append(c.DefaultRoute.Targets, config.RouteTarget{Provider: "second", Model: "upstream-model"})
			c.Fallback = tc.policy
			g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			server := httptest.NewServer(g)
			defer server.Close()
			_, body := clientRequest(ir.OpenAIChat, false)
			resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if secondHits.Load() != tc.wantSecond {
				t.Fatalf("second provider hits=%d want=%d", secondHits.Load(), tc.wantSecond)
			}
		})
	}
}

func TestRetryThenSuccess(t *testing.T) {
	hits := atomic.Int64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("retry-after", "0")
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write(responseFor(ir.OpenAIChat))
	}))
	defer upstream.Close()
	c := testConfig(upstream.URL, ir.OpenAIChat)
	c.Retry.MaxAttempts = 2
	metrics := &observe.Metrics{}
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	_, body := clientRequest(ir.OpenAIChat, false)
	resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || hits.Load() != 2 || metrics.Retries.Load() != 1 {
		t.Fatalf("retry failed status=%d hits=%d retries=%d", resp.StatusCode, hits.Load(), metrics.Retries.Load())
	}
}

func TestRetryAfterHTTPDateAndOverallDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(responseFor(ir.OpenAIChat))
	}))
	defer upstream.Close()
	c := testConfig(upstream.URL, ir.OpenAIChat)
	c.Server.RequestTimeout = 40 * time.Millisecond
	c.Providers[0].Timeout = time.Second
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, body := clientRequest(ir.OpenAIChat, false)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("overall deadline not enforced: %d %s", w.Code, w.Body.String())
	}
}

func TestHTTPConstraintsAndRequestID(t *testing.T) {
	c := testConfig("https://example.com", ir.OpenAIChat)
	c.Server.MaxHeaders = 2
	c.Metrics.Enabled = true
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Header().Get("x-airoute-request-id") == "" {
			t.Fatalf("%s omitted request ID", path)
		}
	}
	_, body := clientRequest(ir.OpenAIChat, false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("content-type", "text/plain")
	g.ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("content-type", "application/json")
	r.Header.Set("x-one", "1")
	r.Header.Set("x-two", "2")
	g.ServeHTTP(w, r)
	if w.Code != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("header count status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGzipRequestAndResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("content-encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write(responseFor(ir.OpenAIChat))
		_ = writer.Close()
	}))
	defer upstream.Close()
	g := gateway.New(config.NewStore(testConfig(upstream.URL, ir.OpenAIChat)), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, body := clientRequest(ir.OpenAIChat, false)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(body))
	_ = writer.Close()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", &compressed)
	r.Header.Set("content-type", "application/json")
	r.Header.Set("content-encoding", "gzip")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("gzip exchange failed: %d %s", w.Code, w.Body.String())
	}
}

func TestAllProtocolDirectionsFaultMatrix(t *testing.T) {
	for _, providerProtocol := range allProtocols() {
		t.Run(string(providerProtocol), func(t *testing.T) {
			for _, scenario := range []struct {
				name       string
				handler    http.HandlerFunc
				wantStatus int
				wantCode   string
			}{
				{name: "rate-limit", wantStatus: 429, wantCode: "rate_limited", handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(429)
					_, _ = io.WriteString(w, `{"error":{"message":"limited"}}`)
				}},
				{name: "invalid-json", wantStatus: 502, wantCode: "protocol_conversion_error", handler: func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("content-type", "application/json")
					_, _ = io.WriteString(w, `{invalid`)
				}},
				{name: "timeout", wantStatus: 504, wantCode: "upstream_timeout", handler: func(w http.ResponseWriter, r *http.Request) {
					select {
					case <-time.After(100 * time.Millisecond):
					case <-r.Context().Done():
					}
					_, _ = w.Write(responseFor(providerProtocol))
				}},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					upstream := httptest.NewServer(scenario.handler)
					defer upstream.Close()
					for _, clientProtocol := range allProtocols() {
						t.Run(string(clientProtocol), func(t *testing.T) {
							c := testConfig(upstream.URL, providerProtocol)
							if scenario.name == "timeout" {
								c.Server.RequestTimeout = 15 * time.Millisecond
								c.Providers[0].Timeout = time.Second
							}
							g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
							server := httptest.NewServer(g)
							defer server.Close()
							path, body := clientRequest(clientProtocol, false)
							resp, err := http.Post(server.URL+path, "application/json", strings.NewReader(body))
							if err != nil {
								t.Fatal(err)
							}
							raw, _ := io.ReadAll(resp.Body)
							resp.Body.Close()
							if resp.StatusCode != scenario.wantStatus || !strings.Contains(strings.ToLower(string(raw)), scenario.wantCode) || resp.Header.Get("x-airoute-request-id") == "" {
								t.Fatalf("fault mapping status=%d code=%s body=%s", resp.StatusCode, scenario.wantCode, raw)
							}
						})
					}
				})
			}
		})
	}
}

func TestAllProtocolDirectionsMidStreamFailure(t *testing.T) {
	for _, providerProtocol := range allProtocols() {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = io.WriteString(w, brokenStream(providerProtocol))
		}))
		for _, clientProtocol := range allProtocols() {
			t.Run(string(providerProtocol)+"_to_"+string(clientProtocol), func(t *testing.T) {
				c := testConfig(upstream.URL, providerProtocol)
				g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				server := httptest.NewServer(g)
				defer server.Close()
				path, body := clientRequest(clientProtocol, true)
				resp, err := http.Post(server.URL+path, "application/json", strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || !strings.Contains(string(raw), "partial") || !strings.Contains(string(raw), "protocol_conversion_error") {
					t.Fatalf("mid-stream failure not surfaced: status=%d body=%s", resp.StatusCode, raw)
				}
			})
		}
		upstream.Close()
	}
}

func brokenStream(protocol ir.Protocol) string {
	switch protocol {
	case ir.OpenAIChat:
		return "data: {\"id\":\"r\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: {\"id\":\"r\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {invalid}\n\n"
	case ir.OpenAIResponses:
		return "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\nevent: response.output_text.delta\ndata: {invalid}\n\n"
	case ir.Anthropic:
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"r\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\nevent: content_block_delta\ndata: {invalid}\n\n"
	case ir.Gemini:
		return "data: {\"modelVersion\":\"m\",\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\ndata: {invalid}\n\n"
	}
	return ""
}

func TestNoFallbackAfterStreamHasStarted(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
	}))
	defer first.Close()
	secondHits := atomic.Int64{}
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondHits.Add(1)
		_, _ = w.Write(responseFor(ir.OpenAIChat))
	}))
	defer second.Close()
	c := testConfig(first.URL, ir.Anthropic)
	c.Providers = append(c.Providers, config.Provider{ID: "second", Protocol: ir.OpenAIChat, BaseURL: second.URL, APIKey: "x", Models: []string{"upstream-model"}, Timeout: time.Second, AllowPrivateURL: true})
	c.DefaultRoute.Targets = append(c.DefaultRoute.Targets, config.RouteTarget{Provider: "second", Model: "upstream-model"})
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	_, body := clientRequest(ir.OpenAIChat, true)
	resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), "partial") || secondHits.Load() != 0 {
		t.Fatalf("stream fallback was unsafe: hits=%d body=%s", secondHits.Load(), raw)
	}
}

func TestFallbackBeforeFirstStreamEvent(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer first.Close()
	secondHits := atomic.Int64{}
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondHits.Add(1)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"r\",\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer second.Close()
	c := testConfig(first.URL, ir.OpenAIChat)
	c.Providers = append(c.Providers, config.Provider{ID: "second", Protocol: ir.OpenAIChat, BaseURL: second.URL, APIKey: "x", Models: []string{"upstream-model"}, Timeout: time.Second, AllowPrivateURL: true})
	c.DefaultRoute.Targets = append(c.DefaultRoute.Targets, config.RouteTarget{Provider: "second", Model: "upstream-model"})
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	_, body := clientRequest(ir.OpenAIChat, true)
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), "recovered") || secondHits.Load() != 1 {
		t.Fatalf("stream did not fallback before its commit point: hits=%d body=%s", secondHits.Load(), raw)
	}
}

func TestClientCancellationReachesUpstream(t *testing.T) {
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	g := gateway.New(config.NewStore(testConfig(upstream.URL, ir.OpenAIChat)), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	_, body := clientRequest(ir.OpenAIChat, false)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	done := make(chan struct{})
	go func() {
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream context was not cancelled")
	}
	<-done
}

func TestErrorNormalizationAuthAndCountTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"message":"limited"}}`)
	}))
	defer upstream.Close()
	c := testConfig(upstream.URL, ir.OpenAIChat)
	c.Auth = config.Auth{Enabled: true, Keys: []config.APIKey{{ID: "client", Value: "secret"}}}
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	resp, e := http.Get(server.URL + "/v1/models")
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("models should require auth, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	_, body := clientRequest(ir.OpenAIChat, false)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("authorization", "Bearer secret")
	req.Header.Set("content-type", "application/json")
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != 429 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("rate limit not normalized: %d %s", resp.StatusCode, raw)
	}
	resp.Body.Close()
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/messages/count_tokens", strings.NewReader(`{"model":"alias","messages":[{"role":"user","content":"hello world"}]}`))
	req.Header.Set("authorization", "Bearer secret")
	req.Header.Set("content-type", "application/json")
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"input_tokens"`) || !strings.Contains(string(raw), `"estimated":true`) || !strings.Contains(string(raw), `"unicode-lexical-v1"`) {
		t.Fatalf("count tokens failed: %d %s", resp.StatusCode, raw)
	}
}

func TestCountTokensUsesNativeProviderCapability(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" || r.Header.Get("x-api-key") != "x" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Contains(raw, []byte(`"model":"upstream-model"`)) || bytes.Contains(raw, []byte("max_tokens")) {
			t.Fatalf("unexpected native payload: %s", raw)
		}
		_, _ = io.WriteString(w, `{"input_tokens":123}`)
	}))
	defer upstream.Close()
	c := testConfig(upstream.URL+"/v1", ir.Anthropic)
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/messages/count_tokens", strings.NewReader(`{"model":"alias","system":"系统提示","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"input_tokens":123`)) || !bytes.Contains(raw, []byte(`"estimated":false`)) || !bytes.Contains(raw, []byte(`"strategy":"provider-native"`)) {
		t.Fatalf("native count response is wrong: %d %s", resp.StatusCode, raw)
	}
}

func TestCapturedBodiesAreRedacted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(responseFor(ir.OpenAIChat)) }))
	defer upstream.Close()
	c := testConfig(upstream.URL, ir.OpenAIChat)
	c.Logging.CaptureBodies = true
	logs := observe.NewStore(10)
	g := gateway.New(config.NewStore(c), testRegistry(), logs, &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	body := `{"model":"alias","api_key":"do-not-log","messages":[{"role":"user","content":"hello"}]}`
	resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	records := logs.List(1)
	if len(records) != 1 || strings.Contains(records[0].RequestBody, "do-not-log") || !strings.Contains(records[0].RequestBody, "[REDACTED]") {
		t.Fatalf("body was not redacted: %#v", records)
	}
}

func TestStrictLossPolicyAllowsNativeAndRejectsCrossProtocol(t *testing.T) {
	hits := atomic.Int64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			_, _ = w.Write(responseFor(ir.OpenAIChat))
		} else {
			_, _ = w.Write(responseFor(ir.Anthropic))
		}
	}))
	defer upstream.Close()
	body := `{"model":"alias","seed":7,"messages":[{"role":"user","content":"hello"}]}`
	for _, tc := range []struct {
		protocol ir.Protocol
		want     int
	}{{ir.OpenAIChat, 200}, {ir.Anthropic, 422}} {
		c := testConfig(upstream.URL, tc.protocol)
		c.Conversion.UnsupportedFields = "strict"
		g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		server := httptest.NewServer(g)
		resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		server.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("target %s: want %d got %d", tc.protocol, tc.want, resp.StatusCode)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("cross-protocol strict request reached upstream; hits=%d", hits.Load())
	}
}

func TestStrictRejectsLossDetectedByTargetEncoder(t *testing.T) {
	hits := atomic.Int64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(responseFor(ir.Anthropic))
	}))
	defer upstream.Close()
	c := testConfig(upstream.URL, ir.Anthropic)
	c.Conversion.UnsupportedFields = "strict"
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	body := `{"model":"alias","messages":[{"role":"assistant","reasoning_content":"unsigned","content":"answer"},{"role":"user","content":"continue"}]}`
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity || hits.Load() != 0 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("strict target loss was not rejected: status=%d hits=%d body=%s", resp.StatusCode, hits.Load(), raw)
	}
}

func TestPrivateUpstreamBlockedByDefault(t *testing.T) {
	hits := atomic.Int64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write(responseFor(ir.OpenAIChat)) }))
	defer upstream.Close()
	c := testConfig(upstream.URL, ir.OpenAIChat)
	c.Providers[0].AllowPrivateURL = false
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	_, body := clientRequest(ir.OpenAIChat, false)
	resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 || hits.Load() != 0 {
		t.Fatalf("private upstream was not blocked status=%d hits=%d", resp.StatusCode, hits.Load())
	}
}

func Test100ConcurrentStreams(t *testing.T) {
	rounds := runConcurrentStreamBatches(t, func(round int) bool { return round < 1 })
	if rounds != 1 {
		t.Fatalf("unexpected rounds: %d", rounds)
	}
}

func runConcurrentStreamBatches(t *testing.T, continueAt func(int) bool) int {
	t.Helper()
	baseline := runtime.NumGoroutine()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		messages, _ := v["messages"].([]any)
		text := "ok"
		if len(messages) > 0 {
			m, _ := messages[0].(map[string]any)
			parts, _ := m["content"].([]any)
			if len(parts) > 0 {
				p, _ := parts[0].(map[string]any)
				if s, ok := p["text"].(string); ok {
					text = s
				}
			}
		}
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", text)
	}))
	defer upstream.Close()
	c := testConfig(upstream.URL, ir.Anthropic)
	c.Server.MaxConcurrent = 128
	g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(200), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	transport := &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 128, IdleConnTimeout: time.Minute}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	rounds := 0
	for continueAt(rounds) {
		var wg sync.WaitGroup
		errors := make(chan error, 100)
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i, round int) {
				defer wg.Done()
				want := fmt.Sprintf("stream-%d-%d", round, i)
				body := fmt.Sprintf(`{"model":"alias","messages":[{"role":"user","content":%q}],"stream":true}`, want)
				resp, e := client.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
				if e != nil {
					errors <- e
					return
				}
				raw, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if !strings.Contains(string(raw), want) {
					errors <- fmt.Errorf("stream %d received wrong response %s", i, raw)
				}
			}(i, rounds)
		}
		wg.Wait()
		close(errors)
		for e := range errors {
			t.Error(e)
		}
		if t.Failed() {
			break
		}
		rounds++
	}
	transport.CloseIdleConnections()
	server.Close()
	g.Client.CloseIdleConnections()
	upstream.Close()
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > baseline+20 {
		t.Fatalf("possible goroutine leak: before=%d after=%d", baseline, after)
	}
	return rounds
}

func BenchmarkNativeGateway(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(responseFor(ir.OpenAIChat)) }))
	defer upstream.Close()
	g := gateway.New(config.NewStore(testConfig(upstream.URL, ir.OpenAIChat)), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(g)
	defer server.Close()
	_, body := clientRequest(ir.OpenAIChat, false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, e := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if e != nil {
			b.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func TestLongSoak(t *testing.T) {
	value := os.Getenv("AIROUTE_SOAK_DURATION")
	if value == "" {
		t.Skip("set AIROUTE_SOAK_DURATION=24h for release soak")
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(duration)
	rounds := runConcurrentStreamBatches(t, func(int) bool { return time.Now().Before(deadline) })
	t.Logf("completed %d rounds of 100 concurrent streams in %s", rounds, duration)
}

func testRegistry() *protocol.Registry {
	return protocol.NewRegistry(openaichat.New(), openairesponses.New(), anthropic.New(), gemini.New())
}
func allProtocols() []ir.Protocol {
	return []ir.Protocol{ir.OpenAIChat, ir.OpenAIResponses, ir.Anthropic, ir.Gemini}
}
func testConfig(base string, p ir.Protocol) *config.Config {
	return &config.Config{Version: 1, Server: config.Server{Listen: "127.0.0.1:0", RequestTimeout: 2 * time.Second, MaxBodySize: 4 << 20, MaxConcurrent: 32}, Providers: []config.Provider{{ID: "target", Protocol: p, BaseURL: base, APIKey: "x", Models: []string{"upstream-model"}, Timeout: 2 * time.Second, AllowPrivateURL: true}}, DefaultRoute: &config.RouteTargetList{Targets: []config.RouteTarget{{Provider: "target", Model: "upstream-model"}}}, Conversion: config.Conversion{UnsupportedFields: "warn"}, Retry: config.Retry{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, OnStatus: []int{429, 500, 502, 503, 504}}, Metrics: config.Metrics{Path: "/metrics"}}
}

func TestApplyRuntimeConfigResizesConcurrencyAndTransport(t *testing.T) {
	previous := testConfig("https://example.com", ir.OpenAIChat)
	previous.Server.MaxConcurrent = 32
	next := *previous
	next.Server.MaxConcurrent = 3
	next.Logging.Level = "debug"
	g := gateway.New(config.NewStore(previous), testRegistry(), observe.NewStore(1), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	level := &slog.LevelVar{}
	level.Set(slog.LevelInfo)
	g.SetLogLevelController(level)
	g.ApplyRuntimeConfig(previous, &next)
	limit, idle := g.RuntimeLimits()
	if limit != 3 || idle != 3 {
		t.Fatalf("runtime objects were not rebuilt: concurrent=%d idle=%d", limit, idle)
	}
	if level.Level() != slog.LevelDebug {
		t.Fatalf("log level was not hot reloaded: %v", level.Level())
	}
}
func pathProtocol(p string) ir.Protocol {
	switch {
	case strings.HasSuffix(p, "/chat/completions"):
		return ir.OpenAIChat
	case strings.HasSuffix(p, "/responses"):
		return ir.OpenAIResponses
	case strings.HasSuffix(p, "/messages"):
		return ir.Anthropic
	case strings.Contains(p, ":generateContent"):
		return ir.Gemini
	}
	return ""
}
func clientRequest(p ir.Protocol, stream bool) (string, string) {
	s := fmt.Sprintf("%t", stream)
	switch p {
	case ir.OpenAIChat:
		return "/v1/chat/completions", `{"model":"alias","messages":[{"role":"user","content":"hello"}],"stream":` + s + `}`
	case ir.OpenAIResponses:
		return "/v1/responses", `{"model":"alias","input":"hello","stream":` + s + `}`
	case ir.Anthropic:
		return "/v1/messages", `{"model":"alias","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":` + s + `}`
	case ir.Gemini:
		action := "generateContent"
		if stream {
			action = "streamGenerateContent"
		}
		return "/v1beta/models/alias:" + action, `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
	}
	panic(p)
}
func responseFor(p ir.Protocol) []byte {
	switch p {
	case ir.OpenAIChat:
		return []byte(`{"id":"r1","model":"upstream-model","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	case ir.OpenAIResponses:
		return []byte(`{"id":"r1","model":"upstream-model","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	case ir.Anthropic:
		return []byte(`{"id":"r1","model":"upstream-model","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	case ir.Gemini:
		return []byte(`{"modelVersion":"upstream-model","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	}
	b, _ := json.Marshal(map[string]string{"error": "unsupported"})
	return b
}
