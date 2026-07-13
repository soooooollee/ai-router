package gemini

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
)

type Adapter struct{}

func New() *Adapter                    { return &Adapter{} }
func (*Adapter) Protocol() ir.Protocol { return ir.Gemini }

func (*Adapter) DecodeRequest(_ context.Context, raw json.RawMessage) (*ir.Request, []ir.Diagnostic, error) {
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	model, e := common.RequireModel(v)
	if e != nil {
		return nil, nil, e
	}
	r := &ir.Request{Model: model, Stream: common.Bool(v["stream"])}
	if sys := common.Map(v["systemInstruction"]); len(sys) > 0 {
		r.Instructions = decodeParts(common.Array(sys["parts"]))
	}
	for _, x := range common.Array(v["contents"]) {
		m := common.Map(x)
		role := common.String(m["role"])
		if role == "model" {
			role = "assistant"
		}
		r.Messages = append(r.Messages, ir.Message{Role: role, Content: decodeParts(common.Array(m["parts"]))})
	}
	for _, group := range common.Array(v["tools"]) {
		g := common.Map(group)
		for _, x := range common.Array(g["functionDeclarations"]) {
			t := common.Map(x)
			r.Tools = append(r.Tools, ir.Tool{Name: common.String(t["name"]), Description: common.String(t["description"]), InputSchema: common.Raw(t["parameters"])})
		}
	}
	if cfg := common.Map(v["generationConfig"]); len(cfg) > 0 {
		r.Sampling = ir.SamplingOptions{Temperature: common.FloatPtr(cfg["temperature"]), TopP: common.FloatPtr(cfg["topP"]), TopK: common.IntPtr(cfg["topK"]), MaxOutputTokens: common.IntPtr(cfg["maxOutputTokens"])}
		for _, s := range common.Array(cfg["stopSequences"]) {
			r.Sampling.Stop = append(r.Sampling.Stop, common.String(s))
		}
		if schema := cfg["responseSchema"]; schema != nil {
			r.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: common.Raw(schema)}
		}
	}
	if tc := common.Map(v["toolConfig"]); len(tc) > 0 {
		fc := common.Map(tc["functionCallingConfig"])
		mode := strings.ToLower(common.String(fc["mode"]))
		choice := &ir.ToolChoice{Type: map[string]string{"auto": "auto", "any": "required", "none": "none"}[mode]}
		if names := common.Array(fc["allowedFunctionNames"]); len(names) > 0 {
			choice.Type = "tool"
			choice.Name = common.String(names[0])
		}
		r.ToolChoice = choice
	}
	d := common.DiagnosticsForExtensions(r)
	d = append(d, common.CaptureRequestExtensions(r, v, "model", "stream", "systemInstruction", "contents", "tools", "toolConfig", "generationConfig", "safetySettings", "cachedContent")...)
	common.SetRequestSource(r, ir.Gemini)
	return r, d, nil
}

func decodeParts(parts []any) []ir.ContentBlock {
	var out []ir.ContentBlock
	for _, x := range parts {
		p := common.Map(x)
		switch {
		case common.Bool(p["thought"]):
			b := ir.ContentBlock{Type: "reasoning", Text: common.String(p["text"])}
			if p["thoughtSignature"] != nil {
				b.Extension = common.Raw(map[string]any{"thoughtSignature": p["thoughtSignature"]})
			}
			out = append(out, b)
		case p["text"] != nil:
			b := ir.ContentBlock{Type: "text", Text: common.String(p["text"])}
			if p["thoughtSignature"] != nil {
				b.Extension = common.Raw(map[string]any{"thoughtSignature": p["thoughtSignature"]})
			}
			out = append(out, b)
		case p["inlineData"] != nil:
			d := common.Map(p["inlineData"])
			typ := "document"
			if common.String(d["mimeType"]) == "" || strings.HasPrefix(common.String(d["mimeType"]), "image/") {
				typ = "image_base64"
			}
			out = append(out, ir.ContentBlock{Type: typ, MediaType: common.String(d["mimeType"]), Data: common.String(d["data"])})
		case p["fileData"] != nil:
			d := common.Map(p["fileData"])
			typ := "document"
			if common.String(d["mimeType"]) == "" || strings.HasPrefix(common.String(d["mimeType"]), "image/") {
				typ = "image_url"
			}
			out = append(out, ir.ContentBlock{Type: typ, MediaType: common.String(d["mimeType"]), URL: common.String(d["fileUri"])})
		case p["functionCall"] != nil:
			f := common.Map(p["functionCall"])
			b := ir.ContentBlock{Type: "tool_call", ID: common.String(f["id"]), Name: common.String(f["name"]), Arguments: common.Raw(f["args"])}
			if p["thoughtSignature"] != nil {
				b.Extension = common.Raw(map[string]any{"thoughtSignature": p["thoughtSignature"]})
			}
			out = append(out, b)
		case p["functionResponse"] != nil:
			f := common.Map(p["functionResponse"])
			out = append(out, ir.ContentBlock{Type: "tool_result", ID: common.String(f["id"]), Name: common.String(f["name"]), Result: common.Raw(f["response"])})
		}
	}
	return out
}

