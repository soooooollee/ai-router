package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/safefile"
)

const backupMarker = ".airoute.bak."
const catalogName = "airoute-model-catalog.json"
const backupBundleFormat = "airoute.codex.backup.v1"
const managedBlockStart = "# >>> AI Router managed configuration (airoute.codex.v1)"
const managedBlockEnd = "# <<< AI Router managed configuration"

type DesiredConfig struct {
	BaseURL          string   `json:"base_url"`
	APIKey           string   `json:"api_key"`
	Model            string   `json:"model"`
	Models           []string `json:"models,omitempty"`
	IntegrationMode  string   `json:"integration_mode,omitempty"`
	ProviderID       string   `json:"provider_id,omitempty"`
	ProviderName     string   `json:"provider_name,omitempty"`
	ProviderBaseURL  string   `json:"provider_base_url,omitempty"`
	ProviderModel    string   `json:"provider_model,omitempty"`
	AirExecutable    string   `json:"-"`
	RouterConfigPath string   `json:"-"`
}

type Adapter struct {
	ConfigPath         string
	RouterConfigPath   string
	AirExecutable      string
	HTTPClient         *http.Client
	LookPath           func(string) (string, error)
	DesktopExecutables []string
}

type fileSnapshot struct {
	Exists  bool   `json:"exists"`
	Content []byte `json:"content,omitempty"`
}

type backupBundle struct {
	Format  string       `json:"format"`
	Config  fileSnapshot `json:"config"`
	Catalog fileSnapshot `json:"catalog"`
}

func New() *Adapter {
	return &Adapter{HTTPClient: &http.Client{Timeout: 60 * time.Second}, LookPath: exec.LookPath}
}

func (a *Adapter) Manifest() application.Manifest {
	return application.Manifest{ID: "codex", Name: "Codex CLI / ChatGPT App", Description: "Codex CLI 与 ChatGPT App 的共享配置", Status: "available", Capabilities: []application.Capability{application.CapabilityDetect, application.CapabilityConfigure, application.CapabilityPreview, application.CapabilityVerify, application.CapabilityRollback, application.CapabilityCleanup, application.CapabilityEdit}, ConfigFormat: "toml"}
}

func (a *Adapter) path() (string, error) {
	if value := strings.TrimSpace(a.ConfigPath); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv("AIROUTE_CODEX_CONFIG_PATH")); value != "" {
		return value, nil
	}
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func (a *Adapter) executable() (string, error) {
	path, err := a.cliExecutable()
	if err == nil {
		return path, nil
	}
	return a.desktopExecutable()
}

func (a *Adapter) cliExecutable() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AIROUTE_CODEX_EXECUTABLE")); value != "" {
		if !isDesktopExecutable(value) {
			return value, nil
		}
		return "", exec.ErrNotFound
	}
	lookPath := a.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath("codex")
	if err != nil {
		return "", err
	}
	if isDesktopExecutable(path) {
		return "", exec.ErrNotFound
	}
	return path, nil
}

func (a *Adapter) desktopExecutable() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AIROUTE_CHATGPT_CODEX_EXECUTABLE")); value != "" {
		return value, nil
	}
	candidates := a.DesktopExecutables
	if len(candidates) == 0 {
		candidates = desktopExecutableCandidates()
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func desktopExecutableCandidates() []string {
	candidates := []string{}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/ChatGPT.app/Contents/Resources/codex",
			"/Applications/Codex.app/Contents/Resources/codex",
		)
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, "Applications", "ChatGPT.app", "Contents", "Resources", "codex"),
				filepath.Join(home, "Applications", "Codex.app", "Contents", "Resources", "codex"),
			)
		}
	}
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			candidates = append(candidates,
				filepath.Join(localAppData, "Programs", "ChatGPT", "resources", "codex.exe"),
				filepath.Join(localAppData, "ChatGPT", "resources", "codex.exe"),
			)
		}
	}
	return candidates
}

