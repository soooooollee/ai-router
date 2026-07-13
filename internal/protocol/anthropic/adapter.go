package anthropic

import (
	"context"
	"encoding/json"

	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
)

type Adapter struct{}

func New() *Adapter                    { return &Adapter{} }
func (*Adapter) Protocol() ir.Protocol { return ir.Anthropic }

func (*Adapter) DecodeRequest(_ context.Context, raw json.RawMessage) (*ir.Request, []ir.Diagnostic, error) {
	v, err := common.DecodeJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	model, err := common.RequireModel(v)
	if err != nil {
		return nil, nil, err
	}
	r := &ir.Request{Model: model, Stream: common.Bool(v["stream"]), Instructions: common.TextBlocks(v["system"]), Sampling: ir.SamplingOptions{Temperature: common.FloatPtr(v["temperature"]), TopP: common.FloatPtr(v["top_p"]), TopK: common.IntPtr(v["top_k"]), MaxOutputTokens: common.IntPtr(v["max_tokens"])}}
	if thinking := common.Map(v["thinking"]); len(thinking) > 0 {
		switch common.String(thinking["type"]) {
		case "disabled":
			enabled := false
			r.Sampling.ReasoningEnabled = &enabled
		case "enabled", "adaptive":
			enabled := true
			r.Sampling.ReasoningEnabled = &enabled
		}
	}
	for _, s := range common.Array(v["stop_sequences"]) {
		r.Sampling.Stop = append(r.Sampling.Stop, common.String(s))
	}
	for _, x := range common.Array(v["messages"]) {
		m := common.Map(x)
		r.Messages = append(r.Messages, ir.Message{Role: common.String(m["role"]), Content: common.TextBlocks(m["content"])})
	}
	for _, x := range common.Array(v["tools"]) {
		t := common.Map(x)
		r.Tools = append(r.Tools, ir.Tool{Name: common.String(t["name"]), Description: common.String(t["description"]), InputSchema: common.Raw(t["input_schema"])})
	}
	if tc := common.Map(v["tool_choice"]); len(tc) > 0 {
		r.ToolChoice = &ir.ToolChoice{Type: common.String(tc["type"]), Name: common.String(tc["name"])}
	}
	d := common.DiagnosticsForExtensions(r)
	d = append(d, common.CaptureRequestExtensions(r, v, "model", "system", "messages", "tools", "tool_choice", "temperature", "top_p", "top_k", "max_tokens", "stop_sequences", "stream", "metadata", "thinking")...)
	common.SetRequestSource(r, ir.Anthropic)
	return r, d, nil
}

func (*Adapter) EncodeRequest(_ context.Context, r *ir.Request) (json.RawMessage, []ir.Diagnostic, error) {
	v := map[string]any{"model": r.Model, "messages": encodeMessages(r.Messages), "stream": r.Stream}
	if len(r.Instructions) > 0 {
		v["system"] = encodeBlocks(r.Instructions)
	}
	if r.Sampling.MaxOutputTokens != nil {
		v["max_tokens"] = *r.Sampling.MaxOutputTokens
	} else {
		v["max_tokens"] = 4096
	}
	if r.Sampling.Temperature != nil {
		v["temperature"] = *r.Sampling.Temperature
	}
	if r.Sampling.TopP != nil {
		v["top_p"] = *r.Sampling.TopP
	}
	if r.Sampling.TopK != nil {
		v["top_k"] = *r.Sampling.TopK
	}
	if len(r.Sampling.Stop) > 0 {
		v["stop_sequences"] = r.Sampling.Stop
	}
	if r.Sampling.ReasoningEnabled != nil && !*r.Sampling.ReasoningEnabled {
		v["thinking"] = map[string]any{"type": "disabled"}
	}
	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			var schema any
			_ = json.Unmarshal(t.InputSchema, &schema)
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": schema})
		}
		v["tools"] = tools
	}
	if r.ToolChoice != nil {
		v["tool_choice"] = map[string]any{"type": r.ToolChoice.Type, "name": r.ToolChoice.Name}
	}
	var d []ir.Diagnostic
	if r.ResponseFormat != nil {
		d = append(d, ir.Diagnostic{Severity: "warning", Code: "response_format_via_instruction", Path: "response_format", Message: "Anthropic Messages has no portable response_format field", Action: "approximated"})
		v["system"] = appendInstruction(v["system"], "Return JSON that matches the requested schema.")
	}
	d = append(d, common.RequestPortabilityDiagnostics(r, ir.Anthropic)...)
	common.RestoreRequestExtensions(v, r, ir.Anthropic)
	return common.Raw(v), d, nil
}

