package provider

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
)

const (
	ProfileGeneric    = "generic"
	ProfileQwen3      = "qwen3"
	ProfileXiaomiMiMo = "xiaomi-mimo"
)

const DetectionProfileVersion = 1

type DetectionStrategy struct {
	Version              int
	Profile              string
	PreferredProtocols   []ir.Protocol
	ToolChoiceStrategies []string
	ToolChoiceMode       string
	ReasoningHistory     string
}

var detectionStrategies = map[string]DetectionStrategy{
	ProfileGeneric: {
		Version: DetectionProfileVersion, Profile: ProfileGeneric,
		PreferredProtocols:   []ir.Protocol{ir.OpenAIResponses, ir.OpenAIChat},
		ToolChoiceStrategies: []string{"forced", "required", "auto"},
		ToolChoiceMode:       "standard",
	},
	ProfileQwen3: {
		Version: DetectionProfileVersion, Profile: ProfileQwen3,
		PreferredProtocols:   []ir.Protocol{ir.OpenAIChat, ir.OpenAIResponses},
		ToolChoiceStrategies: []string{"forced", "required", "auto"},
		ToolChoiceMode:       "standard",
	},
	ProfileXiaomiMiMo: {
		Version: DetectionProfileVersion, Profile: ProfileXiaomiMiMo,
		PreferredProtocols:   []ir.Protocol{ir.OpenAIResponses, ir.OpenAIChat},
		ToolChoiceStrategies: []string{"auto", "auto"},
		ToolChoiceMode:       "auto-only", ReasoningHistory: "preserve",
	},
}

func ProviderDetectionStrategy(profile string) DetectionStrategy {
	strategy, ok := detectionStrategies[profile]
	if !ok {
		strategy = detectionStrategies[ProfileGeneric]
	}
	strategy.PreferredProtocols = append([]ir.Protocol(nil), strategy.PreferredProtocols...)
	strategy.ToolChoiceStrategies = append([]string(nil), strategy.ToolChoiceStrategies...)
	return strategy
}

func DetectProfile(baseURL string, models []string) string {
	value := strings.ToLower(baseURL + " " + strings.Join(models, " "))
	switch {
	case strings.Contains(value, "qwen"):
		return ProfileQwen3
	case strings.Contains(value, "mimo") || strings.Contains(value, "xiaomi"):
		return ProfileXiaomiMiMo
	default:
		return ProfileGeneric
	}
}

type Capabilities struct {
	NativeTokenCount bool `json:"native_token_count"`
}

func ProviderCapabilities(p config.Provider, _ string) Capabilities {
	return Capabilities{NativeTokenCount: p.Protocol == ir.Anthropic}
}

func EffectiveProfile(p config.Provider, model string) string {
	if p.Profile != "" && p.Profile != ProfileGeneric {
		return p.Profile
	}
	lowerModel := strings.ToLower(model)
	if strings.Contains(lowerModel, "qwen3") {
		return ProfileQwen3
	}
	if strings.Contains(strings.ToLower(p.BaseURL), "xiaomimimo.com") || strings.HasPrefix(lowerModel, "mimo-") {
		return ProfileXiaomiMiMo
	}
	return ProfileGeneric
}

// ApplyAuthenticationHeaders applies the provider's native upstream
// authentication scheme. Xiaomi's Anthropic-compatible endpoint intentionally
// uses Bearer/api-key authentication instead of Anthropic's x-api-key header.
func ApplyAuthenticationHeaders(header http.Header, p config.Provider) {
	switch p.Protocol {
	case ir.Anthropic:
		if EffectiveProfile(p, "") == ProfileXiaomiMiMo {
			header.Set("authorization", "Bearer "+p.APIKey)
			header.Del("x-api-key")
		} else {
			header.Set("x-api-key", p.APIKey)
			header.Del("authorization")
		}
	case ir.Gemini:
		header.Set("x-goog-api-key", p.APIKey)
	default:
		header.Set("authorization", "Bearer "+p.APIKey)
	}
}

