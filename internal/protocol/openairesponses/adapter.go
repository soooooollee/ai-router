package openairesponses

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
)

type Adapter struct{}

func New() *Adapter                    { return &Adapter{} }
func (*Adapter) Protocol() ir.Protocol { return ir.OpenAIResponses }

func (*Adapter) DecodeRequest(_ context.Context, raw json.RawMessage) (*ir.Request, []ir.Diagnostic, error) {
	v, err := common.DecodeJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	model, err := common.RequireModel(v)
	if err != nil {
		return nil, nil, err
	}
	r := &ir.Request{Model: model, Stream: common.Bool(v["stream"]), Instructions: common.TextBlocks(v["instructions"]), Sampling: ir.SamplingOptions{Temperature: common.FloatPtr(v["temperature"]), TopP: common.FloatPtr(v["top_p"]), MaxOutputTokens: common.IntPtr(v["max_output_tokens"])}}
	switch input := v["input"].(type) {
	case string:
		r.Messages = []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: input}}}}
	case []any:
		for _, x := range input {
			m := common.Map(x)
			switch common.String(m["type"]) {
			case "message", "":
				msg := ir.Message{Role: common.String(m["role"]), Content: common.TextBlocks(m["content"])}
				if msg.Role == "" {
					msg.Role = "user"
				}
				r.Messages = append(r.Messages, msg)
			case "function_call":
				r.Messages = appendCall(r.Messages, ir.ContentBlock{Type: "tool_call", ID: first(common.String(m["call_id"]), common.String(m["id"])), Name: common.String(m["name"]), Arguments: json.RawMessage(common.String(m["arguments"]))})
			case "function_call_output":
				r.Messages = append(r.Messages, ir.Message{Role: "tool", Content: []ir.ContentBlock{{Type: "tool_result", ID: common.String(m["call_id"]), Result: common.Raw(m["output"])}}})
			case "custom_tool_call":
				arguments, _ := json.Marshal(map[string]any{"input": m["input"]})
				r.Messages = appendCall(r.Messages, ir.ContentBlock{Type: "tool_call", SourceType: "custom", ID: first(common.String(m["call_id"]), common.String(m["id"])), Name: common.String(m["name"]), Arguments: arguments})
			case "custom_tool_call_output":
				r.Messages = append(r.Messages, ir.Message{Role: "tool", Content: []ir.ContentBlock{{Type: "tool_result", SourceType: "custom", ID: common.String(m["call_id"]), Result: common.Raw(m["output"])}}})
			case "reasoning":
				msg := ir.Message{Role: "assistant"}
				for _, summary := range common.Array(m["summary"]) {
					sm := common.Map(summary)
					msg.Content = append(msg.Content, ir.ContentBlock{Type: "reasoning", Text: common.String(sm["text"]), Extension: common.Raw(m)})
				}
				r.Messages = append(r.Messages, msg)
			}
		}
	}
	for _, x := range common.Array(v["tools"]) {
		t := common.Map(x)
		switch common.String(t["type"]) {
		case "function":
			r.Tools = append(r.Tools, ir.Tool{Type: "function", Name: common.String(t["name"]), Description: common.String(t["description"]), InputSchema: common.Raw(t["parameters"])})
		case "custom":
			r.Tools = append(r.Tools, ir.Tool{Type: "custom", Name: common.String(t["name"]), Description: customToolDescription(t), InputSchema: customToolInputSchema(), Extension: common.Raw(t)})
		}
	}
	if tc := v["tool_choice"]; tc != nil {
		switch x := tc.(type) {
		case string:
			r.ToolChoice = &ir.ToolChoice{Type: x}
		case map[string]any:
			r.ToolChoice = &ir.ToolChoice{Type: "tool", Name: common.String(x["name"])}
		}
	}
	if f := common.Map(v["text"]); len(f) > 0 {
		if form := common.Map(f["format"]); len(form) > 0 {
			r.ResponseFormat = &ir.ResponseFormat{Type: common.String(form["type"]), Name: common.String(form["name"]), Schema: common.Raw(form["schema"]), Strict: common.Bool(form["strict"])}
		}
	}
	if reason := common.Map(v["reasoning"]); len(reason) > 0 {
		r.Sampling.ReasoningEffort = common.String(reason["effort"])
	}
	d := common.DiagnosticsForExtensions(r)
	d = append(d, common.CaptureRequestExtensions(r, v, "model", "instructions", "input", "tools", "tool_choice", "text", "reasoning", "temperature", "top_p", "max_output_tokens", "stream", "metadata")...)
	common.SetRequestSource(r, ir.OpenAIResponses)
	return r, d, nil
}