func isDesktopExecutable(path string) bool {
	paths := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		paths = append(paths, resolved)
	}
	for _, candidate := range paths {
		normalized := strings.ToLower(filepath.ToSlash(candidate))
		if strings.Contains(normalized, "/chatgpt.app/contents/resources/codex") ||
			strings.Contains(normalized, "/codex.app/contents/resources/codex") ||
			strings.Contains(normalized, "/chatgpt/resources/codex.exe") {
			return true
		}
	}
	return false
}

func (a *Adapter) Detect(ctx context.Context) (application.Detection, error) {
	cliPath, _ := a.cliExecutable()
	desktopPath, _ := a.desktopExecutable()
	var cli executableDetection
	var desktop executableDetection
	var group sync.WaitGroup
	if cliPath != "" {
		group.Add(1)
		go func() {
			defer group.Done()
			cli = detectExecutable(ctx, cliPath)
		}()
	}
	if desktopPath != "" {
		group.Add(1)
		go func() {
			defer group.Done()
			desktop = detectExecutable(ctx, desktopPath)
		}()
	}
	group.Wait()

	message := "未检测到 Codex CLI 或 ChatGPT App"
	switch {
	case cli.installed && desktop.installed:
		message = "Codex CLI 与 ChatGPT App 已安装，共享同一配置"
	case cli.installed:
		message = "Codex CLI 已安装；ChatGPT App 未检测到，仍使用同一配置入口"
	case desktop.installed:
		message = "ChatGPT App 已安装；Codex CLI 未检测到，仍使用同一配置入口"
	case cli.timedOut || desktop.timedOut:
		message = "Codex CLI 或 ChatGPT App 响应超时"
	case cli.path != "" || desktop.path != "":
		message = "检测到 Codex CLI 或 ChatGPT App，但 Codex 无法正常运行"
	}
	versions := make([]string, 0, 2)
	for _, version := range []string{cli.version, desktop.version} {
		if version != "" && (len(versions) == 0 || versions[0] != version) {
			versions = append(versions, version)
		}
	}
	executable := cli.path
	if executable == "" {
		executable = desktop.path
	}
	return application.Detection{
		Installed:  cli.installed || desktop.installed,
		Executable: executable,
		Version:    strings.Join(versions, " / "),
		Message:    message,
	}, nil
}

type executableDetection struct {
	path      string
	version   string
	installed bool
	timedOut  bool
}

func detectExecutable(ctx context.Context, path string) executableDetection {
	result := executableDetection{path: path}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, commandErr := exec.CommandContext(checkCtx, path, "--version").CombinedOutput()
	if commandErr != nil {
		result.timedOut = errors.Is(checkCtx.Err(), context.DeadlineExceeded)
		return result
	}
	result.installed = true
	result.version = strings.TrimSpace(string(output))
	return result
}

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

func replaceManagedCatalogPath(raw []byte, path string) []byte {
	lines := strings.Split(string(raw), "\n")
	section := ""
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
		}
		key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
		if section == "" && key == "model_catalog_json" {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[index] = indent + "model_catalog_json = " + tomlString(path)
			break
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func envWithOverride(name, value string) []string {
	prefix := name + "="
	environment := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, prefix) {
			environment = append(environment, item)
		}
	}
	return append(environment, prefix+value)
}

