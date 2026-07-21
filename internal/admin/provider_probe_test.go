package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/ir"
)

func TestProviderProbeValidatesSchemasAndCompleteSSE(t *testing.T) {
	if code, _ := validateProviderJSON(ir.OpenAIResponses, []byte(`{"choices":[]}`)); code != "schema_mismatch" {
		t.Fatalf("Responses schema mismatch was not detected: %q", code)
	}
	if code, _ := validateProviderJSON(ir.OpenAIChat, []byte(`{"choices":[]}`)); code != "" {
		t.Fatalf("valid Chat response was rejected: %q", code)
	}
	if code, _ := validateProviderSSE(ir.OpenAIChat, "text/event-stream", []byte("data: [DONE]\n\n")); code != "sse_incomplete" {
		t.Fatalf("empty Chat SSE lifecycle was accepted: %q", code)
	}
	valid := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
	if code, message := validateProviderSSE(ir.OpenAIChat, "text/event-stream", valid); code != "" {
		t.Fatalf("valid Chat SSE was rejected: %s %s", code, message)
	}
}

func TestProviderProbeErrorTaxonomyAndHTMLRedaction(t *testing.T) {
	tests := []struct {
		status int
		body   string
		code   string
	}{
		{http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, "authentication_failed"},
		{http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, "rate_limited"},
		{http.StatusPaymentRequired, `{"error":{"message":"balance"}}`, "quota_exhausted"},
		{http.StatusBadRequest, `{"error":{"message":"Function tools with reasoning_effort are not supported"}}`, "tools_with_reasoning_unsupported"},
	}
	for _, test := range tests {
		if code, _ := classifyProviderError(test.status, []byte(test.body)); code != test.code {
			t.Errorf("status %d classified as %q, want %q", test.status, code, test.code)
		}
	}
	message := providerErrorMessage([]byte("<html><body><h1>404 Not Found</h1></body></html>"))
	if strings.Contains(strings.ToLower(message), "<html") || message != "provider returned an HTML error page" {
		t.Fatalf("HTML error was not summarized safely: %q", message)
	}
}

func TestProviderProbeCacheKeyIncludesDetectorVersion(t *testing.T) {
	key := providerProbeCacheKey("https://example.com/v1", "secret", []string{"model"}, false)
	if key == "" || strings.Contains(key, "secret") || len(key) != 64 {
		t.Fatalf("probe cache key is not a secret-safe versioned digest: %q", key)
	}
}

func TestXiaomiAnthropicURLOverridesProfileCandidatesAndAuthentication(t *testing.T) {
	var requests int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/anthropic/v1/messages" {
			t.Errorf("unexpected probe path: %s", r.URL.Path)
		}
		if r.Header.Get("authorization") != "Bearer mimo-secret" {
			t.Errorf("missing Xiaomi Bearer authentication: %#v", r.Header)
		}
		if r.Header.Get("x-api-key") != "" {
			t.Errorf("unexpected Anthropic x-api-key header: %#v", r.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("invalid request payload: %v", err)
		}
		if payload["stream"] == true {
			tools, _ := payload["tools"].([]any)
			if len(tools) != 1 {
				t.Errorf("Codex end-to-end request did not contain one tool: %#v", payload)
			}
			toolChoice, _ := payload["tool_choice"].(map[string]any)
			if toolChoice["type"] != "auto" {
				t.Errorf("Xiaomi Anthropic tool choice must use the Anthropic auto shape: %#v", payload["tool_choice"])
			}
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte(
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-e2e\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"mimo-v2.5\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-call-1\",\"name\":\"apply_patch\",\"input\":{}}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"input\\\":\\\"*** Begin Patch\\\\n*** End Patch\\\"}\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			))
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"mimo-v2.5","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	s := New(config.NewStore(&config.Config{}), protocol.NewRegistry(), observe.NewStore(2), &observe.Metrics{}, "test", "")
	report := s.detectProviderCapabilities(
		context.Background(),
		upstream.URL+"/anthropic",
		"mimo-secret",
		[]string{"mimo-v2.5"},
		true,
	)
	if !report.OK || report.Protocol != ir.Anthropic {
		t.Fatalf("Xiaomi Anthropic endpoint was not detected: %#v", report)
	}
	if report.CodexCompatibility.Status != "full" ||
		report.CodexCompatibility.Protocol != ir.Anthropic ||
		report.CodexCompatibility.RecommendedIntegrationMode != "compatibility" {
		t.Fatalf("unexpected Anthropic Codex conclusion: %#v", report.CodexCompatibility)
	}
	anthropicReport := report.Protocols[ir.Anthropic]
	if anthropicReport.CodexEndToEnd == nil || anthropicReport.CodexEndToEnd.State != capabilitySupported {
		t.Fatalf("Anthropic Codex end-to-end evidence was not retained: %#v", anthropicReport.CodexEndToEnd)
	}
	if requests != 3 {
		t.Fatalf("expected native verification plus Codex tool call and continuation, got %d requests", requests)
	}
}

func TestToolsWithReasoningRequiresBothPositiveSignals(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r","status":"completed","output":[{"type":"function_call","call_id":"c","name":"airoute_probe","arguments":"{}"}]}`))
	}))
	defer upstream.Close()

	s := New(config.NewStore(&config.Config{}), protocol.NewRegistry(), observe.NewStore(2), &observe.Metrics{}, "test", "")
	provider := config.Provider{Protocol: ir.OpenAIResponses, BaseURL: upstream.URL + "/v1", Models: []string{"model"}, AllowPrivateURL: true}
	result := s.runProviderCheckWithStrategy(context.Background(), provider, probeToolsWithReasoning, toolChoiceForced, nil)
	if result.State != capabilityInconclusive || result.ErrorCode != "combined_reasoning_not_observed" {
		t.Fatalf("tool-only response was accepted as tools+reasoning support: %#v", result)
	}
}

