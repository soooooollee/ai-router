package ir

import "testing"

func TestStreamNormalizerSynthesizesLegalLifecycle(t *testing.T) {
	n := NewStreamNormalizer()
	input := []Event{
		{Type: "text.delta", ResponseID: "r", Delta: "hello", Index: 0},
		{Type: "tool_call.start", ResponseID: "r", Index: 1, Block: &ContentBlock{Type: "tool_call", ID: "c", Name: "f"}},
		{Type: "tool_call.arguments.delta", ResponseID: "r", Index: 1, Arguments: `{}`},
		{Type: "message.end", ResponseID: "r", StopReason: "tool_use"},
		{Type: "response.end", ResponseID: "r"},
	}
	var got []string
	for _, event := range input {
		for _, normalized := range n.Push(event) {
			got = append(got, normalized.Type)
		}
	}
	want := []string{"response.start", "message.start", "content.start", "text.delta", "content.start", "tool_call.start", "tool_call.arguments.delta", "content.end", "tool_call.end", "content.end", "message.end", "response.end"}
	if len(got) != len(want) {
		t.Fatalf("lifecycle length\nwant %#v\n got %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d want=%s got=%s; all=%#v", i, want[i], got[i], got)
		}
	}
}

func TestStreamNormalizerSeparatesReasoningAndTextSharingSourceIndex(t *testing.T) {
	n := NewStreamNormalizer()
	input := []Event{
		{Type: "reasoning.delta", ResponseID: "r", Delta: "think", Index: 0},
		{Type: "text.delta", ResponseID: "r", Delta: "answer", Index: 0},
		{Type: "response.end", ResponseID: "r"},
	}
	var events []Event
	for _, event := range input {
		events = append(events, n.Push(event)...)
	}
	var reasoningIndex, textIndex = -1, -1
	var ended = map[int]*ContentBlock{}
	for _, event := range events {
		switch event.Type {
		case "reasoning.delta":
			reasoningIndex = event.Index
		case "text.delta":
			textIndex = event.Index
		case "content.end":
			ended[event.Index] = event.Block
		}
	}
	if reasoningIndex < 0 || textIndex < 0 || reasoningIndex == textIndex {
		t.Fatalf("reasoning=%d text=%d events=%#v", reasoningIndex, textIndex, events)
	}
	if ended[reasoningIndex] == nil || ended[reasoningIndex].Text != "think" || ended[textIndex] == nil || ended[textIndex].Text != "answer" {
		t.Fatalf("completed blocks lost content: %#v", ended)
	}
}
