package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/gateway"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/anthropic"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
	"github.com/zbss/airoute/internal/protocol/openaichat"
	"github.com/zbss/airoute/internal/protocol/openairesponses"
	providerprofile "github.com/zbss/airoute/internal/provider"
	"github.com/zbss/airoute/internal/secure"
)

type codexCompatibilityReport struct {
	Status                        string      `json:"status"`
	Protocol                      ir.Protocol `json:"protocol,omitempty"`
	Message                       string      `json:"message"`
	Verified                      bool        `json:"verified"`
	Confidence                    float64     `json:"confidence"`
	RecommendedOmitFields         []string    `json:"recommended_omit_fields,omitempty"`
	RecommendedIntegrationMode    string      `json:"recommended_integration_mode,omitempty"`
	RecommendedCompatibilityMode  string      `json:"recommended_compatibility_mode,omitempty"`
	RecommendedToolChoiceMode     string      `json:"recommended_tool_choice_mode,omitempty"`
	RecommendedReasoningHistory   string      `json:"recommended_reasoning_history,omitempty"`
	RecommendedReasoningWithTools string      `json:"recommended_reasoning_with_tools,omitempty"`
}

func codexCompatibility(reports map[ir.Protocol]protocolCapabilityReport) codexCompatibilityReport {
	var sawReachable bool
	var sawUnavailable bool
	var best codexCompatibilityReport
	for _, protocolName := range []ir.Protocol{ir.OpenAIResponses, ir.OpenAIChat} {
		report, ok := reports[protocolName]
		if !ok {
			continue
		}
		if isAvailabilityError(report.Basic.ErrorCode) {
			sawUnavailable = true
		}
		if !report.Basic.OK {
			continue
		}
		sawReachable = true
		if report.Streaming == nil || report.Tools == nil {
			candidate := codexCompatibilityReport{Status: "unverified", Protocol: protocolName, Message: "API 可连接，但 Codex 能力检测尚未完整执行", Confidence: 0.4}
			best = preferCodexCompatibility(best, candidate)
			continue
		}
		if protocolName == ir.OpenAIResponses &&
			report.Streaming.State == capabilitySupported &&
			report.Tools.State == capabilitySupported &&
			report.Reasoning != nil && report.Reasoning.State == capabilitySupported &&
			report.ToolsWithReasoning != nil && report.ToolsWithReasoning.State == capabilitySupported &&
			report.ToolRoundTrip != nil && report.ToolRoundTrip.State == capabilitySupported && hasCapabilityEvidence(*report.ToolRoundTrip, "reasoning_history_preserved") &&
			report.CodexDirect != nil && report.CodexDirect.State == capabilitySupported &&
			report.CodexEndToEnd != nil && report.CodexEndToEnd.State == capabilitySupported {
			candidate := codexCompatibilityReport{
				Status:                        "full",
				Protocol:                      protocolName,
				Message:                       "上游原生 Responses、Codex custom tools、reasoning 与多轮工具续接均已验证，可使用官方直连",
				Verified:                      true,
				Confidence:                    0.95,
				RecommendedIntegrationMode:    "direct",
				RecommendedReasoningHistory:   "preserve",
				RecommendedReasoningWithTools: "supported",
			}
			best = preferCodexCompatibility(best, candidate)
			continue
		}
		// A successful Codex-through-Router tool call and continuation is the
		// authoritative compatibility signal. A standalone streaming probe may
		// time out on slow services even though the actual Router path works.
		if report.Tools.State == capabilitySupported &&
			report.CodexEndToEnd != nil && report.CodexEndToEnd.State == capabilitySupported {
			field := "reasoning_effort"
			compatibilityMode := "codex-chat"
			if protocolName == ir.OpenAIResponses {
				field = "reasoning"
				compatibilityMode = "codex-responses"
			}
			message := "Codex 经 AI Router 的流式工具调用与多轮续接已验证"
			if report.ToolsWithReasoning == nil || report.ToolsWithReasoning.State != capabilitySupported {
				if protocolName == ir.OpenAIChat {
					message = "Codex 使用 high 推理等级时通常会同时发送 tools（如 apply_patch、shell 等工具定义）和 reasoning_effort（用于指定模型推理强度），但该上游的 /v1/chat/completions 会拒绝同时包含 tools + reasoning_effort 的请求；AI Router 会在工具请求中移除 reasoning_effort 并保留工具调用"
				} else {
					message = "该上游不能同时处理 Codex tools 与 reasoning；AI Router 会在工具请求中移除 reasoning 并保留工具调用"
				}
			}
			candidate := codexCompatibilityReport{
				Status:                        "degraded",
				Protocol:                      protocolName,
				Message:                       message,
				Verified:                      true,
				Confidence:                    0.92,
				RecommendedOmitFields:         []string{field},
				RecommendedIntegrationMode:    "compatibility",
				RecommendedCompatibilityMode:  compatibilityMode,
				RecommendedReasoningHistory:   "preserve",
				RecommendedReasoningWithTools: "disabled",
			}
			if hasCapabilityEvidence(*report.CodexEndToEnd, "reasoning_configuration_preserved") {
				candidate.Message = "Codex 经 AI Router 的流式工具调用与多轮续接已验证；保留上游推理设置"
				candidate.RecommendedOmitFields = nil
				candidate.RecommendedReasoningWithTools = "supported"
				if protocolName == ir.OpenAIChat {
					candidate.RecommendedCompatibilityMode = ""
				}
			}
			best = preferCodexCompatibility(best, candidate)
			continue
		}
		if report.Streaming.State == capabilityInconclusive || report.Tools.State == capabilityInconclusive || report.Streaming.State == capabilityNotTested || report.Tools.State == capabilityNotTested {
			pending := make([]string, 0, 2)
			if report.Streaming.State == capabilityInconclusive || report.Streaming.State == capabilityNotTested {
				pending = append(pending, capabilityPendingMessage("流式输出", *report.Streaming))
			}
			if report.Tools.State == capabilityInconclusive || report.Tools.State == capabilityNotTested {
				pending = append(pending, capabilityPendingMessage("function tools", *report.Tools))
			}
			message := "API 可连接，但流式输出或工具能力尚未得到确定性验证"
			if len(pending) > 0 {
				message = strings.Join(pending, "；")
			}
			candidate := codexCompatibilityReport{Status: "unverified", Protocol: protocolName, Message: message, Confidence: 0.5}
			if report.Tools.State == capabilitySupported {
				field := "reasoning_effort"
				compatibilityMode := "codex-chat"
				if protocolName == ir.OpenAIResponses {
					field = "reasoning"
					compatibilityMode = "codex-responses"
				}
				candidate.Message = "function tools 已验证，但 Codex 经 AI Router 的流式工具链路尚未在时限内完成"
				candidate.Confidence = 0.65
				candidate.RecommendedOmitFields = []string{field}
				candidate.RecommendedIntegrationMode = "compatibility"
				candidate.RecommendedCompatibilityMode = compatibilityMode
				candidate.RecommendedReasoningHistory = "preserve"
				candidate.RecommendedReasoningWithTools = "disabled"
			}
			best = preferCodexCompatibility(best, candidate)
			continue
		}
		if report.Streaming.State != capabilitySupported || report.Tools.State != capabilitySupported {
			candidate := codexCompatibilityReport{Status: "incompatible", Protocol: protocolName, Message: "已验证该协议无法同时满足 Codex 的流式输出和 function tools 基本要求", Verified: true, Confidence: 0.9}
			best = preferCodexCompatibility(best, candidate)
			continue
		}
		advanced := []*capabilityCheck{report.Reasoning, report.ToolsWithReasoning, report.ToolRoundTrip, report.CodexEndToEnd}
		if protocolName == ir.OpenAIResponses {
			advanced = append(advanced, report.CodexDirect)
		}
		hasUnsupported := false
		hasUnverified := false
		for _, check := range advanced {
			if check == nil || check.State == capabilityInconclusive || check.State == capabilityNotTested {
				hasUnverified = true
				continue
			}
			if check.State == capabilityUnsupported {
				hasUnsupported = true
			}
		}
		if !hasUnsupported && hasUnverified {
			message := "流式输出和 function tools 可用，但 reasoning、组合能力或 Codex 多轮链路缺少确定性证据"
			candidate := codexCompatibilityReport{Status: "unverified", Protocol: protocolName, Message: message, Confidence: 0.55}
			if report.CodexEndToEnd != nil && report.CodexEndToEnd.State == capabilityInconclusive {
				otherAdvancedVerified := report.Reasoning != nil && report.Reasoning.State == capabilitySupported &&
					report.ToolsWithReasoning != nil && report.ToolsWithReasoning.State == capabilitySupported &&
					report.ToolRoundTrip != nil && report.ToolRoundTrip.State == capabilitySupported
				candidate.Message = codexEndToEndPendingMessage(*report.CodexEndToEnd, otherAdvancedVerified)
				candidate.RecommendedIntegrationMode = "compatibility"
				candidate.RecommendedCompatibilityMode = "codex-chat"
				candidate.RecommendedReasoningHistory = "preserve"
				if report.ToolsWithReasoning != nil && report.ToolsWithReasoning.State == capabilitySupported {
					candidate.RecommendedReasoningWithTools = "supported"
				} else {
					candidate.RecommendedReasoningWithTools = "disabled"
					candidate.RecommendedOmitFields = []string{"reasoning_effort"}
				}
				if hasCapabilityEvidence(*report.CodexEndToEnd, "reasoning_configuration_preserved") {
					candidate.RecommendedReasoningWithTools = "supported"
					candidate.RecommendedOmitFields = nil
					candidate.RecommendedCompatibilityMode = ""
				}
				if protocolName == ir.OpenAIResponses {
					candidate.RecommendedCompatibilityMode = "codex-responses"
					if candidate.RecommendedReasoningWithTools == "disabled" {
						candidate.RecommendedOmitFields = []string{"reasoning"}
					}
				}
			}
			best = preferCodexCompatibility(best, candidate)
			continue
		}
		field := "reasoning_effort"
		compatibilityMode := "codex-chat"
		if protocolName == ir.OpenAIResponses {
			field = "reasoning"
			compatibilityMode = "codex-responses"
		}
		message := "上游只提供 OpenAI Chat 或缺少 Codex 原生 custom tools 能力，需要 AI Router 进行兼容转换"
		if report.CodexEndToEnd == nil || report.CodexEndToEnd.State != capabilitySupported {
			message = "流式输出和 function tools 已验证，但 tools 与 reasoning 或 Codex 多轮工具链路未完全通过"
		}
		candidate := codexCompatibilityReport{
			Status:                        "degraded",
			Protocol:                      protocolName,
			Message:                       message,
			Verified:                      true,
			Confidence:                    0.9,
			RecommendedOmitFields:         []string{field},
			RecommendedIntegrationMode:    "compatibility",
			RecommendedCompatibilityMode:  compatibilityMode,
			RecommendedReasoningHistory:   "preserve",
			RecommendedReasoningWithTools: "disabled",
		}
		best = preferCodexCompatibility(best, candidate)
	}
	if best.Status != "" {
		return best
	}
	if report, ok := reports[ir.Anthropic]; ok && report.Basic.OK {
		if report.CodexEndToEnd != nil && report.CodexEndToEnd.State == capabilitySupported {
			return codexCompatibilityReport{
				Status:                        "full",
				Protocol:                      ir.Anthropic,
				Message:                       "上游原生 Anthropic Messages 与 Codex 经 AI Router 的 custom tools、流式输出和多轮工具续接均已验证",
				Verified:                      true,
				Confidence:                    0.95,
				RecommendedIntegrationMode:    "compatibility",
				RecommendedReasoningHistory:   "preserve",
				RecommendedReasoningWithTools: "supported",
			}
		}
		return codexCompatibilityReport{
			Status:                        "degraded",
			Protocol:                      ir.Anthropic,
			Message:                       "上游原生 Anthropic Messages 可用，但 Codex 经 AI Router 的 custom tools 或多轮工具续接尚未完整通过",
			Verified:                      false,
			Confidence:                    0.7,
			RecommendedIntegrationMode:    "compatibility",
			RecommendedReasoningHistory:   "preserve",
			RecommendedReasoningWithTools: "disabled",
		}
	}
	if sawUnavailable && !sawReachable {
		return codexCompatibilityReport{Status: "unavailable", Message: providerDetectionFailureMessage(reports), Confidence: 1}
	}
	if sawReachable {
		return codexCompatibilityReport{Status: "incompatible", Message: "已验证的 OpenAI 协议无法同时满足 Codex 的流式输出和 function tools 基本要求", Verified: true, Confidence: 0.9}
	}
	return codexCompatibilityReport{
		Status:     "unavailable",
		Message:    providerDetectionFailureMessage(reports),
		Confidence: 0.9,
	}
}

