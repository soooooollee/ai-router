package ir

import "sort"

// StreamNormalizer turns provider-specific partial streams into the complete
// canonical lifecycle consumed by the gateway core.
type StreamNormalizer struct {
	responseStarted  bool
	messageStarted   bool
	messageEnded     bool
	contentStarted   map[int]string
	contentEnded     map[int]bool
	toolStarted      map[int]bool
	contentBlocks    map[int]*ContentBlock
	contentIndexes   map[contentKey]int
	lastContentKey   map[int]contentKey
	nextContentIndex int
}

type contentKey struct {
	source int
	typ    string
}

func NewStreamNormalizer() *StreamNormalizer {
	return &StreamNormalizer{
		contentStarted: map[int]string{},
		contentEnded:   map[int]bool{},
		toolStarted:    map[int]bool{},
		contentBlocks:  map[int]*ContentBlock{},
		contentIndexes: map[contentKey]int{},
		lastContentKey: map[int]contentKey{},
	}
}

func (n *StreamNormalizer) Push(event Event) []Event {
	var out []Event
	resolveIndex := func(source int, typ string) int {
		key := contentKey{source: source, typ: typ}
		if index, ok := n.contentIndexes[key]; ok {
			n.lastContentKey[source] = key
			return index
		}
		index := source
		if _, occupied := n.contentStarted[index]; occupied {
			index = n.nextContentIndex
			for {
				if _, occupied = n.contentStarted[index]; !occupied {
					break
				}
				index++
			}
		}
		n.contentIndexes[key] = index
		n.lastContentKey[source] = key
		if index >= n.nextContentIndex {
			n.nextContentIndex = index + 1
		}
		return index
	}
	indexFor := func(source int, typ string) int {
		return resolveIndex(source, typ)
	}
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
			copyBlock := *block
			copyBlock.Type = typ
			n.contentBlocks[index] = &copyBlock
			out = append(out, Event{Type: "content.start", ResponseID: event.ResponseID, Index: index, Block: &copyBlock})
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
		var block *ContentBlock
		if current := n.contentBlocks[index]; current != nil {
			copyBlock := *current
			copyBlock.Arguments = append([]byte(nil), current.Arguments...)
			block = &copyBlock
		}
		out = append(out, Event{Type: "content.end", ResponseID: event.ResponseID, Index: index, Block: block})
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
		index := indexFor(event.Index, typ)
		startContent(index, typ, event.Block)
	case "text.delta":
		event.Index = indexFor(event.Index, "text")
		startContent(event.Index, "text", nil)
		n.contentBlocks[event.Index].Text += event.Delta
		out = append(out, event)
	case "reasoning.delta", "reasoning.signature.delta":
		event.Index = indexFor(event.Index, "reasoning")
		startContent(event.Index, "reasoning", nil)
		n.contentBlocks[event.Index].Text += event.Delta
		out = append(out, event)
	case "tool_call.start":
		event.Index = indexFor(event.Index, "tool_call")
		startContent(event.Index, "tool_call", event.Block)
		out = append(out, event)
		n.toolStarted[event.Index] = true
	case "tool_call.arguments.delta":
		event.Index = indexFor(event.Index, "tool_call")
		startContent(event.Index, "tool_call", nil)
		n.contentBlocks[event.Index].Arguments = append(n.contentBlocks[event.Index].Arguments, event.Arguments...)
		out = append(out, event)
	case "tool_call.end":
		event.Index = indexFor(event.Index, "tool_call")
		if n.toolStarted[event.Index] {
			out = append(out, event)
			delete(n.toolStarted, event.Index)
		}
	case "content.end":
		key, ok := n.lastContentKey[event.Index]
		if !ok {
			key = contentKey{source: event.Index, typ: "text"}
		}
		index := indexFor(key.source, key.typ)
		startContent(index, key.typ, event.Block)
		closeContent(index)
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
