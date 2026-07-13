package openaichat

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
)

type Adapter struct{}

func New() *Adapter                    { return &Adapter{} }
func (*Adapter) Protocol() ir.Protocol { return ir.OpenAIChat }

func (*Adapter) DecodeRequest(_ context.Context, raw json.RawMessage) (*ir.Request, []ir.Diagnostic, error) {
	v, err := common.DecodeJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	model, err := common.RequireModel(v)
	if err != nil {
		return nil, nil, err
	}
	r := &ir.Request{Model: model, Stream: common.Bool(v["stream"]), Sampling: ir.SamplingOptions{Temperature: common.FloatPtr(v["temperature"]), TopP: common.FloatPtr(v["top_p"]), MaxOutputTokens: common.IntPtr(v["max_completion_tokens"])}}
	r.Sampling.ReasoningEffort = common.String(v["reasoning_effort"])
	if enabled, ok := v["enable_thinking"].(bool); ok {
		r.Sampling.ReasoningEnabled = &enabled
	}
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
	if r.Sampling.MaxOutputTokens == nil {
		r.Sampling.MaxOutputTokens = common.IntPtr(v["max_tokens"])
	}
	if s, ok := v["stop"].(string); ok {
		r.Sampling.Stop = []string{s}
	} else {
		for _, x := range common.Array(v["stop"]) {
			r.Sampling.Stop = append(r.Sampling.Stop, common.String(x))
		}
	}
	for _, item := range common.Array(v["messages"]) {
		m := common.Map(item)
		msg := ir.Message{Role: common.String(m["role"]), Name: common.String(m["name"]), ToolCallID: common.String(m["tool_call_id"]), Content: common.TextBlocks(m["content"])}
		if refusal := common.String(m["refusal"]); refusal != "" {
			msg.Content = append(msg.Content, ir.ContentBlock{Type: "refusal", Text: refusal})
		}
		if reasoning := common.String(m["reasoning_content"]); reasoning != "" {
			msg.Content = append([]ir.ContentBlock{{Type: "reasoning", Text: reasoning}}, msg.Content...)
		}
		if msg.Role == "system" || msg.Role == "developer" {
			r.Instructions = append(r.Instructions, msg.Content...)
		} else {
			if calls := common.Array(m["tool_calls"]); len(calls) > 0 {
				for _, c := range calls {
					cm := common.Map(c)
					fn := common.Map(cm["function"])
					arguments := json.RawMessage(common.String(fn["arguments"]))
					if !json.Valid(arguments) {
						arguments = json.RawMessage(`{}`)
					}
					msg.Content = append(msg.Content, ir.ContentBlock{Type: "tool_call", ID: common.String(cm["id"]), Name: common.String(fn["name"]), Arguments: arguments})
				}
			}
			if msg.Role == "tool" {
				msg.Content = []ir.ContentBlock{{Type: "tool_result", ID: msg.ToolCallID, Result: common.Raw(m["content"])}}
			}
			r.Messages = append(r.Messages, msg)
		}
	}
	for _, x := range common.Array(v["tools"]) {
		m := common.Map(x)
		fn := common.Map(m["function"])
		r.Tools = append(r.Tools, ir.Tool{Name: common.String(fn["name"]), Description: common.String(fn["description"]), InputSchema: common.Raw(fn["parameters"])})
	}
	if tc := v["tool_choice"]; tc != nil {
		switch x := tc.(type) {
		case string:
			r.ToolChoice = &ir.ToolChoice{Type: x}
		case map[string]any:
			fn := common.Map(x["function"])
			r.ToolChoice = &ir.ToolChoice{Type: "tool", Name: common.String(fn["name"])}
		}
	}
	if f := common.Map(v["response_format"]); len(f) > 0 {
		rf := &ir.ResponseFormat{Type: common.String(f["type"])}
		if js := common.Map(f["json_schema"]); len(js) > 0 {
			rf.Type = "json_schema"
			rf.Name = common.String(js["name"])
			rf.Schema = common.Raw(js["schema"])
			rf.Strict = common.Bool(js["strict"])
		}
		r.ResponseFormat = rf
	}
	d := common.DiagnosticsForExtensions(r)
	d = append(d, common.CaptureRequestExtensions(r, v, "model", "messages", "tools", "tool_choice", "response_format", "temperature", "top_p", "max_tokens", "max_completion_tokens", "stop", "stream", "stream_options", "reasoning_effort")...)
	common.SetRequestSource(r, ir.OpenAIChat)
	return r, d, nil
}