// PrepareRequest applies non-overriding defaults and isolated wire quirks
// after a protocol adapter has produced valid JSON.
func PrepareRequest(raw []byte, p config.Provider, request *ir.Request) ([]byte, []ir.Diagnostic, error) {
	if len(p.RequestFields) == 0 && len(p.RequestPolicy.OmitFields) == 0 && p.CompatibilityMode == "" && p.ToolChoiceMode == "" && p.ReasoningHistory == "" && p.ReasoningWithTools == "" && EffectiveProfile(p, request.Model) == ProfileGeneric {
		return raw, nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, err
	}
	for key, value := range p.RequestFields {
		if _, exists := payload[key]; !exists {
			payload[key] = value
		}
	}
	if p.MaxOutputTokens > 0 {
		for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
			if value, ok := number(payload[key]); ok && value > p.MaxOutputTokens {
				payload[key] = p.MaxOutputTokens
			}
		}
	}
	switch EffectiveProfile(p, request.Model) {
	case ProfileQwen3:
		normalizeQwenSystemMessages(payload)
		if p.ReasoningMode == "disabled" {
			payload["enable_thinking"] = false
		} else if p.ReasoningMode == "enabled" {
			payload["enable_thinking"] = true
		} else if request.Sampling.ReasoningEnabled != nil {
			payload["enable_thinking"] = *request.Sampling.ReasoningEnabled
		} else if effort := strings.ToLower(request.Sampling.ReasoningEffort); effort != "" {
			payload["enable_thinking"] = effort != "none" && effort != "off" && effort != "disabled"
		}
	case ProfileXiaomiMiMo:
		if p.ReasoningMode == "disabled" || (p.ReasoningMode != "enabled" && request.Sampling.ReasoningEnabled != nil && !*request.Sampling.ReasoningEnabled) {
			payload["thinking"] = map[string]any{"type": "disabled"}
		}
	}
	var diagnostics []ir.Diagnostic
	toolsPresent := len(array(payload["tools"])) > 0
	if toolsPresent && p.ToolChoiceMode == "auto-only" {
		if p.Protocol == ir.Anthropic {
			payload["tool_choice"] = map[string]any{"type": "auto"}
		} else {
			payload["tool_choice"] = "auto"
		}
		diagnostics = append(diagnostics, ir.Diagnostic{Severity: "warning", Code: "tool_choice_auto_only", Path: "tool_choice", Message: "provider only supports automatic tool selection", Action: "rewritten"})
	}
	if toolsPresent && p.ReasoningWithTools == "disabled" {
		for _, field := range []string{"reasoning", "reasoning_effort"} {
			delete(payload, field)
		}
		payload["thinking"] = map[string]any{"type": "disabled"}
		diagnostics = append(diagnostics, ir.Diagnostic{Severity: "warning", Code: "reasoning_disabled_with_tools", Path: "reasoning", Message: "provider does not support reasoning together with tools", Action: "dropped"})
	}
	if p.ReasoningHistory == "drop" {
		dropReasoningHistory(payload)
		diagnostics = append(diagnostics, ir.Diagnostic{Severity: "warning", Code: "reasoning_history_dropped", Path: "messages", Message: "provider policy removed reasoning history", Action: "dropped"})
	}
	omitFields := append([]string(nil), p.RequestPolicy.OmitFields...)
	if p.CompatibilityMode == "codex-chat" {
		omitFields = append(omitFields, "reasoning_effort")
	}
	seen := map[string]bool{}
	for _, field := range omitFields {
		if seen[field] {
			continue
		}
		seen[field] = true
		if _, exists := payload[field]; !exists {
			continue
		}
		delete(payload, field)
		code := "request_field_omitted_by_policy"
		message := "provider request policy omitted an incompatible field"
		if p.CompatibilityMode == "codex-chat" && field == "reasoning_effort" {
			code = "codex_chat_compatibility_mode"
			message = "Codex Chat compatibility mode omitted reasoning_effort after Responses-to-Chat conversion"
		}
		diagnostics = append(diagnostics, ir.Diagnostic{
			Severity: "warning",
			Code:     code,
			Path:     field,
			Message:  message,
			Action:   "dropped",
		})
	}
	encoded, err := json.Marshal(payload)
	return encoded, diagnostics, err
}

func array(value any) []any {
	items, _ := value.([]any)
	return items
}

func dropReasoningHistory(payload map[string]any) {
	messages := array(payload["messages"])
	for _, rawMessage := range messages {
		if message, ok := rawMessage.(map[string]any); ok {
			delete(message, "reasoning_content")
			delete(message, "reasoning")
		}
	}
	input := array(payload["input"])
	filtered := input[:0]
	for _, item := range input {
		entry, _ := item.(map[string]any)
		if entry["type"] == "reasoning" {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(input) > 0 {
		payload["input"] = filtered
	}
}

func number(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func normalizeQwenSystemMessages(payload map[string]any) {
	messages, ok := payload["messages"].([]any)
	if !ok {
		return
	}
	var system []string
	others := make([]any, 0, len(messages))
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok || (message["role"] != "system" && message["role"] != "developer") {
			others = append(others, value)
			continue
		}
		system = append(system, textContent(message["content"])...)
	}
	if len(system) == 0 {
		payload["messages"] = others
		return
	}
	payload["messages"] = append([]any{map[string]any{"role": "system", "content": strings.Join(system, "\n\n")}}, others...)
}

func textContent(value any) []string {
	switch content := value.(type) {
	case string:
		return []string{content}
	case []any:
		var out []string
		for _, partValue := range content {
			part, ok := partValue.(map[string]any)
			if ok {
				if text, ok := part["text"].(string); ok && text != "" {
					out = append(out, text)
				}
			}
		}
		return out
	default:
		return nil
	}
}