// Include the upstream reason instead of equating every rejection with a missing endpoint.
func providerDetectionFailureMessage(reports map[ir.Protocol]protocolCapabilityReport) string {
	var details []string
	for _, protocolName := range []ir.Protocol{ir.OpenAIChat, ir.OpenAIResponses, ir.Anthropic, ir.Gemini} {
		report, exists := reports[protocolName]
		if !exists || report.Basic.OK {
			continue
		}
		check := report.Basic
		reason := check.Error
		if reason == "" {
			reason = check.ErrorCode
		}
		if reason == "" {
			reason = "请求未通过"
		}
		if check.Status > 0 {
			reason = fmt.Sprintf("HTTP %d：%s", check.Status, reason)
		}
		details = append(details, string(protocolName)+"："+reason)
	}
	if len(details) == 0 {
		return "模型服务连接检测未通过"
	}
	return "模型服务连接检测未通过。" + strings.Join(details, "；")
}

func codexEndToEndPendingMessage(check capabilityCheck, otherAdvancedVerified bool) string {
	suffix := "；流式输出和 function tools 已验证，其他高级能力仍需继续确认"
	if otherAdvancedVerified {
		suffix = "；其余流式输出、function tools、reasoning 与多轮续接均已验证"
	}
	latency := formatProbeLatency(check.LatencyMS)
	code := strings.ToLower(check.ErrorCode)
	switch {
	case strings.Contains(code, "timeout") || strings.Contains(strings.ToLower(check.Error), "deadline exceeded"):
		return "Codex 经 AI Router 端到端验证超时" + latency + suffix
	case strings.Contains(code, "custom_tool_not_observed"):
		return "Codex 经 AI Router 端到端验证未观察到模型触发 apply_patch custom tool" + latency + suffix
	case strings.Contains(code, "call_id_missing"):
		return "Codex 经 AI Router 端到端验证收到 custom tool 调用，但缺少 call ID" + latency + suffix
	case strings.Contains(code, "round_trip"):
		return "Codex 经 AI Router 的第二轮工具结果续接未完成" + latency + suffix
	default:
		if check.ErrorCode != "" {
			return "Codex 经 AI Router 端到端验证尚未确认（" + check.ErrorCode + "）" + latency + suffix
		}
		return "Codex 经 AI Router 端到端验证尚未确认" + latency + suffix
	}
}

