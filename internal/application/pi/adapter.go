// Package pi integrates the Pi coding agent with AI Router.
//
// Pi stores its global model catalog and global defaults in separate files.
// Keeping them as one transaction is important: a provider without the
// matching default model (or vice versa) leaves the CLI in a confusing state.
package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/safefile"
)

const (
	providerID         = "airoute"
	backupMarker       = ".airoute.bak."
	backupBundleFormat = "airoute.pi.backup.v1"
	modelsFileName     = "models.json"
	settingsFileName   = "settings.json"
)

type DesiredConfig struct {
	BaseURL      string   `json:"base_url"`
	APIKey       string   `json:"api_key"`
	Model        string   `json:"model"`
	Models       []string `json:"models,omitempty"`
	WireProtocol string   `json:"wire_protocol,omitempty"`
}

// Adapter can be pointed at a temporary AgentDir by tests or an explicit
// AIROUTE_PI_AGENT_DIR by operators. WriteFile is injectable so failed second
// writes can be tested without making filesystem permissions unreliable.
type Adapter struct {
	AgentDir       string
	HTTPClient     *http.Client
	LookPath       func(string) (string, error)
	CommandTimeout time.Duration
	WriteFile      func(string, []byte) error
}

type fileSnapshot struct {
	Exists  bool   `json:"exists"`
	Content []byte `json:"content,omitempty"`
}

type backupBundle struct {
	Format   string       `json:"format"`
	Models   fileSnapshot `json:"models"`
	Settings fileSnapshot `json:"settings"`
}

type documents struct {
	Models          map[string]any
	Settings        map[string]any
	ModelsRaw       []byte
	SettingsRaw     []byte
	ModelsUsesJSONC bool
}

type configuredFiles struct {
	Models   []byte
	Settings []byte
}

type wireConfig struct {
	API     string
	BaseURL string
	Path    string
}

func New() *Adapter {
	return &Adapter{
		HTTPClient:     &http.Client{Timeout: 60 * time.Second},
		LookPath:       exec.LookPath,
		CommandTimeout: 3 * time.Second,
	}
}

func (a *Adapter) Manifest() application.Manifest {
	return application.Manifest{
		ID:           "pi",
		Name:         "Pi Coding Agent",
		Description:  "Pi Coding Agent 全局模型与默认提供商配置",
		Status:       "available",
		ConfigFormat: "jsonc",
		Capabilities: []application.Capability{
			application.CapabilityDetect,
			application.CapabilityConfigure,
			application.CapabilityPreview,
			application.CapabilityVerify,
			application.CapabilityRollback,
			application.CapabilityCleanup,
			application.CapabilityEdit,
		},
	}
}

func (a *Adapter) agentDir() (string, error) {
	if value := strings.TrimSpace(a.AgentDir); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv("AIROUTE_PI_AGENT_DIR")); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

func (a *Adapter) paths() (string, string, error) {
	dir, err := a.agentDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, modelsFileName), filepath.Join(dir, settingsFileName), nil
}

func (a *Adapter) executable() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AIROUTE_PI_EXECUTABLE")); value != "" {
		return value, nil
	}
	lookPath := a.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return lookPath("pi")
}

func (a *Adapter) Detect(ctx context.Context) (application.Detection, error) {
	path, err := a.executable()
	if err != nil {
		return application.Detection{Message: "未检测到 Pi Coding Agent 命令"}, nil
	}
	timeout := a.CommandTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, commandErr := exec.CommandContext(checkCtx, path, "--version").CombinedOutput()
	version := strings.TrimSpace(string(output))
	if checkCtx.Err() == context.DeadlineExceeded {
		return application.Detection{Installed: true, Executable: path, Version: version, Message: "Pi Coding Agent 命令响应超时"}, nil
	}
	if commandErr != nil {
		return application.Detection{Installed: true, Executable: path, Version: version, Message: "已检测到 Pi Coding Agent，但版本读取失败"}, nil
	}
	return application.Detection{Installed: true, Executable: path, Version: version, Message: "Pi Coding Agent 已安装"}, nil
}

func (a *Adapter) write(path string, content []byte) error {
	if a.WriteFile != nil {
		return a.WriteFile(path, content)
	}
	return safefile.AtomicWrite(path, content, 0600)
}

