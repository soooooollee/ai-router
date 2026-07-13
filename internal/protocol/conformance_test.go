package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/anthropic"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/gemini"
	"github.com/zbss/airoute/internal/protocol/ir"
	"github.com/zbss/airoute/internal/protocol/openaichat"
	"github.com/zbss/airoute/internal/protocol/openairesponses"
)

func TestAllRequestConversionDirections(t *testing.T) {
	r := adapters()
	for source, raw := range requestSamples() {
		for _, target := range protocols() {
			t.Run(string(source)+"_to_"+string(target), func(t *testing.T) {
				s, _ := r.Get(source)
				d, _ := r.Get(target)
				canonical, _, err := s.DecodeRequest(context.Background(), raw)
				if err != nil {
					t.Fatalf("decode source: %v", err)
				}
				encoded, _, err := d.EncodeRequest(context.Background(), canonical)
				if err != nil {
					t.Fatalf("encode target: %v", err)
				}
				if !json.Valid(encoded) {
					t.Fatalf("invalid target JSON: %s", encoded)
				}
				roundtrip, _, err := d.DecodeRequest(context.Background(), encoded)
				if err != nil {
					t.Fatalf("decode target: %v\n%s", err, encoded)
				}
				if roundtrip.Model != "test-model" {
					t.Fatalf("model lost: %q", roundtrip.Model)
				}
				if len(roundtrip.Messages) == 0 {
					t.Fatal("messages lost")
				}
				if len(roundtrip.Tools) != 1 {
					t.Fatalf("tool lost: %#v", roundtrip.Tools)
				}
				if !common.HasType(roundtrip, "image_url") && !common.HasType(roundtrip, "image_base64") {
					t.Fatalf("image lost: %s", encoded)
				}
			})
		}
	}
}