func appendCall(messages []ir.Message, b ir.ContentBlock) []ir.Message {
	if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		messages[len(messages)-1].Content = append(messages[len(messages)-1].Content, b)
		return messages
	}
	return append(messages, ir.Message{Role: "assistant", Content: []ir.ContentBlock{b}})
}

func (*Adapter) EncodeRequest(_ context.Context, r *ir.Request) (json.RawMessage, []ir.Diagnostic, error) {
	v := map[string]any{"model": r.Model, "stream": r.Stream}
	if len(r.Instructions) > 0 {
		v["instructions"] = common.Text(r.Instructions)
	}
	var input []any
	for _, m := range r.Messages {
		var content []any
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				typ := "input_text"
				if m.Role == "assistant" {
					typ = "output_text"
				}
				content = append(content, map[string]any{"type": typ, "text": b.Text})
			case "image_url":
				content = append(content, map[string]any{"type": "input_image", "image_url": b.URL})
			case "image_base64":
				content = append(content, map[string]any{"type": "input_image", "image_url": "data:" + b.MediaType + ";base64," + b.Data})
			case "document":
				item := map[string]any{"type": "input_file"}
				if b.FileID != "" {
					item["file_id"] = b.FileID
				}
				if b.URL != "" {
					item["file_url"] = b.URL
				}
				if b.Data != "" {
					item["file_data"] = b.Data
				}
				if b.Filename != "" {
					item["filename"] = b.Filename
				}
				content = append(content, item)
			case "refusal":
				content = append(content, map[string]any{"type": "input_text", "text": b.Text})
			case "tool_call":
				if b.SourceType == "custom" {
					input = append(input, map[string]any{"type": "custom_tool_call", "call_id": b.ID, "name": b.Name, "input": customToolInput(b.Arguments)})
				} else {
					input = append(input, map[string]any{"type": "function_call", "call_id": b.ID, "name": b.Name, "arguments": string(b.Arguments)})
				}
			case "tool_result":
				var out any
				if json.Unmarshal(b.Result, &out) != nil {
					out = string(b.Result)
				}
				typeName := "function_call_output"
				if b.SourceType == "custom" {
					typeName = "custom_tool_call_output"
				}
				input = append(input, map[string]any{"type": typeName, "call_id": b.ID, "output": out})
			case "reasoning":
				item := map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": b.Text}}}
				mergeReasoningOpaque(item, b.Extension)
				input = append(input, item)
			}
		}
		if len(content) > 0 {
			input = append(input, map[string]any{"type": "message", "role": m.Role, "content": content})
		}
	}
	v["input"] = input
	if len(r.Tools) > 0 {
		var tools []any
		for _, t := range r.Tools {
			if t.Type == "custom" {
				var custom map[string]any
				_ = json.Unmarshal(t.Extension, &custom)
				if custom == nil {
					custom = map[string]any{"type": "custom", "name": t.Name, "description": t.Description}
				}
				custom["type"] = "custom"
				custom["name"] = t.Name
				tools = append(tools, custom)
				continue
			}
			var schema any
			_ = json.Unmarshal(t.InputSchema, &schema)
			tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": schema})
		}
		v["tools"] = tools
	}
	if r.ToolChoice != nil {
		if r.ToolChoice.Name != "" {
			v["tool_choice"] = map[string]any{"type": "function", "name": r.ToolChoice.Name}
		} else {
			v["tool_choice"] = r.ToolChoice.Type
		}
	}
	if r.Sampling.MaxOutputTokens != nil {
		v["max_output_tokens"] = *r.Sampling.MaxOutputTokens
	}
	if r.Sampling.Temperature != nil {
		v["temperature"] = *r.Sampling.Temperature
	}
	if r.Sampling.TopP != nil {
		v["top_p"] = *r.Sampling.TopP
	}
	if r.Sampling.ReasoningEffort != "" {
		v["reasoning"] = map[string]any{"effort": r.Sampling.ReasoningEffort}
	}
	if r.ResponseFormat != nil {
		var schema any
		_ = json.Unmarshal(r.ResponseFormat.Schema, &schema)
		v["text"] = map[string]any{"format": map[string]any{"type": r.ResponseFormat.Type, "name": r.ResponseFormat.Name, "schema": schema, "strict": r.ResponseFormat.Strict}}
	}
	common.RestoreRequestExtensions(v, r, ir.OpenAIResponses)
	return common.Raw(v), common.RequestPortabilityDiagnostics(r, ir.OpenAIResponses), nil
}