func readJSONDocument(path string, allowJSONC bool) (map[string]any, []byte, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, raw, false, nil
	}
	clean := raw
	usesJSONC := false
	if allowJSONC {
		var cleanErr error
		clean, usesJSONC, cleanErr = normalizeJSONC(raw)
		if cleanErr != nil {
			return nil, raw, usesJSONC, fmt.Errorf("%s 不是可读取的 JSONC: %w", path, cleanErr)
		}
	}
	document := map[string]any{}
	if err = json.Unmarshal(clean, &document); err != nil {
		return nil, raw, usesJSONC, fmt.Errorf("%s 不是有效 JSON", path)
	}
	if document == nil {
		return nil, raw, usesJSONC, fmt.Errorf("%s 必须是 JSON 对象", path)
	}
	return document, raw, usesJSONC, nil
}

// normalizeJSONC removes JavaScript comments and trailing commas only for
// parsing. Callers retain the marker and refuse automatic rewriting, so a
// successful read can never silently erase a user's comments or formatting.
func normalizeJSONC(raw []byte) ([]byte, bool, error) {
	clean := append([]byte(nil), raw...)
	usesJSONC := false
	inString, escaped := false, false
	for i := 0; i < len(clean); i++ {
		ch := clean[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch != '/' || i+1 >= len(clean) {
			continue
		}
		next := clean[i+1]
		if next == '/' {
			usesJSONC = true
			clean[i], clean[i+1] = ' ', ' '
			i += 2
			for i < len(clean) && clean[i] != '\n' && clean[i] != '\r' {
				clean[i] = ' '
				i++
			}
			continue
		}
		if next == '*' {
			usesJSONC = true
			clean[i], clean[i+1] = ' ', ' '
			i += 2
			closed := false
			for i < len(clean) {
				if i+1 < len(clean) && clean[i] == '*' && clean[i+1] == '/' {
					clean[i], clean[i+1] = ' ', ' '
					i++
					closed = true
					break
				}
				if clean[i] != '\n' && clean[i] != '\r' {
					clean[i] = ' '
				}
				i++
			}
			if !closed {
				return nil, usesJSONC, errors.New("未闭合的块注释")
			}
		}
	}
	if inString {
		return nil, usesJSONC, errors.New("未闭合的字符串")
	}
	inString, escaped = false, false
	for i := 0; i < len(clean); i++ {
		ch := clean[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch != ',' {
			continue
		}
		j := i + 1
		for j < len(clean) && (clean[j] == ' ' || clean[j] == '\t' || clean[j] == '\n' || clean[j] == '\r') {
			j++
		}
		if j < len(clean) && (clean[j] == '}' || clean[j] == ']') {
			clean[i] = ' '
			usesJSONC = true
		}
	}
	return clean, usesJSONC, nil
}

func (a *Adapter) readDocuments() (string, string, documents, error) {
	modelsPath, settingsPath, err := a.paths()
	if err != nil {
		return "", "", documents{}, err
	}
	models, modelsRaw, modelsJSONC, err := readJSONDocument(modelsPath, true)
	if err != nil {
		return "", "", documents{}, err
	}
	settings, settingsRaw, _, err := readJSONDocument(settingsPath, false)
	if err != nil {
		return "", "", documents{}, err
	}
	return modelsPath, settingsPath, documents{Models: models, Settings: settings, ModelsRaw: modelsRaw, SettingsRaw: settingsRaw, ModelsUsesJSONC: modelsJSONC}, nil
}

func decodeDesired(raw json.RawMessage) (DesiredConfig, error) {
	var desired DesiredConfig
	if err := json.Unmarshal(raw, &desired); err != nil {
		return desired, fmt.Errorf("Pi Coding Agent 配置无效: %w", err)
	}
	desired.BaseURL = strings.TrimRight(strings.TrimSpace(desired.BaseURL), "/")
	desired.APIKey = strings.TrimSpace(desired.APIKey)
	desired.Model = strings.TrimSpace(desired.Model)
	desired.WireProtocol = strings.TrimSpace(desired.WireProtocol)
	if desired.WireProtocol == "" {
		desired.WireProtocol = "openai-chat"
	}
	if desired.BaseURL == "" || desired.Model == "" {
		return desired, errors.New("网关地址和默认模型为必填项")
	}
	if desired.APIKey == "" {
		desired.APIKey = "airoute-local"
	}
	if _, err := piWireConfig(desired.BaseURL, desired.WireProtocol); err != nil {
		return desired, err
	}
	return desired, nil
}

func piWireConfig(baseURL, wireProtocol string) (wireConfig, error) {
	root := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	root = strings.TrimSuffix(root, "/v1beta")
	root = strings.TrimSuffix(root, "/v1")
	if root == "" {
		return wireConfig{}, errors.New("网关地址无效")
	}
	switch wireProtocol {
	case "openai-chat":
		return wireConfig{API: "openai-completions", BaseURL: root + "/v1", Path: "/chat/completions"}, nil
	case "openai-responses":
		return wireConfig{API: "openai-responses", BaseURL: root + "/v1", Path: "/responses"}, nil
	case "anthropic-messages":
		return wireConfig{API: "anthropic-messages", BaseURL: root, Path: "/v1/messages"}, nil
	case "gemini-generate-content":
		return wireConfig{API: "google-generative-ai", BaseURL: root + "/v1beta", Path: "/models"}, nil
	default:
		return wireConfig{}, fmt.Errorf("不支持的 Pi 请求协议 %q", wireProtocol)
	}
}

func copyDocument(document map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	copy := map[string]any{}
	if err = json.Unmarshal(raw, &copy); err != nil {
		return nil, err
	}
	return copy, nil
}

func selectedModels(desired DesiredConfig) []any {
	unique := map[string]struct{}{}
	for _, model := range append([]string{desired.Model}, desired.Models...) {
		if model = strings.TrimSpace(model); model != "" {
			unique[model] = struct{}{}
		}
	}
	names := make([]string, 0, len(unique))
	for model := range unique {
		names = append(names, model)
	}
	sort.Strings(names)
	models := make([]any, 0, len(names))
	for _, model := range names {
		models = append(models, map[string]any{"id": model})
	}
	return models
}

func mergeDocuments(current documents, desired DesiredConfig) (configuredFiles, error) {
	models, err := copyDocument(current.Models)
	if err != nil {
		return configuredFiles{}, err
	}
	settings, err := copyDocument(current.Settings)
	if err != nil {
		return configuredFiles{}, err
	}
	wire, err := piWireConfig(desired.BaseURL, desired.WireProtocol)
	if err != nil {
		return configuredFiles{}, err
	}
	providers, _ := models["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers[providerID] = map[string]any{
		"name":    "AI Router",
		"api":     wire.API,
		"baseUrl": wire.BaseURL,
		"apiKey":  desired.APIKey,
		"models":  selectedModels(desired),
	}
	models["providers"] = providers
	settings["defaultProvider"] = providerID
	settings["defaultModel"] = desired.Model
	modelsRaw, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return configuredFiles{}, err
	}
	settingsRaw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return configuredFiles{}, err
	}
	return configuredFiles{Models: append(modelsRaw, '\n'), Settings: append(settingsRaw, '\n')}, nil
}

