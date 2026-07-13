package provider

import (
	"encoding/json"
	"strings"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
)

const (
	ProfileGeneric    = "generic"
	ProfileQwen3      = "qwen3"
	ProfileXiaomiMiMo = "xiaomi-mimo"
)

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

// PrepareRequest applies non-overriding defaults and isolated wire quirks
// after a protocol adapter has produced valid JSON.
func PrepareRequest(raw []byte, p config.Provider, request *ir.Request) ([]byte, error) {
	if len(p.RequestFields) == 0 && EffectiveProfile(p, request.Model) == ProfileGeneric {
		return raw, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
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
	return json.Marshal(payload)
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
