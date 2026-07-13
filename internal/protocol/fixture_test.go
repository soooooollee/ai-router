package protocol_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zbss/airoute/internal/protocol/ir"
)

type protocolFixture struct {
	Protocol ir.Protocol     `json:"protocol"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
	Stream   []struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	} `json:"stream"`
}

func TestCheckedInProtocolFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "tests", "fixtures", "*.json"))
	if err != nil || len(files) != 4 {
		t.Fatalf("fixture inventory: %v %v", files, err)
	}
	registry := adapters()
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
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
			request, _, err := adapter.DecodeRequest(context.Background(), fixture.Request)
			if err != nil {
				t.Fatalf("decode request: %v", err)
			}
			encodedRequest, _, err := adapter.EncodeRequest(context.Background(), request)
			if err != nil || !json.Valid(encodedRequest) {
				t.Fatalf("request golden invalid: %v %s", err, encodedRequest)
			}
			response, _, err := adapter.DecodeResponse(context.Background(), fixture.Response)
			if err != nil {
				t.Fatalf("decode response: %v", err)
			}
			encodedResponse, _, err := adapter.EncodeResponse(context.Background(), response)
			if err != nil || !json.Valid(encodedResponse) {
				t.Fatalf("response golden invalid: %v %s", err, encodedResponse)
			}
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
			}
			if decodedEvents == 0 {
				t.Fatal("stream fixture produced no canonical events")
			}
		})
	}
}