func (*Adapter) EncodeRequest(_ context.Context, r *ir.Request) (json.RawMessage, []ir.Diagnostic, error) {
	v := map[string]any{"model": r.Model, "stream": r.Stream}
	if len(r.Instructions) > 0 {
		v["systemInstruction"] = map[string]any{"parts": encodeParts(r.Instructions)}
	}
	var contents []any
	for _, m := range r.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "tool" {
			role = "user"
		}
		contents = append(contents, map[string]any{"role": role, "parts": encodeParts(m.Content)})
	}
	v["contents"] = contents
	if len(r.Tools) > 0 {
		var decl []any
		for _, t := range r.Tools {
			var schema any
			_ = json.Unmarshal(t.InputSchema, &schema)
			decl = append(decl, map[string]any{"name": t.Name, "description": t.Description, "parameters": schema})
		}
		v["tools"] = []any{map[string]any{"functionDeclarations": decl}}
	}
	cfg := map[string]any{}
	if r.Sampling.Temperature != nil {
		cfg["temperature"] = *r.Sampling.Temperature
	}
	if r.Sampling.TopP != nil {
		cfg["topP"] = *r.Sampling.TopP
	}
	if r.Sampling.TopK != nil {
		cfg["topK"] = *r.Sampling.TopK
	}
	if r.Sampling.MaxOutputTokens != nil {
		cfg["maxOutputTokens"] = *r.Sampling.MaxOutputTokens
	}
	if len(r.Sampling.Stop) > 0 {
		cfg["stopSequences"] = r.Sampling.Stop
	}
	if r.ResponseFormat != nil {
		var schema any
		_ = json.Unmarshal(r.ResponseFormat.Schema, &schema)
		cfg["responseMimeType"] = "application/json"
		cfg["responseSchema"] = schema
	}
	if len(cfg) > 0 {
		v["generationConfig"] = cfg
	}
	if r.ToolChoice != nil {
		mode := "AUTO"
		if r.ToolChoice.Type == "none" {
			mode = "NONE"
		} else if r.ToolChoice.Type == "required" || r.ToolChoice.Name != "" {
			mode = "ANY"
		}
		tc := map[string]any{"mode": mode}
		if r.ToolChoice.Name != "" {
			tc["allowedFunctionNames"] = []string{r.ToolChoice.Name}
		}
		v["toolConfig"] = map[string]any{"functionCallingConfig": tc}
	}
	common.RestoreRequestExtensions(v, r, ir.Gemini)
	return common.Raw(v), common.RequestPortabilityDiagnostics(r, ir.Gemini), nil
}

func encodeParts(blocks []ir.ContentBlock) []any {
	var out []any
	for _, b := range blocks {
		switch b.Type {
		case "text":
			part := map[string]any{"text": b.Text}
			mergeThoughtSignature(part, b.Extension, false)
			out = append(out, part)
		case "reasoning":
			part := map[string]any{"text": b.Text, "thought": true}
			mergeThoughtSignature(part, b.Extension, false)
			out = append(out, part)
		case "image_url":
			out = append(out, map[string]any{"fileData": map[string]any{"mimeType": b.MediaType, "fileUri": b.URL}})
		case "image_base64":
			out = append(out, map[string]any{"inlineData": map[string]any{"mimeType": b.MediaType, "data": b.Data}})
		case "document":
			if b.URL != "" {
				out = append(out, map[string]any{"fileData": map[string]any{"mimeType": b.MediaType, "fileUri": b.URL}})
			} else {
				out = append(out, map[string]any{"inlineData": map[string]any{"mimeType": b.MediaType, "data": b.Data}})
			}
		case "refusal":
			out = append(out, map[string]any{"text": b.Text})
		case "tool_call":
			var args any
			_ = json.Unmarshal(b.Arguments, &args)
			part := map[string]any{"functionCall": map[string]any{"id": b.ID, "name": b.Name, "args": args}}
			mergeThoughtSignature(part, b.Extension, true)
			out = append(out, part)
		case "tool_result":
			var result any
			_ = json.Unmarshal(b.Result, &result)
			out = append(out, map[string]any{"functionResponse": map[string]any{"id": b.ID, "name": b.Name, "response": result}})
		}
	}
	return out
}

func mergeThoughtSignature(part map[string]any, extension json.RawMessage, required bool) {
	var ext map[string]any
	_ = json.Unmarshal(extension, &ext)
	if signature := common.String(ext["thoughtSignature"]); signature != "" {
		part["thoughtSignature"] = signature
	} else if required {
		// Gemini explicitly documents this sentinel for function calls injected by
		// context-engineering systems that cannot possess a model-issued signature.
		part["thoughtSignature"] = "skip_thought_signature_validator"
	}
}