func formatProbeLatency(milliseconds int64) string {
	if milliseconds <= 0 {
		return ""
	}
	if milliseconds < 1000 {
		return fmt.Sprintf("（耗时 %d ms）", milliseconds)
	}
	return fmt.Sprintf("（耗时 %.1f 秒）", float64(milliseconds)/1000)
}

func capabilityPendingMessage(label string, check capabilityCheck) string {
	latency := formatProbeLatency(check.LatencyMS)
	code := strings.ToLower(check.ErrorCode)
	errorMessage := strings.ToLower(check.Error)
	switch {
	case check.State == capabilityNotTested:
		return label + "检测未执行"
	case strings.Contains(code, "timeout") || strings.Contains(errorMessage, "deadline exceeded"):
		return label + "验证超时" + latency
	case code == "tool_not_observed":
		return label + "请求已被接受，但模型未返回工具调用" + latency
	case code == "schema_mismatch":
		return label + "响应结构不符合预期" + latency
	case strings.Contains(code, "sse") || strings.Contains(code, "stream") || strings.Contains(code, "content_type"):
		return label + "返回的流式响应结构不符合预期" + latency
	case check.ErrorCode != "":
		return label + "尚未确认（" + check.ErrorCode + "）" + latency
	default:
		return label + "尚未确认" + latency
	}
}