func encodeMessages(messages []ir.Message) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		role := m.Role
		if role == "tool" {
			role = "user"
		}
		out = append(out, map[string]any{"role": role, "content": encodeBlocks(m.Content)})
	}
	return out
}
func encodeBlocks(blocks []ir.ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			block := map[string]any{"type": "text", "text": b.Text}
			var ext map[string]any
			_ = json.Unmarshal(b.Extension, &ext)
			if cacheControl, ok := ext["cache_control"]; ok {
				block["cache_control"] = cacheControl
			}
			out = append(out, block)
		case "reasoning":
			if b.Source != ir.Anthropic {
				continue
			}
			block := map[string]any{"type": "thinking", "thinking": b.Text}
			var ext map[string]any
			_ = json.Unmarshal(b.Extension, &ext)
			if signature := common.String(ext["signature"]); signature != "" {
				block["signature"] = signature
			}
			out = append(out, block)
		case "image_url":
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": b.URL}})
		case "image_base64":
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": b.MediaType, "data": b.Data}})
		case "document":
			source := map[string]any{}
			switch {
			case b.FileID != "":
				source = map[string]any{"type": "file", "file_id": b.FileID}
			case b.URL != "":
				source = map[string]any{"type": "url", "url": b.URL}
			default:
				source = map[string]any{"type": "base64", "media_type": b.MediaType, "data": b.Data}
			}
			out = append(out, map[string]any{"type": "document", "source": source})
		case "refusal":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case "tool_call":
			var input any
			_ = json.Unmarshal(b.Arguments, &input)
			out = append(out, map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": input})
		case "tool_result":
			var result any
			if json.Unmarshal(b.Result, &result) != nil {
				result = string(b.Result)
			}
			out = append(out, map[string]any{"type": "tool_result", "tool_use_id": b.ID, "content": result, "is_error": b.IsError})
		case "extension":
			var block map[string]any
			if json.Unmarshal(b.Extension, &block) == nil && common.String(block["type"]) == "redacted_thinking" {
				out = append(out, block)
			}
		}
	}
	return out
}
func appendInstruction(v any, text string) any {
	a := common.Array(v)
	a = append(a, map[string]any{"type": "text", "text": text})
	return a
}

func (*Adapter) DecodeResponse(_ context.Context, raw json.RawMessage) (*ir.Response, []ir.Diagnostic, error) {
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	u := common.Map(v["usage"])
	r := &ir.Response{ID: common.String(v["id"]), Model: common.String(v["model"]), StopReason: common.String(v["stop_reason"]), Messages: []ir.Message{{Role: "assistant", Content: common.TextBlocks(v["content"])}}, Usage: ir.Usage{InputTokens: common.Int(u["input_tokens"]), OutputTokens: common.Int(u["output_tokens"]), CachedTokens: common.Int(u["cache_read_input_tokens"])}}
	r.Usage.TotalTokens = r.Usage.InputTokens + r.Usage.OutputTokens
	common.SetResponseSource(r, ir.Anthropic)
	return r, nil, nil
}
func (*Adapter) EncodeResponse(_ context.Context, r *ir.Response) (json.RawMessage, []ir.Diagnostic, error) {
	m := common.FirstAssistant(r)
	usage := map[string]any{"input_tokens": r.Usage.InputTokens, "output_tokens": r.Usage.OutputTokens}
	if r.Usage.CachedTokens > 0 {
		usage["cache_read_input_tokens"] = r.Usage.CachedTokens
	}
	v := map[string]any{"id": r.ID, "type": "message", "role": "assistant", "model": r.Model, "content": encodeBlocks(m.Content), "stop_reason": common.StopToAnthropic(r.StopReason), "stop_sequence": nil, "usage": usage}
	return common.Raw(v), common.ResponsePortabilityDiagnostics(r, ir.Anthropic), nil
}

