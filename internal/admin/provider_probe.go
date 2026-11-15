package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/common"
	"github.com/zbss/airoute/internal/protocol/ir"
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
	providerDetectorVersion                     = 8
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
	ctx = providerprofile.WithProbeSession(ctx)
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
				preserveReasoning := providerprofile.IsOpenCodeGo(baseURL) && combined.State != capabilityUnsupported
				if preserveReasoning {
					probeProvider.ReasoningHistory = "preserve"
				}
				if candidate == ir.OpenAIChat && combined.State != capabilitySupported && !preserveReasoning {
					probeProvider.CompatibilityMode = "codex-chat"
				}
				if combined.State != capabilitySupported && !preserveReasoning {
					probeProvider.ReasoningWithTools = "disabled"
				}
				emitProviderProgress(emit, "codex", "running", "正在验证 Codex 与 apply_patch 完整链路", candidate)
				endToEnd = s.verifyCodexEndToEnd(ctx, probeProvider)
				if preserveReasoning {
					endToEnd.Evidence = append(endToEnd.Evidence, "reasoning_configuration_preserved")
				}
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
				preserveReasoning := providerprofile.IsOpenCodeGo(baseURL) && combined.State != capabilityUnsupported
				if preserveReasoning {
					p.ReasoningHistory = "preserve"
				}
				if combined.State != capabilitySupported && !preserveReasoning {
					p.ReasoningWithTools = "disabled"
				}
				endToEnd = s.verifyCodexEndToEnd(ctx, p)
				if preserveReasoning {
					endToEnd.Evidence = append(endToEnd.Evidence, "reasoning_configuration_preserved")
				}
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
	ctx = providerprofile.WithProbeSession(ctx)
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
		if p.APIKey != "" {
			result.Error = strings.ReplaceAll(result.Error, p.APIKey, "[REDACTED]")
		}
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

func (s *Server) minimalProviderTest(ctx context.Context, p config.Provider) (int, error) {
	result := s.runProviderCheck(ctx, p, probeBasic)
	if isAvailabilityError(result.ErrorCode) || result.ErrorCode == "blocked_target" || result.ErrorCode == "invalid_probe" {
		return result.Status, fmt.Errorf("%s", result.Error)
	}
	return result.Status, nil
}
