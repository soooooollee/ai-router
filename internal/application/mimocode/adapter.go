package mimocode

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
	"sort"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/safefile"
)

const backupMarker = ".airoute.bak."

type DesiredConfig struct {
	BaseURL string   `json:"base_url"`
	APIKey  string   `json:"api_key"`
	Model   string   `json:"model"`
	Models  []string `json:"models,omitempty"`
}

type Adapter struct {
	ConfigPath string
	HTTPClient *http.Client
	LookPath   func(string) (string, error)
}

func New() *Adapter {
	return &Adapter{HTTPClient: &http.Client{Timeout: 60 * time.Second}, LookPath: exec.LookPath}
}

func (a *Adapter) Manifest() application.Manifest {
	return application.Manifest{
		ID: "mimo-code", Name: "MiMo Code", Description: "小米 MiMo 编码助手",
		Status: "available", ConfigFormat: "json",
		Capabilities: []application.Capability{application.CapabilityDetect, application.CapabilityConfigure, application.CapabilityPreview, application.CapabilityVerify, application.CapabilityRollback, application.CapabilityCleanup, application.CapabilityEdit},
	}
}

func (a *Adapter) path() (string, error) {
	if value := strings.TrimSpace(a.ConfigPath); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv("AIROUTE_MIMOCODE_CONFIG_PATH")); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "mimocode", "mimocode.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "mimocode", "mimocode.json"), nil
}

func (a *Adapter) executable() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AIROUTE_MIMOCODE_EXECUTABLE")); value != "" {
		return value, nil
	}
	lookPath := a.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if path, err := lookPath("mimo"); err == nil {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".mimocode", "bin", "mimo")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	}
	return "", exec.ErrNotFound
}

func (a *Adapter) Detect(ctx context.Context) (application.Detection, error) {
	path, err := a.executable()
	if err != nil {
		return application.Detection{Installed: false, Message: "未检测到 MiMo Code 命令"}, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, commandErr := exec.CommandContext(checkCtx, path, "--version").CombinedOutput()
	version := strings.TrimSpace(string(output))
	if commandErr != nil {
		return application.Detection{Installed: true, Executable: path, Version: version, Message: "已检测到 MiMo Code，但版本读取失败"}, nil
	}
	return application.Detection{Installed: true, Executable: path, Version: version, Message: "MiMo Code 已安装"}, nil
}

func readDocument(path string) (map[string]any, []byte, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	document := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &document) != nil {
		return nil, nil, fmt.Errorf("%s 不是有效 JSON", path)
	}
	return document, raw, nil
}