func (*Adapter) EncodeRequest(_ context.Context, r *ir.Request) (json.RawMessage, []ir.Diagnostic, error) {
	msgs := []any{}
	if len(r.Instructions) > 0 {
		msgs = append(msgs, map[string]any{"role": "system", "content": encodeContent(r.Instructions)})
	}
	for _, m := range r.Messages {
		base := map[string]any{"role": m.Role}
		var normal []ir.ContentBlock
		var calls []any
		for _, b := range m.Content {
			switch b.Type {
			case "reasoning":
				base["reasoning_content"] = b.Text
			case "tool_call":
				var args any
				if json.Unmarshal(b.Arguments, &args) != nil {
					args = string(b.Arguments)
				}
				calls = append(calls, map[string]any{"id": b.ID, "type": "function", "function": map[string]any{"name": b.Name, "arguments": string(common.Raw(args))}})
			case "tool_result":
				base["role"] = "tool"
				base["tool_call_id"] = b.ID
				var result any
				_ = json.Unmarshal(b.Result, &result)
				base["content"] = result
			default:
				normal = append(normal, b)
			}
		}
		if len(normal) > 0 {
			base["content"] = encodeContent(normal)
		} else if _, ok := base["content"]; !ok {
			base["content"] = nil
		}
		if len(calls) > 0 {
			base["tool_calls"] = calls
		}
		msgs = append(msgs, base)
	}
	v := map[string]any{"model": r.Model, "messages": msgs, "stream": r.Stream}
	if r.Stream {
		v["stream_options"] = map[string]any{"include_usage": true}
	}
	if r.Sampling.Temperature != nil {
		v["temperature"] = *r.Sampling.Temperature
	}
	if r.Sampling.TopP != nil {
		v["top_p"] = *r.Sampling.TopP
	}
	if r.Sampling.MaxOutputTokens != nil {
		v["max_completion_tokens"] = *r.Sampling.MaxOutputTokens
	}
	if len(r.Sampling.Stop) > 0 {
		v["stop"] = r.Sampling.Stop
	}
	if r.Sampling.ReasoningEffort != "" {
		v["reasoning_effort"] = r.Sampling.ReasoningEffort
	}
	if len(r.Tools) > 0 {
		var tools []any
		for _, t := range r.Tools {
			var schema any
			_ = json.Unmarshal(t.InputSchema, &schema)
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": schema}})
		}
		v["tools"] = tools
	}
	if r.ToolChoice != nil {
		if r.ToolChoice.Name != "" {
			v["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": r.ToolChoice.Name}}
		} else {
			v["tool_choice"] = r.ToolChoice.Type
		}
	}
	if r.ResponseFormat != nil {
		v["response_format"] = encodeFormat(r.ResponseFormat)
	}
	common.RestoreRequestExtensions(v, r, ir.OpenAIChat)
	return common.Raw(v), common.RequestPortabilityDiagnostics(r, ir.OpenAIChat), nil
}