func (a *Adapter) preflightWithCodex(ctx context.Context, configRaw, catalogRaw []byte) error {
	executable, err := a.executable()
	if err != nil {
		return nil
	}
	directory, err := os.MkdirTemp("", "airoute-codex-preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	catalogPath := filepath.Join(directory, catalogName)
	configPath := filepath.Join(directory, "config.toml")
	stagedConfig := replaceManagedCatalogPath(configRaw, catalogPath)
	if err = safefile.AtomicWrite(catalogPath, append(bytes.TrimSpace(catalogRaw), '\n'), 0600); err != nil {
		return err
	}
	if err = safefile.AtomicWrite(configPath, stagedConfig, 0600); err != nil {
		return err
	}
	checkContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(checkContext, executable, "features", "list")
	command.Env = envWithOverride("CODEX_HOME", directory)
	output, commandErr := command.CombinedOutput()
	if commandErr == nil {
		return nil
	}
	if errors.Is(checkContext.Err(), context.DeadlineExceeded) {
		return errors.New("Codex 配置预检超时")
	}
	message := strings.TrimSpace(string(output))
	if len(message) > 2000 {
		message = message[:2000]
	}
	if message == "" {
		message = commandErr.Error()
	}
	return fmt.Errorf("Codex 拒绝加载生成的配置: %s", message)
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

func modelContextWindow(model string) int {
	if strings.Contains(strings.ToLower(model), "mimo-v2.5") {
		return 1048576
	}
	return 262144
}

func catalogModels(desired DesiredConfig) []string {
	if desired.IntegrationMode == "direct" {
		return []string{desired.ProviderModel}
	}
	seen := map[string]bool{}
	models := make([]string, 0, len(desired.Models)+1)
	for _, model := range append([]string{desired.Model}, desired.Models...) {
		model = strings.TrimSpace(model)
		if model != "" && !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	return models
}

func modelCatalog(desired DesiredConfig) ([]byte, error) {
	models := make([]map[string]any, 0, len(desired.Models)+1)
	for priority, model := range catalogModels(desired) {
		mimo := strings.Contains(strings.ToLower(model), "mimo-v2.5")
		contextWindow := modelContextWindow(model)
		levels := []map[string]string{
			{"effort": "low", "description": "Low reasoning"},
			{"effort": "medium", "description": "Medium reasoning"},
			{"effort": "high", "description": "High reasoning"},
		}
		defaultLevel := "medium"
		modalities := []string{"text", "image"}
		supportVerbosity := true
		if mimo {
			levels = []map[string]string{
				{"effort": "none", "description": "No reasoning"},
				{"effort": "high", "description": "High reasoning"},
			}
			defaultLevel = "high"
			supportVerbosity = false
			if strings.Contains(strings.ToLower(model), "pro") {
				modalities = []string{"text"}
			}
		}
		models = append(models, map[string]any{
			"slug":                             model,
			"display_name":                     model,
			"description":                      "AI Router route " + model,
			"base_instructions":                "You are Codex, a coding agent.",
			"context_window":                   contextWindow,
			"max_context_window":               contextWindow,
			"effective_context_window_percent": 95,
			"default_reasoning_level":          defaultLevel,
			"supported_reasoning_levels":       levels,
			"supports_reasoning_summaries":     true,
			"default_reasoning_summary":        "none",
			"support_verbosity":                supportVerbosity,
			"supports_parallel_tool_calls":     false,
			"supports_search_tool":             false,
			"supports_image_detail_original":   len(modalities) > 1,
			"input_modalities":                 modalities,
			"apply_patch_tool_type":            "freeform",
			"shell_type":                       "shell_command",
			"web_search_tool_type":             "text",
			"supported_in_api":                 true,
			"visibility":                       "list",
			"priority":                         priority,
			"truncation_policy":                map[string]any{"mode": "tokens", "limit": 10000},
			"additional_speed_tiers":           []any{},
			"service_tiers":                    []any{},
			"experimental_supported_tools":     []any{},
		})
	}
	return json.MarshalIndent(map[string]any{"models": models}, "", "  ")
}

func snapshot(path string) (fileSnapshot, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{Exists: true, Content: raw}, nil
}

func restoreSnapshot(path string, state fileSnapshot) error {
	if !state.Exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return safefile.AtomicWrite(path, state.Content, 0600)
}

func createBundleBackup(configPath, catalogPath string) (string, error) {
	configState, err := snapshot(configPath)
	if err != nil {
		return "", err
	}
	catalogState, err := snapshot(catalogPath)
	if err != nil {
		return "", err
	}
	if !configState.Exists && !catalogState.Exists {
		return "", nil
	}
	raw, err := json.Marshal(backupBundle{Format: backupBundleFormat, Config: configState, Catalog: catalogState})
	if err != nil {
		return "", err
	}
	return safefile.BackupData(configPath, backupMarker, raw)
}

func backupName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

func previewJSON(raw []byte) json.RawMessage {
	encoded, _ := json.Marshal(string(raw))
	return encoded
}

func lineDiff(before, after []byte) string {
	left, right := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	lines := make([]string, 0)
	for i := 0; i < max(len(left), len(right)) && len(lines) < 240; i++ {
		var a, b string
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		if a == b {
			continue
		}
		if i < len(left) {
			lines = append(lines, "- "+a)
		}
		if i < len(right) {
			lines = append(lines, "+ "+b)
		}
	}
	if len(lines) == 0 {
		return "没有配置变化"
	}
	return strings.Join(lines, "\n")
}

func (a *Adapter) compose(raw json.RawMessage) (string, []byte, []byte, DesiredConfig, error) {
	desired, err := decodeDesired(raw)
	if err != nil {
		return "", nil, nil, desired, err
	}
	if desired.IntegrationMode == "direct" {
		desired.RouterConfigPath = strings.TrimSpace(a.RouterConfigPath)
		if desired.RouterConfigPath == "" {
			return "", nil, nil, desired, errors.New("AI Router 未提供运行配置路径，无法安全生成官方直连认证命令")
		}
		desired.AirExecutable = strings.TrimSpace(a.AirExecutable)
		if desired.AirExecutable == "" {
			desired.AirExecutable, err = os.Executable()
			if err != nil {
				return "", nil, nil, desired, fmt.Errorf("定位 air 可执行文件: %w", err)
			}
		}
	}
	path, err := a.path()
	if err != nil {
		return "", nil, nil, desired, err
	}
	current, err := read(path)
	if err != nil {
		return "", nil, nil, desired, err
	}
	catalogPath := filepath.Join(filepath.Dir(path), catalogName)
	next := merge(current, desired, catalogPath)
	if err = validateCodexConfig(next); err != nil {
		return "", nil, nil, desired, err
	}
	return path, current, next, desired, nil
}

func (a *Adapter) Read(ctx context.Context) (application.State, error) {
	path, err := a.path()
	if err != nil {
		return application.State{}, err
	}
	raw, err := read(path)
	if err != nil {
		return application.State{}, err
	}
	detection, err := a.Detect(ctx)
	if err != nil {
		return application.State{}, err
	}
	state := managed(raw)
	provider, _ := state["provider"].(string)
	if provider == "airoute-direct" {
		state["integration_mode"] = "direct"
	} else if provider == "airoute" {
		state["integration_mode"] = "compatibility"
	}
	return application.State{Manifest: a.Manifest(), Detection: detection, Path: path, Exists: len(bytes.TrimSpace(raw)) > 0, Managed: state, PreservedFields: len(strings.Split(strings.TrimSpace(string(raw)), "\n")), Synced: (provider == "airoute" || provider == "airoute-direct") && state["base_url"] != nil && state["model"] != nil}, nil
}

func (a *Adapter) ConfigurationSynced(_ context.Context) (bool, error) {
	path, err := a.path()
	if err != nil {
		return false, err
	}
	raw, err := read(path)
	if err != nil {
		return false, err
	}
	state := managed(raw)
	provider, _ := state["provider"].(string)
	baseURL, _ := state["base_url"].(string)
	model, _ := state["model"].(string)
	return (provider == "airoute" || provider == "airoute-direct") && strings.TrimSpace(baseURL) != "" && strings.TrimSpace(model) != "", nil
}

func (a *Adapter) Preview(ctx context.Context, raw json.RawMessage) (application.Preview, error) {
	path, current, next, desired, err := a.compose(raw)
	if err != nil {
		return application.Preview{}, err
	}
	catalog, err := modelCatalog(desired)
	if err != nil {
		return application.Preview{}, err
	}
	if err = a.preflightWithCodex(ctx, next, catalog); err != nil {
		return application.Preview{}, err
	}
	return application.Preview{Path: path, Current: previewJSON(current), Content: previewJSON(next), Diff: lineDiff(current, next), PreservedFields: len(strings.Split(strings.TrimSpace(string(current)), "\n")), WillCreateBackup: len(bytes.TrimSpace(current)) > 0}, nil
}

func (a *Adapter) Apply(ctx context.Context, raw json.RawMessage) (application.ApplyResult, error) {
	path, current, next, desired, err := a.compose(raw)
	if err != nil {
		return application.ApplyResult{}, err
	}
	catalogPath := filepath.Join(filepath.Dir(path), catalogName)
	catalog, err := modelCatalog(desired)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = a.preflightWithCodex(ctx, next, catalog); err != nil {
		return application.ApplyResult{}, err
	}
	previousCatalog, err := snapshot(catalogPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := createBundleBackup(path, catalogPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(catalogPath, append(catalog, '\n'), 0600); err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(path, next, 0600); err != nil {
		_ = restoreSnapshot(catalogPath, previousCatalog)
		return application.ApplyResult{}, err
	}
	state := managed(next)
	expectedProvider := "airoute"
	if desired.IntegrationMode == "direct" {
		expectedProvider = "airoute-direct"
	}
	if state["provider"] != expectedProvider || state["model"] == nil {
		_ = restoreSnapshot(catalogPath, previousCatalog)
		_ = restoreSnapshot(path, fileSnapshot{Exists: len(current) > 0, Content: current})
		return application.ApplyResult{}, errors.New("写入后的 Codex 配置无效")
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: backupName(backup)}, nil
}

func (a *Adapter) ApplyRaw(_ context.Context, input application.RawConfig) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	next := []byte(strings.TrimSpace(input.Content) + "\n")
	if err = validateCodexConfig(next); err != nil {
		return application.ApplyResult{}, err
	}
	catalogPath := filepath.Join(filepath.Dir(path), catalogName)
	previousCatalog, err := snapshot(catalogPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := createBundleBackup(path, catalogPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	state := managed(next)
	if (state["provider"] == "airoute" || state["provider"] == "airoute-direct") && state["model"] != nil && len(input.Config) > 0 {
		desired, decodeErr := decodeDesired(input.Config)
		if decodeErr != nil {
			return application.ApplyResult{}, decodeErr
		}
		desired.Model, _ = state["model"].(string)
		if value, ok := state["base_url"].(string); ok && value != "" {
			desired.BaseURL = value
		}
		if value, ok := state["api_key"].(string); ok && value != "" {
			desired.APIKey = value
		}
		catalog, catalogErr := modelCatalog(desired)
		if catalogErr != nil {
			return application.ApplyResult{}, catalogErr
		}
		if err = safefile.AtomicWrite(catalogPath, append(catalog, '\n'), 0600); err != nil {
			return application.ApplyResult{}, err
		}
	}
	if err = safefile.AtomicWrite(path, next, 0600); err != nil {
		_ = restoreSnapshot(catalogPath, previousCatalog)
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: backupName(backup)}, nil
}

func (a *Adapter) Cleanup(_ context.Context) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	current, err := read(path)
	if err != nil {
		return application.ApplyResult{}, err
	}
	catalogPath := filepath.Join(filepath.Dir(path), catalogName)
	backup, err := createBundleBackup(path, catalogPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	next := stripManaged(current)
	if len(bytes.TrimSpace(next)) > 0 {
		next = append(next, '\n')
	}
	if err = safefile.AtomicWrite(path, next, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	if err = os.Remove(catalogPath); err != nil && !os.IsNotExist(err) {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: backupName(backup)}, nil
}

func (a *Adapter) Verify(ctx context.Context, options application.VerifyOptions) (application.VerifyResult, error) {
	result := application.VerifyResult{OK: true, Verified: time.Now().UTC()}
	desired, err := decodeDesired(options.Config)
	if err != nil {
		return result, err
	}
	started := time.Now()
	detection, _ := a.Detect(ctx)
	result.Stages = append(result.Stages, application.VerifyStage{ID: "installation", Label: "安装检测", OK: detection.Installed, Message: detection.Message, Latency: time.Since(started).Milliseconds()})
	if !detection.Installed {
		result.OK = false
	}
	started = time.Now()
	baseURL := desired.BaseURL
	model := desired.Model
	apiKey := desired.APIKey
	stageID, stageLabel := "gateway", "网关链路"
	if desired.IntegrationMode == "direct" {
		baseURL = desired.ProviderBaseURL
		model = desired.ProviderModel
		stageID, stageLabel = "provider", "官方直连"
		apiKey, err = a.providerAPIKey(desired.ProviderID)
		if err != nil {
			return result, err
		}
	}
	payload, _ := json.Marshal(map[string]any{"model": model, "input": "Reply with OK.", "max_output_tokens": 32, "stream": true, "reasoning": map[string]any{"effort": "high", "summary": "auto"}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+apiKey)
	resp, requestErr := a.HTTPClient.Do(req)
	detail := baseURL + "/responses"
	ok := requestErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300
	if resp != nil {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			ok = false
			detail = readErr.Error()
		} else if ok && (!bytes.Contains(body, []byte("response.output_item.added")) || !bytes.Contains(body, []byte("response.output_text.delta")) || !bytes.Contains(body, []byte("response.completed"))) {
			ok = false
			detail = "Responses 流缺少 Codex 必需的输出项生命周期事件"
		}
		if !ok {
			if detail == baseURL+"/responses" {
				detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
		}
	}
	if requestErr != nil {
		detail = requestErr.Error()
	}
	if !ok {
		result.OK = false
	}
	result.Stages = append(result.Stages, application.VerifyStage{ID: stageID, Label: stageLabel, OK: ok, Message: map[bool]string{true: "Codex Responses 流式链路验证成功", false: "Codex Responses 流式链路验证失败"}[ok], Detail: detail, Latency: time.Since(started).Milliseconds()})
	return result, nil
}

func (a *Adapter) providerAPIKey(providerID string) (string, error) {
	if strings.TrimSpace(a.RouterConfigPath) == "" {
		return "", errors.New("AI Router 运行配置路径为空")
	}
	current, err := config.Load(a.RouterConfigPath)
	if err != nil {
		return "", err
	}
	for _, provider := range current.Providers {
		if provider.ID == providerID {
			if strings.TrimSpace(provider.APIKey) == "" {
				return "", fmt.Errorf("Provider %s 没有 API Key", providerID)
			}
			return provider.APIKey, nil
		}
	}
	return "", fmt.Errorf("Provider %s 不存在", providerID)
}

func (a *Adapter) Backups(_ context.Context) ([]application.Backup, error) {
	path, err := a.path()
	if err != nil {
		return nil, err
	}
	entries, err := safefile.List(path, backupMarker)
	if err != nil {
		return nil, err
	}
	out := make([]application.Backup, 0, len(entries))
	for _, entry := range entries {
		out = append(out, application.Backup{Name: entry.Name, Path: entry.Path, Size: entry.Size, ModifiedAt: entry.ModifiedAt, ContainsSensitive: true})
	}
	return out, nil
}

func (a *Adapter) DeleteBackup(_ context.Context, name string) error {
	path, err := a.path()
	if err != nil {
		return err
	}
	return safefile.RemoveBackup(path, backupMarker, name)
}

func (a *Adapter) Rollback(_ context.Context, name string) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	backupPath, ok := safefile.ResolveBackup(path, backupMarker, name)
	if !ok {
		return application.ApplyResult{}, safefile.ErrInvalidBackupName
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	catalogPath := filepath.Join(filepath.Dir(path), catalogName)
	currentBackup, err := createBundleBackup(path, catalogPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	var bundle backupBundle
	if json.Unmarshal(raw, &bundle) == nil && bundle.Format == backupBundleFormat {
		if err = restoreSnapshot(catalogPath, bundle.Catalog); err != nil {
			return application.ApplyResult{}, err
		}
		if err = restoreSnapshot(path, bundle.Config); err != nil {
			return application.ApplyResult{}, err
		}
	} else {
		if err = safefile.AtomicWrite(path, raw, 0600); err != nil {
			return application.ApplyResult{}, err
		}
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: backupName(currentBackup)}, nil
}
