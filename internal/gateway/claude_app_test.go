package gateway

import (
	"encoding/hex"
	"testing"
)

func TestDecodeClaudeAppRouteModel(t *testing.T) {
	encoded := "anthropic/claude-ccr-h" + hex.EncodeToString([]byte("qwen3"))
	if got := decodeClaudeAppRouteModel(encoded); got != "qwen3" {
		t.Fatalf("decoded model = %q", got)
	}
	if got := decodeClaudeAppRouteModel("ordinary-model"); got != "ordinary-model" {
		t.Fatalf("ordinary model changed to %q", got)
	}
}
