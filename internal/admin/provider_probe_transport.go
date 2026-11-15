package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
	providerprofile "github.com/zbss/airoute/internal/provider"
)

func (s *Server) doProviderProbeRequest(ctx context.Context, p config.Provider, endpoint string, payload []byte, accept string) (int, string, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, "", nil, err
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("accept", accept)
	applyProviderProbeHeaders(request, p)
	client := s.RestrictedClient
	if p.AllowPrivateURL {
		client = s.Client
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", nil, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 512<<10))
	return response.StatusCode, response.Header.Get("content-type"), body, readErr
}

func errorCodeForRequest(ctx context.Context, err error) string {
	if errorsIsTimeout(ctx, err) {
		return "timeout"
	}
	if ctx.Err() == context.Canceled {
		return "canceled"
	}
	return "network_error"
}

func validateProviderJSON(protocolName ir.Protocol, body []byte) (string, string) {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return "schema_mismatch", "provider returned HTTP 2xx with a non-JSON response"
	}
	switch protocolName {
	case ir.OpenAIChat:
		if _, ok := value["choices"].([]any); !ok {
			return "schema_mismatch", "OpenAI Chat response is missing choices"
		}
	case ir.OpenAIResponses:
		if _, ok := value["output"].([]any); !ok {
			return "schema_mismatch", "OpenAI Responses response is missing output"
		}
	case ir.Anthropic:
		if _, ok := value["content"].([]any); !ok {
			return "schema_mismatch", "Anthropic response is missing content"
		}
	case ir.Gemini:
		if _, ok := value["candidates"].([]any); !ok {
			return "schema_mismatch", "Gemini response is missing candidates"
		}
	}
	return "", ""
}

func validateProviderSSE(protocolName ir.Protocol, contentType string, body []byte) (string, string) {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return "sse_incomplete", "provider returned a non-SSE response to a streaming request"
	}
	lines := strings.Split(string(body), "\n")
	validJSONEvent := false
	completed := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			completed = true
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(data), &value) != nil {
			continue
		}
		validJSONEvent = true
		if protocolName == ir.OpenAIResponses {
			typeName, _ := value["type"].(string)
			if typeName == "response.completed" || typeName == "response.done" || typeName == "response.failed" {
				completed = true
			}
		}
	}
	if !validJSONEvent {
		return "sse_incomplete", "SSE stream did not contain a valid protocol event"
	}
	if !completed && protocolName == ir.OpenAIChat {
		return "sse_incomplete", "OpenAI Chat SSE stream ended without [DONE]"
	}
	if !completed && protocolName == ir.OpenAIResponses {
		return "sse_incomplete", "OpenAI Responses SSE stream ended without a completion event"
	}
	return "", ""
}

func providerReasoningObserved(protocolName ir.Protocol, body []byte) bool {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	switch protocolName {
	case ir.OpenAIChat:
		for _, rawChoice := range common.Array(value["choices"]) {
			message := common.Map(common.Map(rawChoice)["message"])
			if strings.TrimSpace(common.String(message["reasoning_content"])) != "" || message["reasoning"] != nil {
				return true
			}
		}
	case ir.OpenAIResponses:
		for _, rawOutput := range common.Array(value["output"]) {
			if common.String(common.Map(rawOutput)["type"]) == "reasoning" {
				return true
			}
		}
	}
	return false
}

func providerRoundTripRequest(p config.Provider, firstBody []byte, profile string) (string, string, []byte, error) {
	var value map[string]any
	if json.Unmarshal(firstBody, &value) != nil {
		return "", "", nil, fmt.Errorf("first tool response was not valid JSON")
	}
	base := strings.TrimRight(p.BaseURL, "/")
	model := ""
	if len(p.Models) > 0 {
		model = p.Models[0]
	}
	switch p.Protocol {
	case ir.OpenAIChat:
		choices := common.Array(value["choices"])
		if len(choices) == 0 {
			return "", "", nil, fmt.Errorf("tool response did not contain a Chat choice")
		}
		assistant := common.Map(common.Map(choices[0])["message"])
		toolCalls := common.Array(assistant["tool_calls"])
		if len(toolCalls) == 0 {
			return "", "", nil, fmt.Errorf("tool response did not contain a Chat tool call")
		}
		callID := common.String(common.Map(toolCalls[0])["id"])
		payload := map[string]any{
			"model": model, "max_completion_tokens": 256,
			"messages": []any{
				map[string]any{"role": "user", "content": "Use get_weather for Beijing, then summarize the result."},
				assistant,
				map[string]any{"role": "tool", "tool_call_id": callID, "content": `{"city":"Beijing","temperature_c":22}`},
			},
		}
		if profile == "xiaomi-mimo" {
			payload["thinking"] = map[string]any{"type": "enabled"}
		}
		return http.MethodPost, appendProbeEndpoint(base, "/chat/completions"), common.Raw(payload), nil
	case ir.OpenAIResponses:
		output := common.Array(value["output"])
		if len(output) == 0 {
			return "", "", nil, fmt.Errorf("tool response did not contain Responses output")
		}
		callID := ""
		for _, item := range output {
			entry := common.Map(item)
			if common.String(entry["type"]) == "function_call" {
				callID = common.String(entry["call_id"])
				break
			}
		}
		if callID == "" {
			return "", "", nil, fmt.Errorf("tool response did not contain a Responses function call")
		}
		input := append([]any{}, output...)
		input = append(input, map[string]any{"type": "function_call_output", "call_id": callID, "output": `{"city":"Beijing","temperature_c":22}`})
		payload := map[string]any{"model": model, "max_output_tokens": 256, "input": input}
		if profile == "xiaomi-mimo" {
			payload["thinking"] = map[string]any{"type": "enabled"}
		}
		return http.MethodPost, appendProbeEndpoint(base, "/responses"), common.Raw(payload), nil
	default:
		return "", "", nil, fmt.Errorf("round trip is not implemented for %s", p.Protocol)
	}
}

