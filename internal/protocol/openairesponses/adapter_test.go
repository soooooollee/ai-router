package openairesponses

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/zbss/airoute/internal/protocol/ir"
)

func TestCustomToolRequestAndHistoryRoundTrip(t *testing.T) {
	raw := []byte(`{
  "model":"gpt-test",
  "tools":[{"type":"custom","name":"apply_patch","description":"Apply a patch","format":{"type":"grammar","syntax":"lark"}}],
  "tool_choice":{"type":"custom","name":"apply_patch"},
  "input":[
    {"type":"custom_tool_call","id":"ctc_1","call_id":"call_patch","name":"apply_patch","input":"*** Begin Patch\n*** End Patch"},
    {"type":"custom_tool_call_output","call_id":"call_patch","output":"Done"}
  ]
}`)
	adapter := New()
	request, _, err := adapter.DecodeRequest(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 1 || request.Tools[0].Type != "custom" || request.Tools[0].Name != "apply_patch" || !bytes.Contains(request.Tools[0].InputSchema, []byte(`"input"`)) {
		t.Fatalf("custom tool was not decoded: %#v", request.Tools)
	}
	if len(request.Messages) != 2 || request.Messages[0].Content[0].SourceType != "custom" || !bytes.Contains(request.Messages[0].Content[0].Arguments, []byte("*** Begin Patch")) || request.Messages[1].Content[0].SourceType != "custom" {
		t.Fatalf("custom tool history was not decoded: %#v", request.Messages)
	}

	encoded, _, err := adapter.EncodeRequest(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(encoded, &document) != nil {
		t.Fatalf("invalid encoded request: %s", encoded)
	}
	tools, _ := document["tools"].([]any)
	input, _ := document["input"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "custom" || len(input) != 2 || input[0].(map[string]any)["type"] != "custom_tool_call" || input[1].(map[string]any)["type"] != "custom_tool_call_output" {
		t.Fatalf("unexpected custom tool round trip: %s", encoded)
	}
}

func TestCustomToolResponseEncoding(t *testing.T) {
	response := &ir.Response{ID: "r1", Model: "gpt-test", Messages: []ir.Message{{Role: "assistant", Content: []ir.ContentBlock{{Type: "tool_call", SourceType: "custom", ID: "call_patch", Name: "apply_patch", Arguments: json.RawMessage(`{"input":"*** Begin Patch\n*** End Patch"}`)}}}}}
	raw, _, err := New().EncodeResponse(context.Background(), response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"type":"custom_tool_call"`)) || !bytes.Contains(raw, []byte(`"input":"*** Begin Patch\n*** End Patch"`)) || bytes.Contains(raw, []byte(`"type":"function_call"`)) {
		t.Fatalf("unexpected custom tool response: %s", raw)
	}
}
