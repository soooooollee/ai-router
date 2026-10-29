package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
)

type conversationKey struct{}

func IsOpenCodeGo(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(u.Hostname(), "opencode.ai") {
		return false
	}
	return strings.HasPrefix(u.Path, "/inference/go/") || strings.HasPrefix(u.Path, "/zen/go/")
}

// WithProbeSession keeps all checks, retries and tool continuations in one
// detection run together, without sharing a session between unrelated runs.
func WithProbeSession(ctx context.Context) context.Context {
	if session, _ := ctx.Value(conversationKey{}).(string); session != "" {
		return ctx
	}
	return context.WithValue(ctx, conversationKey{}, "airoute-"+rand.Text())
}

// WithConversation preserves explicit client sessions across protocol conversion.
// Stateless clients get a best-effort fingerprint of their first user message,
// scoped to the caller. Identical opening messages need an explicit session ID
// to distinguish separate conversations; truncated histories do too.
func WithConversation(ctx context.Context, headers http.Header, request *ir.Request, caller string, raw ...json.RawMessage) context.Context {
	if session, _ := ctx.Value(conversationKey{}).(string); session != "" {
		return ctx
	}
	for _, name := range []string{"x-opencode-session", "session_id", "x-session-id", "x-claude-code-session-id"} {
		if session := strings.TrimSpace(headers.Get(name)); session != "" {
			return context.WithValue(ctx, conversationKey{}, session)
		}
	}
	metadata := request.Metadata
	if metadata == nil && len(raw) > 0 {
		var envelope struct {
			Metadata map[string]any `json:"metadata"`
		}
		_ = json.Unmarshal(raw[0], &envelope)
		metadata = envelope.Metadata
	}
	if session, _ := metadata["session_id"].(string); strings.TrimSpace(session) != "" {
		return context.WithValue(ctx, conversationKey{}, strings.TrimSpace(session))
	}
	// Claude Code also encodes the session in metadata.user_id.
	if user, _ := metadata["user_id"].(string); user != "" {
		var identity struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal([]byte(user), &identity)
		if identity.SessionID != "" {
			return context.WithValue(ctx, conversationKey{}, identity.SessionID)
		}
		if _, session, ok := strings.Cut(user, "_session_"); ok && session != "" {
			return context.WithValue(ctx, conversationKey{}, session)
		}
	}
	for _, message := range request.Messages {
		if message.Role != "user" {
			continue
		}
		content, _ := json.Marshal(message.Content)
		// Only a digest leaves the router; neither the prompt nor client key is
		// exposed in an upstream header. Do not use one global provider session.
		input, _ := json.Marshal([]string{caller, headers.Get("authorization"), headers.Get("x-api-key"), headers.Get("x-goog-api-key"), string(content)})
		digest := sha256.Sum256(input)
		return context.WithValue(ctx, conversationKey{}, "airoute-"+hex.EncodeToString(digest[:]))
	}
	return WithProbeSession(ctx)
}

// ApplyOpenCodeHeaders is shared by probes and actual upstream requests and is
// applied after configured headers, so explicit provider overrides win.
func ApplyOpenCodeHeaders(request *http.Request, p config.Provider) {
	if !IsOpenCodeGo(p.BaseURL) {
		return
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "AI-Router (+https://github.com/soooooollee/ai-router)")
	}
	if request.Header.Get("x-opencode-session") == "" {
		ctx := WithProbeSession(request.Context())
		request.Header.Set("x-opencode-session", ctx.Value(conversationKey{}).(string))
	}
}
