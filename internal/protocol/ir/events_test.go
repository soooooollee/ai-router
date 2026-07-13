package ir

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCompletedResponseUsesCanonicalEventLifecycle(t *testing.T) {
	original := &Response{ID: "r1", Model: "m", StopReason: "tool_use", Metadata: map[string]any{"region": "test"}, Messages: []Message{{Role: "assistant", Content: []ContentBlock{
		{Type: "reasoning", Text: "think", Source: OpenAIResponses},
		{Type: "text", Text: "answer", Source: OpenAIResponses},
		{Type: "tool_call", ID: "c1", Name: "weather", Arguments: json.RawMessage(`{"city":"Paris"}`), Source: OpenAIResponses},
		{Type: "document", MediaType: "application/pdf", FileID: "file_1", Source: OpenAIResponses},
	}}}, Usage: Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
	events := EventsFromResponse(original)
	required := map[string]bool{"response.start": false, "message.start": false, "content.start": false, "text.delta": false, "reasoning.delta": false, "tool_call.start": false, "tool_call.arguments.delta": false, "tool_call.end": false, "content.end": false, "usage.update": false, "message.end": false, "response.end": false}
	for _, event := range events {
		if _, ok := required[event.Type]; ok {
			required[event.Type] = true
		}
	}
	for event, seen := range required {
		if !seen {
			t.Fatalf("canonical lifecycle omitted %s: %#v", event, events)
		}
	}
	aggregated, err := AggregateEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(aggregated, original) {
		want, _ := json.Marshal(original)
		got, _ := json.Marshal(aggregated)
		t.Fatalf("event aggregation changed response\nwant %s\n got %s", want, got)
	}
}