func providerToolCallObserved(protocolName ir.Protocol, body []byte) bool {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	switch protocolName {
	case ir.OpenAIChat:
		choices, _ := value["choices"].([]any)
		for _, item := range choices {
			choice, _ := item.(map[string]any)
			message, _ := choice["message"].(map[string]any)
			toolCalls, _ := message["tool_calls"].([]any)
			if len(toolCalls) > 0 {
				return true
			}
		}
	case ir.OpenAIResponses:
		output, _ := value["output"].([]any)
		for _, item := range output {
			entry, _ := item.(map[string]any)
			if entry["type"] == "function_call" {
				return true
			}
		}
	}
	return false
}

func providerProbeRequest(p config.Provider, kind providerProbeKind) (string, string, []byte, error) {
	strategy := toolChoiceForced
	if p.Profile == "xiaomi-mimo" {
		strategy = toolChoiceAuto
	}
	return providerProbeRequestWithStrategy(p, kind, strategy)
}

func providerProbeRequestWithStrategy(p config.Provider, kind providerProbeKind, strategy toolChoiceStrategy) (string, string, []byte, error) {
	if kind == probeModels {
		return http.MethodGet, providerModelsURL(p), nil, nil
	}
	model := ""
	if len(p.Models) > 0 {
		model = p.Models[0]
	}
	base := strings.TrimRight(p.BaseURL, "/")
	probeTool := map[string]any{
		"name":        "airoute_probe",
		"description": "Look up weather for a city. Always call this tool when asked for weather.",
		"parameters": map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string", "description": "City name"}},
			"required":   []any{"city"}, "additionalProperties": false,
		},
	}
	var endpoint string
	var payload map[string]any
	switch p.Protocol {
	case ir.OpenAIChat:
		endpoint = appendProbeEndpoint(base, "/chat/completions")
		payload = map[string]any{
			"model": model, "max_completion_tokens": 256,
			"messages": []any{map[string]any{"role": "user", "content": "Reply OK"}},
		}
		if kind == probeStreaming {
			payload["stream"] = true
		}
		if kind == probeTools || kind == probeToolsWithReasoning {
			payload["messages"] = []any{map[string]any{"role": "user", "content": "Use airoute_probe to look up the weather in Beijing. Do not answer without calling the tool."}}
			payload["tools"] = []any{map[string]any{"type": "function", "function": probeTool}}
			payload["tool_choice"] = chatToolChoice(strategy)
			if p.Profile == "xiaomi-mimo" && kind == probeTools {
				payload["thinking"] = map[string]any{"type": "disabled"}
			}
		}
		if kind == probeReasoning || kind == probeToolsWithReasoning {
			payload["reasoning_effort"] = "low"
			if p.Profile == "xiaomi-mimo" {
				delete(payload, "reasoning_effort")
				payload["thinking"] = map[string]any{"type": "enabled"}
			}
		}
	case ir.OpenAIResponses:
		endpoint = appendProbeEndpoint(base, "/responses")
		payload = map[string]any{"model": model, "max_output_tokens": 256, "input": "Reply OK"}
		if kind == probeStreaming {
			payload["stream"] = true
		}
		if kind == probeTools || kind == probeToolsWithReasoning {
			payload["input"] = "Use airoute_probe to look up the weather in Beijing. Do not answer without calling the tool."
			payload["tools"] = []any{map[string]any{"type": "function", "name": probeTool["name"], "description": probeTool["description"], "parameters": probeTool["parameters"]}}
			payload["tool_choice"] = responsesToolChoice(strategy)
			if p.Profile == "xiaomi-mimo" && kind == probeTools {
				payload["thinking"] = map[string]any{"type": "disabled"}
			}
		}
		if kind == probeReasoning || kind == probeToolsWithReasoning {
			payload["reasoning"] = map[string]any{"effort": "low"}
			if p.Profile == "xiaomi-mimo" {
				delete(payload, "reasoning")
				payload["thinking"] = map[string]any{"type": "enabled"}
			}
		}
	case ir.Anthropic:
		endpoint = appendProbeEndpoint(base, "/messages")
		payload = map[string]any{"model": model, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "Reply OK"}}}
	case ir.Gemini:
		if !strings.Contains(base, "/v1beta") {
			base += "/v1beta"
		}
		endpoint = base + "/models/" + url.PathEscape(model) + ":generateContent"
		payload = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Reply OK"}}}}}
	default:
		return "", "", nil, fmt.Errorf("unsupported provider protocol %q", p.Protocol)
	}
	return http.MethodPost, endpoint, common.Raw(payload), nil
}

