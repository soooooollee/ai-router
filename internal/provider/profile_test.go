package provider

import (
	"encoding/json"
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
	raw, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[]}`), config.Provider{Profile: ProfileQwen3}, request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload["enable_thinking"] != false {
		t.Fatalf("Qwen3 thinking flag not mapped: %s", raw)
	}
}

func TestQwen3SystemBlocksBecomeOneLeadingStringMessage(t *testing.T) {
	request := &ir.Request{Model: "Qwen/Qwen3.6-35B-A3B"}
	raw, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}]}`), config.Provider{Profile: ProfileQwen3}, request)
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
	raw, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[]}`), config.Provider{Profile: ProfileQwen3, ReasoningMode: "disabled"}, request)
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
	raw, err := PrepareRequest([]byte(`{"model":"Qwen/Qwen3.6-35B-A3B","messages":[],"max_completion_tokens":32000}`), config.Provider{Profile: ProfileQwen3, MaxOutputTokens: 1024}, request)
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
	raw, err := PrepareRequest([]byte(`{"model":"m","temperature":0.2}`), config.Provider{RequestFields: map[string]any{"temperature": 0.8, "service_tier": "flex"}}, request)
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
	raw, err := PrepareRequest([]byte(`{"model":"mimo-v2.5","messages":[]}`), config.Provider{BaseURL: "https://api.xiaomimimo.com/v1"}, request)
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
