package claudeapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/safefile"
)

const configID = "8f69f2f1-3275-4ad8-9317-4aa7e972f311"

type DesiredConfig struct {
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`
	OpusModel   string `json:"opus_model"`
	SonnetModel string `json:"sonnet_model"`
	HaikuModel  string `json:"haiku_model"`
}

type inferenceModel struct {
	LabelOverride string `json:"labelOverride"`
	Name          string `json:"name"`
}

type gatewayConfig struct {
	InferenceCredentialKind          string           `json:"inferenceCredentialKind"`
	InferenceGatewayAPIKey           string           `json:"inferenceGatewayApiKey"`
	InferenceGatewayAuthScheme       string           `json:"inferenceGatewayAuthScheme"`
	InferenceGatewayBaseURL          string           `json:"inferenceGatewayBaseUrl"`
	InferenceModels                  []inferenceModel `json:"inferenceModels"`
	InferenceModelsUpdatedAt         string           `json:"inferenceModelsUpdatedAt"`
	InferenceModelsVersion           string           `json:"inferenceModelsVersion"`
	InferenceProvider                string           `json:"inferenceProvider"`
	ModelDiscoveryEnabled            bool             `json:"modelDiscoveryEnabled"`
	UnstableDisableModelVerification bool             `json:"unstableDisableModelVerification"`
}

type fileSnapshot struct {
	Exists  bool   `json:"exists"`
	Content string `json:"content,omitempty"`
}

type stateBundle struct {
	Version int           `json:"version"`
	Desired DesiredConfig `json:"desired,omitempty"`
	Root    fileSnapshot  `json:"root"`
	Meta    fileSnapshot  `json:"meta"`
	Library fileSnapshot  `json:"library"`
}

type Adapter struct {
	Candidates []string
	DataDir    string
	HTTPClient *http.Client
}

func New() *Adapter { return &Adapter{HTTPClient: &http.Client{Timeout: 60 * time.Second}} }

func (a *Adapter) Manifest() application.Manifest {
	return application.Manifest{
		ID: "claude-app", Name: "Claude App", Description: "Claude 桌面应用",
		Status: "available", ConfigFormat: "json",
		Capabilities: []application.Capability{application.CapabilityDetect, application.CapabilityConfigure, application.CapabilityPreview, application.CapabilityVerify, application.CapabilityRollback, application.CapabilityCleanup, application.CapabilityEdit},
	}
}

func (a *Adapter) candidates() []string {
	if len(a.Candidates) > 0 {
		return a.Candidates
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Applications/Claude.app", filepath.Join(home, "Applications", "Claude.app")}
	case "windows":
		return []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "AnthropicClaude", "Claude.exe"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Claude", "Claude.exe")}
	default:
		return []string{"/usr/bin/claude-desktop", "/usr/local/bin/claude-desktop"}
	}
}

func (a *Adapter) dataDir() (string, error) {
	if strings.TrimSpace(a.DataDir) != "" {
		return a.DataDir, nil
	}
	if override := strings.TrimSpace(os.Getenv("AIROUTE_CLAUDE_APP_DATA_DIR")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude-3p"), nil
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Claude-3p"), nil
	default:
		return filepath.Join(home, ".config", "Claude-3p"), nil
	}
}

func (a *Adapter) paths() (root, meta, library, state string, err error) {
	dir, err := a.dataDir()
	if err != nil {
		return "", "", "", "", err
	}
	libraryDir := filepath.Join(dir, "configLibrary")
	return filepath.Join(dir, "claude_desktop_config.json"), filepath.Join(libraryDir, "_meta.json"), filepath.Join(libraryDir, configID+".json"), filepath.Join(libraryDir, ".airoute-claude-app-state.json"), nil
}

func (a *Adapter) Detect(context.Context) (application.Detection, error) {
	for _, candidate := range a.candidates() {
		if candidate != "" {
			if _, err := os.Stat(candidate); err == nil {
				return application.Detection{Installed: true, Executable: candidate, Message: "Claude App 已安装"}, nil
			}
		}
	}
	return application.Detection{Installed: false, Message: "未检测到 Claude App"}, nil
}

func readObject(path string) (map[string]any, []byte, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	value := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &value) != nil {
		return nil, nil, fmt.Errorf("%s 不是有效 JSON", path)
	}
	return value, raw, nil
}

func decodeRouteID(value string) string {
	const prefix = "anthropic/claude-ccr-h"
	if !strings.HasPrefix(strings.ToLower(value), prefix) {
		return value
	}
	raw, err := hex.DecodeString(value[len(prefix):])
	if err != nil {
		return value
	}
	return string(raw)
}

func desiredFromGateway(config gatewayConfig) DesiredConfig {
	d := DesiredConfig{BaseURL: config.InferenceGatewayBaseURL, APIKey: config.InferenceGatewayAPIKey}
	for _, model := range config.InferenceModels {
		alias := decodeRouteID(model.Name)
		switch {
		case strings.Contains(model.LabelOverride, "默认"):
			d.Model = alias
		case strings.Contains(model.LabelOverride, "Sonnet"):
			d.SonnetModel = alias
		case strings.Contains(model.LabelOverride, "Opus"):
			d.OpusModel = alias
		case strings.Contains(model.LabelOverride, "Haiku"):
			d.HaikuModel = alias
		}
		if d.Model == "" {
			d.Model = alias
		}
	}
	return d
}

func (a *Adapter) Read(ctx context.Context) (application.State, error) {
	_, _, library, statePath, err := a.paths()
	if err != nil {
		return application.State{}, err
	}
	_, raw, err := readObject(library)
	if err != nil {
		return application.State{}, err
	}
	var config gatewayConfig
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &config)
	}
	d := desiredFromGateway(config)
	if stateRaw, readErr := os.ReadFile(statePath); readErr == nil {
		var bundle stateBundle
		if json.Unmarshal(stateRaw, &bundle) == nil && bundle.Version == 1 && bundle.Desired.Model != "" {
			d = bundle.Desired
		}
	}
	detection, err := a.Detect(ctx)
	if err != nil {
		return application.State{}, err
	}
	managed := map[string]any{"base_url": d.BaseURL, "api_key": d.APIKey, "model": d.Model, "opus_model": d.OpusModel, "sonnet_model": d.SonnetModel, "haiku_model": d.HaikuModel}
	return application.State{Manifest: a.Manifest(), Detection: detection, Path: library, Exists: len(bytes.TrimSpace(raw)) > 0, Managed: managed, PreservedFields: 0, Synced: config.InferenceProvider == "gateway" && d.BaseURL != "" && d.Model != ""}, nil
}

func decodeDesired(raw json.RawMessage) (DesiredConfig, error) {
	var desired DesiredConfig
	if err := json.Unmarshal(raw, &desired); err != nil {
		return desired, fmt.Errorf("Claude App 配置无效: %w", err)
	}
	desired.BaseURL = strings.TrimRight(strings.TrimSpace(desired.BaseURL), "/")
	desired.APIKey = strings.TrimSpace(desired.APIKey)
	desired.Model = strings.TrimSpace(desired.Model)
	desired.OpusModel = strings.TrimSpace(desired.OpusModel)
	desired.SonnetModel = strings.TrimSpace(desired.SonnetModel)
	desired.HaikuModel = strings.TrimSpace(desired.HaikuModel)
	if desired.BaseURL == "" || desired.Model == "" {
		return desired, errors.New("网关地址和默认模型为必填项")
	}
	if desired.APIKey == "" {
		desired.APIKey = "airoute-local"
	}
	return desired, nil
}

func routeID(alias string) string {
	return "anthropic/claude-ccr-h" + hex.EncodeToString([]byte(alias))
}

func buildGateway(desired DesiredConfig, now time.Time) gatewayConfig {
	type role struct{ label, model string }
	roles := []role{{"默认", desired.Model}, {"Sonnet", desired.SonnetModel}, {"Opus", desired.OpusModel}, {"Haiku", desired.HaikuModel}}
	models := make([]inferenceModel, 0, len(roles))
	seen := map[string]bool{}
	for _, role := range roles {
		if role.model == "" || seen[role.model] {
			continue
		}
		seen[role.model] = true
		models = append(models, inferenceModel{LabelOverride: "AI Router · " + role.label + " · " + role.model, Name: routeID(role.model)})
	}
	versionInput, _ := json.Marshal(map[string]any{"endpoint": desired.BaseURL, "models": models})
	hash := sha256.Sum256(versionInput)
	return gatewayConfig{"static", desired.APIKey, "x-api-key", desired.BaseURL, models, now.UTC().Format(time.RFC3339), hex.EncodeToString(hash[:8]), "gateway", true, true}
}

func mergeJSON(current map[string]any, additions map[string]any) ([]byte, error) {
	for key, value := range additions {
		current[key] = value
	}
	raw, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

type composed struct {
	root, meta, library, state string
	previous, next             stateBundle
	libraryNext                []byte
}

func snapshot(path string) (fileSnapshot, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{Exists: true, Content: string(raw)}, nil
}

func (a *Adapter) compose(raw json.RawMessage) (composed, error) {
	desired, err := decodeDesired(raw)
	if err != nil {
		return composed{}, err
	}
	root, meta, library, state, err := a.paths()
	if err != nil {
		return composed{}, err
	}
	rootObject, _, err := readObject(root)
	if err != nil {
		return composed{}, err
	}
	metaObject, _, err := readObject(meta)
	if err != nil {
		return composed{}, err
	}
	rootRaw, err := mergeJSON(rootObject, map[string]any{"deploymentMode": "3p"})
	if err != nil {
		return composed{}, err
	}
	entries := []any{}
	if current, ok := metaObject["entries"].([]any); ok {
		for _, entry := range current {
			if item, ok := entry.(map[string]any); !ok || item["id"] != configID {
				entries = append(entries, entry)
			}
		}
	}
	entries = append(entries, map[string]any{"id": configID, "name": "AI Router"})
	metaRaw, err := mergeJSON(metaObject, map[string]any{"appliedId": configID, "entries": entries})
	if err != nil {
		return composed{}, err
	}
	libraryRaw, err := json.MarshalIndent(buildGateway(desired, time.Now()), "", "  ")
	if err != nil {
		return composed{}, err
	}
	libraryRaw = append(libraryRaw, '\n')
	previous := stateBundle{Version: 1}
	if currentState, readErr := os.ReadFile(state); readErr == nil {
		var saved stateBundle
		if json.Unmarshal(currentState, &saved) == nil && saved.Version == 1 {
			previous.Desired = saved.Desired
		}
	}
	previous.Root, err = snapshot(root)
	if err != nil {
		return composed{}, err
	}
	previous.Meta, err = snapshot(meta)
	if err != nil {
		return composed{}, err
	}
	previous.Library, err = snapshot(library)
	if err != nil {
		return composed{}, err
	}
	next := stateBundle{Version: 1, Desired: desired, Root: fileSnapshot{true, string(rootRaw)}, Meta: fileSnapshot{true, string(metaRaw)}, Library: fileSnapshot{true, string(libraryRaw)}}
	return composed{root, meta, library, state, previous, next, libraryRaw}, nil
}

func (a *Adapter) Preview(_ context.Context, raw json.RawMessage) (application.Preview, error) {
	c, err := a.compose(raw)
	if err != nil {
		return application.Preview{}, err
	}
	current := []byte(c.previous.Library.Content)
	if len(bytes.TrimSpace(current)) == 0 {
		current = []byte("{}")
	}
	return application.Preview{Path: c.library, Current: json.RawMessage(current), Content: c.libraryNext, Diff: "将更新 Claude-3p 部署模式、配置库索引和 AI Router 网关配置。", WillCreateBackup: c.previous.Root.Exists || c.previous.Meta.Exists || c.previous.Library.Exists}, nil
}

func writeSnapshot(path string, snapshot fileSnapshot) error {
	if !snapshot.Exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return safefile.AtomicWrite(path, []byte(snapshot.Content), 0600)
}

func (a *Adapter) Apply(_ context.Context, raw json.RawMessage) (application.ApplyResult, error) {
	c, err := a.compose(raw)
	if err != nil {
		return application.ApplyResult{}, err
	}
	previousRaw, _ := json.MarshalIndent(c.previous, "", "  ")
	if err = safefile.AtomicWrite(c.state, append(previousRaw, '\n'), 0600); err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := safefile.Backup(c.state, ".airoute.bak.")
	if err != nil {
		return application.ApplyResult{}, err
	}
	for path, snapshot := range map[string]fileSnapshot{c.root: c.next.Root, c.meta: c.next.Meta, c.library: c.next.Library} {
		if err = writeSnapshot(path, snapshot); err != nil {
			return application.ApplyResult{}, err
		}
	}
	currentRaw, _ := json.MarshalIndent(c.next, "", "  ")
	_ = safefile.AtomicWrite(c.state, append(currentRaw, '\n'), 0600)
	_ = safefile.Prune(c.state, ".airoute.bak.", 10)
	return application.ApplyResult{OK: true, Path: c.library, Backup: filepath.Base(backup)}, nil
}

func (a *Adapter) ApplyRaw(_ context.Context, input application.RawConfig) (application.ApplyResult, error) {
	c, err := a.compose(input.Config)
	if err != nil {
		return application.ApplyResult{}, err
	}
	var document map[string]any
	if err = json.Unmarshal([]byte(input.Content), &document); err != nil {
		return application.ApplyResult{}, fmt.Errorf("Claude App 网关配置不是有效 JSON: %w", err)
	}
	if document == nil {
		return application.ApplyResult{}, errors.New("Claude App 网关配置必须是 JSON 对象")
	}
	libraryRaw := []byte(strings.TrimSpace(input.Content) + "\n")
	c.next.Library = fileSnapshot{Exists: true, Content: string(libraryRaw)}
	previousRaw, _ := json.MarshalIndent(c.previous, "", "  ")
	if err = safefile.AtomicWrite(c.state, append(previousRaw, '\n'), 0600); err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := safefile.Backup(c.state, ".airoute.bak.")
	if err != nil {
		return application.ApplyResult{}, err
	}
	for path, snapshot := range map[string]fileSnapshot{c.root: c.next.Root, c.meta: c.next.Meta, c.library: c.next.Library} {
		if err = writeSnapshot(path, snapshot); err != nil {
			return application.ApplyResult{}, err
		}
	}
	currentRaw, _ := json.MarshalIndent(c.next, "", "  ")
	_ = safefile.AtomicWrite(c.state, append(currentRaw, '\n'), 0600)
	_ = safefile.Prune(c.state, ".airoute.bak.", 10)
	return application.ApplyResult{OK: true, Path: c.library, Backup: claudeAppBackupName(backup)}, nil
}

func (a *Adapter) Cleanup(_ context.Context) (application.ApplyResult, error) {
	root, meta, library, state, err := a.paths()
	if err != nil {
		return application.ApplyResult{}, err
	}
	rootObject, _, err := readObject(root)
	if err != nil {
		return application.ApplyResult{}, err
	}
	metaObject, _, err := readObject(meta)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if rootObject["deploymentMode"] == "3p" {
		delete(rootObject, "deploymentMode")
	}
	if metaObject["appliedId"] == configID {
		delete(metaObject, "appliedId")
	}
	if entries, ok := metaObject["entries"].([]any); ok {
		kept := make([]any, 0, len(entries))
		for _, entry := range entries {
			if item, ok := entry.(map[string]any); ok && item["id"] == configID {
				continue
			}
			kept = append(kept, entry)
		}
		if len(kept) == 0 {
			delete(metaObject, "entries")
		} else {
			metaObject["entries"] = kept
		}
	}
	rootRaw, err := json.MarshalIndent(rootObject, "", "  ")
	if err != nil {
		return application.ApplyResult{}, err
	}
	metaRaw, err := json.MarshalIndent(metaObject, "", "  ")
	if err != nil {
		return application.ApplyResult{}, err
	}
	current := stateBundle{Version: 1}
	current.Root, _ = snapshot(root)
	current.Meta, _ = snapshot(meta)
	current.Library, _ = snapshot(library)
	currentRaw, _ := json.MarshalIndent(current, "", "  ")
	if err = safefile.AtomicWrite(state, append(currentRaw, '\n'), 0600); err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := safefile.Backup(state, ".airoute.bak.")
	if err != nil {
		return application.ApplyResult{}, err
	}
	next := stateBundle{Version: 1, Root: fileSnapshot{Exists: true, Content: string(append(rootRaw, '\n'))}, Meta: fileSnapshot{Exists: true, Content: string(append(metaRaw, '\n'))}}
	for path, value := range map[string]fileSnapshot{root: next.Root, meta: next.Meta, library: next.Library} {
		if err = writeSnapshot(path, value); err != nil {
			return application.ApplyResult{}, err
		}
	}
	nextRaw, _ := json.MarshalIndent(next, "", "  ")
	_ = safefile.AtomicWrite(state, append(nextRaw, '\n'), 0600)
	_ = safefile.Prune(state, ".airoute.bak.", 10)
	return application.ApplyResult{OK: true, Path: library, Backup: claudeAppBackupName(backup)}, nil
}

func claudeAppBackupName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

func verifyStage(id, label string, ok bool, message, detail string, started time.Time) application.VerifyStage {
	return application.VerifyStage{ID: id, Label: label, OK: ok, Message: message, Detail: detail, Latency: time.Since(started).Milliseconds()}
}

func (a *Adapter) Verify(ctx context.Context, options application.VerifyOptions) (application.VerifyResult, error) {
	result := application.VerifyResult{OK: true, Verified: time.Now().UTC()}
	started := time.Now()
	state, err := a.Read(ctx)
	if err != nil {
		return result, err
	}
	result.Stages = append(result.Stages, verifyStage("installation", "安装检测", state.Detection.Installed, state.Detection.Message, state.Detection.Executable, started))
	if !state.Detection.Installed {
		result.OK = false
	}
	started = time.Now()
	desired, err := decodeDesired(options.Config)
	if err != nil {
		result.OK = false
		result.Stages = append(result.Stages, verifyStage("configuration", "配置检测", false, "Claude App 配置不可用", err.Error(), started))
		return result, nil
	}
	actual := state.Managed
	synced := actual["base_url"] == desired.BaseURL && actual["model"] == desired.Model && actual["sonnet_model"] == desired.SonnetModel && actual["opus_model"] == desired.OpusModel && actual["haiku_model"] == desired.HaikuModel
	result.Stages = append(result.Stages, verifyStage("configuration", "配置检测", synced, map[bool]string{true: "Claude-3p 配置已同步", false: "页面配置尚未写入 Claude App"}[synced], state.Path, started))
	if !synced {
		result.OK = false
	}
	started = time.Now()
	body, _ := json.Marshal(map[string]any{"model": desired.Model, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "请只回复：AI_ROUTER_READY"}}})
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, desired.BaseURL+"/v1/messages", bytes.NewReader(body))
	if reqErr == nil {
		req.Header.Set("content-type", "application/json")
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("x-api-key", desired.APIKey)
	}
	client := a.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if reqErr == nil {
		var resp *http.Response
		resp, reqErr = client.Do(req)
		if reqErr == nil {
			responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if readErr != nil {
				reqErr = readErr
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				reqErr = fmt.Errorf("网关返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
			}
		}
	}
	ok := reqErr == nil
	detail := desired.BaseURL
	if reqErr != nil {
		detail = reqErr.Error()
		result.OK = false
	}
	result.Stages = append(result.Stages, verifyStage("gateway", "网关链路", ok, map[bool]string{true: "Anthropic 协议链路验证成功", false: "网关链路验证失败"}[ok], detail, started))
	return result, nil
}

func (a *Adapter) Backups(context.Context) ([]application.Backup, error) {
	_, _, _, state, err := a.paths()
	if err != nil {
		return nil, err
	}
	entries, err := safefile.List(state, ".airoute.bak.")
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
	_, _, _, state, err := a.paths()
	if err != nil {
		return err
	}
	return safefile.RemoveBackup(state, ".airoute.bak.", name)
}

func (a *Adapter) Rollback(_ context.Context, name string) (application.ApplyResult, error) {
	root, meta, library, state, err := a.paths()
	if err != nil {
		return application.ApplyResult{}, err
	}
	backupPath, ok := safefile.ResolveBackup(state, ".airoute.bak.", name)
	if !ok {
		return application.ApplyResult{}, errors.New("无效的 Claude App 备份名称")
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	var bundle stateBundle
	if err = json.Unmarshal(raw, &bundle); err != nil || bundle.Version != 1 {
		return application.ApplyResult{}, errors.New("Claude App 备份内容无效")
	}
	current := stateBundle{Version: 1}
	current.Root, _ = snapshot(root)
	current.Meta, _ = snapshot(meta)
	current.Library, _ = snapshot(library)
	currentRaw, _ := json.MarshalIndent(current, "", "  ")
	_ = safefile.AtomicWrite(state, append(currentRaw, '\n'), 0600)
	currentBackup, err := safefile.Backup(state, ".airoute.bak.")
	if err != nil {
		return application.ApplyResult{}, err
	}
	for path, snapshot := range map[string]fileSnapshot{root: bundle.Root, meta: bundle.Meta, library: bundle.Library} {
		if err = writeSnapshot(path, snapshot); err != nil {
			return application.ApplyResult{}, err
		}
	}
	bundleRaw, _ := json.MarshalIndent(bundle, "", "  ")
	_ = safefile.AtomicWrite(state, append(bundleRaw, '\n'), 0600)
	return application.ApplyResult{OK: true, Path: library, Backup: filepath.Base(currentBackup)}, nil
}