func (*Adapter) DecodeStreamEvent(_ context.Context, event string, raw json.RawMessage) ([]ir.Event, []ir.Diagnostic, error) {
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	switch event {
	case "message_start":
		m := common.Map(v["message"])
		out := []ir.Event{{Type: "response.start", ResponseID: common.String(m["id"])}}
		if u := common.Map(m["usage"]); len(u) > 0 {
			usage := ir.Usage{InputTokens: common.Int(u["input_tokens"]), OutputTokens: common.Int(u["output_tokens"])}
			out = append(out, ir.Event{Type: "usage.update", Usage: &usage})
		}
		return out, nil, nil
	case "content_block_start":
		b := common.Map(v["content_block"])
		typ := common.String(b["type"])
		if typ == "tool_use" {
			return []ir.Event{{Type: "tool_call.start", Index: common.Int(v["index"]), Block: &ir.ContentBlock{Type: "tool_call", ID: common.String(b["id"]), Name: common.String(b["name"])}}}, nil, nil
		}
		return []ir.Event{{Type: "content.start", Index: common.Int(v["index"]), Block: &ir.ContentBlock{Type: typ}}}, nil, nil
	case "content_block_delta":
		d := common.Map(v["delta"])
		switch common.String(d["type"]) {
		case "text_delta":
			return []ir.Event{{Type: "text.delta", Index: common.Int(v["index"]), Delta: common.String(d["text"])}}, nil, nil
		case "thinking_delta":
			return []ir.Event{{Type: "reasoning.delta", Index: common.Int(v["index"]), Delta: common.String(d["thinking"])}}, nil, nil
		case "signature_delta":
			return []ir.Event{{Type: "reasoning.signature.delta", Index: common.Int(v["index"]), Delta: common.String(d["signature"])}}, nil, nil
		case "input_json_delta":
			return []ir.Event{{Type: "tool_call.arguments.delta", Index: common.Int(v["index"]), Arguments: common.String(d["partial_json"])}}, nil, nil
		}
	case "content_block_stop":
		return []ir.Event{{Type: "content.end", Index: common.Int(v["index"])}}, nil, nil
	case "message_delta":
		d := common.Map(v["delta"])
		u := common.Map(v["usage"])
		var out []ir.Event
		if s := common.String(d["stop_reason"]); s != "" {
			out = append(out, ir.Event{Type: "message.end", StopReason: s})
		}
		if len(u) > 0 {
			usage := ir.Usage{OutputTokens: common.Int(u["output_tokens"])}
			out = append(out, ir.Event{Type: "usage.update", Usage: &usage})
		}
		return out, nil, nil
	case "message_stop":
		return []ir.Event{{Type: "response.end"}}, nil, nil
	case "error":
		er := common.Map(v["error"])
		return []ir.Event{{Type: "error", Error: &ir.Error{Type: common.String(er["type"]), Message: common.String(er["message"])}}}, nil, nil
	}
	return nil, nil, nil
}

func (*Adapter) EncodeStreamEvent(_ context.Context, e ir.Event) ([]protocol.SSE, []ir.Diagnostic, error) {
	var event string
	var v any
	switch e.Type {
	case "response.start":
		event = "message_start"
		v = map[string]any{"type": event, "message": map[string]any{"id": e.ResponseID, "type": "message", "role": "assistant", "content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}}
	case "content.start":
		event = "content_block_start"
		v = map[string]any{"type": event, "index": e.Index, "content_block": map[string]any{"type": "text", "text": ""}}
	case "tool_call.start":
		if e.Block == nil {
			return nil, nil, nil
		}
		event = "content_block_start"
		v = map[string]any{"type": event, "index": e.Index, "content_block": map[string]any{"type": "tool_use", "id": e.Block.ID, "name": e.Block.Name, "input": map[string]any{}}}
	case "text.delta":
		event = "content_block_delta"
		v = map[string]any{"type": event, "index": e.Index, "delta": map[string]any{"type": "text_delta", "text": e.Delta}}
	case "reasoning.delta":
		event = "content_block_delta"
		v = map[string]any{"type": event, "index": e.Index, "delta": map[string]any{"type": "thinking_delta", "thinking": e.Delta}}
	case "reasoning.signature.delta":
		event = "content_block_delta"
		v = map[string]any{"type": event, "index": e.Index, "delta": map[string]any{"type": "signature_delta", "signature": e.Delta}}
	case "tool_call.arguments.delta":
		event = "content_block_delta"
		v = map[string]any{"type": event, "index": e.Index, "delta": map[string]any{"type": "input_json_delta", "partial_json": e.Arguments}}
	case "content.end":
		event = "content_block_stop"
		v = map[string]any{"type": event, "index": e.Index}
	case "message.end":
		event = "message_delta"
		v = map[string]any{"type": event, "delta": map[string]any{"stop_reason": common.StopToAnthropic(e.StopReason), "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 0}}
	case "usage.update":
		if e.Usage == nil {
			return nil, nil, nil
		}
		event = "message_delta"
		v = map[string]any{"type": event, "delta": map[string]any{"stop_reason": nil}, "usage": map[string]any{"output_tokens": e.Usage.OutputTokens}}
	case "response.end":
		event = "message_stop"
		v = map[string]any{"type": event}
	case "error":
		event = "error"
		v = map[string]any{"type": event, "error": e.Error}
	default:
		return nil, nil, nil
	}
	return []protocol.SSE{{Event: event, Data: common.Raw(v)}}, nil, nil
}

var _ protocol.Adapter = (*Adapter)(nil)
