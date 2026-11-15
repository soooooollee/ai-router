package gemini_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soooooollee/ai-router/internal/protocol"
	"github.com/soooooollee/ai-router/internal/protocol/gemini"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
)

func encodeAll(t *testing.T, events ...ir.Event) []protocol.SSE {
	t.Helper()
	encoder := (&gemini.Adapter{}).NewStreamEncoder()
	var chunks []protocol.SSE
	for _, event := range events {
		encoded, _, err := encoder.EncodeStreamEvent(context.Background(), event)
		if err != nil {
			t.Fatalf("encode %s: %v", event.Type, err)
		}
		chunks = append(chunks, encoded...)
	}
	return chunks
}

func functionCalls(t *testing.T, chunks []protocol.SSE) []any {
	t.Helper()
	var calls []any
	for _, chunk := range chunks {
		var payload map[string]any
		if err := json.Unmarshal(chunk.Data, &payload); err != nil {
			t.Fatalf("chunk is not JSON: %s", chunk.Data)
		}
		candidates, _ := payload["candidates"].([]any)
		for _, candidate := range candidates {
			parts, _ := candidate.(map[string]any)["content"].(map[string]any)["parts"].([]any)
			for _, part := range parts {
				if call, ok := part.(map[string]any)["functionCall"]; ok {
					calls = append(calls, call)
				}
			}
		}
	}
	return calls
}

// Gemini accepts a function call only once its arguments are complete, so the
// encoder must withhold the deltas or the model gets a call without arguments.
func TestStreamEncoderBuffersToolArgumentsUntilContentEnd(t *testing.T) {
	chunks := encodeAll(t,
		ir.Event{Type: "tool_call.start", Index: 0, Block: &ir.ContentBlock{Type: "tool_call", ID: "c1", Name: "lookup"}},
		ir.Event{Type: "tool_call.arguments.delta", Index: 0, Arguments: `{"q":`},
		ir.Event{Type: "tool_call.arguments.delta", Index: 0, Arguments: `"x"}`},
		ir.Event{Type: "content.end", Index: 0},
	)
	calls := functionCalls(t, chunks)
	if len(calls) != 1 {
		t.Fatalf("expected exactly one function call, got %d: %s", len(calls), chunks)
	}
	args, _ := calls[0].(map[string]any)["args"].(map[string]any)
	if args["q"] != "x" {
		t.Fatalf("arguments were not reassembled: %#v", calls[0])
	}
}

func TestStreamEncoderFlushesPendingToolsBeforeMessageEnd(t *testing.T) {
	chunks := encodeAll(t,
		ir.Event{Type: "tool_call.start", Index: 3, Block: &ir.ContentBlock{Type: "tool_call", ID: "c9", Name: "lookup"}},
		ir.Event{Type: "tool_call.arguments.delta", Index: 3, Arguments: `{}`},
		ir.Event{Type: "message.end", Index: 3, StopReason: "tool_use"},
	)
	calls := functionCalls(t, chunks)
	if len(calls) != 1 || calls[0].(map[string]any)["name"] != "lookup" {
		t.Fatalf("pending tool call was not flushed at message end: %s", chunks)
	}
	// The finish reason must still be emitted after the flushed call.
	var sawFinish bool
	for _, chunk := range chunks {
		var payload map[string]any
		_ = json.Unmarshal(chunk.Data, &payload)
		candidates, _ := payload["candidates"].([]any)
		for _, candidate := range candidates {
			if reason := candidate.(map[string]any)["finishReason"]; reason != nil {
				sawFinish = true
			}
		}
	}
	if !sawFinish {
		t.Fatalf("message.end lost its finish reason: %s", chunks)
	}
}