func encodeContent(blocks []ir.ContentBlock) any {
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text
	}
	var out []any
	for _, b := range blocks {
		switch b.Type {
		case "text", "reasoning":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case "image_url":
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": b.URL}})
		case "image_base64":
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + b.MediaType + ";base64," + b.Data}})
		case "document":
			file := map[string]any{}
			if b.FileID != "" {
				file["file_id"] = b.FileID
			}
			if b.Data != "" {
				file["file_data"] = b.Data
			}
			if b.Filename != "" {
				file["filename"] = b.Filename
			}
			out = append(out, map[string]any{"type": "file", "file": file})
		case "refusal":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		}
	}
	return out
}
func encodeFormat(f *ir.ResponseFormat) any {
	if f.Type != "json_schema" {
		return map[string]any{"type": f.Type}
	}
	var schema any
	_ = json.Unmarshal(f.Schema, &schema)
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": f.Name, "schema": schema, "strict": f.Strict}}
}

func (*Adapter) DecodeResponse(_ context.Context, raw json.RawMessage) (*ir.Response, []ir.Diagnostic, error) {
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	resp := &ir.Response{ID: common.String(v["id"]), Model: common.String(v["model"])}
	for _, c := range common.Array(v["choices"]) {
		cm := common.Map(c)
		m := common.Map(cm["message"])
		msg := ir.Message{Role: "assistant", Content: common.TextBlocks(m["content"])}
		if refusal := common.String(m["refusal"]); refusal != "" {
			msg.Content = append(msg.Content, ir.ContentBlock{Type: "refusal", Text: refusal})
		}
		if reasoning := common.String(m["reasoning_content"]); reasoning != "" {
			msg.Content = append([]ir.ContentBlock{{Type: "reasoning", Text: reasoning}}, msg.Content...)
		}
		for _, tc := range common.Array(m["tool_calls"]) {
			t := common.Map(tc)
			fn := common.Map(t["function"])
			msg.Content = append(msg.Content, ir.ContentBlock{Type: "tool_call", ID: common.String(t["id"]), Name: common.String(fn["name"]), Arguments: json.RawMessage(common.String(fn["arguments"]))})
		}
		resp.Messages = append(resp.Messages, msg)
		resp.StopReason = common.String(cm["finish_reason"])
	}
	u := common.Map(v["usage"])
	promptDetails := common.Map(u["prompt_tokens_details"])
	completionDetails := common.Map(u["completion_tokens_details"])
	resp.Usage = ir.Usage{InputTokens: common.Int(u["prompt_tokens"]), OutputTokens: common.Int(u["completion_tokens"]), TotalTokens: common.Int(u["total_tokens"]), CachedTokens: common.Int(promptDetails["cached_tokens"]), ReasoningTokens: common.Int(completionDetails["reasoning_tokens"])}
	common.SetResponseSource(resp, ir.OpenAIChat)
	return resp, nil, nil
}
func (*Adapter) EncodeResponse(_ context.Context, r *ir.Response) (json.RawMessage, []ir.Diagnostic, error) {
	m := common.FirstAssistant(r)
	msg := map[string]any{"role": "assistant", "content": common.Text(m.Content)}
	var calls []any
	for _, b := range m.Content {
		if b.Type == "reasoning" {
			msg["reasoning_content"] = b.Text
		}
		if b.Type == "refusal" {
			msg["refusal"] = b.Text
		}
		if b.Type == "tool_call" {
			calls = append(calls, map[string]any{"id": b.ID, "type": "function", "function": map[string]any{"name": b.Name, "arguments": string(b.Arguments)}})
		}
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	usage := map[string]any{"prompt_tokens": r.Usage.InputTokens, "completion_tokens": r.Usage.OutputTokens, "total_tokens": r.Usage.TotalTokens}
	if r.Usage.CachedTokens > 0 {
		usage["prompt_tokens_details"] = map[string]any{"cached_tokens": r.Usage.CachedTokens}
	}
	if r.Usage.ReasoningTokens > 0 {
		usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": r.Usage.ReasoningTokens}
	}
	v := map[string]any{"id": r.ID, "object": "chat.completion", "created": time.Now().Unix(), "model": r.Model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": common.StopToOpenAI(r.StopReason)}}, "usage": usage}
	return common.Raw(v), common.ResponsePortabilityDiagnostics(r, ir.OpenAIChat), nil
}

func (*Adapter) DecodeStreamEvent(_ context.Context, _ string, raw json.RawMessage) ([]ir.Event, []ir.Diagnostic, error) {
	if string(raw) == "[DONE]" {
		return []ir.Event{{Type: "response.end"}}, nil, nil
	}
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	var out []ir.Event
	for _, c := range common.Array(v["choices"]) {
		cm := common.Map(c)
		d := common.Map(cm["delta"])
		if common.String(d["role"]) != "" {
			out = append(out, ir.Event{Type: "response.start", ResponseID: common.String(v["id"])})
		}
		if s := common.String(d["content"]); s != "" {
			out = append(out, ir.Event{Type: "text.delta", Delta: s})
		}
		if s := common.String(d["reasoning_content"]); s != "" {
			out = append(out, ir.Event{Type: "reasoning.delta", Delta: s})
		}
		for i, tc := range common.Array(d["tool_calls"]) {
			t := common.Map(tc)
			fn := common.Map(t["function"])
			index := i
			if _, present := t["index"]; present {
				index = common.Int(t["index"])
			}
			if common.String(t["id"]) != "" {
				out = append(out, ir.Event{Type: "tool_call.start", Index: index, Block: &ir.ContentBlock{Type: "tool_call", ID: common.String(t["id"]), Name: common.String(fn["name"])}})
			}
			if a := common.String(fn["arguments"]); a != "" {
				out = append(out, ir.Event{Type: "tool_call.arguments.delta", Index: index, Arguments: a})
			}
		}
		if f := common.String(cm["finish_reason"]); f != "" {
			out = append(out, ir.Event{Type: "message.end", StopReason: f})
		}
	}
	if u := common.Map(v["usage"]); len(u) > 0 {
		usage := ir.Usage{InputTokens: common.Int(u["prompt_tokens"]), OutputTokens: common.Int(u["completion_tokens"]), TotalTokens: common.Int(u["total_tokens"])}
		out = append(out, ir.Event{Type: "usage.update", Usage: &usage})
	}
	return out, nil, nil
}
func (*Adapter) EncodeStreamEvent(_ context.Context, e ir.Event) ([]protocol.SSE, []ir.Diagnostic, error) {
	if e.Type == "response.end" {
		return []protocol.SSE{{Data: []byte("[DONE]")}}, nil, nil
	}
	delta := map[string]any{}
	var finish any
	switch e.Type {
	case "response.start":
		delta["role"] = "assistant"
	case "text.delta":
		delta["content"] = e.Delta
	case "reasoning.delta":
		delta["reasoning_content"] = e.Delta
	case "tool_call.start":
		if e.Block == nil {
			return nil, nil, nil
		}
		delta["tool_calls"] = []any{map[string]any{"index": e.Index, "id": e.Block.ID, "type": "function", "function": map[string]any{"name": e.Block.Name, "arguments": ""}}}
	case "tool_call.arguments.delta":
		delta["tool_calls"] = []any{map[string]any{"index": e.Index, "function": map[string]any{"arguments": e.Arguments}}}
	case "message.end":
		finish = common.StopToOpenAI(e.StopReason)
	case "usage.update":
		if e.Usage == nil {
			return nil, nil, nil
		}
		chunk := map[string]any{"id": e.ResponseID, "object": "chat.completion.chunk", "choices": []any{}, "usage": map[string]any{"prompt_tokens": e.Usage.InputTokens, "completion_tokens": e.Usage.OutputTokens, "total_tokens": e.Usage.TotalTokens}}
		return []protocol.SSE{{Data: common.Raw(chunk)}}, nil, nil
	case "error":
		return []protocol.SSE{{Data: common.Raw(map[string]any{"error": e.Error})}}, nil, nil
	default:
		return nil, nil, nil
	}
	chunk := map[string]any{"id": e.ResponseID, "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	return []protocol.SSE{{Data: common.Raw(chunk)}}, nil, nil
}

var _ protocol.Adapter = (*Adapter)(nil)