func bundlePreview(models, settings []byte) json.RawMessage {
	if len(bytes.TrimSpace(models)) == 0 {
		models = []byte("{}")
	}
	if len(bytes.TrimSpace(settings)) == 0 {
		settings = []byte("{}")
	}
	encoded, _ := json.Marshal(map[string]json.RawMessage{
		modelsFileName:   json.RawMessage(models),
		settingsFileName: json.RawMessage(settings),
	})
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

func diffDocuments(current documents, next configuredFiles) string {
	parts := make([]string, 0, 2)
	if diff := lineDiff(current.ModelsRaw, next.Models); diff != "没有配置变化" {
		parts = append(parts, modelsFileName+"\n"+diff)
	}
	if diff := lineDiff(current.SettingsRaw, next.Settings); diff != "没有配置变化" {
		parts = append(parts, settingsFileName+"\n"+diff)
	}
	if len(parts) == 0 {
		return "没有配置变化"
	}
	return strings.Join(parts, "\n\n")
}

func (a *Adapter) compose(raw json.RawMessage) (string, string, documents, configuredFiles, DesiredConfig, error) {
	desired, err := decodeDesired(raw)
	if err != nil {
		return "", "", documents{}, configuredFiles{}, desired, err
	}
	modelsPath, settingsPath, current, err := a.readDocuments()
	if err != nil {
		return "", "", documents{}, configuredFiles{}, desired, err
	}
	if current.ModelsUsesJSONC {
		return "", "", documents{}, configuredFiles{}, desired, errors.New("Pi models.json 使用 JSONC 注释或尾逗号；为保留现有格式，AI Router 不会自动重写。请在手动编辑预览中提交标准 JSON")
	}
	next, err := mergeDocuments(current, desired)
	return modelsPath, settingsPath, current, next, desired, err
}

func managed(models, settings map[string]any) map[string]any {
	result := map[string]any{}
	providers, _ := models["providers"].(map[string]any)
	provider, _ := providers[providerID].(map[string]any)
	if provider == nil {
		return result
	}
	if value, ok := provider["baseUrl"].(string); ok {
		result["base_url"] = value
	}
	if value, ok := provider["apiKey"].(string); ok {
		result["api_key"] = value
	}
	if value, ok := provider["api"].(string); ok {
		result["api"] = value
		result["wire_protocol"] = map[string]string{
			"openai-completions":   "openai-chat",
			"openai-responses":     "openai-responses",
			"anthropic-messages":   "anthropic-messages",
			"google-generative-ai": "gemini-generate-content",
		}[value]
	}
	if value, ok := settings["defaultModel"].(string); ok {
		result["model"] = value
	}
	if value, ok := settings["defaultProvider"].(string); ok {
		result["default_provider"] = value
	}
	if rawModels, ok := provider["models"].([]any); ok {
		models := make([]string, 0, len(rawModels))
		for _, entry := range rawModels {
			if model, _ := entry.(map[string]any); model != nil {
				if id, _ := model["id"].(string); id != "" {
					models = append(models, id)
				}
			}
		}
		result["models"] = models
	}
	return result
}

func synced(state map[string]any) bool {
	baseURL, _ := state["base_url"].(string)
	model, _ := state["model"].(string)
	provider, _ := state["default_provider"].(string)
	protocol, _ := state["wire_protocol"].(string)
	return provider == providerID && strings.TrimSpace(baseURL) != "" && strings.TrimSpace(model) != "" && protocol != ""
}

func (a *Adapter) Read(ctx context.Context) (application.State, error) {
	modelsPath, _, current, err := a.readDocuments()
	if err != nil {
		return application.State{}, err
	}
	detection, err := a.Detect(ctx)
	if err != nil {
		return application.State{}, err
	}
	state := managed(current.Models, current.Settings)
	return application.State{
		Manifest:        a.Manifest(),
		Detection:       detection,
		Path:            modelsPath,
		Exists:          len(bytes.TrimSpace(current.ModelsRaw)) > 0 || len(bytes.TrimSpace(current.SettingsRaw)) > 0,
		Managed:         state,
		PreservedFields: len(current.Models) + len(current.Settings),
		Synced:          synced(state),
	}, nil
}

func (a *Adapter) ConfigurationSynced(_ context.Context) (bool, error) {
	_, _, current, err := a.readDocuments()
	if err != nil {
		return false, err
	}
	return synced(managed(current.Models, current.Settings)), nil
}

func (a *Adapter) Preview(_ context.Context, raw json.RawMessage) (application.Preview, error) {
	modelsPath, _, current, next, _, err := a.compose(raw)
	if err != nil {
		return application.Preview{}, err
	}
	return application.Preview{
		Path:             modelsPath,
		Current:          bundlePreview(current.ModelsRaw, current.SettingsRaw),
		Content:          bundlePreview(next.Models, next.Settings),
		Diff:             diffDocuments(current, next),
		PreservedFields:  len(current.Models) + len(current.Settings),
		WillCreateBackup: len(bytes.TrimSpace(current.ModelsRaw)) > 0 || len(bytes.TrimSpace(current.SettingsRaw)) > 0,
	}, nil
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

func (a *Adapter) restoreSnapshot(path string, state fileSnapshot) error {
	if !state.Exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return a.write(path, state.Content)
}

func createBundleBackup(modelsPath, settingsPath string) (string, error) {
	models, err := snapshot(modelsPath)
	if err != nil {
		return "", err
	}
	settings, err := snapshot(settingsPath)
	if err != nil {
		return "", err
	}
	if !models.Exists && !settings.Exists {
		return "", nil
	}
	raw, err := json.Marshal(backupBundle{Format: backupBundleFormat, Models: models, Settings: settings})
	if err != nil {
		return "", err
	}
	return safefile.BackupData(modelsPath, backupMarker, raw)
}

func backupName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

func (a *Adapter) writePair(modelsPath, settingsPath string, beforeModels, beforeSettings fileSnapshot, next configuredFiles) error {
	if err := a.write(modelsPath, next.Models); err != nil {
		return err
	}
	if err := a.write(settingsPath, next.Settings); err != nil {
		if restoreErr := a.restoreSnapshot(modelsPath, beforeModels); restoreErr != nil {
			return fmt.Errorf("写入 Pi settings.json 失败，且恢复 models.json 失败: %w", restoreErr)
		}
		return err
	}
	return nil
}

func (a *Adapter) Apply(_ context.Context, raw json.RawMessage) (application.ApplyResult, error) {
	modelsPath, settingsPath, _, next, _, err := a.compose(raw)
	if err != nil {
		return application.ApplyResult{}, err
	}
	beforeModels, err := snapshot(modelsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	beforeSettings, err := snapshot(settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := createBundleBackup(modelsPath, settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = a.writePair(modelsPath, settingsPath, beforeModels, beforeSettings, next); err != nil {
		return application.ApplyResult{}, err
	}
	_, _, written, err := a.readDocuments()
	if err != nil || !synced(managed(written.Models, written.Settings)) {
		_ = a.restoreSnapshot(modelsPath, beforeModels)
		_ = a.restoreSnapshot(settingsPath, beforeSettings)
		return application.ApplyResult{}, errors.New("写入后的 Pi 配置无效")
	}
	_ = safefile.Prune(modelsPath, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: modelsPath, Backup: backupName(backup)}, nil
}

func parseRawBundle(content string) (configuredFiles, error) {
	bundle := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(content), &bundle); err != nil {
		return configuredFiles{}, fmt.Errorf("Pi 配置不是有效 JSON: %w", err)
	}
	models, okModels := bundle[modelsFileName]
	settings, okSettings := bundle[settingsFileName]
	if !okModels || !okSettings || len(bundle) != 2 {
		return configuredFiles{}, errors.New("Pi 手动配置必须同时包含 models.json 与 settings.json")
	}
	var modelsDoc, settingsDoc map[string]any
	if err := json.Unmarshal(models, &modelsDoc); err != nil || modelsDoc == nil {
		return configuredFiles{}, errors.New("Pi models.json 必须是标准 JSON 对象")
	}
	if err := json.Unmarshal(settings, &settingsDoc); err != nil || settingsDoc == nil {
		return configuredFiles{}, errors.New("Pi settings.json 必须是标准 JSON 对象")
	}
	modelsOut, _ := json.MarshalIndent(modelsDoc, "", "  ")
	settingsOut, _ := json.MarshalIndent(settingsDoc, "", "  ")
	return configuredFiles{Models: append(modelsOut, '\n'), Settings: append(settingsOut, '\n')}, nil
}

func (a *Adapter) ApplyRaw(_ context.Context, input application.RawConfig) (application.ApplyResult, error) {
	next, err := parseRawBundle(input.Content)
	if err != nil {
		return application.ApplyResult{}, err
	}
	modelsPath, settingsPath, err := a.paths()
	if err != nil {
		return application.ApplyResult{}, err
	}
	beforeModels, err := snapshot(modelsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	beforeSettings, err := snapshot(settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := createBundleBackup(modelsPath, settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = a.writePair(modelsPath, settingsPath, beforeModels, beforeSettings, next); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(modelsPath, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: modelsPath, Backup: backupName(backup)}, nil
}

func (a *Adapter) Cleanup(_ context.Context) (application.ApplyResult, error) {
	modelsPath, settingsPath, current, err := a.readDocuments()
	if err != nil {
		return application.ApplyResult{}, err
	}
	if current.ModelsUsesJSONC {
		return application.ApplyResult{}, errors.New("Pi models.json 使用 JSONC 注释或尾逗号，AI Router 不会自动清理以免丢失格式")
	}
	models, err := copyDocument(current.Models)
	if err != nil {
		return application.ApplyResult{}, err
	}
	settings, err := copyDocument(current.Settings)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if providers, ok := models["providers"].(map[string]any); ok {
		delete(providers, providerID)
		if len(providers) == 0 {
			delete(models, "providers")
		} else {
			models["providers"] = providers
		}
	}
	if provider, _ := settings["defaultProvider"].(string); provider == providerID {
		delete(settings, "defaultProvider")
		delete(settings, "defaultModel")
	}
	modelsRaw, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return application.ApplyResult{}, err
	}
	settingsRaw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return application.ApplyResult{}, err
	}
	next := configuredFiles{Models: append(modelsRaw, '\n'), Settings: append(settingsRaw, '\n')}
	beforeModels, err := snapshot(modelsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	beforeSettings, err := snapshot(settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := createBundleBackup(modelsPath, settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = a.writePair(modelsPath, settingsPath, beforeModels, beforeSettings, next); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(modelsPath, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: modelsPath, Backup: backupName(backup)}, nil
}

func requestFor(ctx context.Context, desired DesiredConfig) (*http.Request, string, error) {
	wire, err := piWireConfig(desired.BaseURL, desired.WireProtocol)
	if err != nil {
		return nil, "", err
	}
	var payload []byte
	endpoint := wire.BaseURL + wire.Path
	switch desired.WireProtocol {
	case "openai-chat":
		payload, _ = json.Marshal(map[string]any{"model": desired.Model, "messages": []any{map[string]any{"role": "user", "content": "Reply with OK."}}, "max_tokens": 16, "stream": true})
	case "openai-responses":
		payload, _ = json.Marshal(map[string]any{"model": desired.Model, "input": "Reply with OK.", "max_output_tokens": 16, "stream": true})
	case "anthropic-messages":
		payload, _ = json.Marshal(map[string]any{"model": desired.Model, "messages": []any{map[string]any{"role": "user", "content": "Reply with OK."}}, "max_tokens": 16, "stream": true})
	case "gemini-generate-content":
		endpoint = wire.BaseURL + wire.Path + "/" + url.PathEscape(desired.Model) + ":streamGenerateContent?alt=sse"
		payload, _ = json.Marshal(map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Reply with OK."}}}}})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("content-type", "application/json")
	if desired.WireProtocol == "anthropic-messages" {
		req.Header.Set("x-api-key", desired.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if desired.WireProtocol == "gemini-generate-content" {
		req.Header.Set("x-goog-api-key", desired.APIKey)
	} else {
		req.Header.Set("authorization", "Bearer "+desired.APIKey)
	}
	return req, endpoint, nil
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
	req, endpoint, err := requestFor(ctx, desired)
	if err != nil {
		return result, err
	}
	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, requestErr := client.Do(req)
	ok := requestErr == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300
	detail := endpoint
	if resp != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if !ok {
			detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}
	if requestErr != nil {
		detail = requestErr.Error()
	}
	if !ok {
		result.OK = false
	}
	message := "Pi Coding Agent 网关链路验证成功"
	if !ok {
		message = "Pi Coding Agent 网关链路验证失败"
	}
	result.Stages = append(result.Stages, application.VerifyStage{ID: "gateway", Label: "网关链路", OK: ok, Message: message, Detail: detail, Latency: time.Since(started).Milliseconds()})
	return result, nil
}

func (a *Adapter) Backups(_ context.Context) ([]application.Backup, error) {
	modelsPath, _, err := a.paths()
	if err != nil {
		return nil, err
	}
	entries, err := safefile.List(modelsPath, backupMarker)
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
	modelsPath, _, err := a.paths()
	if err != nil {
		return err
	}
	return safefile.RemoveBackup(modelsPath, backupMarker, name)
}

func (a *Adapter) Rollback(_ context.Context, name string) (application.ApplyResult, error) {
	modelsPath, settingsPath, err := a.paths()
	if err != nil {
		return application.ApplyResult{}, err
	}
	backupPath, ok := safefile.ResolveBackup(modelsPath, backupMarker, name)
	if !ok {
		return application.ApplyResult{}, safefile.ErrInvalidBackupName
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	var bundle backupBundle
	if err = json.Unmarshal(raw, &bundle); err != nil || bundle.Format != backupBundleFormat {
		return application.ApplyResult{}, errors.New("备份不是有效的 Pi 配置快照")
	}
	beforeModels, err := snapshot(modelsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	beforeSettings, err := snapshot(settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	currentBackup, err := createBundleBackup(modelsPath, settingsPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = a.restoreSnapshot(modelsPath, bundle.Models); err != nil {
		return application.ApplyResult{}, err
	}
	if err = a.restoreSnapshot(settingsPath, bundle.Settings); err != nil {
		_ = a.restoreSnapshot(modelsPath, beforeModels)
		_ = a.restoreSnapshot(settingsPath, beforeSettings)
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(modelsPath, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: modelsPath, Backup: backupName(currentBackup)}, nil
}
