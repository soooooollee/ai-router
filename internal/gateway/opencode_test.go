package gateway_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/gateway"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol/ir"
)

type openCodeTransport func(*http.Request) (*http.Response, error)

func (f openCodeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenCodeForwardingAcrossProtocolsAndRetries(t *testing.T) {
	for _, source := range allProtocols() {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", source, stream), func(t *testing.T) {
				c := testConfig("https://opencode.ai/inference/go/openai/v1", ir.OpenAIChat)
				c.Retry.MaxAttempts = 2
				g := gateway.New(config.NewStore(c), testRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				calls := 0
				g.Client = &http.Client{Transport: openCodeTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Header.Get("x-opencode-session") != "conversation-123" || !strings.HasPrefix(r.Header.Get("User-Agent"), "AI-Router") || r.Header.Get("Authorization") != "Bearer x" {
						t.Errorf("lost session/application/authentication headers: %v", r.Header)
					}
					if r.URL.Path != "/inference/go/openai/v1/chat/completions" {
						t.Errorf("incorrect endpoint: %s", r.URL)
					}
					status, contentType, body := 200, "application/json", string(responseFor(ir.OpenAIChat))
					if calls == 1 {
						status, body = 503, `{"error":{"message":"try again"}}`
					} else if stream {
						contentType = "text/event-stream"
						body = "data: {\"id\":\"chat1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				path, body := clientRequest(source, stream)
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("session_id", "conversation-123")
				recorder := httptest.NewRecorder()
				g.ServeHTTP(recorder, req)
				if recorder.Code != 200 || calls != 2 {
					t.Fatalf("forwarding failed: calls=%d status=%d body=%s", calls, recorder.Code, recorder.Body)
				}
				if stream && !strings.Contains(recorder.Body.String(), "OK") {
					t.Fatalf("stream content missing: %s", recorder.Body)
				}
			})
		}
	}
}