func decodeDesired(raw json.RawMessage) (DesiredConfig, error) {
	var desired DesiredConfig
	if err := json.Unmarshal(raw, &desired); err != nil {
		return desired, fmt.Errorf("MiMo Code 配置无效: %w", err)
	}
	desired.BaseURL = strings.TrimRight(strings.TrimSpace(desired.BaseURL), "/")
	if !strings.HasSuffix(desired.BaseURL, "/v1") {
		desired.BaseURL += "/v1"
	}
	desired.APIKey = strings.TrimSpace(desired.APIKey)
	desired.Model = strings.TrimSpace(desired.Model)
	if desired.BaseURL == "/v1" || desired.Model == "" {
		return desired, errors.New("网关地址和默认模型为必填项")
	}
	if desired.APIKey == "" {
		desired.APIKey = "airoute-local"
	}
	return desired, nil
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

func merge(document map[string]any, desired DesiredConfig) ([]byte, error) {
	next, err := copyDocument(document)
	if err != nil {
		return nil, err
	}
	providers, _ := next["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	models := map[string]any{}
	unique := map[string]struct{}{}
	for _, model := range append([]string{desired.Model}, desired.Models...) {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		unique[model] = struct{}{}
	}
	names := make([]string, 0, len(unique))
	for model := range unique {
		names = append(names, model)
	}
	sort.Strings(names)
	for _, model := range names {
		models[model] = map[string]any{"name": model, "reasoning": true, "tool_call": true}
	}
	providers["airoute"] = map[string]any{
		"name": "AI Router", "npm": "@ai-sdk/openai-compatible", "api": desired.BaseURL,
		"options": map[string]any{"apiKey": desired.APIKey}, "models": models,
	}
	next["provider"] = providers
	next["model"] = "airoute/" + desired.Model
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func managed(document map[string]any) map[string]any {
	result := map[string]any{}
	if model, ok := document["model"].(string); ok {
		result["model"] = strings.TrimPrefix(model, "airoute/")
	}
	providers, _ := document["provider"].(map[string]any)
	provider, _ := providers["airoute"].(map[string]any)
	if value, ok := provider["api"].(string); ok {
		result["base_url"] = value
	}
	options, _ := provider["options"].(map[string]any)
	if value, ok := options["apiKey"].(string); ok {
		result["api_key"] = value
	}
	return result
}

func structuredDiff(before, after []byte) string {
	var left, right map[string]any
	if json.Unmarshal(before, &left) != nil {
		left = map[string]any{}
	}
	if json.Unmarshal(after, &right) != nil {
		return "配置内容已更新"
	}
	lines := make([]string, 0)
	keys := map[string]struct{}{}
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		a, _ := json.Marshal(left[key])
		b, _ := json.Marshal(right[key])
		if !bytes.Equal(a, b) {
			lines = append(lines, fmt.Sprintf("~ %s", key))
		}
	}
	if len(lines) == 0 {
		return "没有配置变化"
	}
	return strings.Join(lines, "\n")
}

func (a *Adapter) compose(raw json.RawMessage) (string, map[string]any, []byte, []byte, DesiredConfig, error) {
	desired, err := decodeDesired(raw)
	if err != nil {
		return "", nil, nil, nil, desired, err
	}
	path, err := a.path()
	if err != nil {
		return "", nil, nil, nil, desired, err
	}
	document, current, err := readDocument(path)
	if err != nil {
		return "", nil, nil, nil, desired, err
	}
	next, err := merge(document, desired)
	return path, document, current, next, desired, err
}

func (a *Adapter) Read(ctx context.Context) (application.State, error) {
	path, err := a.path()
	if err != nil {
		return application.State{}, err
	}
	document, raw, err := readDocument(path)
	if err != nil {
		return application.State{}, err
	}
	detection, err := a.Detect(ctx)
	if err != nil {
		return application.State{}, err
	}
	state := managed(document)
	return application.State{
		Manifest: a.Manifest(), Detection: detection, Path: path, Exists: len(bytes.TrimSpace(raw)) > 0,
		Managed: state, PreservedFields: len(document),
		Synced: state["base_url"] != nil && state["model"] != nil,
	}, nil
}

func (a *Adapter) ConfigurationSynced(_ context.Context) (bool, error) {
	path, err := a.path()
	if err != nil {
		return false, err
	}
	document, _, err := readDocument(path)
	if err != nil {
		return false, err
	}
	state := managed(document)
	baseURL, _ := state["base_url"].(string)
	model, _ := state["model"].(string)
	return strings.TrimSpace(baseURL) != "" && strings.TrimSpace(model) != "", nil
}

func (a *Adapter) Preview(_ context.Context, raw json.RawMessage) (application.Preview, error) {
	path, document, current, next, _, err := a.compose(raw)
	if err != nil {
		return application.Preview{}, err
	}
	currentPreview := current
	if len(bytes.TrimSpace(currentPreview)) == 0 {
		currentPreview = []byte("{}")
	}
	return application.Preview{
		Path: path, Current: currentPreview, Content: next, Diff: structuredDiff(currentPreview, next),
		PreservedFields: len(document), WillCreateBackup: len(bytes.TrimSpace(current)) > 0,
	}, nil
}

func (a *Adapter) Apply(_ context.Context, raw json.RawMessage) (application.ApplyResult, error) {
	path, _, _, next, _, err := a.compose(raw)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := safefile.Backup(path, backupMarker)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(path, next, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	if _, _, err = readDocument(path); err != nil {
		return application.ApplyResult{}, errors.New("写入后的 MiMo Code 配置无效")
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: filepath.Base(backup)}, nil
}

func (a *Adapter) ApplyRaw(_ context.Context, input application.RawConfig) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	var document map[string]any
	if err = json.Unmarshal([]byte(input.Content), &document); err != nil {
		return application.ApplyResult{}, fmt.Errorf("MiMo Code 配置不是有效 JSON: %w", err)
	}
	if document == nil {
		return application.ApplyResult{}, errors.New("MiMo Code 配置必须是 JSON 对象")
	}
	backup, err := safefile.Backup(path, backupMarker)
	if err != nil {
		return application.ApplyResult{}, err
	}
	next := []byte(strings.TrimSpace(input.Content) + "\n")
	if err = safefile.AtomicWrite(path, next, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: mimoBackupName(backup)}, nil
}

func (a *Adapter) Cleanup(_ context.Context) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	document, _, err := readDocument(path)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if providers, ok := document["provider"].(map[string]any); ok {
		delete(providers, "airoute")
		if len(providers) == 0 {
			delete(document, "provider")
		} else {
			document["provider"] = providers
		}
	}
	if model, _ := document["model"].(string); strings.HasPrefix(model, "airoute/") {
		delete(document, "model")
	}
	next, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := safefile.Backup(path, backupMarker)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(path, append(next, '\n'), 0600); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: mimoBackupName(backup)}, nil
}

func mimoBackupName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
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
	payload, _ := json.Marshal(map[string]any{"model": desired.Model, "messages": []any{map[string]any{"role": "user", "content": "Reply with OK."}}, "max_tokens": 16})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, desired.BaseURL+"/chat/completions", bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+desired.APIKey)
	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, requestErr := client.Do(req)
	detail := desired.BaseURL + "/chat/completions"
	ok := requestErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300
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
	message := "MiMo Code 链路验证成功"
	if !ok {
		message = "MiMo Code 链路验证失败"
	}
	result.Stages = append(result.Stages, application.VerifyStage{ID: "gateway", Label: "网关链路", OK: ok, Message: message, Detail: detail, Latency: time.Since(started).Milliseconds()})
	return result, nil
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
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		return application.ApplyResult{}, errors.New("备份不是有效的 MiMo Code JSON 配置")
	}
	currentBackup, err := safefile.Backup(path, backupMarker)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(path, raw, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, backupMarker, 10)
	return application.ApplyResult{OK: true, Path: path, Backup: filepath.Base(currentBackup)}, nil
}