func preferCodexCompatibility(current, candidate codexCompatibilityReport) codexCompatibilityReport {
	rank := map[string]int{"unavailable": 1, "incompatible": 2, "unverified": 3, "degraded": 4, "full": 5}
	if rank[candidate.Status] > rank[current.Status] || (candidate.Status == "unverified" && current.Status == "unverified" && candidate.Confidence > current.Confidence) {
		return candidate
	}
	// If neither endpoint supports Codex's native custom-tool contract, prefer
	// the explicit Chat endpoint for Router conversion. This avoids selecting a
	// Responses facade that is itself backed by Chat and then converting twice.
	if candidate.Status == "degraded" && current.Status == "degraded" &&
		candidate.Protocol == ir.OpenAIChat && current.Protocol == ir.OpenAIResponses {
		return candidate
	}
	return current
}

func hasCapabilityEvidence(check capabilityCheck, expected string) bool {
	for _, evidence := range check.Evidence {
		if evidence == expected {
			return true
		}
	}
	return false
}

func isAvailabilityError(code string) bool {
	switch code {
	case "authentication_failed", "rate_limited", "quota_exhausted", "network_error", "timeout", "canceled":
		return true
	default:
		return false
	}
}

func selectProviderProtocol(reports map[ir.Protocol]protocolCapabilityReport, compatibility codexCompatibilityReport) ir.Protocol {
	if compatibility.Protocol != "" {
		return compatibility.Protocol
	}
	for _, protocolName := range []ir.Protocol{ir.OpenAIResponses, ir.OpenAIChat, ir.Anthropic, ir.Gemini} {
		if report, ok := reports[protocolName]; ok && report.Basic.OK {
			return protocolName
		}
	}
	return ""
}

