package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const managedBlockStart = "# >>> AI Router managed configuration (airoute.codex.v1)"
const managedBlockEnd = "# <<< AI Router managed configuration"

func read(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return raw, err
}

func decodeDesired(raw json.RawMessage) (DesiredConfig, error) {
	var desired DesiredConfig
	if err := json.Unmarshal(raw, &desired); err != nil {
		return desired, fmt.Errorf("Codex 配置无效: %w", err)
	}
	desired.BaseURL = strings.TrimRight(strings.TrimSpace(desired.BaseURL), "/")
	if !strings.HasSuffix(desired.BaseURL, "/v1") {
		desired.BaseURL += "/v1"
	}
	desired.APIKey = strings.TrimSpace(desired.APIKey)
	desired.Model = strings.TrimSpace(desired.Model)
	desired.IntegrationMode = strings.TrimSpace(desired.IntegrationMode)
	if desired.IntegrationMode == "" {
		desired.IntegrationMode = "compatibility"
	}
	if desired.IntegrationMode != "direct" && desired.IntegrationMode != "passthrough" && desired.IntegrationMode != "compatibility" {
		return desired, errors.New("Codex 接入模式必须是 direct、passthrough 或 compatibility")
	}
	desired.ProviderID = strings.TrimSpace(desired.ProviderID)
	desired.ProviderName = strings.TrimSpace(desired.ProviderName)
	desired.ProviderBaseURL = strings.TrimRight(strings.TrimSpace(desired.ProviderBaseURL), "/")
	if desired.ProviderBaseURL != "" && !strings.HasSuffix(desired.ProviderBaseURL, "/v1") {
		desired.ProviderBaseURL += "/v1"
	}
	desired.ProviderModel = strings.TrimSpace(desired.ProviderModel)
	if desired.IntegrationMode == "direct" {
		if desired.ProviderID == "" || desired.ProviderBaseURL == "" || desired.ProviderModel == "" {
			return desired, errors.New("官方直连需要唯一的上游 Provider、API 地址和模型")
		}
		return desired, nil
	}
	if desired.BaseURL == "/v1" || desired.Model == "" {
		return desired, errors.New("网关地址和默认模型为必填项")
	}
	if desired.APIKey == "" {
		desired.APIKey = "airoute-local"
	}
	return desired, nil
}

func tomlString(value string) string { return strconv.Quote(value) }

func parseString(line string) string {
	_, value, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
	if decoded, err := strconv.Unquote(value); err == nil {
		return decoded
	}
	return strings.Trim(value, "'\"")
}

func managed(raw []byte) map[string]any {
	result := map[string]any{}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
			continue
		}
		key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
		if section == "" && key == "model" {
			result["model"] = parseString(trimmed)
		}
		if section == "" && key == "model_provider" {
			result["provider"] = parseString(trimmed)
		}
		if section == "" && key == "model_catalog_json" {
			result["model_catalog_json"] = parseString(trimmed)
		}
		if section == "[model_providers.airoute]" || section == "[model_providers.airoute-direct]" {
			switch key {
			case "base_url":
				result["base_url"] = parseString(trimmed)
			case "experimental_bearer_token":
				result["api_key"] = parseString(trimmed)
			}
		}
	}
	return result
}

func stripManaged(current []byte) []byte {
	lines := strings.Split(string(current), "\n")
	out := make([]string, 0, len(lines))
	section := ""
	skippingAiroute := false
	skippingOwnedBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == managedBlockStart {
			skippingOwnedBlock = true
			continue
		}
		if skippingOwnedBlock {
			if trimmed == managedBlockEnd {
				skippingOwnedBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
			skippingAiroute = section == "[model_providers.airoute]" || strings.HasPrefix(section, "[model_providers.airoute.") ||
				section == "[model_providers.airoute-direct]" || strings.HasPrefix(section, "[model_providers.airoute-direct.")
			if skippingAiroute {
				continue
			}
		}
		if skippingAiroute {
			continue
		}
		key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
		if section == "" && isManagedRootKey(key) {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.TrimSpace(strings.Join(out, "\n")))
}

func sectionHasAnyKey(lines []string, target string, keys ...string) bool {
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
			continue
		}
		if section != target {
			continue
		}
		key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
		for _, candidate := range keys {
			if key == candidate {
				return true
			}
		}
	}
	return false
}

func replaceTOMLKey(line, oldKey, newKey string) string {
	index := strings.Index(line, oldKey)
	if index < 0 {
		return line
	}
	return line[:index] + newKey + line[index+len(oldKey):]
}

func isGeneratedOrphanCodexAppsSection(lines []string) bool {
	const target = "[mcp_servers.codex_apps]"
	section := ""
	assignments := make([]string, 0, 1)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
			continue
		}
		if section != target || trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "=") {
			continue
		}
		assignments = append(assignments, strings.Join(strings.Fields(trimmed), " "))
	}
	return len(assignments) == 1 && assignments[0] == "startup_timeout_sec = 120"
}

