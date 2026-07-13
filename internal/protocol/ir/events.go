package ir

import (
	"encoding/json"
	"fmt"
)

// EventsFromResponse converts a completed response through the same canonical
// lifecycle used by streaming responses. This keeps non-streaming and
// streaming semantics on one stable boundary.
func EventsFromResponse(response *Response) []Event {
	if response == nil {
		return nil
	}
	events := []Event{{Type: "response.start", ResponseID: response.ID, Model: response.Model, Metadata: cloneMap(response.Metadata)}}
	for mi, message := range response.Messages {
		events = append(events, Event{Type: "message.start", MessageID: fmt.Sprintf("message-%d", mi), Role: message.Role, Index: mi})
		for bi, original := range message.Content {
			block := original
			switch block.Type {
			case "text", "reasoning":
				block.Text = ""
			case "tool_call":
				block.Arguments = nil
			}
			events = append(events, Event{Type: "content.start", Index: bi, Block: &block})
			switch original.Type {
			case "text":
				if original.Text != "" {
					events = append(events, Event{Type: "text.delta", Index: bi, Delta: original.Text})
				}
			case "reasoning":
				if original.Text != "" {
					events = append(events, Event{Type: "reasoning.delta", Index: bi, Delta: original.Text})
				}
			case "tool_call":
				call := original
				call.Arguments = nil
				events = append(events, Event{Type: "tool_call.start", Index: bi, Block: &call})
				if len(original.Arguments) > 0 {
					events = append(events, Event{Type: "tool_call.arguments.delta", Index: bi, Arguments: string(original.Arguments)})
				}
				events = append(events, Event{Type: "tool_call.end", Index: bi})
			}
			events = append(events, Event{Type: "content.end", Index: bi})
		}
		events = append(events, Event{Type: "message.end", Index: mi, StopReason: response.StopReason})
	}
	usage := response.Usage
	events = append(events, Event{Type: "usage.update", Usage: &usage}, Event{Type: "response.end", ResponseID: response.ID})
	return events
}

// AggregateEvents constructs a completed response from canonical lifecycle
// events. It accepts partial provider streams as well as EventsFromResponse.
func AggregateEvents(events []Event) (*Response, error) {
	response := &Response{}
	currentMessage := -1
	ensureMessage := func() *Message {
		if currentMessage < 0 {
			response.Messages = append(response.Messages, Message{Role: "assistant"})
			currentMessage = len(response.Messages) - 1
		}
		return &response.Messages[currentMessage]
	}
	ensureBlock := func(index int, typ string) *ContentBlock {
		message := ensureMessage()
		for len(message.Content) <= index {
			message.Content = append(message.Content, ContentBlock{})
		}
		block := &message.Content[index]
		if block.Type == "" {
			block.Type = typ
		}
		return block
	}
	for _, event := range events {
		switch event.Type {
		case "response.start":
			response.ID, response.Model, response.Metadata = event.ResponseID, event.Model, cloneMap(event.Metadata)
		case "message.start":
			role := event.Role
			if role == "" {
				role = "assistant"
			}
			response.Messages = append(response.Messages, Message{Role: role})
			currentMessage = len(response.Messages) - 1
		case "content.start":
			if event.Block != nil {
				block := *event.Block
				*ensureBlock(event.Index, block.Type) = block
			}
		case "text.delta":
			ensureBlock(event.Index, "text").Text += event.Delta
		case "reasoning.delta":
			ensureBlock(event.Index, "reasoning").Text += event.Delta
		case "tool_call.start":
			if event.Block != nil {
				block := ensureBlock(event.Index, "tool_call")
				block.Type, block.ID, block.Name, block.Source, block.Extension = "tool_call", event.Block.ID, event.Block.Name, event.Block.Source, append(json.RawMessage(nil), event.Block.Extension...)
			}
		case "tool_call.arguments.delta":
			block := ensureBlock(event.Index, "tool_call")
			block.Arguments = append(block.Arguments, event.Arguments...)
		case "usage.update":
			if event.Usage != nil {
				response.Usage = *event.Usage
			}
		case "message.end":
			if event.StopReason != "" {
				response.StopReason = event.StopReason
			}
		case "error":
			if event.Error != nil {
				return nil, fmt.Errorf("%s: %s", event.Error.Type, event.Error.Message)
			}
		}
	}
	return response, nil
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