func (*Adapter) DecodeResponse(_ context.Context, raw json.RawMessage) (*ir.Response, []ir.Diagnostic, error) {
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	r := &ir.Response{ID: common.String(v["id"]), Model: common.String(v["model"]), StopReason: common.String(v["status"])}
	msg := ir.Message{Role: "assistant"}
	for _, x := range common.Array(v["output"]) {
		m := common.Map(x)
		switch common.String(m["type"]) {
		case "message":
			msg.Content = append(msg.Content, common.TextBlocks(m["content"])...)
		case "function_call":
			msg.Content = append(msg.Content, ir.ContentBlock{Type: "tool_call", ID: common.String(m["call_id"]), Name: common.String(m["name"]), Arguments: json.RawMessage(common.String(m["arguments"]))})
		case "reasoning":
			for _, s := range common.Array(m["summary"]) {
				sm := common.Map(s)
				msg.Content = append(msg.Content, ir.ContentBlock{Type: "reasoning", Text: common.String(sm["text"]), Extension: common.Raw(m)})
			}
		}
	}
	r.Messages = []ir.Message{msg}
	u := common.Map(v["usage"])
	inDetails := common.Map(u["input_tokens_details"])
	outDetails := common.Map(u["output_tokens_details"])
	r.Usage = ir.Usage{InputTokens: common.Int(u["input_tokens"]), OutputTokens: common.Int(u["output_tokens"]), TotalTokens: common.Int(u["total_tokens"]), CachedTokens: common.Int(inDetails["cached_tokens"]), ReasoningTokens: common.Int(outDetails["reasoning_tokens"])}
	common.SetResponseSource(r, ir.OpenAIResponses)
	return r, nil, nil
}
func (*Adapter) EncodeResponse(_ context.Context, r *ir.Response) (json.RawMessage, []ir.Diagnostic, error) {
	m := common.FirstAssistant(r)
	var output []any
	var text []any
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			text = append(text, map[string]any{"type": "output_text", "text": b.Text, "annotations": []any{}})
		case "tool_call":
			if b.SourceType == "custom" {
				output = append(output, customToolCallItem(b, "completed"))
			} else {
				output = append(output, map[string]any{"type": "function_call", "id": "fc_" + b.ID, "call_id": b.ID, "name": b.Name, "arguments": string(b.Arguments), "status": "completed"})
			}
		case "reasoning":
			item := map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": b.Text}}, "status": "completed"}
			mergeReasoningOpaque(item, b.Extension)
			output = append(output, item)
		case "refusal":
			text = append(text, map[string]any{"type": "refusal", "refusal": b.Text})
		}
	}
	if len(text) > 0 {
		output = append(output, map[string]any{"type": "message", "id": "msg_" + r.ID, "status": "completed", "role": "assistant", "content": text})
	}
	usage := map[string]any{"input_tokens": r.Usage.InputTokens, "output_tokens": r.Usage.OutputTokens, "total_tokens": r.Usage.TotalTokens}
	if r.Usage.CachedTokens > 0 {
		usage["input_tokens_details"] = map[string]any{"cached_tokens": r.Usage.CachedTokens}
	}
	if r.Usage.ReasoningTokens > 0 {
		usage["output_tokens_details"] = map[string]any{"reasoning_tokens": r.Usage.ReasoningTokens}
	}
	v := map[string]any{"id": r.ID, "object": "response", "status": "completed", "model": r.Model, "output": output, "usage": usage}
	return common.Raw(v), common.ResponsePortabilityDiagnostics(r, ir.OpenAIResponses), nil
}

