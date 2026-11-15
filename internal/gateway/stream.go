package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/soooooollee/ai-router/internal/observe"
	"github.com/soooooollee/ai-router/internal/protocol"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
	"github.com/soooooollee/ai-router/internal/streaming"
)

func (g *Gateway) stream(w http.ResponseWriter, ctx context.Context, resp *http.Response, dec *streaming.Decoder, prefetched []streaming.Event, clientProtocol, providerProtocol ir.Protocol, model, id string, customTools map[string]bool, captureBody bool, record *observe.Record) {
	defer resp.Body.Close()
	decoderAdapter, _ := g.Registry.Get(providerProtocol)
	encoderAdapter, _ := g.Registry.Get(clientProtocol)
	// Some client protocols must buffer (Gemini only accepts a complete
	// function call), so encoding goes through a per-response encoder.
	clientEncoder := protocol.NewStreamEncoder(encoderAdapter)
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	if dec == nil {
		dec = streaming.NewDecoder(resp.Body, 8<<20)
	}
	first := true
	responseID := id
	sequence := 0
	normalizer := ir.NewStreamNormalizer()
	customToolIndexes := map[int]bool{}
	var lastUsage *ir.Usage
	capturedEvents := make([]ir.Event, 0, 64)
	capturedBytes := 0
	captureTruncated := false
	if captureBody {
		defer func() {
			payload, _ := json.Marshal(map[string]any{"stream": true, "truncated": captureTruncated, "events": capturedEvents})
			record.ResponseBody = string(payload)
		}()
	}
	for eventIndex := 0; ; eventIndex++ {
		var se streaming.Event
		var err error
		if eventIndex < len(prefetched) {
			se = prefetched[eventIndex]
		} else {
			se, err = dec.Next()
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				g.failStream(w, flusher, encoderAdapter, ctx, responseID, record, "upstream_stream_error", "upstream_unavailable", err.Error())
			}
			return
		}
		events, d, err := decoderAdapter.DecodeStreamEvent(ctx, se.Name, se.Data)
		record.Diagnostics = append(record.Diagnostics, d...)
		if err != nil {
			g.failStream(w, flusher, encoderAdapter, ctx, responseID, record, "protocol_conversion_error", "protocol_conversion_error", err.Error())
			return
		}
		for _, event := range events {
			if event.Type == "tool_call.start" && event.Block != nil && customTools[event.Block.Name] {
				event.Block.SourceType = "custom"
				customToolIndexes[event.Index] = true
			}
			if event.Type == "tool_call.arguments.delta" && customToolIndexes[event.Index] {
				event.Block = &ir.ContentBlock{Type: "tool_call", SourceType: "custom"}
			}
			if event.Model == "" {
				event.Model = model
			}
			if event.ResponseID != "" {
				responseID = event.ResponseID
			} else {
				event.ResponseID = responseID
			}
			if first && (event.Type == "text.delta" || event.Type == "reasoning.delta" || event.Type == "tool_call.start") {
				first = false
				record.FirstTokenMS = time.Since(record.StartedAt).Milliseconds()
			}
			if event.Usage != nil {
				if lastUsage == nil {
					lastUsage = &ir.Usage{}
				}
				mergeStreamUsage(lastUsage, *event.Usage)
				record.Usage = *lastUsage
			}
			for _, normalized := range normalizer.Push(event) {
				sequence++
				normalized.Sequence = sequence
				if captureBody {
					eventSize := len(normalized.Delta) + len(normalized.Arguments) + len(normalized.Raw) + 128
					if capturedBytes+eventSize <= 1<<20 {
						capturedEvents = append(capturedEvents, normalized)
						capturedBytes += eventSize
					} else {
						captureTruncated = true
					}
				}
				if normalized.Type == "response.end" && normalized.Usage == nil {
					normalized.Usage = lastUsage
				}
				chunks, d, err := clientEncoder.EncodeStreamEvent(ctx, normalized)
				record.Diagnostics = append(record.Diagnostics, d...)
				if err != nil {
					g.failStream(w, flusher, encoderAdapter, ctx, responseID, record, "protocol_conversion_error", "protocol_conversion_error", err.Error())
					return
				}
				for _, chunk := range chunks {
					if err := streaming.Write(w, chunk.Event, chunk.Data); err != nil {
						return
					}
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		}
	}
}

func mergeStreamUsage(total *ir.Usage, next ir.Usage) {
	if total == nil {
		return
	}
	total.InputTokens = max(total.InputTokens, next.InputTokens)
	total.OutputTokens = max(total.OutputTokens, next.OutputTokens)
	total.CachedTokens = max(total.CachedTokens, next.CachedTokens)
	total.ReasoningTokens = max(total.ReasoningTokens, next.ReasoningTokens)
	total.TotalTokens = max(total.TotalTokens, next.TotalTokens)
	total.TotalTokens = max(total.TotalTokens, total.InputTokens+total.OutputTokens)
}

func markCustomToolCalls(response *ir.Response, customTools map[string]bool) {
	if response == nil || len(customTools) == 0 {
		return
	}
	for messageIndex := range response.Messages {
		for blockIndex := range response.Messages[messageIndex].Content {
			block := &response.Messages[messageIndex].Content[blockIndex]
			if block.Type == "tool_call" && customTools[block.Name] {
				block.SourceType = "custom"
			}
		}
	}
}

// failStream closes out a stream that already sent its 200 header. The client
// status cannot change any more, but a truncated stream must not be recorded as
// a success, and the client needs an explicit error event instead of an abrupt
// end of data.
func (g *Gateway) failStream(w io.Writer, flusher http.Flusher, adapter protocol.Adapter, ctx context.Context, responseID string, record *observe.Record, code, errType, message string) {
	record.Status = http.StatusBadGateway
	record.ErrorCode = code
	g.Metrics.Errors.Add(1)
	writeEncodedStreamError(w, flusher, adapter, ctx, responseID, errType, message)
}

func writeEncodedStreamError(w io.Writer, flusher http.Flusher, adapter protocol.Adapter, ctx context.Context, responseID, typ, message string) {
	chunks, _, err := adapter.EncodeStreamEvent(ctx, ir.Event{Type: "error", ResponseID: responseID, Error: &ir.Error{Type: typ, Message: redactSnippet([]byte(message))}})
	if err != nil {
		return
	}
	for _, chunk := range chunks {
		if streaming.Write(w, chunk.Event, chunk.Data) != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
