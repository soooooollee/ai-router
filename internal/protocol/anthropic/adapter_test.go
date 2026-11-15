package anthropic_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zbss/airoute/internal/protocol/anthropic"
	"github.com/zbss/airoute/internal/protocol/ir"
)

func decode(t *testing.T, body string) *ir.Request {
	t.Helper()
	r, _, err := (&anthropic.Adapter{}).DecodeRequest(context.Background(), json.RawMessage(body))
	if err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return r
}

func encodePayload(t *testing.T, r *ir.Request) map[string]any {
	t.Helper()
	raw, _, err := (&anthropic.Adapter{}).EncodeRequest(context.Background(), r)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("encoded payload is not an object: %s", raw)
	}
	return payload
}

func TestThinkingBudgetSurvivesRoundTrip(t *testing.T) {
	body := `{"model":"claude-sonnet-4-5","max_tokens":16000,"thinking":{"type":"enabled","budget_tokens":8192},"messages":[{"role":"user","content":"hi"}]}`
	r := decode(t, body)
	if r.Sampling.ReasoningEnabled == nil || !*r.Sampling.ReasoningEnabled || r.Sampling.ReasoningBudget != 8192 {
		t.Fatalf("thinking budget was not decoded: %#v", r.Sampling)
	}
	thinking, _ := encodePayload(t, r)["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(8192) {
		t.Fatalf("thinking budget was not re-encoded: %#v", thinking)
	}
}

func TestThinkingBudgetNeverExceedsMaxTokens(t *testing.T) {
	r := decode(t, `{"model":"m","max_tokens":4000,"thinking":{"type":"enabled","budget_tokens":8000},"messages":[{"role":"user","content":"hi"}]}`)
	thinking, _ := encodePayload(t, r)["thinking"].(map[string]any)
	if thinking["budget_tokens"] != float64(3999) {
		t.Fatalf("budget must stay below max_tokens: %#v", thinking)
	}
}

func TestDisabledThinkingStillWins(t *testing.T) {
	r := decode(t, `{"model":"m","max_tokens":16000,"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hi"}]}`)
	thinking, _ := encodePayload(t, r)["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Fatalf("explicitly disabled thinking was overridden: %#v", thinking)
	}
}
