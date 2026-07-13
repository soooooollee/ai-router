package ir

import "sort"

// StreamNormalizer turns provider-specific partial streams into the complete
// canonical lifecycle consumed by the gateway core.
type StreamNormalizer struct {
	responseStarted bool
	messageStarted  bool
	messageEnded    bool
	contentStarted  map[int]string
	contentEnded    map[int]bool
	toolStarted     map[int]bool
}

func NewStreamNormalizer() *StreamNormalizer {
	return &StreamNormalizer{contentStarted: map[int]string{}, contentEnded: map[int]bool{}, toolStarted: map[int]bool{}}
}

func (n *StreamNormalizer) Push(event Event) []Event {
	var out []Event
	startEnvelope := func() {
		if !n.responseStarted {
			out = append(out, Event{Type: "response.start", ResponseID: event.ResponseID, Model: event.Model})
			n.responseStarted = true
		}
		if !n.messageStarted {
			out = append(out, Event{Type: "message.start", ResponseID: event.ResponseID, Role: "assistant"})
			n.messageStarted = true
		}
	}
	startContent := func(index int, typ string, block *ContentBlock) {
		startEnvelope()
		if _, ok := n.contentStarted[index]; !ok {
			if block == nil {
				block = &ContentBlock{Type: typ}
			}
			out = append(out, Event{Type: "content.start", ResponseID: event.ResponseID, Index: index, Block: block})
			n.contentStarted[index] = typ
		}
	}
	closeContent := func(index int) {
		if n.contentEnded[index] {
			return
		}
		if n.toolStarted[index] {
			out = append(out, Event{Type: "tool_call.end", ResponseID: event.ResponseID, Index: index})
			delete(n.toolStarted, index)
		}
		out = append(out, Event{Type: "content.end", ResponseID: event.ResponseID, Index: index})
		n.contentEnded[index] = true
	}
	closeAll := func() {
		indexes := make([]int, 0, len(n.contentStarted))
		for index := range n.contentStarted {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			closeContent(index)
		}
	}

	switch event.Type {
	case "response.start":
		if !n.responseStarted {
			out = append(out, event)
			n.responseStarted = true
		}
	case "message.start":
		if !n.responseStarted {
			out = append(out, Event{Type: "response.start", ResponseID: event.ResponseID, Model: event.Model})
			n.responseStarted = true
		}
		if !n.messageStarted {
			out = append(out, event)
			n.messageStarted = true
		}
	case "content.start":
		typ := "text"
		if event.Block != nil && event.Block.Type != "" {
			typ = event.Block.Type
		}
		startContent(event.Index, typ, event.Block)
	case "text.delta":
		startContent(event.Index, "text", nil)
		out = append(out, event)
	case "reasoning.delta", "reasoning.signature.delta":
		startContent(event.Index, "reasoning", nil)
		out = append(out, event)
	case "tool_call.start":
		startContent(event.Index, "tool_call", event.Block)
		out = append(out, event)
		n.toolStarted[event.Index] = true
	case "tool_call.arguments.delta":
		startContent(event.Index, "tool_call", nil)
		out = append(out, event)
	case "tool_call.end":
		if n.toolStarted[event.Index] {
			out = append(out, event)
			delete(n.toolStarted, event.Index)
		}
	case "content.end":
		startContent(event.Index, "text", nil)
		closeContent(event.Index)
	case "message.end":
		startEnvelope()
		closeAll()
		if !n.messageEnded {
			out = append(out, event)
			n.messageEnded = true
		}
	case "response.end":
		startEnvelope()
		closeAll()
		if !n.messageEnded {
			out = append(out, Event{Type: "message.end", ResponseID: event.ResponseID})
			n.messageEnded = true
		}
		out = append(out, event)
	case "usage.update", "error":
		out = append(out, event)
	default:
		out = append(out, event)
	}
	return out
}