func TestCombinedToolEvidenceRecoversAnInconclusivePlainToolProbe(t *testing.T) {
	plain := capabilityCheck{State: capabilityInconclusive, ErrorCode: "timeout", LatencyMS: 8000}
	combined := capabilityCheck{OK: true, State: capabilitySupported, Evidence: []string{"tool_call_observed", "reasoning_observed_with_tool_call"}}
	result := inferToolSupportFromCombinedCheck(plain, combined)
	if result.State != capabilitySupported || !hasCapabilityEvidence(result, "tools_inferred_from_tools_with_reasoning") {
		t.Fatalf("combined tool evidence did not recover the plain tool timeout: %#v", result)
	}

	explicit := capabilityCheck{OK: true, State: capabilitySupported, Evidence: []string{"plain_tool_probe"}}
	result = inferToolSupportFromCombinedCheck(explicit, combined)
	if !hasCapabilityEvidence(result, "plain_tool_probe") || hasCapabilityEvidence(result, "tools_inferred_from_tools_with_reasoning") {
		t.Fatalf("explicit plain-tool evidence was unnecessarily replaced: %#v", result)
	}
}

func TestCodexCompatibilityRequiresReasoningAndPrefersVerifiedDegradedProtocol(t *testing.T) {
	supported := capabilityCheck{OK: true, State: capabilitySupported, Confidence: 1}
	inconclusive := capabilityCheck{State: capabilityInconclusive, Confidence: 0.5, ErrorCode: "reasoning_not_observed"}
	unsupported := capabilityCheck{State: capabilityUnsupported, Confidence: 0.9, ErrorCode: "tools_with_reasoning_unsupported"}
	roundTrip := supported
	roundTrip.Evidence = []string{"tool_result_round_trip_succeeded", "reasoning_history_preserved"}

	reports := map[ir.Protocol]protocolCapabilityReport{
		ir.OpenAIResponses: {
			Basic: supported, Streaming: &supported, Tools: &supported, Reasoning: &inconclusive,
			ToolsWithReasoning: &supported, ToolRoundTrip: &roundTrip, CodexEndToEnd: &supported,
		},
	}
	result := codexCompatibility(reports)
	if result.Status == "full" {
		t.Fatalf("missing reasoning evidence was promoted to full compatibility: %#v", result)
	}

	reports[ir.OpenAIChat] = protocolCapabilityReport{
		Basic: supported, Streaming: &supported, Tools: &supported, Reasoning: &supported,
		ToolsWithReasoning: &unsupported, ToolRoundTrip: &supported, CodexEndToEnd: &supported,
	}
	result = codexCompatibility(reports)
	if result.Status != "degraded" || result.Protocol != ir.OpenAIChat {
		t.Fatalf("verified degraded Chat should outrank unverified Responses: %#v", result)
	}
}

func TestCodexCompatibilityUsesRouterEndToEndToResolveStreamingTimeout(t *testing.T) {
	supported := capabilityCheck{OK: true, State: capabilitySupported, Confidence: 1}
	unsupported := capabilityCheck{State: capabilityUnsupported, Confidence: 0.9, ErrorCode: "tools_with_reasoning_unsupported"}
	timedOut := capabilityCheck{State: capabilityInconclusive, Confidence: 1, ErrorCode: "timeout"}
	reports := map[ir.Protocol]protocolCapabilityReport{
		ir.OpenAIChat: {
			Basic: supported, Streaming: &timedOut, Tools: &supported,
			ToolsWithReasoning: &unsupported, CodexEndToEnd: &supported,
		},
	}
	result := codexCompatibility(reports)
	if result.Status != "degraded" || result.RecommendedIntegrationMode != "compatibility" || result.RecommendedCompatibilityMode != "codex-chat" || result.RecommendedReasoningWithTools != "disabled" {
		t.Fatalf("Router end-to-end evidence did not resolve the standalone streaming timeout: %#v", result)
	}
	if !strings.Contains(result.Message, "移除 reasoning_effort 并保留工具调用") {
		t.Fatalf("Chat compatibility explanation is not actionable: %q", result.Message)
	}

	reports[ir.OpenAIChat] = protocolCapabilityReport{
		Basic: supported, Streaming: &timedOut, Tools: &supported,
		ToolsWithReasoning: &unsupported, CodexEndToEnd: &timedOut,
	}
	result = codexCompatibility(reports)
	if result.Status != "unverified" || result.RecommendedIntegrationMode != "compatibility" || result.RecommendedCompatibilityMode != "codex-chat" {
		t.Fatalf("an inconclusive Router test should still recommend an explicit compatibility policy: %#v", result)
	}
}

