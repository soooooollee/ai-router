package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zbss/airoute/internal/protocol/ir"
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
