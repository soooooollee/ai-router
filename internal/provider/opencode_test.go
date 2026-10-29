package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
)

func TestOpenCodeSessionContinuityAndIsolation(t *testing.T) {
	first := ir.Message{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: "Fix the bug"}}}
	request := &ir.Request{Messages: []ir.Message{first}}
	session := func(headers http.Header, caller string) string {
		ctx := WithConversation(context.Background(), headers, request, caller)
		return ctx.Value(conversationKey{}).(string)
	}
	original := session(nil, "alice")
	request.Messages = append(request.Messages, ir.Message{Role: "assistant"}, ir.Message{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: "Continue"}}})
	if session(nil, "alice") != original || session(nil, "bob") == original {
		t.Fatal("fallback session must survive follow-ups and isolate callers")
	}
	request.Messages[0].Content = []ir.ContentBlock{{Type: "text", Text: "A new task"}}
	if session(nil, "alice") == original {
		t.Fatal("different conversations share a session")
	}
	for _, header := range []string{"x-opencode-session", "session_id", "x-session-id", "x-claude-code-session-id"} {
		headers := http.Header{}
		headers.Set(header, "client-session")
		if got := session(headers, "alice"); got != "client-session" {
			t.Errorf("%s was lost: %s", header, got)
		}
	}
	for _, userID := range []string{`{"session_id":"claude-session","account_uuid":"account"}`, "user_device_account_account_session_claude-session"} {
		request.Metadata = map[string]any{"user_id": userID}
		if got := session(nil, "alice"); got != "claude-session" {
			t.Errorf("metadata session lost: %s", got)
		}
	}
	request.Metadata = nil
	ctxFromBody := WithConversation(context.Background(), nil, request, "alice", []byte(`{"metadata":{"user_id":"{\"session_id\":\"claude-body-session\"}"}}`))
	if ctxFromBody.Value(conversationKey{}) != "claude-body-session" {
		t.Fatal("Claude metadata session was lost during decoding")
	}
	ctx := WithProbeSession(context.Background())
	if WithConversation(ctx, nil, request, "alice") != ctx || WithProbeSession(ctx) != ctx {
		t.Fatal("probe continuation lost its session")
	}
	if ctx.Value(conversationKey{}) == WithProbeSession(context.Background()).Value(conversationKey{}) {
		t.Fatal("separate probes share a session")
	}
}

func TestOpenCodeHeadersOnlyForGoAndPreserveOverrides(t *testing.T) {
	for _, base := range []string{
		"https://opencode.ai/inference/go/openai/v1",
		"https://opencode.ai/inference/go/anthropic/v1",
		"https://opencode.ai/zen/go/v1",
		"https://opencode.ai/inference/openai/v1",
		"https://opencode.ai.evil.example/inference/go/openai/v1",
		"https://example.com/inference/go/openai/v1",
	} {
		t.Run(base, func(t *testing.T) {
			r, _ := http.NewRequest("POST", base, nil)
			ApplyOpenCodeHeaders(r, config.Provider{BaseURL: base})
			want := base == "https://opencode.ai/inference/go/openai/v1" || base == "https://opencode.ai/inference/go/anthropic/v1" || base == "https://opencode.ai/zen/go/v1"
			if (r.Header.Get("x-opencode-session") != "") != want || (r.Header.Get("User-Agent") != "") != want {
				t.Fatalf("unexpected headers for %s: %v", base, r.Header)
			}
			r.Header.Set("x-opencode-session", "configured-session")
			r.Header.Set("User-Agent", "Custom-Agent/1.0")
			ApplyOpenCodeHeaders(r, config.Provider{BaseURL: base})
			if r.Header.Get("x-opencode-session") != "configured-session" || r.Header.Get("User-Agent") != "Custom-Agent/1.0" {
				t.Fatal("configured headers were overwritten")
			}
		})
	}
}
