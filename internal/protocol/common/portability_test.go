package common_test

import (
	"testing"

	"github.com/soooooollee/ai-router/internal/protocol/common"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
)

var allProtocols = []ir.Protocol{ir.OpenAIChat, ir.OpenAIResponses, ir.Anthropic, ir.Gemini}

func samplingRequest(source ir.Protocol, s ir.SamplingOptions) *ir.Request {
	return &ir.Request{
		Source:   source,
		Model:    "m",
		Sampling: s,
		Messages: []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: "hi", Source: source}}}},
	}
}

func intPtr(v int) *int { return &v }

func codes(diagnostics []ir.Diagnostic) map[string]ir.Diagnostic {
	out := map[string]ir.Diagnostic{}
	for _, d := range diagnostics {
		out[d.Code] = d
	}
	return out
}

// wants returns the sampling controls a target protocol cannot express.
func wants(target ir.Protocol) []string {
	switch target {
	case ir.OpenAIChat, ir.OpenAIResponses:
		return []string{"top_k_not_portable", "reasoning_budget_not_portable"}
	case ir.Anthropic:
		return []string{"reasoning_effort_not_portable"}
	default:
		return []string{"reasoning_effort_not_portable", "reasoning_budget_not_portable"}
	}
}

// Every control is dropped somewhere, so each conversion must report exactly
// the controls the target cannot express. Missing entries are silent loss.
func TestSamplingDiagnosticsReportEveryDroppedControl(t *testing.T) {
	for _, source := range allProtocols {
		for _, target := range allProtocols {
			if source == target {
				continue
			}
			r := samplingRequest(source, ir.SamplingOptions{TopK: intPtr(40), ReasoningEffort: "high", ReasoningBudget: 8192})
			d := common.SamplingPortabilityDiagnostics(r, target)
			got := codes(d)
			expected := map[string]bool{}
			for _, code := range wants(target) {
				expected[code] = true
				if _, ok := got[code]; !ok {
					t.Fatalf("%s -> %s: missing diagnostic %q in %#v", source, target, code, d)
				}
			}
			if len(got) != len(expected) {
				t.Fatalf("%s -> %s: reported unexpected diagnostics: %#v", source, target, d)
			}
			for _, d := range d {
				if d.Severity != "warning" || d.Path == "" || d.Action == "" {
					t.Fatalf("%s -> %s: diagnostic must be actionable and addressable: %#v", source, target, d)
				}
			}
			if !common.IsLossy(d) {
				t.Fatalf("%s -> %s: dropped sampling controls must count as loss: %#v", source, target, d)
			}
		}
	}
	if d := codes(common.SamplingPortabilityDiagnostics(samplingRequest(ir.Gemini, ir.SamplingOptions{TopK: intPtr(40)}), ir.OpenAIChat)); d["top_k_not_portable"].Action != "dropped" || d["top_k_not_portable"].Path != "sampling.top_k" {
		t.Fatalf("diagnostics must point at the offending field: %#v", d["top_k_not_portable"])
	}
}

func TestSamplingDiagnosticsSilentForNativeConversion(t *testing.T) {
	for _, p := range allProtocols {
		r := samplingRequest(p, ir.SamplingOptions{TopK: intPtr(40), ReasoningEffort: "high", ReasoningBudget: 8192})
		if d := common.SamplingPortabilityDiagnostics(r, p); len(d) != 0 {
			t.Fatalf("native %s conversion must not report sampling loss: %#v", p, d)
		}
	}
}

func TestSamplingDiagnosticsSilentWithoutControls(t *testing.T) {
	r := samplingRequest(ir.OpenAIChat, ir.SamplingOptions{})
	if d := common.SamplingPortabilityDiagnostics(r, ir.Gemini); len(d) != 0 {
		t.Fatalf("request without sampling controls must stay silent: %#v", d)
	}
}

func TestRequestPortabilityDiagnosticsIncludesSampling(t *testing.T) {
	r := samplingRequest(ir.OpenAIChat, ir.SamplingOptions{ReasoningEffort: "high"})
	d := common.RequestPortabilityDiagnostics(r, ir.Anthropic)
	if _, ok := codes(d)["reasoning_effort_not_portable"]; !ok {
		t.Fatalf("request-level portability diagnostics must include sampling loss: %#v", d)
	}
}
