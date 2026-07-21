package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

type providerProbeKind string

const (
	probeBasic                providerProbeKind = "basic"
	probeModels               providerProbeKind = "models"
	probeStreaming            providerProbeKind = "streaming"
	probeTools                providerProbeKind = "tools"
	probeReasoning            providerProbeKind = "reasoning"
	probeToolsWithReasoning   providerProbeKind = "tools_with_reasoning"
	probeToolRoundTrip        providerProbeKind = "tool_round_trip"
	providerProbeTimeout                        = 8 * time.Second
	providerCodexProbeTimeout                   = 18 * time.Second
	providerDetectionTimeout                    = 45 * time.Second
	providerProbeSuccessTTL                     = 10 * time.Minute
	providerProbeFailureTTL                     = 30 * time.Second
	providerDetectorVersion                     = 7
)

type capabilityState string

const (
	capabilitySupported    capabilityState = "supported"
	capabilityUnsupported  capabilityState = "unsupported"
	capabilityInconclusive capabilityState = "inconclusive"
	capabilityNotTested    capabilityState = "not_tested"
)

type capabilityCheck struct {
	OK          bool            `json:"ok"`
	State       capabilityState `json:"state"`
	Confidence  float64         `json:"confidence"`
	Status      int             `json:"status,omitempty"`
	LatencyMS   int64           `json:"latency_ms"`
	ErrorCode   string          `json:"error_code,omitempty"`
	Error       string          `json:"error,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
	Evidence    []string        `json:"evidence,omitempty"`
	body        []byte
}

type protocolCapabilityReport struct {
	Applicability      string           `json:"applicability,omitempty"`
	Basic              capabilityCheck  `json:"basic"`
	Streaming          *capabilityCheck `json:"streaming,omitempty"`
	Tools              *capabilityCheck `json:"tools,omitempty"`
	Reasoning          *capabilityCheck `json:"reasoning,omitempty"`
	ToolsWithReasoning *capabilityCheck `json:"tools_with_reasoning,omitempty"`
	ToolRoundTrip      *capabilityCheck `json:"tool_round_trip,omitempty"`
	CodexDirect        *capabilityCheck `json:"codex_direct,omitempty"`
	CodexEndToEnd      *capabilityCheck `json:"codex_end_to_end,omitempty"`
}

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

type providerDetectionReport struct {
	DetectorVersion    int                                      `json:"detector_version"`
	CheckedAt          time.Time                                `json:"checked_at"`
	OK                 bool                                     `json:"ok"`
	Protocol           ir.Protocol                              `json:"protocol,omitempty"`
	Profile            string                                   `json:"profile"`
	ProfileVersion     int                                      `json:"profile_version"`
	Label              string                                   `json:"label,omitempty"`
	Models             []string                                 `json:"models"`
	LatencyMS          int64                                    `json:"latency_ms"`
	Attempts           []map[string]any                         `json:"attempts"`
	Protocols          map[ir.Protocol]protocolCapabilityReport `json:"protocols"`
	CodexCompatibility codexCompatibilityReport                 `json:"codex_compatibility"`
	Cached             bool                                     `json:"cached,omitempty"`
	ModelReports       map[string]modelCapabilitySummary        `json:"model_reports,omitempty"`
}

type modelCapabilitySummary struct {
	Protocol ir.Protocol     `json:"protocol,omitempty"`
	Basic    capabilityCheck `json:"basic"`
}

type providerProbeCacheEntry struct {
	expires time.Time
	report  providerDetectionReport
}

type providerProbeCall struct {
	done   chan struct{}
	report providerDetectionReport
}

type providerDetectionProgress struct {
	Stage    string      `json:"stage"`
	Status   string      `json:"status"`
	Message  string      `json:"message"`
	Protocol ir.Protocol `json:"protocol,omitempty"`
}

type providerProgressEmitter func(providerDetectionProgress)

func (s *Server) detectProviderCapabilities(ctx context.Context, baseURL, apiKey string, models []string, allowPrivateURL bool) providerDetectionReport {
	return s.detectProviderCapabilitiesWithOptions(ctx, baseURL, apiKey, models, allowPrivateURL, false)
}

func (s *Server) detectProviderCapabilitiesWithOptions(ctx context.Context, baseURL, apiKey string, models []string, allowPrivateURL, forceRefresh bool) providerDetectionReport {
	return s.detectProviderCapabilitiesWithProgress(ctx, baseURL, apiKey, models, allowPrivateURL, forceRefresh, nil)
}

func (s *Server) detectProviderCapabilitiesWithProgress(ctx context.Context, baseURL, apiKey string, models []string, allowPrivateURL, forceRefresh bool, emit providerProgressEmitter) providerDetectionReport {
	started := time.Now()
	key := providerProbeCacheKey(baseURL, apiKey, models, allowPrivateURL)
	now := time.Now()
	s.probeMu.Lock()
	if s.probeCache == nil {
		s.probeCache = make(map[string]providerProbeCacheEntry)
	}
	if s.probeInFlight == nil {
		s.probeInFlight = make(map[string]*providerProbeCall)
	}
	for cacheKey, entry := range s.probeCache {
		if !entry.expires.After(now) {
			delete(s.probeCache, cacheKey)
		}
	}
	if !forceRefresh {
		if entry, ok := s.probeCache[key]; ok {
			report := entry.report
			report.Cached = true
			s.probeMu.Unlock()
			emitProviderProgress(emit, "cache", "completed", "已使用有效的检测缓存", report.Protocol)
			return report
		}
	}
	if call, ok := s.probeInFlight[key]; ok {
		s.probeMu.Unlock()
		emitProviderProgress(emit, "deduplication", "running", "正在等待相同模型服务的检测结果", "")
		select {
		case <-call.done:
			report := call.report
			report.Cached = true
			return report
		case <-ctx.Done():
			return canceledProviderDetection(baseURL, models, started, ctx.Err())
		}
	}
	call := &providerProbeCall{done: make(chan struct{})}
	s.probeInFlight[key] = call
	s.probeMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, providerDetectionTimeout)
	report := s.resolveProviderCapabilities(probeCtx, baseURL, apiKey, models, allowPrivateURL, emit)
	cancel()
	ttl := providerProbeFailureTTL
	if report.OK {
		ttl = providerProbeSuccessTTL
	}
	s.probeMu.Lock()
	call.report = report
	s.probeCache[key] = providerProbeCacheEntry{expires: time.Now().Add(ttl), report: report}
	delete(s.probeInFlight, key)
	close(call.done)
	s.probeMu.Unlock()
	return report
}

func emitProviderProgress(emit providerProgressEmitter, stage, status, message string, protocolName ir.Protocol) {
	if emit != nil {
		emit(providerDetectionProgress{Stage: stage, Status: status, Message: message, Protocol: protocolName})
	}
}

func providerProbeCacheKey(baseURL, apiKey string, models []string, allowPrivateURL bool) string {
	payload := fmt.Sprintf("v%d\x00", providerDetectorVersion) + strings.TrimSpace(baseURL) + "\x00" + apiKey + "\x00" + strings.Join(models, "\x00") + fmt.Sprintf("\x00%t", allowPrivateURL)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
}

func canceledProviderDetection(baseURL string, models []string, started time.Time, err error) providerDetectionReport {
	message := "协议识别已取消"
	if err != nil {
		message += "：" + err.Error()
	}
	return providerDetectionReport{
		DetectorVersion: providerDetectorVersion,
		CheckedAt:       time.Now(),
		Profile:         detectProviderProfile(baseURL, models),
		ProfileVersion:  providerprofile.DetectionProfileVersion,
		Models:          models,
		LatencyMS:       time.Since(started).Milliseconds(),
		Attempts:        []map[string]any{},
		Protocols:       map[ir.Protocol]protocolCapabilityReport{},
		CodexCompatibility: codexCompatibilityReport{
			Status:     "unavailable",
			Message:    message,
			Confidence: 1,
		},
	}
}

func (s *Server) resolveProviderCapabilities(ctx context.Context, baseURL, apiKey string, models []string, allowPrivateURL bool, emit providerProgressEmitter) providerDetectionReport {
	started := time.Now()
	profile := detectProviderProfile(baseURL, models)
	emitProviderProgress(emit, "fingerprint", "completed", "模型服务特征识别完成", "")
	candidates := preferredProtocolCandidates(baseURL, profile)
	reports := make(map[ir.Protocol]protocolCapabilityReport, len(candidates))
	attempts := make([]map[string]any, 0, len(candidates))
	strategy := providerprofile.ProviderDetectionStrategy(profile)
	newProbeProvider := func(candidate ir.Protocol) config.Provider {
		return config.Provider{
			Protocol: candidate, Profile: profile, BaseURL: baseURL,
			APIKey: apiKey, Models: models, AllowPrivateURL: allowPrivateURL,
			Timeout: providerProbeTimeout, ToolChoiceMode: strategy.ToolChoiceMode,
			ReasoningHistory: strategy.ReasoningHistory,
		}
	}

	// Generic OpenAI-compatible services frequently expose only one of the two
	// endpoints. Probe the cheap endpoint contracts together so a missing or
	// hanging Responses facade does not delay the usable Chat path by a full
	// timeout window.
	basicChecks := make(map[ir.Protocol]capabilityCheck, len(candidates))
	if shouldProbeOpenAIBasicsInParallel(profile, candidates) {
		checks := make([]capabilityCheck, len(candidates))
		var basics sync.WaitGroup
		basics.Add(len(candidates))
		for index, candidate := range candidates {
			emitProviderProgress(emit, "protocol", "running", "正在并行验证候选协议", candidate)
			go func(index int, candidate ir.Protocol) {
				defer basics.Done()
				checks[index] = s.runProviderCheck(ctx, newProbeProvider(candidate), probeBasic)
			}(index, candidate)
		}
		basics.Wait()
		for index, candidate := range candidates {
			basicChecks[candidate] = checks[index]
		}
	}

	for _, candidate := range candidates {
		p := newProbeProvider(candidate)
		basic, alreadyProbed := basicChecks[candidate]
		if !alreadyProbed {
			emitProviderProgress(emit, "protocol", "running", "正在验证候选协议", candidate)
			basic = s.runProviderCheck(ctx, p, probeBasic)
		}
		report := protocolCapabilityReport{Applicability: "candidate", Basic: basic}
		attempt := map[string]any{"protocol": candidate, "status": basic.Status, "ok": basic.OK, "state": basic.State, "latency_ms": basic.LatencyMS}
		if basic.Error != "" {
			attempt["error"] = basic.Error
		}
		if basic.ErrorCode != "" {
			attempt["error_code"] = basic.ErrorCode
		}
		attempts = append(attempts, attempt)
		protocolStatus := "completed"
		if !basic.OK {
			protocolStatus = "failed"
		}
		protocolMessage := "协议基础验证完成"
		if !basic.OK {
			protocolMessage = "候选协议未通过，正在尝试下一项"
		}
		emitProviderProgress(emit, "protocol", protocolStatus, protocolMessage, candidate)
		if basic.OK && (candidate == ir.OpenAIChat || candidate == ir.OpenAIResponses) {
			if candidate == ir.OpenAIResponses {
				emitProviderProgress(emit, "codex-direct", "running", "正在优先验证上游 Codex 官方直连能力", candidate)
				direct := s.verifyCodexDirect(ctx, p)
				report.CodexDirect = &direct
				emitProviderProgress(emit, "codex-direct", string(direct.State), "Codex 官方直连验证完成", candidate)
				// Generic Responses facades commonly accept ordinary function
				// calls but reject Codex custom tools. In that deterministic
				// case, probe the explicit Chat endpoint before spending another
				// 15–30 seconds on advanced Responses checks.
				if profile == providerprofile.ProfileGeneric && direct.State == capabilityUnsupported {
					reports[candidate] = report
					continue
				}
			}
			streaming, tools, reasoning, combined, roundTrip := s.runOpenAIAdvancedChecks(ctx, p, profile, emit)
			report.Streaming = &streaming
			report.Tools = &tools
			report.Reasoning = &reasoning
			report.ToolsWithReasoning = &combined
			report.ToolRoundTrip = &roundTrip
			direct := notTestedCheck("only native OpenAI Responses can use Codex official direct mode")
			if report.CodexDirect != nil {
				direct = *report.CodexDirect
			} else if candidate == ir.OpenAIResponses && streaming.State == capabilitySupported {
				emitProviderProgress(emit, "codex-direct", "running", "正在验证上游 Codex 官方直连能力", candidate)
				direct = s.verifyCodexDirect(ctx, p)
				emitProviderProgress(emit, "codex-direct", string(direct.State), "Codex 官方直连验证完成", candidate)
			}
			report.CodexDirect = &direct
			endToEnd := notTestedCheck("requires tools support and a non-deterministic streaming failure")
			if tools.State == capabilitySupported && streaming.State != capabilityUnsupported {
				probeProvider := p
				if candidate == ir.OpenAIChat && combined.State != capabilitySupported {
					probeProvider.CompatibilityMode = "codex-chat"
				}
				if combined.State != capabilitySupported {
					probeProvider.ReasoningWithTools = "disabled"
				}
				emitProviderProgress(emit, "codex", "running", "正在验证 Codex 与 apply_patch 完整链路", candidate)
				endToEnd = s.verifyCodexEndToEnd(ctx, probeProvider)
				emitProviderProgress(emit, "codex", string(endToEnd.State), "Codex 端到端验证完成", candidate)
			}
			report.CodexEndToEnd = &endToEnd
		}
		if basic.OK && candidate == ir.Anthropic {
			emitProviderProgress(emit, "codex", "running", "正在验证 Anthropic 经 AI Router 的 Codex 完整链路", candidate)
			endToEnd := s.verifyCodexEndToEnd(ctx, p)
			report.CodexEndToEnd = &endToEnd
			emitProviderProgress(emit, "codex", string(endToEnd.State), "Anthropic 经 Router 的 Codex 验证完成", candidate)
		}
		reports[candidate] = report

		compatibility := codexCompatibility(reports)
		if compatibility.Status == "full" && compatibility.Verified {
			break
		}
		if profile != "generic" {
			if (compatibility.Status == "full" || compatibility.Status == "degraded") && compatibility.Protocol == candidate {
				break
			}
		}
	}

	// If the fast path skipped an incomplete Responses facade but no usable
	// Chat endpoint exists, finish the Responses compatibility checks so the
	// provider is not left with an unnecessarily unverified result.
	if responses, ok := reports[ir.OpenAIResponses]; ok && responses.Basic.OK && responses.Tools == nil {
		chat, hasChat := reports[ir.OpenAIChat]
		if !hasChat || !chat.Basic.OK {
			p := config.Provider{
				Protocol: ir.OpenAIResponses, Profile: profile, BaseURL: baseURL,
				APIKey: apiKey, Models: models, AllowPrivateURL: allowPrivateURL,
				Timeout: providerProbeTimeout,
			}
			strategy := providerprofile.ProviderDetectionStrategy(profile)
			p.ToolChoiceMode = strategy.ToolChoiceMode
			p.ReasoningHistory = strategy.ReasoningHistory
			streaming, tools, reasoning, combined, roundTrip := s.runOpenAIAdvancedChecks(ctx, p, profile, emit)
			responses.Streaming = &streaming
			responses.Tools = &tools
			responses.Reasoning = &reasoning
			responses.ToolsWithReasoning = &combined
			responses.ToolRoundTrip = &roundTrip
			endToEnd := notTestedCheck("requires tools support and a non-deterministic streaming failure")
			if tools.State == capabilitySupported && streaming.State != capabilityUnsupported {
				p.CompatibilityMode = "codex-responses"
				if combined.State != capabilitySupported {
					p.ReasoningWithTools = "disabled"
				}
				endToEnd = s.verifyCodexEndToEnd(ctx, p)
			}
			responses.CodexEndToEnd = &endToEnd
			reports[ir.OpenAIResponses] = responses
		}
	}

	compatibility := codexCompatibility(reports)
	if profile == "xiaomi-mimo" && compatibility.Protocol != "" {
		compatibility.RecommendedToolChoiceMode = "auto-only"
		compatibility.RecommendedReasoningHistory = "preserve"
	}
	selected := selectProviderProtocol(reports, compatibility)
	emitProviderProgress(emit, "selection", "completed", "已生成协议与 Codex 兼容性结论", selected)
	modelReports := s.probeAdditionalModels(ctx, baseURL, apiKey, models, allowPrivateURL, selected, profile)
	if selected != "" && len(models) > 0 {
		if modelReports == nil {
			modelReports = make(map[string]modelCapabilitySummary, len(models))
		}
		modelReports[models[0]] = modelCapabilitySummary{Protocol: selected, Basic: reports[selected].Basic}
	}
	result := providerDetectionReport{
		DetectorVersion:    providerDetectorVersion,
		CheckedAt:          time.Now(),
		OK:                 selected != "",
		Protocol:           selected,
		Profile:            profile,
		ProfileVersion:     providerprofile.DetectionProfileVersion,
		Models:             models,
		LatencyMS:          time.Since(started).Milliseconds(),
		Attempts:           attempts,
		Protocols:          reports,
		CodexCompatibility: compatibility,
		ModelReports:       modelReports,
	}
	if selected != "" {
		result.Label = detectedProviderLabel(selected, profile)
	}
	return result
}

func shouldProbeOpenAIBasicsInParallel(profile string, candidates []ir.Protocol) bool {
	if profile != providerprofile.ProfileGeneric || len(candidates) != 2 {
		return false
	}
	seen := map[ir.Protocol]bool{}
	for _, candidate := range candidates {
		seen[candidate] = true
	}
	return seen[ir.OpenAIChat] && seen[ir.OpenAIResponses]
}

func (s *Server) runOpenAIAdvancedChecks(ctx context.Context, p config.Provider, profile string, emit providerProgressEmitter) (capabilityCheck, capabilityCheck, capabilityCheck, capabilityCheck, capabilityCheck) {
	// Keep at most two probes in flight, but put the Codex-critical streaming
	// and tool checks in the first phase. The reasoning-only and combined checks
	// then share the second phase instead of extending the critical path twice.
	var streaming capabilityCheck
	var tools capabilityCheck
	var initial sync.WaitGroup
	initial.Add(2)
	emitProviderProgress(emit, "capabilities", "running", "正在并行验证流式输出与 function tools", p.Protocol)
	go func() {
		defer initial.Done()
		streaming = s.runProviderCheck(ctx, p, probeStreaming)
	}()
	go func() {
		defer initial.Done()
		tools = s.runProviderToolNegotiation(ctx, p, probeTools, profile)
	}()
	initial.Wait()
	var reasoning capabilityCheck
	var combined capabilityCheck
	var advanced sync.WaitGroup
	advanced.Add(2)
	emitProviderProgress(emit, "capabilities", "running", "正在并行验证 reasoning 与工具组合", p.Protocol)
	go func() {
		defer advanced.Done()
		reasoning = s.runProviderCheck(ctx, p, probeReasoning)
	}()
	go func() {
		defer advanced.Done()
		combined = s.runProviderToolNegotiation(ctx, p, probeToolsWithReasoning, profile)
	}()
	advanced.Wait()
	tools = inferToolSupportFromCombinedCheck(tools, combined)
	roundTrip := notTestedCheck("requires a verified tool call")
	if tools.State == capabilitySupported && combined.State == capabilitySupported {
		emitProviderProgress(emit, "capabilities", "running", "正在验证多轮工具续接", p.Protocol)
		roundTrip = s.runProviderToolRoundTrip(ctx, p, combined, profile)
	} else if tools.State == capabilitySupported {
		roundTrip = notTestedCheck("tools with reasoning did not pass; Codex Router end-to-end will verify the degraded continuation path")
	}
	emitProviderProgress(emit, "capabilities", "completed", "协议能力协商完成", p.Protocol)
	return streaming, tools, reasoning, combined, roundTrip
}

func inferToolSupportFromCombinedCheck(tools, combined capabilityCheck) capabilityCheck {
	if tools.State == capabilitySupported || combined.State != capabilitySupported {
		return tools
	}
	inferred := combined
	inferred.Evidence = append(append([]string{}, combined.Evidence...), "tools_inferred_from_tools_with_reasoning")
	return inferred
}

func preferredProtocolCandidates(baseURL, profile string) []ir.Protocol {
	lowerBase := strings.ToLower(baseURL)
	strategy := providerprofile.ProviderDetectionStrategy(profile)
	switch {
	case strings.Contains(lowerBase, "anthropic"):
		candidates := []ir.Protocol{ir.Anthropic}
		for _, candidate := range strategy.PreferredProtocols {
			if candidate != ir.Anthropic {
				candidates = append(candidates, candidate)
			}
		}
		if profile == providerprofile.ProfileGeneric {
			candidates = append(candidates, ir.Gemini)
		}
		return candidates
	case strings.Contains(lowerBase, "generativelanguage"), strings.Contains(lowerBase, "gemini"):
		return []ir.Protocol{ir.Gemini, ir.OpenAIResponses, ir.OpenAIChat, ir.Anthropic}
	case profile != providerprofile.ProfileGeneric:
		return strategy.PreferredProtocols
	default:
		return strategy.PreferredProtocols
	}
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
		return codexCompatibilityReport{Status: "unavailable", Message: "认证、限流、配额、网络或超时导致检测无法完成", Confidence: 1}
	}
	if sawReachable {
		return codexCompatibilityReport{Status: "incompatible", Message: "已验证的 OpenAI 协议无法同时满足 Codex 的流式输出和 function tools 基本要求", Verified: true, Confidence: 0.9}
	}
	return codexCompatibilityReport{
		Status:     "unavailable",
		Message:    "未检测到可访问的 OpenAI 协议端点",
		Confidence: 0.9,
	}
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
	if rank[candidate.Status] > rank[current.Status] {
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

func (s *Server) probeAdditionalModels(ctx context.Context, baseURL, apiKey string, models []string, allowPrivateURL bool, protocolName ir.Protocol, profile string) map[string]modelCapabilitySummary {
	if protocolName == "" || len(models) < 2 {
		return nil
	}
	result := make(map[string]modelCapabilitySummary, len(models)-1)
	for _, model := range models[1:] {
		provider := config.Provider{Protocol: protocolName, Profile: profile, BaseURL: baseURL, APIKey: apiKey, Models: []string{model}, AllowPrivateURL: allowPrivateURL, Timeout: providerProbeTimeout}
		result[model] = modelCapabilitySummary{Protocol: protocolName, Basic: s.runProviderCheck(ctx, provider, probeBasic)}
	}
	return result
}

// verifyCodexDirect sends the Codex-specific Responses shape straight to the
// upstream. This is intentionally separate from verifyCodexEndToEnd: the latter
// proves that AI Router can repair/translate the provider, while this check is
// the gate for advertising Codex's official custom-provider path.
func (s *Server) verifyCodexDirect(ctx context.Context, p config.Provider) capabilityCheck {
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

func (s *Server) verifyCodexEndToEnd(ctx context.Context, p config.Provider) capabilityCheck {
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
		"model": model,
		"input": []any{
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

func (s *Server) runProviderCheck(ctx context.Context, p config.Provider, kind providerProbeKind) capabilityCheck {
	strategy := toolChoiceForced
	if p.Profile == "xiaomi-mimo" {
		strategy = toolChoiceAuto
	}
	return s.runProviderCheckWithStrategy(ctx, p, kind, strategy, nil)
}

type toolChoiceStrategy string

const (
	toolChoiceForced   toolChoiceStrategy = "forced"
	toolChoiceRequired toolChoiceStrategy = "required"
	toolChoiceAuto     toolChoiceStrategy = "auto"
)

func (s *Server) runProviderToolNegotiation(ctx context.Context, p config.Provider, kind providerProbeKind, profile string) capabilityCheck {
	profileStrategy := providerprofile.ProviderDetectionStrategy(profile)
	strategies := make([]toolChoiceStrategy, 0, len(profileStrategy.ToolChoiceStrategies))
	for _, value := range profileStrategy.ToolChoiceStrategies {
		strategies = append(strategies, toolChoiceStrategy(value))
	}
	var results []capabilityCheck
	for _, strategy := range strategies {
		result := s.runProviderCheckWithStrategy(ctx, p, kind, strategy, nil)
		result.Evidence = append(result.Evidence, "tool_choice="+string(strategy))
		results = append(results, result)
		if result.State == capabilitySupported || isAvailabilityError(result.ErrorCode) {
			return result
		}
		// A provider-level rejection of tools + reasoning is independent of
		// forced/required/auto tool_choice. Retrying the same incompatible
		// combination was the main source of 30–45 second false "hangs".
		if kind == probeToolsWithReasoning &&
			(result.ErrorCode == "tools_with_reasoning_unsupported" ||
				result.ErrorCode == "unsupported_parameter") {
			return result
		}
		// A real tool call already proves that tool-choice negotiation worked. If
		// the same response contains no reasoning evidence, retrying with another
		// tool_choice mode cannot establish the missing combined capability.
		if result.ErrorCode == "combined_reasoning_not_observed" {
			return result
		}
		if ctx.Err() != nil {
			return result
		}
	}
	for _, result := range results {
		if result.State == capabilityInconclusive {
			result.Error = "provider accepted tools, but an automatic tool call was not observed"
			result.ErrorCode = "tool_not_observed"
			result.Confidence = 0.4
			return result
		}
	}
	if len(results) > 0 {
		result := results[len(results)-1]
		result.State = capabilityUnsupported
		result.OK = false
		result.Confidence = 0.9
		return result
	}
	return notTestedCheck("tool negotiation did not run")
}

func (s *Server) runProviderToolRoundTrip(ctx context.Context, p config.Provider, first capabilityCheck, profile string) capabilityCheck {
	method, endpoint, payload, err := providerRoundTripRequest(p, first.body, profile)
	if err != nil {
		return capabilityCheck{State: capabilityInconclusive, Confidence: 0.4, ErrorCode: "round_trip_unavailable", Error: err.Error()}
	}
	result := s.runProviderCheckWithStrategy(ctx, p, probeToolRoundTrip, toolChoiceAuto, &probeRequestOverride{Method: method, Endpoint: endpoint, Payload: payload})
	if result.State == capabilitySupported {
		result.Evidence = append(result.Evidence, "tool_result_round_trip_succeeded")
		if providerReasoningObserved(p.Protocol, first.body) {
			result.Evidence = append(result.Evidence, "reasoning_history_preserved")
		}
	}
	return result
}

type probeRequestOverride struct {
	Method   string
	Endpoint string
	Payload  []byte
}

func (s *Server) runProviderCheckWithStrategy(ctx context.Context, p config.Provider, kind providerProbeKind, strategy toolChoiceStrategy, override *probeRequestOverride) capabilityCheck {
	started := time.Now()
	if p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	method, endpoint, payload, err := providerProbeRequestWithStrategy(p, kind, strategy)
	if override != nil {
		method, endpoint, payload = override.Method, override.Endpoint, override.Payload
		err = nil
	}
	if err != nil {
		return failedCapability(started, "invalid_probe", err.Error())
	}
	if err = secure.ValidatePublicTarget(ctx, endpoint, p.AllowPrivateURL); err != nil {
		return failedCapability(started, "blocked_target", err.Error())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return failedCapability(started, "invalid_probe", err.Error())
	}
	request.Header.Set("accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("content-type", "application/json")
	}
	applyProviderProbeHeaders(request, p)
	client := s.RestrictedClient
	if p.AllowPrivateURL {
		client = s.Client
	}
	response, err := client.Do(request)
	if err != nil {
		code := "network_error"
		if errorsIsTimeout(ctx, err) {
			code = "timeout"
		} else if ctx.Err() == context.Canceled {
			code = "canceled"
		}
		return failedCapability(started, code, err.Error())
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	result := capabilityCheck{
		OK:          response.StatusCode >= 200 && response.StatusCode < 300,
		State:       capabilitySupported,
		Confidence:  1,
		Status:      response.StatusCode,
		LatencyMS:   time.Since(started).Milliseconds(),
		ContentType: response.Header.Get("content-type"),
		body:        body,
	}
	if !result.OK {
		result.State = capabilityUnsupported
		result.Confidence = 0.9
		result.ErrorCode, result.Error = classifyProviderError(response.StatusCode, body)
		if isAvailabilityError(result.ErrorCode) {
			result.State = capabilityInconclusive
		}
		return result
	}
	if kind == probeStreaming {
		if code, message := validateProviderSSE(p.Protocol, result.ContentType, body); code != "" {
			result.OK = false
			result.State = capabilityInconclusive
			result.Confidence = 0.4
			result.ErrorCode = code
			result.Error = message
			return result
		}
		result.Evidence = append(result.Evidence, "valid_sse_lifecycle")
		return result
	}
	if kind == probeModels {
		var value map[string]any
		if json.Unmarshal(body, &value) != nil || common.Array(value["data"]) == nil {
			result.OK = false
			result.State = capabilityInconclusive
			result.Confidence = 0.4
			result.ErrorCode = "schema_mismatch"
			result.Error = "model discovery response is missing data"
			return result
		}
		result.Evidence = append(result.Evidence, "valid_model_list")
		return result
	}
	if code, message := validateProviderJSON(p.Protocol, body); code != "" {
		result.OK = false
		result.State = capabilityInconclusive
		result.Confidence = 0.4
		result.ErrorCode = code
		result.Error = message
		return result
	}
	result.Evidence = append(result.Evidence, "valid_"+string(p.Protocol)+"_response")
	if kind == probeTools || kind == probeToolsWithReasoning {
		if !providerToolCallObserved(p.Protocol, body) {
			result.OK = false
			result.State = capabilityInconclusive
			result.Confidence = 0.35
			result.ErrorCode = "tool_not_observed"
			result.Error = "provider accepted tools but did not emit a tool call"
			return result
		}
		result.Evidence = append(result.Evidence, "tool_call_observed")
	}
	if kind == probeToolsWithReasoning {
		if !providerReasoningObserved(p.Protocol, body) {
			result.OK = false
			result.State = capabilityInconclusive
			result.Confidence = 0.45
			result.ErrorCode = "combined_reasoning_not_observed"
			result.Error = "provider emitted a tool call, but returned no evidence that reasoning was active in the same request"
			return result
		}
		result.Evidence = append(result.Evidence, "reasoning_observed_with_tool_call")
	}
	if kind == probeReasoning {
		if !providerReasoningObserved(p.Protocol, body) {
			result.OK = false
			result.State = capabilityInconclusive
			result.Confidence = 0.5
			result.ErrorCode = "reasoning_not_observed"
			result.Error = "reasoning parameter was accepted, but no reasoning evidence was returned"
			return result
		}
		result.Evidence = append(result.Evidence, "reasoning_observed")
	}
	return result
}

func failedCapability(started time.Time, code, message string) capabilityCheck {
	return capabilityCheck{State: capabilityInconclusive, Confidence: 1, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: code, Error: message}
}

func errorsIsTimeout(ctx context.Context, err error) bool {
	if ctx.Err() == context.DeadlineExceeded {
		return true
	}
	type timeoutError interface{ Timeout() bool }
	value, ok := err.(timeoutError)
	return ok && value.Timeout()
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

func (s *Server) minimalProviderTest(ctx context.Context, p config.Provider) (int, error) {
	result := s.runProviderCheck(ctx, p, probeBasic)
	if isAvailabilityError(result.ErrorCode) || result.ErrorCode == "blocked_target" || result.ErrorCode == "invalid_probe" {
		return result.Status, fmt.Errorf("%s", result.Error)
	}
	return result.Status, nil
}
