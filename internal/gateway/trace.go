package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const traceparentHeader = "traceparent"

type traceContextKey struct{}

// traceparentFromRequest reuses a valid inbound W3C trace context and starts a
// new one otherwise, so a request can be followed across the gateway, the
// upstream and the caller's own tracing.
func traceparentFromRequest(value string) string {
	if validTraceparent(value) {
		return strings.TrimSpace(value)
	}
	return newTraceparent()
}

func newTraceparent() string {
	var traceID [16]byte
	var span [8]byte
	_, _ = rand.Read(traceID[:])
	_, _ = rand.Read(span[:])
	return "00-" + hex.EncodeToString(traceID[:]) + "-" + hex.EncodeToString(span[:]) + "-01"
}

func validTraceparent(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return false
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return false
	}
	return strings.Trim(parts[1], "0") != ""
}

// traceID is the 32 hex character identifier inside a traceparent.
func traceID(traceparent string) string {
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) != 4 {
		return ""
	}
	return parts[1]
}

func withTraceparent(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, traceContextKey{}, value)
}

func traceparentFromContext(ctx context.Context) string {
	value, _ := ctx.Value(traceContextKey{}).(string)
	return value
}