func (*Adapter) DecodeStreamEvent(_ context.Context, event string, raw json.RawMessage) ([]ir.Event, []ir.Diagnostic, error) {
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	switch event {
	case "response.created", "response.in_progress":
		if event == "response.in_progress" {
			return nil, nil, nil
		}
		resp := common.Map(v["response"])
		return []ir.Event{{Type: "response.start", ResponseID: common.String(resp["id"])}}, nil, nil
	case "response.output_text.delta":
		return []ir.Event{{Type: "text.delta", Index: common.Int(v["output_index"]), Delta: common.String(v["delta"])}}, nil, nil
	case "response.reasoning_summary_text.delta":
		return []ir.Event{{Type: "reasoning.delta", Index: common.Int(v["output_index"]), Delta: common.String(v["delta"])}}, nil, nil
	case "response.output_item.added":
		item := common.Map(v["item"])
		if common.String(item["type"]) == "function_call" {
			return []ir.Event{{Type: "tool_call.start", Index: common.Int(v["output_index"]), Block: &ir.ContentBlock{Type: "tool_call", ID: common.String(item["call_id"]), Name: common.String(item["name"])}}}, nil, nil
		}
	case "response.function_call_arguments.delta":
		return []ir.Event{{Type: "tool_call.arguments.delta", Index: common.Int(v["output_index"]), Arguments: common.String(v["delta"])}}, nil, nil
	case "response.completed":
		resp := common.Map(v["response"])
		u := common.Map(resp["usage"])
		inDetails := common.Map(u["input_tokens_details"])
		outDetails := common.Map(u["output_tokens_details"])
		usage := ir.Usage{InputTokens: common.Int(u["input_tokens"]), OutputTokens: common.Int(u["output_tokens"]), CachedTokens: common.Int(inDetails["cached_tokens"]), ReasoningTokens: common.Int(outDetails["reasoning_tokens"]), TotalTokens: common.Int(u["total_tokens"])}
		return []ir.Event{{Type: "usage.update", Usage: &usage}, {Type: "message.end", StopReason: "stop"}, {Type: "response.end"}}, nil, nil
	case "response.failed", "error":
		return []ir.Event{{Type: "error", Error: &ir.Error{Type: "upstream_error", Message: common.String(v["message"])}}}, nil, nil
	}
	return nil, nil, nil
}