func notTestedCheck(reason string) capabilityCheck {
	return capabilityCheck{State: capabilityNotTested, ErrorCode: "not_tested", Error: reason}
}

// verifyCodexDirect sends the Codex-specific Responses shape straight to the
// upstream. This is intentionally separate from verifyCodexEndToEnd: the latter
// proves that AI Router can repair/translate the provider, while this check is
// the gate for advertising Codex's official custom-provider path.
func (s *Server) verifyCodexDirect(ctx context.Context, p config.Provider) capabilityCheck {
	ctx = providerprofile.WithProbeSession(ctx)
	started := time.Now()
	model := ""
	if len(p.Models) > 0 {
		model = p.Models[0]
	}
	endpoint := appendProbeEndpoint(strings.TrimRight(p.BaseURL, "/"), "/responses")
	if err := secure.ValidatePublicTarget(ctx, endpoint, p.AllowPrivateURL); err != nil {
		return failedCapability(started, "blocked_target", err.Error())
	}
	payload := common.Raw(map[string]any{
		"model":     model,
		"input":     "Use apply_patch to add a newline to probe.txt. Do not answer without calling the tool.",
		"stream":    true,
		"reasoning": map[string]any{"effort": "low"},
		"tools": []any{map[string]any{
			"type": "custom", "name": "apply_patch", "description": "Apply a patch",
			"format": map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: patch"},
		}},
		"tool_choice": map[string]any{"type": "custom", "name": "apply_patch"},
	})
	status, contentType, body, err := s.doProviderProbeRequest(ctx, p, endpoint, payload, "text/event-stream")
	if err != nil {
		return failedCapability(started, errorCodeForRequest(ctx, err), err.Error())
	}
	if status < 200 || status >= 300 {
		code, message := classifyProviderError(status, body)
		state := capabilityUnsupported
		if isAvailabilityError(code) {
			state = capabilityInconclusive
		}
		return capabilityCheck{State: state, Confidence: 0.95, Status: status, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_direct_" + code, Error: message, ContentType: contentType, body: body}
	}
	if code, message := validateProviderSSE(ir.OpenAIResponses, contentType, body); code != "" {
		return capabilityCheck{State: capabilityUnsupported, Confidence: 0.9, Status: status, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_direct_" + code, Error: message, ContentType: contentType, body: body}
	}
	if !strings.Contains(string(body), "custom_tool_call") || !strings.Contains(string(body), "response.custom_tool_call_input") {
		return capabilityCheck{State: capabilityUnsupported, Confidence: 0.9, Status: status, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_direct_custom_tool_not_observed", Error: "upstream Responses stream did not emit Codex custom tool events", ContentType: contentType, body: body}
	}
	callID := codexCustomCallID(body)
	if callID == "" {
		return capabilityCheck{State: capabilityUnsupported, Confidence: 0.9, Status: status, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_direct_call_id_missing", Error: "upstream custom tool event did not contain a call ID", ContentType: contentType, body: body}
	}
	followUpPayload := common.Raw(map[string]any{
		"model": model,
		"input": []any{
			map[string]any{"type": "custom_tool_call", "call_id": callID, "name": "apply_patch", "input": "*** Begin Patch\n*** End Patch"},
			map[string]any{"type": "custom_tool_call_output", "call_id": callID, "output": "patch applied"},
		},
		"max_output_tokens": 64,
	})
	followStatus, followType, followBody, followErr := s.doProviderProbeRequest(ctx, p, endpoint, followUpPayload, "application/json")
	if followErr != nil {
		return failedCapability(started, errorCodeForRequest(ctx, followErr), followErr.Error())
	}
	if followStatus < 200 || followStatus >= 300 {
		code, message := classifyProviderError(followStatus, followBody)
		state := capabilityUnsupported
		if isAvailabilityError(code) {
			state = capabilityInconclusive
		}
		return capabilityCheck{State: state, Confidence: 0.95, Status: followStatus, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_direct_round_trip_" + code, Error: message, ContentType: followType, body: followBody}
	}
	if code, message := validateProviderJSON(ir.OpenAIResponses, followBody); code != "" {
		return capabilityCheck{State: capabilityUnsupported, Confidence: 0.9, Status: followStatus, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_direct_round_trip_" + code, Error: message, ContentType: followType, body: followBody}
	}
	return capabilityCheck{OK: true, State: capabilitySupported, Confidence: 0.98, Status: status, LatencyMS: time.Since(started).Milliseconds(), ContentType: contentType, Evidence: []string{"codex_official_direct", "native_custom_apply_patch", "native_custom_tool_round_trip"}, body: body}
}

func (s *Server) verifyCodexEndToEnd(ctx context.Context, p config.Provider) capabilityCheck {
	ctx = providerprofile.WithProbeSession(ctx)
	started := time.Now()
	model := ""
	if len(p.Models) > 0 {
		model = p.Models[0]
	}
	p.ID = "detection-target"
	p.Models = []string{model}
	p.Timeout = providerCodexProbeTimeout
	if p.Protocol == ir.OpenAIResponses {
		p.CompatibilityMode = "codex-responses"
	}
	temporary := &config.Config{
		Version:      1,
		Server:       config.Server{RequestTimeout: providerCodexProbeTimeout, MaxBodySize: 4 << 20, MaxConcurrent: 2},
		Providers:    []config.Provider{p},
		DefaultRoute: &config.RouteTargetList{Targets: []config.RouteTarget{{Provider: p.ID, Model: model}}},
		Conversion:   config.Conversion{UnsupportedFields: "warn"},
		Retry:        config.Retry{MaxAttempts: 1},
		Metrics:      config.Metrics{Path: "/metrics"},
	}
	registry := protocol.NewRegistry(openaichat.New(), openairesponses.New(), anthropic.New())
	g := gateway.New(config.NewStore(temporary), registry, observe.NewStore(2), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Reuse the admin clients so private-address permission, proxy behavior, and
	// test transports match the direct protocol probes.
	g.Client = s.Client
	g.RestrictedClient = s.RestrictedClient
	body := common.Raw(map[string]any{
		"model":     model,
		"input":     "Use apply_patch to add a newline to probe.txt. Do not answer without calling the tool.",
		"stream":    true,
		"reasoning": map[string]any{"effort": "low"},
		"tools": []any{map[string]any{
			"type": "custom", "name": "apply_patch", "description": "Apply a patch",
			"format": map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: patch"},
		}},
		"tool_choice": map[string]any{"type": "custom", "name": "apply_patch"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("content-type", "application/json")
	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, request)
	resultBody := recorder.Body.Bytes()
	if recorder.Code < 200 || recorder.Code >= 300 {
		code, message := classifyProviderError(recorder.Code, resultBody)
		return capabilityCheck{State: capabilityInconclusive, Confidence: 0.7, Status: recorder.Code, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_" + code, Error: message, body: resultBody}
	}
	text := string(resultBody)
	if !strings.Contains(text, "response.custom_tool_call_input") || !strings.Contains(text, "custom_tool_call") {
		return capabilityCheck{State: capabilityInconclusive, Confidence: 0.5, Status: recorder.Code, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_custom_tool_not_observed", Error: "AI Router completed the request but did not reconstruct a Codex custom tool event", body: resultBody}
	}
	callID := codexCustomCallID(resultBody)
	if callID == "" {
		return capabilityCheck{State: capabilityInconclusive, Confidence: 0.5, Status: recorder.Code, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_custom_tool_call_id_missing", Error: "Codex custom tool event did not contain a call ID", body: resultBody}
	}
	followUpBody := common.Raw(map[string]any{
		"model":             model,
		"max_output_tokens": 256,
		"input": []any{
			map[string]any{"role": "user", "content": "Apply the patch, then reply OK after the tool succeeds."},
			map[string]any{"type": "custom_tool_call", "call_id": callID, "name": "apply_patch", "input": "*** Begin Patch\n*** End Patch"},
			map[string]any{"type": "custom_tool_call_output", "call_id": callID, "output": "patch applied"},
		},
	})
	followUp := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(followUpBody)).WithContext(ctx)
	followUp.Header.Set("content-type", "application/json")
	followUpRecorder := httptest.NewRecorder()
	g.ServeHTTP(followUpRecorder, followUp)
	if followUpRecorder.Code < 200 || followUpRecorder.Code >= 300 {
		code, message := classifyProviderError(followUpRecorder.Code, followUpRecorder.Body.Bytes())
		return capabilityCheck{State: capabilityInconclusive, Confidence: 0.7, Status: followUpRecorder.Code, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_round_trip_" + code, Error: message, body: followUpRecorder.Body.Bytes()}
	}
	if code, message := validateProviderJSON(ir.OpenAIResponses, followUpRecorder.Body.Bytes()); code != "" {
		return capabilityCheck{State: capabilityInconclusive, Confidence: 0.5, Status: followUpRecorder.Code, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "codex_round_trip_" + code, Error: message, body: followUpRecorder.Body.Bytes()}
	}
	return capabilityCheck{OK: true, State: capabilitySupported, Confidence: 0.95, Status: recorder.Code, LatencyMS: time.Since(started).Milliseconds(), ContentType: recorder.Header().Get("content-type"), Evidence: []string{"codex_responses_through_airoute", "custom_apply_patch_reconstructed", "stream_lifecycle_valid", "codex_tool_result_round_trip_succeeded"}, body: resultBody}
}

func codexCustomCallID(stream []byte) string {
	for _, line := range strings.Split(string(stream), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &value) != nil {
			continue
		}
		item := common.Map(value["item"])
		if common.String(item["type"]) == "custom_tool_call" {
			if callID := common.String(item["call_id"]); callID != "" {
				return callID
			}
		}
	}
	return ""
}