func TestRequestExtensionsSameProtocolPreservedAndCrossProtocolDiagnosed(t *testing.T) {
	r := adapters()
	chat, _ := r.Get(ir.OpenAIChat)
	canonical, _, err := chat.DecodeRequest(context.Background(), json.RawMessage(`{"model":"m","messages":[{"role":"user","content":"hi"}],"vendor_magic":{"mode":"fast"}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, diagnostics, err := chat.EncodeRequest(context.Background(), canonical)
	if err != nil {
		t.Fatal(err)
	}
	var same map[string]any
	_ = json.Unmarshal(encoded, &same)
	if same["vendor_magic"] == nil || common.IsLossy(diagnostics) {
		t.Fatalf("same-protocol extension was not preserved: %s %#v", encoded, diagnostics)
	}
	anthropicAdapter, _ := r.Get(ir.Anthropic)
	_, diagnostics, err = anthropicAdapter.EncodeRequest(context.Background(), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !common.IsLossy(diagnostics) {
		t.Fatalf("cross-protocol extension loss was not diagnosed: %#v", diagnostics)
	}
}

func TestQwen3ThinkingHistoryAndStreamToolIndex(t *testing.T) {
	chat, _ := adapters().Get(ir.OpenAIChat)
	canonical, _, err := chat.DecodeRequest(context.Background(), json.RawMessage(`{"model":"Qwen/Qwen3.6-35B-A3B","enable_thinking":false,"messages":[{"role":"assistant","reasoning_content":"private chain state","content":"answer"},{"role":"user","content":"continue"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Sampling.ReasoningEnabled == nil || *canonical.Sampling.ReasoningEnabled || canonical.Messages[0].Content[0].Type != "reasoning" {
		t.Fatalf("Qwen3 thinking request was not normalized: %#v", canonical)
	}
	encoded, _, err := chat.EncodeRequest(context.Background(), canonical)
	if err != nil || !bytes.Contains(encoded, []byte(`"reasoning_content":"private chain state"`)) || !bytes.Contains(encoded, []byte(`"enable_thinking":false`)) {
		t.Fatalf("Qwen3 thinking history was not preserved: %v %s", err, encoded)
	}
	events, _, err := chat.DecodeStreamEvent(context.Background(), "", json.RawMessage(`{"id":"r","choices":[{"delta":{"tool_calls":[{"index":4,"id":"call_4","function":{"name":"lookup","arguments":"{\"x\":"}}]}}]}`))
	if err != nil || len(events) != 2 || events[0].Index != 4 || events[1].Index != 4 {
		t.Fatalf("Qwen3 stream tool index was lost: %v %#v", err, events)
	}
}

func TestAllResponseConversionDirections(t *testing.T) {
	r := adapters()
	for source, raw := range responseSamples() {
		for _, target := range protocols() {
			t.Run(string(source)+"_to_"+string(target), func(t *testing.T) {
				s, _ := r.Get(source)
				d, _ := r.Get(target)
				canonical, _, err := s.DecodeResponse(context.Background(), raw)
				if err != nil {
					t.Fatal(err)
				}
				encoded, _, err := d.EncodeResponse(context.Background(), canonical)
				if err != nil {
					t.Fatal(err)
				}
				if !json.Valid(encoded) {
					t.Fatalf("invalid JSON: %s", encoded)
				}
				roundtrip, _, err := d.DecodeResponse(context.Background(), encoded)
				if err != nil {
					t.Fatal(err)
				}
				if len(roundtrip.Messages) == 0 {
					t.Fatal("response message lost")
				}
			})
		}
	}
}

func TestParallelToolCallsAllProtocols(t *testing.T) {
	response := &ir.Response{ID: "r", Model: "m", Messages: []ir.Message{
		{Role: "assistant", Content: []ir.ContentBlock{
			{Type: "tool_call", ID: "call_1", Name: "first", Arguments: json.RawMessage(`{"x":1}`)},
			{Type: "tool_call", ID: "call_2", Name: "second", Arguments: json.RawMessage(`{"y":2}`)},
		}},
	}}
	for _, target := range protocols() {
		t.Run(string(target), func(t *testing.T) {
			adapter, _ := adapters().Get(target)
			encoded, _, err := adapter.EncodeResponse(context.Background(), response)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := adapter.DecodeResponse(context.Background(), encoded)
			if err != nil {
				t.Fatalf("decode parallel calls: %v\n%s", err, encoded)
			}
			calls := 0
			for _, message := range decoded.Messages {
				for _, block := range message.Content {
					if block.Type == "tool_call" {
						calls++
					}
				}
			}
			if calls != 2 {
				t.Fatalf("parallel tool calls lost: got %d\n%s", calls, encoded)
			}
		})
	}
}

func TestAllStreamingConversionDirections(t *testing.T) {
	r := adapters()
	canonical := []ir.Event{{Type: "response.start", ResponseID: "r1"}, {Type: "content.start", Index: 0, Block: &ir.ContentBlock{Type: "text"}}, {Type: "text.delta", Index: 0, Delta: "hello"}, {Type: "content.end", Index: 0}, {Type: "message.end", StopReason: "stop"}, {Type: "response.end"}}
	for _, source := range protocols() {
		for _, target := range protocols() {
			t.Run(string(source)+"_to_"+string(target), func(t *testing.T) {
				s, _ := r.Get(source)
				d, _ := r.Get(target)
				var decoded []ir.Event
				for _, event := range canonical {
					chunks, _, err := s.EncodeStreamEvent(context.Background(), event)
					if err != nil {
						t.Fatal(err)
					}
					for _, chunk := range chunks {
						events, _, err := s.DecodeStreamEvent(context.Background(), chunk.Event, chunk.Data)
						if err != nil {
							t.Fatal(err)
						}
						decoded = append(decoded, events...)
					}
				}
				seenText := false
				encodedCount := 0
				for _, event := range decoded {
					if event.Type == "text.delta" && event.Delta == "hello" {
						seenText = true
					}
					chunks, _, err := d.EncodeStreamEvent(context.Background(), event)
					if err != nil {
						t.Fatal(err)
					}
					encodedCount += len(chunks)
				}
				if !seenText {
					t.Fatalf("source %s lost text event: %#v", source, decoded)
				}
				if encodedCount == 0 {
					t.Fatalf("target %s produced no events", target)
				}
			})
		}
	}
}

func TestToolCallAndResultRoundTrip(t *testing.T) {
	r := adapters()
	canonical := &ir.Request{Model: "m", Messages: []ir.Message{
		{Role: "assistant", Content: []ir.ContentBlock{{Type: "tool_call", ID: "c1", Name: "weather", Arguments: json.RawMessage(`{"city":"Paris"}`)}}},
		{Role: "tool", Content: []ir.ContentBlock{{Type: "tool_result", ID: "c1", Name: "weather", Result: json.RawMessage(`{"temperature":20}`)}}},
	}}
	for _, p := range protocols() {
		t.Run(string(p), func(t *testing.T) {
			a, _ := r.Get(p)
			raw, _, err := a.EncodeRequest(context.Background(), canonical)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := a.DecodeRequest(context.Background(), raw)
			if err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			found := false
			for _, m := range decoded.Messages {
				for _, b := range m.Content {
					if b.Type == "tool_call" {
						var args map[string]any
						if json.Unmarshal(b.Arguments, &args) != nil || args["city"] != "Paris" {
							t.Fatalf("tool arguments corrupted: %s", b.Arguments)
						}
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("tool call lost: %s", raw)
			}
		})
	}
}

func TestReasoningAndStructuredOutputConversion(t *testing.T) {
	r := adapters()
	canonical := &ir.Request{Model: "m", Messages: []ir.Message{{Role: "assistant", Content: []ir.ContentBlock{{Type: "reasoning", Text: "think"}, {Type: "text", Text: "answer"}}}}, ResponseFormat: &ir.ResponseFormat{Type: "json_schema", Name: "result", Schema: json.RawMessage(`{"type":"object"}`), Strict: true}}
	for _, p := range protocols() {
		t.Run(string(p), func(t *testing.T) {
			a, _ := r.Get(p)
			raw, d, err := a.EncodeRequest(context.Background(), canonical)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := a.DecodeRequest(context.Background(), raw)
			if err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			reasoning := false
			for _, m := range decoded.Messages {
				for _, b := range m.Content {
					reasoning = reasoning || b.Type == "reasoning"
				}
			}
			if p == ir.Anthropic {
				if reasoning {
					t.Fatalf("unsigned cross-provider reasoning must not be forged: %s", raw)
				}
				if !common.IsLossy(d) {
					t.Fatal("Anthropic reasoning/structured-output loss was not diagnosed")
				}
			} else {
				if !reasoning {
					t.Fatalf("reasoning lost: %s", raw)
				}
				if decoded.ResponseFormat == nil {
					t.Fatalf("response format lost: %s", raw)
				}
			}
		})
	}
}

func TestCachedUsageRoundTrip(t *testing.T) {
	r := adapters()
	canonical := &ir.Response{ID: "r", Model: "m", Messages: []ir.Message{{Role: "assistant", Content: []ir.ContentBlock{{Type: "text", Text: "ok"}}}}, StopReason: "stop", Usage: ir.Usage{InputTokens: 10, OutputTokens: 2, CachedTokens: 3, TotalTokens: 12}}
	for _, p := range protocols() {
		t.Run(string(p), func(t *testing.T) {
			a, _ := r.Get(p)
			raw, _, err := a.EncodeResponse(context.Background(), canonical)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := a.DecodeResponse(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Usage.CachedTokens != 3 {
				t.Fatalf("cached usage lost: %s", raw)
			}
		})
	}
}

func TestOpaqueReasoningStateRoundTrip(t *testing.T) {
	t.Run("anthropic signature and redaction", func(t *testing.T) {
		a := anthropic.New()
		raw := json.RawMessage(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"summary","signature":"opaque"},{"type":"redacted_thinking","data":"cipher"},{"type":"tool_use","id":"c1","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}],"max_tokens":10}`)
		canonical, _, err := a.DecodeRequest(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _, err := a.EncodeRequest(context.Background(), canonical)
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		_ = json.Unmarshal(encoded, &v)
		blocks := common.Array(common.Map(common.Array(v["messages"])[0])["content"])
		if common.String(common.Map(blocks[0])["signature"]) != "opaque" || common.String(common.Map(blocks[1])["data"]) != "cipher" {
			t.Fatalf("opaque Anthropic state lost: %s", encoded)
		}
	})

	t.Run("gemini signature and injected sentinel", func(t *testing.T) {
		a := gemini.New()
		raw := json.RawMessage(`{"model":"m","contents":[{"role":"model","parts":[{"functionCall":{"id":"c1","name":"f","args":{}},"thoughtSignature":"opaque"}]}]}`)
		canonical, _, err := a.DecodeRequest(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _, _ := a.EncodeRequest(context.Background(), canonical)
		var v map[string]any
		_ = json.Unmarshal(encoded, &v)
		part := common.Map(common.Array(common.Map(common.Array(v["contents"])[0])["parts"])[0])
		if common.String(part["thoughtSignature"]) != "opaque" {
			t.Fatalf("Gemini signature lost: %s", encoded)
		}

		injected := &ir.Request{Model: "m", Messages: []ir.Message{{Role: "assistant", Content: []ir.ContentBlock{{Type: "tool_call", ID: "c2", Name: "f", Arguments: json.RawMessage(`{}`)}}}}}
		encoded, _, _ = a.EncodeRequest(context.Background(), injected)
		_ = json.Unmarshal(encoded, &v)
		part = common.Map(common.Array(common.Map(common.Array(v["contents"])[0])["parts"])[0])
		if common.String(part["thoughtSignature"]) != "skip_thought_signature_validator" {
			t.Fatalf("Gemini injected call lacks documented sentinel: %s", encoded)
		}
	})

	t.Run("responses opaque reasoning", func(t *testing.T) {
		a := openairesponses.New()
		raw := json.RawMessage(`{"model":"m","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher","summary":[{"type":"summary_text","text":"summary"}]}]}`)
		canonical, _, err := a.DecodeRequest(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _, _ := a.EncodeRequest(context.Background(), canonical)
		var v map[string]any
		_ = json.Unmarshal(encoded, &v)
		item := common.Map(common.Array(v["input"])[0])
		if common.String(item["id"]) != "rs_1" || common.String(item["encrypted_content"]) != "cipher" {
			t.Fatalf("Responses opaque reasoning lost: %s", encoded)
		}
	})
}

func TestResponsesStreamSequenceAndDetailedUsage(t *testing.T) {
	a := openairesponses.New()
	chunks, _, err := a.EncodeStreamEvent(context.Background(), ir.Event{Type: "text.delta", Sequence: 7, ResponseID: "r", Delta: "x"})
	if err != nil || len(chunks) != 1 {
		t.Fatalf("encode: %v %#v", err, chunks)
	}
	var v map[string]any
	_ = json.Unmarshal(chunks[0].Data, &v)
	if common.Int(v["sequence_number"]) != 7 {
		t.Fatalf("sequence missing: %s", chunks[0].Data)
	}
	events, _, err := a.DecodeStreamEvent(context.Background(), "response.completed", json.RawMessage(`{"response":{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}}`))
	if err != nil || len(events) == 0 || events[0].Usage.CachedTokens != 3 || events[0].Usage.ReasoningTokens != 2 {
		t.Fatalf("detailed usage lost: %v %#v", err, events)
	}
}

func TestDocumentRefusalAndSourceProtocolCoverage(t *testing.T) {
	r := adapters()
	for source, raw := range requestSamples() {
		a, _ := r.Get(source)
		request, _, err := a.DecodeRequest(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		for mi, message := range request.Messages {
			for bi, block := range message.Content {
				if block.Source != source {
					t.Fatalf("%s message[%d].content[%d] source=%q", source, mi, bi, block.Source)
				}
			}
		}
	}

	documentRequest := &ir.Request{Model: "m", Messages: []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "document", MediaType: "application/pdf", Data: "cGRm", Filename: "report.pdf"}}}}}
	for _, target := range protocols() {
		a, _ := r.Get(target)
		raw, _, err := a.EncodeRequest(context.Background(), documentRequest)
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := a.DecodeRequest(context.Background(), raw)
		if err != nil {
			t.Fatalf("%s: %v\n%s", target, err, raw)
		}
		if !common.HasType(decoded, "document") {
			t.Fatalf("%s lost document: %s", target, raw)
		}
	}

	refusalResponse := &ir.Response{ID: "r", Model: "m", Messages: []ir.Message{{Role: "assistant", Content: []ir.ContentBlock{{Type: "refusal", Text: "cannot comply"}}}}, StopReason: "stop"}
	for _, target := range protocols() {
		a, _ := r.Get(target)
		raw, diagnostics, err := a.EncodeResponse(context.Background(), refusalResponse)
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := a.DecodeResponse(context.Background(), raw)
		if err != nil || len(decoded.Messages) == 0 || len(decoded.Messages[0].Content) == 0 {
			t.Fatalf("%s refusal response invalid: %v %s", target, err, raw)
		}
		if target == ir.Anthropic || target == ir.Gemini {
			if !common.IsLossy(diagnostics) || decoded.Messages[0].Content[0].Type != "text" {
				t.Fatalf("%s refusal approximation not diagnosed: %#v %s", target, diagnostics, raw)
			}
		} else {
			found := false
			for _, block := range decoded.Messages[0].Content {
				found = found || block.Type == "refusal"
			}
			if !found {
				t.Fatalf("%s refusal semantics lost: %s", target, raw)
			}
		}
	}
}

func adapters() *protocol.Registry {
	return protocol.NewRegistry(openaichat.New(), openairesponses.New(), anthropic.New(), gemini.New())
}
func protocols() []ir.Protocol {
	return []ir.Protocol{ir.OpenAIChat, ir.OpenAIResponses, ir.Anthropic, ir.Gemini}
}

func requestSamples() map[ir.Protocol]json.RawMessage {
	return map[ir.Protocol]json.RawMessage{
		ir.OpenAIChat:      json.RawMessage(`{"model":"test-model","messages":[{"role":"system","content":"Be concise."},{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}],"tools":[{"type":"function","function":{"name":"weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],"stream":true}`),
		ir.OpenAIResponses: json.RawMessage(`{"model":"test-model","instructions":"Be concise.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"https://example.com/a.png"}]}],"tools":[{"type":"function","name":"weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}],"stream":true}`),
		ir.Anthropic:       json.RawMessage(`{"model":"test-model","system":"Be concise.","messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}]}],"tools":[{"name":"weather","description":"Get weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],"max_tokens":256,"stream":true}`),
		ir.Gemini:          json.RawMessage(`{"model":"test-model","systemInstruction":{"parts":[{"text":"Be concise."}]},"contents":[{"role":"user","parts":[{"text":"hello"},{"fileData":{"fileUri":"https://example.com/a.png","mimeType":"image/png"}}]}],"tools":[{"functionDeclarations":[{"name":"weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]}],"stream":true}`),
	}
}

func responseSamples() map[ir.Protocol]json.RawMessage {
	return map[ir.Protocol]json.RawMessage{
		ir.OpenAIChat:      json.RawMessage(`{"id":"r1","model":"test-model","choices":[{"message":{"role":"assistant","content":"hello","tool_calls":[{"id":"c1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
		ir.OpenAIResponses: json.RawMessage(`{"id":"r1","model":"test-model","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},{"type":"function_call","call_id":"c1","name":"weather","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`),
		ir.Anthropic:       json.RawMessage(`{"id":"r1","model":"test-model","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"c1","name":"weather","input":{"city":"Paris"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`),
		ir.Gemini:          json.RawMessage(`{"modelVersion":"test-model","candidates":[{"content":{"role":"model","parts":[{"text":"hello"},{"functionCall":{"id":"c1","name":"weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`),
	}
}