func chatToolChoice(strategy toolChoiceStrategy) any {
	switch strategy {
	case toolChoiceAuto:
		return "auto"
	case toolChoiceRequired:
		return "required"
	default:
		return map[string]any{"type": "function", "function": map[string]any{"name": "airoute_probe"}}
	}
}

func responsesToolChoice(strategy toolChoiceStrategy) any {
	switch strategy {
	case toolChoiceAuto:
		return "auto"
	case toolChoiceRequired:
		return "required"
	default:
		return map[string]any{"type": "function", "name": "airoute_probe"}
	}
}

func appendProbeEndpoint(base, suffix string) string {
	if strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/v1beta") {
		return base + suffix
	}
	return base + "/v1" + suffix
}

func applyProviderProbeHeaders(request *http.Request, p config.Provider) {
	providerprofile.ApplyAuthenticationHeaders(request.Header, p)
	if p.Protocol == ir.Anthropic {
		request.Header.Set("anthropic-version", "2023-06-01")
	}
	for key, value := range p.Headers {
		if strings.EqualFold(key, "host") || strings.EqualFold(key, "content-length") {
			continue
		}
		request.Header.Set(key, value)
	}
	providerprofile.ApplyOpenCodeHeaders(request, p)
}

func classifyProviderError(status int, body []byte) (string, string) {
	message := providerErrorMessage(body)
	lower := strings.ToLower(message)
	code := "http_error"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = "authentication_failed"
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		code = "timeout"
	case status == http.StatusNotFound:
		code = "endpoint_not_found"
	case status == http.StatusTooManyRequests:
		code = "rate_limited"
	case status == http.StatusPaymentRequired || strings.Contains(lower, "quota") || strings.Contains(lower, "insufficient balance") || strings.Contains(lower, "余额不足"):
		code = "quota_exhausted"
	case strings.Contains(lower, "account invalid") || strings.Contains(lower, "invalid api key"):
		code = "authentication_failed"
	case strings.Contains(lower, "reasoning_effort") && strings.Contains(lower, "tool"):
		code = "tools_with_reasoning_unsupported"
	case strings.Contains(lower, "unsupported") && (strings.Contains(lower, "parameter") || strings.Contains(lower, "field")):
		code = "unsupported_parameter"
	case strings.Contains(lower, "content filter") || strings.Contains(lower, "content_filter"):
		code = "content_filtered"
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return code, message
}

func providerErrorMessage(body []byte) string {
	var value any
	if json.Unmarshal(body, &value) != nil {
		message := strings.TrimSpace(string(body))
		lower := strings.ToLower(message)
		if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html") {
			return "provider returned an HTML error page"
		}
		if len(message) > 500 {
			message = message[:500] + "…"
		}
		return message
	}
	var find func(any) string
	find = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			for _, key := range []string{"cause", "message", "error"} {
				if item, exists := typed[key]; exists {
					if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
						var nested any
						if json.Unmarshal([]byte(text), &nested) == nil {
							if nestedText := find(nested); nestedText != "" {
								return nestedText
							}
						}
						return strings.TrimSpace(text)
					}
					if nestedText := find(item); nestedText != "" {
						return nestedText
					}
				}
			}
			for _, item := range typed {
				if nestedText := find(item); nestedText != "" {
					return nestedText
				}
			}
		case []any:
			for _, item := range typed {
				if nestedText := find(item); nestedText != "" {
					return nestedText
				}
			}
		}
		return ""
	}
	return find(value)
}