func TestCodexCompatibilityExplainsInconclusiveRouterEndToEnd(t *testing.T) {
	supported := capabilityCheck{OK: true, State: capabilitySupported, Confidence: 1}
	timedOut := capabilityCheck{State: capabilityInconclusive, Confidence: 1, ErrorCode: "codex_timeout", Error: "request deadline exceeded", LatencyMS: 18000}
	reports := map[ir.Protocol]protocolCapabilityReport{
		ir.OpenAIChat: {
			Basic: supported, Streaming: &supported, Tools: &supported, Reasoning: &supported,
			ToolsWithReasoning: &supported, ToolRoundTrip: &supported, CodexEndToEnd: &timedOut,
		},
	}
	result := codexCompatibility(reports)
	if result.Status != "unverified" || result.RecommendedIntegrationMode != "compatibility" || result.RecommendedCompatibilityMode != "codex-chat" {
		t.Fatalf("inconclusive Router result did not retain a usable compatibility recommendation: %#v", result)
	}
	if !strings.Contains(result.Message, "端到端验证超时") || !strings.Contains(result.Message, "18.0 秒") || !strings.Contains(result.Message, "其余流式输出") {
		t.Fatalf("timeout reason and latency were not exposed: %q", result.Message)
	}

	missingTool := timedOut
	missingTool.ErrorCode = "codex_custom_tool_not_observed"
	missingTool.Error = "AI Router completed the request but did not reconstruct a Codex custom tool event"
	missingTool.LatencyMS = 4321
	reports[ir.OpenAIChat] = protocolCapabilityReport{
		Basic: supported, Streaming: &supported, Tools: &supported, Reasoning: &supported,
		ToolsWithReasoning: &supported, ToolRoundTrip: &supported, CodexEndToEnd: &missingTool,
	}
	result = codexCompatibility(reports)
	if !strings.Contains(result.Message, "未观察到模型触发 apply_patch custom tool") || !strings.Contains(result.Message, "4.3 秒") {
		t.Fatalf("missing custom-tool reason was not exposed: %q", result.Message)
	}
}

func TestChatRouterEndToEndOverridesInconclusiveStandaloneStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		tools, hasTools := payload["tools"]
		if payload["stream"] == true && !hasTools {
			// The standalone stream contract is intentionally inconclusive.
			w.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
			return
		}
		if payload["stream"] == true && tools != nil {
			name := requestedChatToolName(payload)
			arguments := `{"city":"Beijing"}`
			if name == "apply_patch" {
				arguments = `{"input":"*** Begin Patch\n*** End Patch"}`
			}
			w.Header().Set("content-type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"r1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"probe\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", name, arguments)
			return
		}
		w.Header().Set("content-type", "application/json")
		if hasTools && payload["reasoning_effort"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Function tools with reasoning_effort are not supported"}}`)
			return
		}
		if hasTools {
			name := requestedChatToolName(payload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "probe",
							"type": "function",
							"function": map[string]any{
								"name":      name,
								"arguments": `{"city":"Beijing"}`,
							},
						}},
					},
				}},
			})
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`)
	}))
	defer upstream.Close()

	s := New(config.NewStore(&config.Config{}), protocol.NewRegistry(), observe.NewStore(2), &observe.Metrics{}, "test", "")
	report := s.detectProviderCapabilities(context.Background(), upstream.URL+"/v1", "key", []string{"gpt-test"}, true)
	chat := report.Protocols[ir.OpenAIChat]
	if report.Protocol != ir.OpenAIChat || report.CodexCompatibility.Status != "degraded" || report.CodexCompatibility.RecommendedIntegrationMode != "compatibility" {
		t.Fatalf("Router end-to-end result did not resolve Chat compatibility: compatibility=%#v streaming=%#v tools=%#v combined=%#v end_to_end=%#v", report.CodexCompatibility, *chat.Streaming, *chat.Tools, *chat.ToolsWithReasoning, *chat.CodexEndToEnd)
	}
	if chat.Streaming == nil || chat.Streaming.State != capabilityInconclusive || chat.CodexEndToEnd == nil || chat.CodexEndToEnd.State != capabilitySupported {
		t.Fatalf("unexpected standalone/Router evidence: streaming=%#v end_to_end=%#v", chat.Streaming, chat.CodexEndToEnd)
	}
}
