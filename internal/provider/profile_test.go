package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
)

func TestQwen3ProfileAndModelDetection(t *testing.T) {
	if got := EffectiveProfile(config.Provider{}, "Qwen/Qwen3.6-35B-A3B"); got != ProfileQwen3 {
		t.Fatalf("Qwen3 model was not detected: %s", got)
	}
	disabled := false
	request := &ir.Request{Model: "Qwen/Qwen3.6-35B-A3B", Sampling: ir.SamplingOptions{ReasoningEnabled: &disabled}}
	raw, _, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[]}`), config.Provider{Profile: ProfileQwen3}, request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload["enable_thinking"] != false {
		t.Fatalf("Qwen3 thinking flag not mapped: %s", raw)
	}
}

func TestDetectionProfileRegistry(t *testing.T) {
	if got := DetectProfile("https://api.xiaomimimo.com/v1", []string{"mimo-v2.5"}); got != ProfileXiaomiMiMo {
		t.Fatalf("MiMo fingerprint was not registered: %s", got)
	}
	strategy := ProviderDetectionStrategy(ProfileXiaomiMiMo)
	if strategy.Version != DetectionProfileVersion || strategy.PreferredProtocols[0] != ir.OpenAIResponses || strategy.ToolChoiceMode != "auto-only" || strategy.ReasoningHistory != "preserve" {
		t.Fatalf("unexpected MiMo detection strategy: %#v", strategy)
	}
	strategy.PreferredProtocols[0] = ir.Gemini
	if ProviderDetectionStrategy(ProfileXiaomiMiMo).PreferredProtocols[0] != ir.OpenAIResponses {
		t.Fatal("callers can mutate the shared detection profile registry")
	}
}

func TestXiaomiAnthropicUsesBearerAuthentication(t *testing.T) {
	xiaomi := config.Provider{
		Protocol: ir.Anthropic,
		Profile:  ProfileXiaomiMiMo,
		BaseURL:  "https://api.xiaomimimo.com/anthropic",
		APIKey:   "mimo-secret",
	}
	header := make(http.Header)
	header.Set("x-api-key", "stale")
	ApplyAuthenticationHeaders(header, xiaomi)
	if header.Get("authorization") != "Bearer mimo-secret" || header.Get("x-api-key") != "" {
		t.Fatalf("unexpected Xiaomi Anthropic authentication: %#v", header)
	}

	standard := config.Provider{
		Protocol: ir.Anthropic,
		BaseURL:  "https://api.anthropic.com",
		APIKey:   "anthropic-secret",
	}
	header = make(http.Header)
	header.Set("authorization", "Bearer stale")
	ApplyAuthenticationHeaders(header, standard)
	if header.Get("x-api-key") != "anthropic-secret" || header.Get("authorization") != "" {
		t.Fatalf("unexpected standard Anthropic authentication: %#v", header)
	}
}

func TestXiaomiAnthropicAutoToolChoiceUsesAnthropicShape(t *testing.T) {
	request := &ir.Request{Model: "mimo-v2.5"}
	raw, _, err := PrepareRequest(
		[]byte(`{"model":"mimo-v2.5","messages":[],"tools":[{"name":"apply_patch","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"apply_patch"}}`),
		config.Provider{
			Protocol:       ir.Anthropic,
			Profile:        ProfileXiaomiMiMo,
			ToolChoiceMode: "auto-only",
		},
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	toolChoice, _ := payload["tool_choice"].(map[string]any)
	if toolChoice["type"] != "auto" {
		t.Fatalf("Anthropic auto tool choice was not protocol-shaped: %s", raw)
	}
}

func TestQwen3SystemBlocksBecomeOneLeadingStringMessage(t *testing.T) {
	request := &ir.Request{Model: "Qwen/Qwen3.6-35B-A3B"}
	raw, _, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}]}`), config.Provider{Profile: ProfileQwen3}, request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	messages := payload["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "first\n\nsecond" || len(messages) != 2 {
		t.Fatalf("Qwen3 system messages were not normalized: %s", raw)
	}
}

func TestQwen3ProviderReasoningModeOverridesClient(t *testing.T) {
	enabled := true
	request := &ir.Request{Model: "Qwen/Qwen3.6-35B-A3B", Sampling: ir.SamplingOptions{ReasoningEnabled: &enabled}}
	raw, _, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[]}`), config.Provider{Profile: ProfileQwen3, ReasoningMode: "disabled"}, request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload["enable_thinking"] != false {
		t.Fatalf("provider reasoning policy was not enforced: %s", raw)
	}
}

