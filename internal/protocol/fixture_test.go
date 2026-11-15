package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/ir"
)

// updateGolden rewrites tests/fixtures/golden from the current adapters:
//
//	go test ./internal/protocol/ -run TestCheckedInProtocolFixtures -update
//
// Always review the resulting diff: it is the protocol contract changing.
var updateGolden = flag.Bool("update", false, "rewrite protocol golden files")

type protocolFixture struct {
	Protocol ir.Protocol     `json:"protocol"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
	Stream   []struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	} `json:"stream"`
}

// goldenFile records both the encoded output and the diagnostics the adapter
// reported, so a conversion that silently starts dropping fields fails the test
// even when the remaining JSON still looks plausible.
type goldenFile struct {
	Output      json.RawMessage `json:"output"`
	Diagnostics []ir.Diagnostic `json:"diagnostics"`
}

const goldenArtifactsPerFixture = 3

var fixtureProtocols = []ir.Protocol{ir.OpenAIChat, ir.OpenAIResponses, ir.Anthropic, ir.Gemini}

func TestCheckedInProtocolFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "tests", "fixtures", "*.json"))
	if err != nil || len(files) != 4 {
		t.Fatalf("fixture inventory: %v %v", files, err)
	}
	goldenDir := filepath.Join("..", "..", "tests", "fixtures", "golden")
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	registry := adapters()
	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var fixture protocolFixture
			if err = json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			adapter, err := registry.Get(fixture.Protocol)
			if err != nil {
				t.Fatal(err)
			}
			request, requestDiagnostics, err := adapter.DecodeRequest(context.Background(), fixture.Request)
			if err != nil {
				t.Fatalf("decode request: %v", err)
			}
			encodedRequest, encodeDiagnostics, err := adapter.EncodeRequest(context.Background(), request)
			if err != nil || !json.Valid(encodedRequest) {
				t.Fatalf("request golden invalid: %v %s", err, encodedRequest)
			}
			compareGolden(t, goldenDir, name+".request.json", encodedRequest, append(requestDiagnostics, encodeDiagnostics...))

			response, responseDiagnostics, err := adapter.DecodeResponse(context.Background(), fixture.Response)
			if err != nil {
				t.Fatalf("decode response: %v", err)
			}
			encodedResponse, responseEncodeDiagnostics, err := adapter.EncodeResponse(context.Background(), response)
			if err != nil || !json.Valid(encodedResponse) {
				t.Fatalf("response golden invalid: %v %s", err, encodedResponse)
			}
			compareGolden(t, goldenDir, name+".response.json", encodedResponse, append(responseDiagnostics, responseEncodeDiagnostics...))

			normalizer := ir.NewStreamNormalizer()
			normalized := make([]ir.Event, 0, 32)
			decodedEvents := 0
			for _, chunk := range fixture.Stream {
				data := chunk.Data
				if string(data) == `"[DONE]"` {
					data = json.RawMessage(`[DONE]`)
				}
				events, _, err := adapter.DecodeStreamEvent(context.Background(), chunk.Event, data)
				if err != nil {
					t.Fatalf("decode stream: %v data=%s", err, data)
				}
				decodedEvents += len(events)
				for _, event := range events {
					normalized = append(normalized, normalizer.Push(event)...)
				}
			}
			if decodedEvents == 0 {
				t.Fatal("stream fixture produced no canonical events")
			}
			// The normalized event sequence is the contract every client protocol
			// has to encode, so a regression here breaks streaming silently.
			streamGolden, err := json.Marshal(normalized)
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, goldenDir, name+".stream.json", streamGolden, nil)

			// The router's real contract is cross-protocol conversion, so every
			// source fixture is also encoded into every target protocol.
			for _, target := range fixtureProtocols {
				targetAdapter, targetErr := registry.Get(target)
				if targetErr != nil {
					t.Fatal(targetErr)
				}
				decoded, sourceDiagnostics, decodeErr := adapter.DecodeRequest(context.Background(), fixture.Request)
				if decodeErr != nil {
					t.Fatalf("decode request for %s: %v", target, decodeErr)
				}
				encoded, encodeDiagnostics, encodeErr := targetAdapter.EncodeRequest(context.Background(), decoded)
				if encodeErr != nil || !json.Valid(encoded) {
					t.Fatalf("cross-protocol golden %s -> %s invalid: %v %s", fixture.Protocol, target, encodeErr, encoded)
				}
				compareGolden(t, goldenDir,
					fmt.Sprintf("cross/%s-to-%s.request.json", fixture.Protocol, target),
					encoded, append(sourceDiagnostics, encodeDiagnostics...))

				// Streaming is the part of the contract a client notices first, and
				// some targets (Gemini) need per-stream buffering to encode at all.
				encoder := protocol.NewStreamEncoder(targetAdapter)
				chunks := make([]map[string]any, 0, 16)
				var streamDiagnostics []ir.Diagnostic
				for _, event := range normalized {
					encodedChunks, d, streamErr := encoder.EncodeStreamEvent(context.Background(), event)
					streamDiagnostics = append(streamDiagnostics, d...)
					if streamErr != nil {
						t.Fatalf("stream %s -> %s: %v", fixture.Protocol, target, streamErr)
					}
					for _, chunk := range encodedChunks {
						chunks = append(chunks, map[string]any{"event": chunk.Event, "data": chunkData(chunk.Data)})
					}
				}
				streamPayload, err := json.Marshal(map[string]any{"chunks": chunks})
				if err != nil {
					t.Fatal(err)
				}
				compareGolden(t, goldenDir,
					fmt.Sprintf("cross/%s-to-%s.stream.json", fixture.Protocol, target),
					streamPayload, streamDiagnostics)
			}
		})
	}
	if !*updateGolden {
		entries, err := os.ReadDir(goldenDir)
		if err != nil {
			t.Fatalf("read golden directory (run go test ./internal/protocol/ -run TestCheckedInProtocolFixtures -update): %v", err)
		}
		crossEntries, err := os.ReadDir(filepath.Join(goldenDir, "cross"))
		if err != nil {
			t.Fatalf("read cross-protocol goldens: %v", err)
		}
		// entries also counts the cross/ directory itself.
		if topLevel := len(entries) - 1; topLevel != len(files)*goldenArtifactsPerFixture || len(crossEntries) != 2*len(files)*len(fixtureProtocols) {
			t.Fatalf("golden inventory: %d top-level + %d cross files, want %d + %d",
				topLevel, len(crossEntries), len(files)*goldenArtifactsPerFixture, len(files)*len(fixtureProtocols))
		}
	}
}

// compareGolden fails on any semantic difference and rewrites the file with
// -update. Comparison is structural, so formatting churn cannot hide a change.
func compareGolden(t *testing.T, dir, name string, output []byte, diagnostics []ir.Diagnostic) {
	t.Helper()
	raw, err := json.Marshal(goldenFile{Output: output, Diagnostics: diagnostics})
	if err != nil {
		t.Fatalf("encode golden %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		var pretty bytes.Buffer
		if err = json.Indent(&pretty, raw, "", "  "); err != nil {
			t.Fatalf("indent %s: %v", name, err)
		}
		if err = os.WriteFile(path, append(pretty.Bytes(), '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s: %v", name, err)
	}
	var wantValue, gotValue any
	if err = json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("golden %s is not valid JSON: %v", name, err)
	}
	if err = json.Unmarshal(raw, &gotValue); err != nil {
		t.Fatalf("output for %s is not valid JSON: %v", name, err)
	}
	if !reflect.DeepEqual(normalizeGolden(wantValue), normalizeGolden(gotValue)) {
		t.Fatalf("golden mismatch for %s (rerun with -update after reviewing the change)\nwant: %s\ngot:  %s", name, want, raw)
	}
}

// normalizeGolden blanks the few values an encoder derives from the wall clock,
// so a golden stays stable without weakening the comparison of everything else.
// No other field is exempt: any change to ids, ordering, or payloads still
// fails the test.
func normalizeGolden(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "created" {
				if _, isNumber := child.(float64); isNumber {
					current[key] = 0
					continue
				}
			}
			current[key] = normalizeGolden(child)
		}
		return current
	case []any:
		for index, child := range current {
			current[index] = normalizeGolden(child)
		}
		return current
	default:
		return value
	}
}

// chunkData keeps SSE payloads readable: JSON stays JSON, sentinels such as
// [DONE] are stored as strings.
func chunkData(data []byte) any {
	if json.Valid(data) {
		return json.RawMessage(data)
	}
	return string(data)
}