func (*Adapter) DecodeResponse(_ context.Context, raw json.RawMessage) (*ir.Response, []ir.Diagnostic, error) {
	v, e := common.DecodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	r := &ir.Response{Model: common.String(v["modelVersion"])}
	for _, x := range common.Array(v["candidates"]) {
		c := common.Map(x)
		content := common.Map(c["content"])
		r.Messages = append(r.Messages, ir.Message{Role: "assistant", Content: decodeParts(common.Array(content["parts"]))})
		r.StopReason = common.String(c["finishReason"])
	}
	u := common.Map(v["usageMetadata"])
	r.Usage = ir.Usage{InputTokens: common.Int(u["promptTokenCount"]), OutputTokens: common.Int(u["candidatesTokenCount"]), CachedTokens: common.Int(u["cachedContentTokenCount"]), ReasoningTokens: common.Int(u["thoughtsTokenCount"]), TotalTokens: common.Int(u["totalTokenCount"])}
	common.SetResponseSource(r, ir.Gemini)
	return r, nil, nil
}
func (*Adapter) EncodeResponse(_ context.Context, r *ir.Response) (json.RawMessage, []ir.Diagnostic, error) {
	m := common.FirstAssistant(r)
	v := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": encodeParts(m.Content)}, "finishReason": stopToGemini(r.StopReason), "index": 0}}, "usageMetadata": map[string]any{"promptTokenCount": r.Usage.InputTokens, "candidatesTokenCount": r.Usage.OutputTokens, "cachedContentTokenCount": r.Usage.CachedTokens, "thoughtsTokenCount": r.Usage.ReasoningTokens, "totalTokenCount": r.Usage.TotalTokens}, "modelVersion": r.Model}
	return common.Raw(v), common.ResponsePortabilityDiagnostics(r, ir.Gemini), nil
}
func stopToGemini(s string) string {
	switch strings.ToLower(s) {
	case "stop", "end_turn", "completed":
		return "STOP"
	case "length", "max_tokens":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return strings.ToUpper(s)
	}
}

func (*Adapter) DecodeStreamEvent(ctx context.Context, _ string, raw json.RawMessage) ([]ir.Event, []ir.Diagnostic, error) {
	r, d, e := (&Adapter{}).DecodeResponse(ctx, raw)
	if e != nil {
		return nil, d, e
	}
	var out []ir.Event
	for _, m := range r.Messages {
		for i, b := range m.Content {
			switch b.Type {
			case "text":
				out = append(out, ir.Event{Type: "text.delta", Index: i, Delta: b.Text})
			case "reasoning":
				out = append(out, ir.Event{Type: "reasoning.delta", Index: i, Delta: b.Text})
			case "tool_call":
				start := b
				start.Arguments = nil
				out = append(out, ir.Event{Type: "tool_call.start", Index: i, Block: &start}, ir.Event{Type: "tool_call.arguments.delta", Index: i, Arguments: string(b.Arguments)})
			}
		}
	}
	if r.StopReason != "" {
		out = append(out, ir.Event{Type: "message.end", StopReason: r.StopReason})
	}
	if r.Usage.TotalTokens > 0 {
		u := r.Usage
		out = append(out, ir.Event{Type: "usage.update", Usage: &u})
	}
	return out, d, nil
}
func (*Adapter) EncodeStreamEvent(_ context.Context, e ir.Event) ([]protocol.SSE, []ir.Diagnostic, error) {
	parts := []any{}
	candidate := map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "index": 0}
	switch e.Type {
	case "text.delta":
		parts = append(parts, map[string]any{"text": e.Delta})
	case "reasoning.delta":
		parts = append(parts, map[string]any{"text": e.Delta, "thought": true})
	case "tool_call.start":
		if e.Block == nil {
			return nil, nil, nil
		}
		var args any
		_ = json.Unmarshal(e.Block.Arguments, &args)
		part := map[string]any{"functionCall": map[string]any{"id": e.Block.ID, "name": e.Block.Name, "args": args}}
		mergeThoughtSignature(part, e.Block.Extension, true)
		parts = append(parts, part)
	case "tool_call.arguments.delta":
		return nil, nil, nil
	case "message.end":
		candidate["finishReason"] = stopToGemini(e.StopReason)
	case "usage.update":
		if e.Usage == nil {
			return nil, nil, nil
		}
		return []protocol.SSE{{Data: common.Raw(map[string]any{"candidates": []any{}, "usageMetadata": map[string]any{"promptTokenCount": e.Usage.InputTokens, "candidatesTokenCount": e.Usage.OutputTokens, "cachedContentTokenCount": e.Usage.CachedTokens, "thoughtsTokenCount": e.Usage.ReasoningTokens, "totalTokenCount": e.Usage.TotalTokens}})}}, nil, nil
	case "response.end":
		return nil, nil, nil
	case "error":
		return []protocol.SSE{{Data: common.Raw(map[string]any{"error": e.Error})}}, nil, nil
	default:
		return nil, nil, nil
	}
	candidate["content"].(map[string]any)["parts"] = parts
	return []protocol.SSE{{Data: common.Raw(map[string]any{"candidates": []any{candidate}})}}, nil, nil
}

var _ protocol.Adapter = (*Adapter)(nil)