func normalizeCodexRuntimeSettings(current []byte) []byte {
	lines := strings.Split(string(current), "\n")
	const featuresSection = "[features]"
	const codexAppsSection = "[mcp_servers.codex_apps]"
	hasHooks := sectionHasAnyKey(lines, featuresSection, "hooks")
	removeOrphanCodexApps := isGeneratedOrphanCodexAppsSection(lines)

	out := make([]string, 0, len(lines))
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
		}
		if removeOrphanCodexApps && section == codexAppsSection {
			continue
		}
		key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
		if section == featuresSection && key == "codex_hooks" {
			if hasHooks {
				continue
			}
			line = replaceTOMLKey(line, "codex_hooks", "hooks")
			hasHooks = true
		}
		out = append(out, line)
	}
	return []byte(strings.TrimSpace(strings.Join(out, "\n")))
}

func merge(current []byte, desired DesiredConfig, catalogPath string) []byte {
	body := string(stripManaged(normalizeCodexRuntimeSettings(current)))
	providerID := "airoute"
	model := desired.Model
	if desired.IntegrationMode == "direct" {
		providerID = "airoute-direct"
		model = desired.ProviderModel
	}
	managedRoot := strings.Join([]string{
		managedBlockStart,
		"model = " + tomlString(model),
		"model_provider = " + tomlString(providerID),
		"model_catalog_json = " + tomlString(catalogPath),
		"model_reasoning_effort = \"high\"",
		"model_supports_reasoning_summaries = true",
		"model_reasoning_summary = \"none\"",
		"model_context_window = " + strconv.Itoa(modelContextWindow(model)),
		"web_search = \"disabled\"",
		managedBlockEnd,
	}, "\n")
	var providerLines []string
	if desired.IntegrationMode == "direct" {
		name := desired.ProviderName
		if name == "" {
			name = desired.ProviderID
		}
		providerLines = []string{
			managedBlockStart,
			"[model_providers.airoute-direct]",
			"name = " + tomlString(name+"（Codex 官方直连）"),
			"base_url = " + tomlString(desired.ProviderBaseURL),
			"wire_api = \"responses\"",
			"",
			"[model_providers.airoute-direct.auth]",
			"command = " + tomlString(desired.AirExecutable),
			"args = [" + tomlString("provider-token") + ", " + tomlString("--config") + ", " + tomlString(desired.RouterConfigPath) + ", " + tomlString("--provider") + ", " + tomlString(desired.ProviderID) + "]",
			managedBlockEnd,
		}
	} else {
		providerLines = []string{
			managedBlockStart,
			"[model_providers.airoute]",
			"name = \"AI Router\"",
			"base_url = " + tomlString(desired.BaseURL),
			"wire_api = \"responses\"",
			"requires_openai_auth = false",
			"experimental_bearer_token = " + tomlString(desired.APIKey),
			managedBlockEnd,
		}
	}
	managedProvider := strings.Join(providerLines, "\n")
	if body == "" {
		return []byte(managedRoot + "\n\n" + managedProvider + "\n")
	}
	return []byte(managedRoot + "\n\n" + body + "\n\n" + managedProvider + "\n")
}

func validateCodexConfig(raw []byte) error {
	var document map[string]any
	if err := toml.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("Codex 配置不是有效 TOML: %w", err)
	}
	providers, _ := document["model_providers"].(map[string]any)
	for _, providerID := range []string{"airoute", "airoute-direct"} {
		airoute, _ := providers[providerID].(map[string]any)
		if airoute == nil || !bytes.Contains(raw, []byte(managedBlockStart)) {
			continue
		}
		baseURL, _ := airoute["base_url"].(string)
		wireAPI, _ := airoute["wire_api"].(string)
		if strings.TrimSpace(baseURL) == "" || wireAPI != "responses" {
			return errors.New("AI Router Codex provider 缺少有效的 base_url 或 responses wire_api")
		}
	}
	mcpServers, _ := document["mcp_servers"].(map[string]any)
	for name, value := range mcpServers {
		server, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("mcp_servers.%s 不是有效的 MCP 配置", name)
		}
		command, hasCommand := nonEmptyString(server["command"])
		urlValue, hasURL := nonEmptyString(server["url"])
		if !hasCommand && !hasURL {
			return fmt.Errorf("mcp_servers.%s 缺少 transport：必须设置 command 或 url", name)
		}
		if hasCommand && hasURL {
			return fmt.Errorf("mcp_servers.%s transport 冲突：command 与 url 不能同时设置", name)
		}
		_ = command
		_ = urlValue
	}
	return nil
}

func nonEmptyString(value any) (string, bool) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	return text, ok && text != ""
}

func isManagedRootKey(key string) bool {
	switch key {
	case "model", "model_provider", "model_catalog_json", "model_reasoning_effort", "model_supports_reasoning_summaries", "model_reasoning_summary", "model_context_window", "web_search":
		return true
	default:
		return false
	}
}
