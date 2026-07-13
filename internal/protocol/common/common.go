package common

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zbss/airoute/internal/protocol/ir"
)

func Raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func Object(raw json.RawMessage) map[string]any {
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	return v
}
func String(v any) string { s, _ := v.(string); return s }
func Bool(v any) bool     { b, _ := v.(bool); return b }
func Int(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}
func FloatPtr(v any) *float64 {
	if n, ok := v.(float64); ok {
		return &n
	}
	return nil
}
func IntPtr(v any) *int {
	n := Int(v)
	if n == 0 {
		return nil
	}
	return &n
}
func Array(v any) []any        { a, _ := v.([]any); return a }
func Map(v any) map[string]any { m, _ := v.(map[string]any); return m }

func DecodeJSON(raw json.RawMessage) (map[string]any, error) {
	var v map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
func RequireModel(v map[string]any) (string, error) {
	model := String(v["model"])
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	return model, nil
}

func TextBlocks(v any) []ir.ContentBlock {
	switch x := v.(type) {
	case string:
		return []ir.ContentBlock{{Type: "text", Text: x}}
	case []any:
		out := make([]ir.ContentBlock, 0, len(x))
		for _, item := range x {
			m := Map(item)
			typ := String(m["type"])
			switch typ {
			case "text", "input_text", "output_text":
				b := ir.ContentBlock{Type: "text", Text: String(m["text"])}
				if m["cache_control"] != nil {
					b.Extension = Raw(map[string]any{"cache_control": m["cache_control"]})
				}
				out = append(out, b)
			case "refusal":
				out = append(out, ir.ContentBlock{Type: "refusal", Text: first(String(m["refusal"]), String(m["text"]))})
			case "image_url":
				im := Map(m["image_url"])
				if len(im) == 0 {
					out = append(out, ir.ContentBlock{Type: "image_url", URL: String(m["url"])})
				} else {
					u := String(im["url"])
					if strings.HasPrefix(u, "data:") && strings.Contains(u, ";base64,") {
						parts := strings.SplitN(strings.TrimPrefix(u, "data:"), ";base64,", 2)
						out = append(out, ir.ContentBlock{Type: "image_base64", MediaType: parts[0], Data: parts[1]})
					} else {
						out = append(out, ir.ContentBlock{Type: "image_url", URL: u})
					}
				}
			case "input_image":
				out = append(out, ir.ContentBlock{Type: "image_url", URL: String(m["image_url"])})
			case "input_file", "file", "document":
				file := Map(m["file"])
				if len(file) == 0 {
					file = m
				}
				source := Map(m["source"])
				if len(source) > 0 {
					file = source
				}
				out = append(out, ir.ContentBlock{Type: "document", MediaType: first(String(file["media_type"]), String(file["mime_type"])), URL: first(String(file["file_url"]), String(file["url"])), Data: first(String(file["file_data"]), String(file["data"])), FileID: String(file["file_id"]), Filename: String(file["filename"]), Extension: Raw(m)})
			case "tool_use", "function_call":
				b := ir.ContentBlock{Type: "tool_call", ID: String(m["id"]), Name: first(String(m["name"]), String(m["function"])), Arguments: Raw(m["input"])}
				if m["signature"] != nil || m["thoughtSignature"] != nil || m["encrypted_content"] != nil {
					b.Extension = Raw(m)
				}
				out = append(out, b)
			case "tool_result", "function_call_output":
				out = append(out, ir.ContentBlock{Type: "tool_result", ID: first(String(m["tool_use_id"]), String(m["call_id"])), Result: Raw(m["content"]), IsError: Bool(m["is_error"])})
			case "thinking", "reasoning":
				b := ir.ContentBlock{Type: "reasoning", Text: first(String(m["thinking"]), String(m["text"]))}
				if m["signature"] != nil || m["encrypted_content"] != nil || m["id"] != nil {
					b.Extension = Raw(m)
				}
				out = append(out, b)
			case "image":
				source := Map(m["source"])
				if String(source["type"]) == "base64" {
					out = append(out, ir.ContentBlock{Type: "image_base64", MediaType: String(source["media_type"]), Data: String(source["data"])})
				} else {
					out = append(out, ir.ContentBlock{Type: "image_url", MediaType: String(source["media_type"]), URL: String(source["url"])})
				}
			default:
				out = append(out, ir.ContentBlock{Type: "extension", SourceType: typ, Extension: Raw(m)})
			}
		}
		return out
	default:
		return nil
	}
}

func SetRequestSource(r *ir.Request, p ir.Protocol) {
	r.Source = p
	for i := range r.Instructions {
		r.Instructions[i].Source = p
	}
	for mi := range r.Messages {
		for bi := range r.Messages[mi].Content {
			r.Messages[mi].Content[bi].Source = p
		}
	}
}

func SetResponseSource(r *ir.Response, p ir.Protocol) {
	for mi := range r.Messages {
		for bi := range r.Messages[mi].Content {
			r.Messages[mi].Content[bi].Source = p
		}
	}
}

func IsLossy(diagnostics []ir.Diagnostic) bool {
	for _, d := range diagnostics {
		if d.Severity == "error" || d.Action == "approximated" || d.Action == "dropped" || d.Action == "rejected" {
			return true
		}
	}
	return false
}

func PortabilityDiagnostics(blocks []ir.ContentBlock, target ir.Protocol, path string) []ir.Diagnostic {
	var out []ir.Diagnostic
	for i, b := range blocks {
		p := fmt.Sprintf("%s[%d]", path, i)
		switch b.Type {
		case "refusal":
			if target == ir.Anthropic || target == ir.Gemini {
				out = append(out, ir.Diagnostic{Severity: "warning", Code: "refusal_as_text", Path: p, Message: "target protocol has no portable refusal content block", Action: "approximated"})
			}
		case "reasoning":
			if target == ir.Anthropic && b.Source != ir.Anthropic {
				out = append(out, ir.Diagnostic{Severity: "warning", Code: "unsigned_thinking", Path: p, Message: "Anthropic thinking requires provider-issued opaque state", Action: "dropped"})
			}
		}
	}
	return out
}

func RequestPortabilityDiagnostics(r *ir.Request, target ir.Protocol) []ir.Diagnostic {
	out := PortabilityDiagnostics(r.Instructions, target, "instructions")
	for i, m := range r.Messages {
		out = append(out, PortabilityDiagnostics(m.Content, target, fmt.Sprintf("messages[%d].content", i))...)
	}
	if len(r.Extensions) > 0 && r.Source != target {
		out = append(out, ir.Diagnostic{Severity: "warning", Code: "request_extensions_not_portable", Path: "extensions", Message: "source-protocol request fields cannot be sent to a different protocol", Action: "dropped"})
	}
	return out
}

// RestoreRequestExtensions re-emits unknown top-level fields only for a
// same-protocol round trip. Portable fields produced by the encoder win.
func RestoreRequestExtensions(out map[string]any, r *ir.Request, target ir.Protocol) {
	if r.Source != target {
		return
	}
	for key, raw := range r.Extensions {
		if _, exists := out[key]; exists {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			out[key] = value
		}
	}
}

func ResponsePortabilityDiagnostics(r *ir.Response, target ir.Protocol) []ir.Diagnostic {
	var out []ir.Diagnostic
	for i, m := range r.Messages {
		out = append(out, PortabilityDiagnostics(m.Content, target, fmt.Sprintf("messages[%d].content", i))...)
	}
	return out
}

func Text(blocks []ir.ContentBlock) string {
	var b strings.Builder
	for _, x := range blocks {
		if x.Type == "text" {
			b.WriteString(x.Text)
		}
	}
	return b.String()
}
func HasType(r *ir.Request, typ string) bool {
	for _, m := range r.Messages {
		for _, b := range m.Content {
			if b.Type == typ {
				return true
			}
		}
	}
	return false
}
func FirstAssistant(resp *ir.Response) ir.Message {
	for _, m := range resp.Messages {
		if m.Role == "assistant" || m.Role == "model" {
			return m
		}
	}
	if len(resp.Messages) > 0 {
		return resp.Messages[0]
	}
	return ir.Message{Role: "assistant"}
}
func StopToOpenAI(s string) string {
	switch s {
	case "end_turn", "stop", "STOP", "completed":
		return "stop"
	case "max_tokens", "length", "MAX_TOKENS":
		return "length"
	case "tool_use", "tool_calls":
		return "tool_calls"
	case "content_filter", "SAFETY":
		return "content_filter"
	default:
		return s
	}
}
func StopToAnthropic(s string) string {
	switch s {
	case "stop", "end_turn", "STOP", "completed":
		return "end_turn"
	case "length", "max_tokens", "MAX_TOKENS":
		return "max_tokens"
	case "tool_calls", "tool_use":
		return "tool_use"
	default:
		return s
	}
}
func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func DiagnosticsForExtensions(r *ir.Request) []ir.Diagnostic {
	var out []ir.Diagnostic
	for mi, m := range r.Messages {
		for bi, b := range m.Content {
			if b.Type == "extension" {
				out = append(out, ir.Diagnostic{Severity: "warning", Code: "unsupported_content_block", Path: fmt.Sprintf("messages[%d].content[%d]", mi, bi), Message: "protocol-specific content block cannot be represented portably", Action: "preserved"})
			} else if len(b.Extension) > 0 {
				out = append(out, ir.Diagnostic{Severity: "warning", Code: "protocol_extension_not_portable", Path: fmt.Sprintf("messages[%d].content[%d]", mi, bi), Message: "protocol-specific metadata has no portable equivalent", Action: "preserved"})
			}
		}
	}
	return out
}

func CaptureRequestExtensions(r *ir.Request, v map[string]any, allowed ...string) []ir.Diagnostic {
	known := map[string]bool{}
	for _, k := range allowed {
		known[k] = true
	}
	if r.Extensions == nil {
		r.Extensions = map[string]json.RawMessage{}
	}
	var keys []string
	for k := range v {
		if !known[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []ir.Diagnostic
	for _, k := range keys {
		r.Extensions[k] = Raw(v[k])
		out = append(out, ir.Diagnostic{Severity: "warning", Code: "unsupported_request_field", Path: k, Message: "request field is specific to the source protocol", Action: "dropped"})
	}
	return out
}
