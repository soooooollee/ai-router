package admin

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/ir"
	providerprofile "github.com/zbss/airoute/internal/provider"
)

type openCodeTransport func(*http.Request) (*http.Response, error)

func (f openCodeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenCodeDetectionHeadersAndFailureDetails(t *testing.T) {
	var mu sync.Mutex
	var sessions []string
	s := New(config.NewStore(&config.Config{}), protocol.NewRegistry(), observe.NewStore(2), &observe.Metrics{}, "test", "")
	s.Client = &http.Client{Transport: openCodeTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "AI-Router") || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("probe is missing application or authentication headers")
		}
		return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Specific upstream rejection for test-key"}}`))}, nil
	})}
	report := s.detectProviderCapabilities(context.Background(), "https://opencode.ai/inference/go/openai/v1", "test-key", []string{"glm-5.3"}, true)
	if report.OK || !strings.Contains(report.CodexCompatibility.Message, "HTTP 400") || !strings.Contains(report.CodexCompatibility.Message, "Specific upstream rejection") || strings.Contains(report.CodexCompatibility.Message, "test-key") {
		t.Fatalf("missing or unsafe failure details: %+v", report)
	}
	if len(sessions) != 2 || sessions[0] == "" || sessions[0] != sessions[1] {
		t.Fatalf("detection probes lost their shared session: %v", sessions)
	}
}

func TestOpenCodeBasicStreamAndModels(t *testing.T) {
	ctx := providerprofile.WithProbeSession(context.Background())
	session := ""
	s := New(config.NewStore(&config.Config{}), protocol.NewRegistry(), observe.NewStore(2), &observe.Metrics{}, "test", "")
	s.Client = &http.Client{Transport: openCodeTransport(func(r *http.Request) (*http.Response, error) {
		got := r.Header.Get("x-opencode-session")
		if got == "" || (session != "" && session != got) {
			t.Error("missing or unstable session")
		}
		session = got
		body := `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`
		contentType := "application/json"
		if r.Method == http.MethodGet {
			if r.URL.Path != "/inference/go/v1/models" {
				t.Errorf("incorrect model URL: %s", r.URL)
			}
			body = `{"data":[{"id":"glm-5.3"}]}`
		} else {
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), `"stream":true`) {
				body = "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n"
				contentType = "text/event-stream"
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	p := config.Provider{BaseURL: "https://opencode.ai/inference/go/openai/v1", APIKey: "test-key", Protocol: ir.OpenAIChat, Models: []string{"glm-5.3"}, AllowPrivateURL: true}
	for _, kind := range []providerProbeKind{probeBasic, probeStreaming, probeModels} {
		if check := s.runProviderCheck(ctx, p, kind); !check.OK {
			t.Errorf("%s failed: %+v", kind, check)
		}
	}
}

func TestVerifiedOpenCodeReasoningIsNotDisabled(t *testing.T) {
	supported := capabilityCheck{OK: true, State: capabilitySupported}
	unobserved := capabilityCheck{State: capabilityInconclusive, ErrorCode: "combined_reasoning_not_observed"}
	endToEnd := supported
	endToEnd.Evidence = []string{"reasoning_configuration_preserved"}
	report := codexCompatibility(map[ir.Protocol]protocolCapabilityReport{
		ir.OpenAIChat: {Basic: supported, Streaming: &supported, Tools: &supported, ToolsWithReasoning: &unobserved, CodexEndToEnd: &endToEnd},
	})
	if !report.Verified || report.RecommendedReasoningWithTools != "supported" || len(report.RecommendedOmitFields) != 0 || report.RecommendedCompatibilityMode != "" {
		t.Fatalf("successful unmodified tool round trip must not recommend disabling thinking: %+v", report)
	}
	endToEnd.OK, endToEnd.State, endToEnd.ErrorCode = false, capabilityInconclusive, "codex_round_trip_timeout"
	report = codexCompatibility(map[ir.Protocol]protocolCapabilityReport{
		ir.OpenAIResponses: {Basic: supported},
		ir.OpenAIChat:      {Basic: supported, Streaming: &supported, Tools: &supported, ToolsWithReasoning: &unobserved, CodexEndToEnd: &endToEnd},
	})
	if report.Protocol != ir.OpenAIChat || report.RecommendedReasoningWithTools != "supported" || report.RecommendedCompatibilityMode != "" || report.Verified {
		t.Fatalf("a timeout must not disable reasoning or prefer an untested facade: %+v", report)
	}
}