func TestProviderMaxOutputTokensCapsLargeAgentBudget(t *testing.T) {
	request := &ir.Request{Model: "Qwen/Qwen3.6-35B-A3B"}
	raw, _, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[],"max_completion_tokens":32000}`), config.Provider{Profile: ProfileQwen3, MaxOutputTokens: 1024}, request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload["max_completion_tokens"] != float64(1024) {
		t.Fatalf("provider output cap not applied: %s", raw)
	}
}

func TestProviderRequestFieldsRemainDefaults(t *testing.T) {
	request := &ir.Request{Model: "m"}
	raw, _, err := PrepareRequest([]byte(`{"model":"m","temperature":0.2}`), config.Provider{RequestFields: map[string]any{"temperature": 0.8, "service_tier": "flex"}}, request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload["temperature"] != 0.2 || payload["service_tier"] != "flex" {
		t.Fatalf("defaults overwrote client fields or were not applied: %s", raw)
	}
}

func TestXiaomiMiMoDisabledThinkingShape(t *testing.T) {
	disabled := false
	request := &ir.Request{Model: "mimo-v2.5", Sampling: ir.SamplingOptions{ReasoningEnabled: &disabled}}
	raw, _, err := PrepareRequest([]byte(`{"model":"mimo-v2.5","messages":[]}`), config.Provider{BaseURL: "https://api.xiaomimimo.com/v1"}, request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	thinking, _ := payload["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Fatalf("MiMo thinking flag not mapped: %s", raw)
	}
}

func TestProviderRequestPolicyOmitsFieldsAndReportsDiagnostic(t *testing.T) {
	request := &ir.Request{Model: "gpt-5.5"}
	raw, diagnostics, err := PrepareRequest(
		[]byte(`{"model":"gpt-5.5","messages":[],"tools":[],"reasoning_effort":"high"}`),
		config.Provider{RequestPolicy: config.RequestPolicy{OmitFields: []string{"reasoning_effort"}}},
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if _, exists := payload["reasoning_effort"]; exists {
		t.Fatalf("request policy did not omit reasoning_effort: %s", raw)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != "request_field_omitted_by_policy" || diagnostics[0].Path != "reasoning_effort" {
		t.Fatalf("unexpected policy diagnostics: %#v", diagnostics)
	}
}

func TestCodexChatCompatibilityModeTransformsConvertedRequest(t *testing.T) {
	request := &ir.Request{Model: "gpt-5.5"}
	raw, diagnostics, err := PrepareRequest(
		[]byte(`{"model":"gpt-5.5","messages":[],"tools":[],"reasoning_effort":"high"}`),
		config.Provider{Protocol: ir.OpenAIChat, CompatibilityMode: "codex-chat"},
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("reasoning_effort")) {
		t.Fatalf("compatibility mode did not transform the request: %s", raw)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != "codex_chat_compatibility_mode" {
		t.Fatalf("unexpected compatibility diagnostics: %#v", diagnostics)
	}
}

func TestDetectedProviderPoliciesRewriteToolChoiceAndReasoning(t *testing.T) {
	request := &ir.Request{Model: "mimo-v2.5"}
	raw, diagnostics, err := PrepareRequest(
		[]byte(`{"model":"mimo-v2.5","tools":[{"type":"function","name":"weather"}],"tool_choice":{"type":"function","name":"weather"},"reasoning":{"effort":"high"}}`),
		config.Provider{Protocol: ir.OpenAIResponses, Profile: ProfileXiaomiMiMo, ToolChoiceMode: "auto-only", ReasoningHistory: "preserve", ReasoningWithTools: "disabled"},
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	thinking, _ := payload["thinking"].(map[string]any)
	if payload["tool_choice"] != "auto" || payload["reasoning"] != nil || thinking["type"] != "disabled" {
		t.Fatalf("provider policies were not applied: %s", raw)
	}
	if len(diagnostics) != 2 {
		t.Fatalf("expected tool and reasoning diagnostics: %#v", diagnostics)
	}
}
