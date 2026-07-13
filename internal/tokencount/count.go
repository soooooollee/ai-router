package tokencount

import (
	"unicode"
	"unicode/utf8"

	"github.com/zbss/airoute/internal/protocol/ir"
)

type Breakdown struct {
	Instructions int `json:"instructions"`
	Messages     int `json:"messages"`
	Tools        int `json:"tools"`
	Media        int `json:"media"`
}

type Result struct {
	InputTokens int       `json:"input_tokens"`
	Estimated   bool      `json:"estimated"`
	Strategy    string    `json:"strategy"`
	Breakdown   Breakdown `json:"breakdown,omitempty"`
}

type Counter interface {
	Count(*ir.Request) Result
}

type Heuristic struct{}

func (Heuristic) Count(request *ir.Request) Result {
	breakdown := Breakdown{}
	for _, block := range request.Instructions {
		breakdown.Instructions += blockTokens(block)
	}
	for _, message := range request.Messages {
		breakdown.Messages += 4 + textTokens(message.Role) + textTokens(message.Name)
		for _, block := range message.Content {
			if block.Type == "image" || block.URL != "" || block.Data != "" || block.FileID != "" {
				breakdown.Media += 85
			}
			breakdown.Messages += blockTokens(block)
		}
	}
	for _, tool := range request.Tools {
		breakdown.Tools += 12 + textTokens(tool.Name) + textTokens(tool.Description) + textTokens(string(tool.InputSchema))
	}
	count := breakdown.Instructions + breakdown.Messages + breakdown.Tools + breakdown.Media
	if count < 1 {
		count = 1
	}
	return Result{InputTokens: count, Estimated: true, Strategy: "unicode-lexical-v1", Breakdown: breakdown}
}

func blockTokens(block ir.ContentBlock) int {
	return textTokens(block.Text) + textTokens(block.Name) + textTokens(string(block.Arguments)) + textTokens(string(block.Result))
}

// textTokens is a deterministic tokenizer fallback. CJK characters and
// punctuation are counted individually; contiguous Latin/digit runs use a
// four-character approximation. This avoids the severe Chinese undercount of
// a global character/4 rule while remaining dependency-free.
func textTokens(value string) int {
	tokens, latinRun := 0, 0
	flushLatin := func() {
		if latinRun > 0 {
			tokens += (latinRun + 3) / 4
			latinRun = 0
		}
	}
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			flushLatin()
			tokens++
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			latinRun++
		case unicode.IsSpace(r):
			flushLatin()
		default:
			flushLatin()
			tokens++
		}
	}
	flushLatin()
	return tokens
}
