package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/soooooollee/ai-router/internal/protocol/ir"
)

type Adapter interface {
	Protocol() ir.Protocol
	DecodeRequest(context.Context, json.RawMessage) (*ir.Request, []ir.Diagnostic, error)
	EncodeRequest(context.Context, *ir.Request) (json.RawMessage, []ir.Diagnostic, error)
	DecodeResponse(context.Context, json.RawMessage) (*ir.Response, []ir.Diagnostic, error)
	EncodeResponse(context.Context, *ir.Response) (json.RawMessage, []ir.Diagnostic, error)
	DecodeStreamEvent(context.Context, string, json.RawMessage) ([]ir.Event, []ir.Diagnostic, error)
	EncodeStreamEvent(context.Context, ir.Event) ([]SSE, []ir.Diagnostic, error)
}

type SSE struct {
	Event string
	Data  []byte
}

// StreamEncoder is implemented by adapters whose streaming encoding needs
// per-request state. Gemini, for example, only accepts a function call once the
// arguments are complete, so it buffers deltas instead of forwarding them.
type StreamEncoder interface {
	NewStreamEncoder() StreamEventEncoder
}

// StreamEventEncoder encodes a normalized event stream. One instance serves one
// response, so implementations may keep per-stream state.
type StreamEventEncoder interface {
	EncodeStreamEvent(context.Context, ir.Event) ([]SSE, []ir.Diagnostic, error)
}

type statelessStreamEncoder struct{ Adapter }

func (e statelessStreamEncoder) EncodeStreamEvent(ctx context.Context, event ir.Event) ([]SSE, []ir.Diagnostic, error) {
	return e.Adapter.EncodeStreamEvent(ctx, event)
}

// NewStreamEncoder returns the encoder a client protocol needs to render a
// normalized stream. Adapters that require per-stream state provide their own;
// everything else keeps using the stateless method.
func NewStreamEncoder(adapter Adapter) StreamEventEncoder {
	if stateful, ok := adapter.(StreamEncoder); ok {
		return stateful.NewStreamEncoder()
	}
	return statelessStreamEncoder{Adapter: adapter}
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[ir.Protocol]Adapter
}

func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{adapters: make(map[ir.Protocol]Adapter)}
	for _, a := range adapters {
		r.Register(a)
	}
	return r
}

func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Protocol()] = a
}

func (r *Registry) Get(p ir.Protocol) (Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[p]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol %q", p)
	}
	return a, nil
}

func Supported(p ir.Protocol) bool {
	switch p {
	case ir.OpenAIChat, ir.OpenAIResponses, ir.Anthropic, ir.Gemini:
		return true
	default:
		return false
	}
}