func (*Adapter) EncodeStreamEvent(_ context.Context, e ir.Event) ([]protocol.SSE, []ir.Diagnostic, error) {
	itemID := func(prefix string) string {
		return fmt.Sprintf("%s_%s_%d", prefix, e.ResponseID, e.Index)
	}
	chunk := func(event string, v map[string]any) protocol.SSE {
		v["type"] = event
		v["sequence_number"] = e.Sequence
		return protocol.SSE{Event: event, Data: common.Raw(v)}
	}
	var chunks []protocol.SSE
	switch e.Type {
	case "response.start":
		chunks = append(chunks, chunk("response.created", map[string]any{"response": map[string]any{"id": e.ResponseID, "object": "response", "status": "in_progress", "model": e.Model, "output": []any{}}}))
	case "content.start":
		if e.Block == nil {
			return nil, nil, nil
		}
		switch e.Block.Type {
		case "text":
			id := itemID("msg")
			chunks = append(chunks,
				chunk("response.output_item.added", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": map[string]any{"id": id, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}}),
				chunk("response.content_part.added", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}),
			)
		case "reasoning":
			id := itemID("rs")
			chunks = append(chunks,
				chunk("response.output_item.added", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": map[string]any{"id": id, "type": "reasoning", "status": "in_progress", "summary": []any{}}}),
				chunk("response.reasoning_summary_part.added", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}}),
			)
		}
	case "text.delta":
		chunks = append(chunks, chunk("response.output_text.delta", map[string]any{"response_id": e.ResponseID, "item_id": itemID("msg"), "output_index": e.Index, "content_index": 0, "delta": e.Delta}))
	case "reasoning.delta":
		chunks = append(chunks, chunk("response.reasoning_summary_text.delta", map[string]any{"response_id": e.ResponseID, "item_id": itemID("rs"), "output_index": e.Index, "summary_index": 0, "delta": e.Delta}))
	case "tool_call.start":
		if e.Block == nil {
			return nil, nil, nil
		}
		if e.Block.SourceType == "custom" {
			item := customToolCallItem(*e.Block, "in_progress")
			item["id"] = itemID("ctc")
			chunks = append(chunks, chunk("response.output_item.added", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": item}))
		} else {
			chunks = append(chunks, chunk("response.output_item.added", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": map[string]any{"id": itemID("fc"), "type": "function_call", "status": "in_progress", "call_id": e.Block.ID, "name": e.Block.Name, "arguments": ""}}))
		}
	case "tool_call.arguments.delta":
		if e.Block != nil && e.Block.SourceType == "custom" {
			return nil, nil, nil
		}
		chunks = append(chunks, chunk("response.function_call_arguments.delta", map[string]any{"response_id": e.ResponseID, "item_id": itemID("fc"), "output_index": e.Index, "delta": e.Arguments}))
	case "content.end":
		if e.Block == nil {
			return nil, nil, nil
		}
		switch e.Block.Type {
		case "text":
			id := itemID("msg")
			part := map[string]any{"type": "output_text", "text": e.Block.Text, "annotations": []any{}}
			item := map[string]any{"id": id, "type": "message", "status": "completed", "role": "assistant", "content": []any{part}}
			chunks = append(chunks,
				chunk("response.output_text.done", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "content_index": 0, "text": e.Block.Text}),
				chunk("response.content_part.done", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "content_index": 0, "part": part}),
				chunk("response.output_item.done", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": item}),
			)
		case "reasoning":
			id := itemID("rs")
			part := map[string]any{"type": "summary_text", "text": e.Block.Text}
			item := map[string]any{"id": id, "type": "reasoning", "status": "completed", "summary": []any{part}}
			chunks = append(chunks,
				chunk("response.reasoning_summary_text.done", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "summary_index": 0, "text": e.Block.Text}),
				chunk("response.reasoning_summary_part.done", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "summary_index": 0, "part": part}),
				chunk("response.output_item.done", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": item}),
			)
		case "tool_call":
			if e.Block.SourceType == "custom" {
				id := itemID("ctc")
				input := customToolInput(e.Block.Arguments)
				item := customToolCallItem(*e.Block, "completed")
				item["id"] = id
				if input != "" {
					chunks = append(chunks, chunk("response.custom_tool_call_input.delta", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "delta": input}))
				}
				chunks = append(chunks,
					chunk("response.custom_tool_call_input.done", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "input": input}),
					chunk("response.output_item.done", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": item}),
				)
			} else {
				id := itemID("fc")
				arguments := string(e.Block.Arguments)
				item := map[string]any{"id": id, "type": "function_call", "status": "completed", "call_id": e.Block.ID, "name": e.Block.Name, "arguments": arguments}
				chunks = append(chunks,
					chunk("response.function_call_arguments.done", map[string]any{"response_id": e.ResponseID, "item_id": id, "output_index": e.Index, "name": e.Block.Name, "arguments": arguments}),
					chunk("response.output_item.done", map[string]any{"response_id": e.ResponseID, "output_index": e.Index, "item": item}),
				)
			}
		}
	case "response.end":
		response := map[string]any{"id": e.ResponseID, "object": "response", "status": "completed", "model": e.Model, "output": []any{}}
		if e.Usage != nil {
			usage := map[string]any{"input_tokens": e.Usage.InputTokens, "output_tokens": e.Usage.OutputTokens, "total_tokens": e.Usage.TotalTokens}
			if e.Usage.CachedTokens > 0 {
				usage["input_tokens_details"] = map[string]any{"cached_tokens": e.Usage.CachedTokens}
			}
			if e.Usage.ReasoningTokens > 0 {
				usage["output_tokens_details"] = map[string]any{"reasoning_tokens": e.Usage.ReasoningTokens}
			}
			response["usage"] = usage
		}
		chunks = append(chunks, chunk("response.completed", map[string]any{"response": response}))
	case "error":
		chunks = append(chunks, chunk("error", map[string]any{"error": e.Error}))
	default:
		return nil, nil, nil
	}
	return chunks, nil, nil
}

func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func customToolInputSchema() json.RawMessage {
	return common.Raw(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"input": map[string]any{"type": "string", "description": "Raw input for the custom tool."}},
		"required":             []string{"input"},
		"additionalProperties": false,
	})
}

func customToolDescription(tool map[string]any) string {
	description := common.String(tool["description"])
	raw, _ := json.Marshal(tool)
	if description != "" {
		description += "\n\n"
	}
	return description + "Original custom tool definition:\n" + string(raw)
}

func customToolInput(arguments json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(arguments, &value) == nil {
		if input, ok := value["input"].(string); ok {
			return input
		}
	}
	return string(arguments)
}

func customToolCallItem(block ir.ContentBlock, status string) map[string]any {
	return map[string]any{
		"id":      "ctc_" + block.ID,
		"type":    "custom_tool_call",
		"status":  status,
		"call_id": block.ID,
		"name":    block.Name,
		"input":   customToolInput(block.Arguments),
	}
}

func mergeReasoningOpaque(item map[string]any, extension json.RawMessage) {
	var ext map[string]any
	_ = json.Unmarshal(extension, &ext)
	for _, key := range []string{"id", "encrypted_content"} {
		if value, ok := ext[key]; ok {
			item[key] = value
		}
	}
}

var _ protocol.Adapter = (*Adapter)(nil)
