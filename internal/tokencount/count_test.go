package tokencount

import (
	"encoding/json"
	"testing"

	"github.com/zbss/airoute/internal/protocol/ir"
)

func TestHeuristicCountsLanguageCodeToolsAndMedia(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"recursive":{"type":"boolean"}}}`)
	request := &ir.Request{
		Instructions: []ir.ContentBlock{{Type: "text", Text: "你是一个 coding assistant."}},
		Messages: []ir.Message{{Role: "user", Content: []ir.ContentBlock{
			{Type: "text", Text: "分析下面的 Go 代码：func main() { fmt.Println(\"hello\") }"},
			{Type: "image", URL: "https://example.com/image.png"},
		}}},
		Tools: []ir.Tool{{Name: "read_file", Description: "Read a file", InputSchema: schema}},
	}
	result := (Heuristic{}).Count(request)
	if !result.Estimated || result.Strategy == "" || result.InputTokens < 100 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Breakdown.Instructions == 0 || result.Breakdown.Messages == 0 || result.Breakdown.Tools == 0 || result.Breakdown.Media != 85 {
		t.Fatalf("missing component count: %#v", result.Breakdown)
	}
}

func TestChineseDoesNotUseGlobalCharactersDividedByFour(t *testing.T) {
	request := &ir.Request{Messages: []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: "这是一个包含中文上下文的令牌计数测试"}}}}}
	result := (Heuristic{}).Count(request)
	if result.InputTokens < 18 {
		t.Fatalf("Chinese was severely undercounted: %#v", result)
	}
}
