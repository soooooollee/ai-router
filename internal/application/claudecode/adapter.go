package claudecode

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
	"strings"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/safefile"
)

var managedEnvKeys = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

type DesiredConfig struct {
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`
	OpusModel   string `json:"opus_model"`
	SonnetModel string `json:"sonnet_model"`
	HaikuModel  string `json:"haiku_model"`
}

type Adapter struct {
	SettingsPath string
	HTTPClient   *http.Client
	LookPath     func(string) (string, error)
	CLITimeout   time.Duration
}

func New() *Adapter {
	return &Adapter{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		LookPath:   exec.LookPath,
		CLITimeout: 120 * time.Second,
	}
}

func (a *Adapter) Manifest() application.Manifest {
	return application.Manifest{
		ID:           "claude-code",
		Name:         "Claude Code",
		Description:  "命令行编码助手",
		Status:       "available",
		Capabilities: []application.Capability{application.CapabilityDetect, application.CapabilityConfigure, application.CapabilityPreview, application.CapabilityVerify, application.CapabilityRollback, application.CapabilityCleanup, application.CapabilityEdit},
		ConfigFormat: "json",
	}
}

func (a *Adapter) path() (string, error) {
	if strings.TrimSpace(a.SettingsPath) != "" {
		return a.SettingsPath, nil
	}
	if override := strings.TrimSpace(os.Getenv("AIROUTE_CLAUDE_SETTINGS_PATH")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func (a *Adapter) Detect(ctx context.Context) (application.Detection, error) {
	lookPath := a.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath("claude")
	if err != nil {
		return application.Detection{Installed: false, Message: "未检测到 Claude Code 命令"}, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, path, "--version").CombinedOutput()
	version := strings.TrimSpace(string(output))
	if len(version) > 256 {
		version = version[:256]
	}
	if err != nil {
		return application.Detection{Installed: true, Executable: path, Version: version, Message: "已检测到命令，但版本读取失败"}, nil
	}
	return application.Detection{Installed: true, Executable: path, Version: version, Message: "Claude Code 已安装"}, nil
}

func readSettings(path string) (map[string]any, []byte, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	settings := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, nil, fmt.Errorf("Claude Code 配置不是有效 JSON: %w", err)
		}
	}
	return settings, raw, nil
}

func managedState(settings map[string]any) map[string]any {
	env, _ := settings["env"].(map[string]any)
	managed := map[string]any{}
	for _, key := range managedEnvKeys {
		if value, ok := env[key].(string); ok && value != "" {
			managed[key] = value
		}
	}
	return managed
}

func (a *Adapter) Read(ctx context.Context) (application.State, error) {
	path, err := a.path()
	if err != nil {
		return application.State{}, err
	}
	settings, raw, err := readSettings(path)
	if err != nil {
		return application.State{}, err
	}
	detection, err := a.Detect(ctx)
	if err != nil {
		return application.State{}, err
	}
	managed := managedState(settings)
	baseURL, _ := managed["ANTHROPIC_BASE_URL"].(string)
	model, _ := managed["ANTHROPIC_MODEL"].(string)
	return application.State{
		Manifest:        a.Manifest(),
		Detection:       detection,
		Path:            path,
		Exists:          len(bytes.TrimSpace(raw)) > 0,
		Managed:         managed,
		PreservedFields: len(settings),
		Synced:          baseURL != "" && model != "",
	}, nil
}

func (a *Adapter) ConfigurationSynced(_ context.Context) (bool, error) {
	path, err := a.path()
	if err != nil {
		return false, err
	}
	settings, _, err := readSettings(path)
	if err != nil {
		return false, err
	}
	managed := managedState(settings)
	baseURL, _ := managed["ANTHROPIC_BASE_URL"].(string)
	model, _ := managed["ANTHROPIC_MODEL"].(string)
	return baseURL != "" && model != "", nil
}

func decodeDesired(raw json.RawMessage) (DesiredConfig, error) {
	var desired DesiredConfig
	if err := json.Unmarshal(raw, &desired); err != nil {
		return desired, fmt.Errorf("invalid Claude Code configuration: %w", err)
	}
	desired.BaseURL = strings.TrimRight(strings.TrimSpace(desired.BaseURL), "/")
	desired.Model = strings.TrimSpace(desired.Model)
	if desired.BaseURL == "" || desired.Model == "" {
		return desired, errors.New("网关地址和主模型为必填项")
	}
	return desired, nil
}

func merge(settings map[string]any, desired DesiredConfig) map[string]any {
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["ANTHROPIC_BASE_URL"] = desired.BaseURL
	if desired.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = desired.APIKey
	} else if _, ok := env["ANTHROPIC_API_KEY"]; !ok {
		env["ANTHROPIC_API_KEY"] = "airoute-local"
	}
	env["ANTHROPIC_MODEL"] = desired.Model
	roles := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   desired.OpusModel,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": desired.SonnetModel,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  desired.HaikuModel,
	}
	for key, value := range roles {
		if strings.TrimSpace(value) == "" {
			delete(env, key)
		} else {
			env[key] = strings.TrimSpace(value)
		}
	}
	settings["env"] = env
	return settings
}

func marshalSettings(settings map[string]any) ([]byte, error) {
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func lineDiff(before, after []byte) string {
	left := strings.Split(string(before), "\n")
	right := strings.Split(string(after), "\n")
	count := max(len(left), len(right))
	lines := make([]string, 0)
	for i := 0; i < count && len(lines) < 240; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if l == r {
			continue
		}
		if i < len(left) {
			lines = append(lines, "- "+l)
		}
		if i < len(right) {
			lines = append(lines, "+ "+r)
		}
	}
	if len(lines) == 0 {
		return "没有配置变化"
	}
	return strings.Join(lines, "\n")
}

type composedConfig struct {
	path      string
	previous  []byte
	next      []byte
	preserved int
}

func (a *Adapter) compose(raw json.RawMessage) (composedConfig, error) {
	desired, err := decodeDesired(raw)
	if err != nil {
		return composedConfig{}, err
	}
	path, err := a.path()
	if err != nil {
		return composedConfig{}, err
	}
	settings, previous, err := readSettings(path)
	if err != nil {
		return composedConfig{}, err
	}
	next, err := marshalSettings(merge(settings, desired))
	if err != nil {
		return composedConfig{}, err
	}
	return composedConfig{path: path, previous: previous, next: next, preserved: len(settings)}, nil
}

func (a *Adapter) Preview(_ context.Context, raw json.RawMessage) (application.Preview, error) {
	composed, err := a.compose(raw)
	if err != nil {
		return application.Preview{}, err
	}
	current := composed.previous
	if len(bytes.TrimSpace(current)) == 0 {
		current = []byte("{}")
	}
	return application.Preview{
		Path:             composed.path,
		Current:          json.RawMessage(current),
		Content:          json.RawMessage(composed.next),
		Diff:             lineDiff(composed.previous, composed.next),
		PreservedFields:  composed.preserved,
		WillCreateBackup: len(composed.previous) > 0,
	}, nil
}

func backupCurrent(path string) (string, error) {
	return safefile.Backup(path, ".airoute.bak.")
}

func (a *Adapter) Apply(ctx context.Context, raw json.RawMessage) (application.ApplyResult, error) {
	_ = ctx
	composed, err := a.compose(raw)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := backupCurrent(composed.path)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(composed.path, composed.next, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	if _, _, err = readSettings(composed.path); err != nil {
		return application.ApplyResult{}, fmt.Errorf("written Claude Code configuration is invalid: %w", err)
	}
	_ = safefile.Prune(composed.path, ".airoute.bak.", 10)
	return application.ApplyResult{OK: true, Path: composed.path, Backup: filepath.Base(backup)}, nil
}

func (a *Adapter) ApplyRaw(_ context.Context, input application.RawConfig) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	var document map[string]any
	if err = json.Unmarshal([]byte(input.Content), &document); err != nil {
		return application.ApplyResult{}, fmt.Errorf("Claude Code 配置不是有效 JSON: %w", err)
	}
	if document == nil {
		return application.ApplyResult{}, errors.New("Claude Code 配置必须是 JSON 对象")
	}
	backup, err := backupCurrent(path)
	if err != nil {
		return application.ApplyResult{}, err
	}
	next := []byte(strings.TrimSpace(input.Content) + "\n")
	if err = safefile.AtomicWrite(path, next, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, ".airoute.bak.", 10)
	return application.ApplyResult{OK: true, Path: path, Backup: backupBase(backup)}, nil
}

func (a *Adapter) Cleanup(_ context.Context) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	settings, _, err := readSettings(path)
	if err != nil {
		return application.ApplyResult{}, err
	}
	env, _ := settings["env"].(map[string]any)
	for _, key := range managedEnvKeys {
		delete(env, key)
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	next, err := marshalSettings(settings)
	if err != nil {
		return application.ApplyResult{}, err
	}
	backup, err := backupCurrent(path)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(path, next, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, ".airoute.bak.", 10)
	return application.ApplyResult{OK: true, Path: path, Backup: backupBase(backup)}, nil
}

func backupBase(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

func desiredFromSettings(settings map[string]any) DesiredConfig {
	env, _ := settings["env"].(map[string]any)
	value := func(key string) string {
		v, _ := env[key].(string)
		return v
	}
	return DesiredConfig{
		BaseURL:     value("ANTHROPIC_BASE_URL"),
		APIKey:      value("ANTHROPIC_API_KEY"),
		Model:       value("ANTHROPIC_MODEL"),
		OpusModel:   value("ANTHROPIC_DEFAULT_OPUS_MODEL"),
		SonnetModel: value("ANTHROPIC_DEFAULT_SONNET_MODEL"),
		HaikuModel:  value("ANTHROPIC_DEFAULT_HAIKU_MODEL"),
	}
}

func stage(id, label string, ok bool, message, detail string, started time.Time) application.VerifyStage {
	return application.VerifyStage{ID: id, Label: label, OK: ok, Message: message, Detail: detail, Latency: time.Since(started).Milliseconds()}
}

func (a *Adapter) Verify(ctx context.Context, options application.VerifyOptions) (application.VerifyResult, error) {
	result := application.VerifyResult{OK: true, Verified: time.Now().UTC()}

	detectStarted := time.Now()
	detection, err := a.Detect(ctx)
	if err != nil {
		return result, err
	}
	result.Stages = append(result.Stages, stage("installation", "安装检测", detection.Installed, detection.Message, strings.TrimSpace(detection.Version), detectStarted))
	if !detection.Installed {
		result.OK = false
	}

	configStarted := time.Now()
	path, err := a.path()
	var settings map[string]any
	var settingsRaw []byte
	if err == nil {
		settings, settingsRaw, err = readSettings(path)
	}
	if err == nil && len(bytes.TrimSpace(settingsRaw)) == 0 {
		err = errors.New("Claude Code 配置文件不存在")
	}
	actual := desiredFromSettings(settings)
	var desired DesiredConfig
	if err == nil && len(options.Config) > 0 {
		desired, err = decodeDesired(options.Config)
	} else if err == nil {
		desired = actual
		_, err = decodeDesired(mustJSON(desired))
	}
	if err != nil {
		result.OK = false
		result.Stages = append(result.Stages, stage("configuration", "配置检测", false, "Claude Code 配置不可用", err.Error(), configStarted))
		return result, nil
	}
	if desired.APIKey == "" {
		desired.APIKey = actual.APIKey
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		result.OK = false
		result.Stages = append(result.Stages, stage("configuration", "配置检测", false, "无法检查配置文件权限", statErr.Error(), configStarted))
		return result, nil
	}
	if info.Mode().Perm()&0077 != 0 {
		result.OK = false
		result.Stages = append(result.Stages, stage("configuration", "配置检测", false, "配置文件权限过宽", fmt.Sprintf("%s 权限为 %04o，要求 0600", path, info.Mode().Perm()), configStarted))
		return result, nil
	}
	synced := actual.BaseURL == desired.BaseURL && actual.Model == desired.Model && actual.OpusModel == desired.OpusModel && actual.SonnetModel == desired.SonnetModel && actual.HaikuModel == desired.HaikuModel
	if !synced {
		result.OK = false
		result.Stages = append(result.Stages, stage("configuration", "配置检测", false, "页面配置尚未写入 Claude Code", path, configStarted))
	} else {
		result.Stages = append(result.Stages, stage("configuration", "配置检测", true, "配置已同步且文件权限安全", path, configStarted))
	}

	gatewayStarted := time.Now()
	requestBody := mustJSON(map[string]any{
		"model":      desired.Model,
		"max_tokens": 16,
		"messages":   []any{map[string]any{"role": "user", "content": "请只回复：AI_ROUTER_READY"}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, desired.BaseURL+"/v1/messages", bytes.NewReader(requestBody))
	if err == nil {
		req.Header.Set("content-type", "application/json")
		req.Header.Set("anthropic-version", "2023-06-01")
		if desired.APIKey != "" {
			req.Header.Set("authorization", "Bearer "+desired.APIKey)
			req.Header.Set("x-api-key", desired.APIKey)
		}
		client := a.HTTPClient
		if client == nil {
			client = &http.Client{Timeout: 60 * time.Second}
		}
		var response *http.Response
		response, err = client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
			if readErr != nil {
				err = readErr
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = fmt.Errorf("gateway returned HTTP %d: %s", response.StatusCode, truncate(string(body), 512))
			} else {
				detail := fmt.Sprintf("request_id=%s provider=%s model=%s response=%s", response.Header.Get("x-airoute-request-id"), response.Header.Get("x-airoute-provider-id"), firstNonEmpty(response.Header.Get("x-airoute-model"), desired.Model), truncate(redactSecrets(string(body), desired.APIKey), 256))
				result.Stages = append(result.Stages, stage("gateway", "网关链路", true, "Anthropic 协议链路验证成功", detail, gatewayStarted))
			}
		}
	}
	if err != nil {
		result.OK = false
		result.Stages = append(result.Stages, stage("gateway", "网关链路", false, "网关链路验证失败", truncate(err.Error(), 512), gatewayStarted))
	}

	if options.RunCLI {
		cliStarted := time.Now()
		if !detection.Installed {
			result.OK = false
			result.Stages = append(result.Stages, stage("cli", "Claude Code Smoke", false, "Claude Code 未安装", "", cliStarted))
		} else {
			timeout := a.CLITimeout
			if timeout <= 0 {
				timeout = 120 * time.Second
			}
			cliCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			command := exec.CommandContext(cliCtx, detection.Executable, "-p", "请只回复：CLAUDE_CODE_READY", "--model", desired.Model, "--output-format", "text", "--max-turns", "1")
			command.Env = append(os.Environ(), "ANTHROPIC_BASE_URL="+desired.BaseURL, "ANTHROPIC_API_KEY="+desired.APIKey)
			workdir, workdirErr := os.MkdirTemp("", "airoute-claude-smoke-")
			if workdirErr != nil {
				result.OK = false
				result.Stages = append(result.Stages, stage("cli", "Claude Code Smoke", false, "无法创建隔离工作目录", workdirErr.Error(), cliStarted))
				return result, nil
			}
			defer os.RemoveAll(workdir)
			command.Dir = workdir
			output := &cappedBuffer{limit: 64 << 10}
			command.Stdout = output
			command.Stderr = output
			cliErr := command.Run()
			if cliErr != nil {
				result.OK = false
				result.Stages = append(result.Stages, stage("cli", "Claude Code Smoke", false, "Claude Code 调用失败", truncate(redactSecrets(output.String()+" "+cliErr.Error(), desired.APIKey), 1024), cliStarted))
			} else {
				result.Stages = append(result.Stages, stage("cli", "Claude Code Smoke", true, "Claude Code 完整调用成功", truncate(redactSecrets(output.String(), desired.APIKey), 512), cliStarted))
			}
		}
	}
	return result, nil
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func redactSecrets(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buffer.Write(p)
	}
	return original, nil
}

func (b *cappedBuffer) String() string { return b.buffer.String() }

func (a *Adapter) Backups(_ context.Context) ([]application.Backup, error) {
	path, err := a.path()
	if err != nil {
		return nil, err
	}
	entries, err := safefile.List(path, ".airoute.bak.")
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
	return safefile.RemoveBackup(path, ".airoute.bak.", name)
}

func (a *Adapter) Rollback(_ context.Context, name string) (application.ApplyResult, error) {
	path, err := a.path()
	if err != nil {
		return application.ApplyResult{}, err
	}
	backupPath, ok := safefile.ResolveBackup(path, ".airoute.bak.", name)
	if !ok {
		return application.ApplyResult{}, errors.New("invalid application backup name")
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		return application.ApplyResult{}, err
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		return application.ApplyResult{}, fmt.Errorf("backup is not valid JSON: %w", err)
	}
	currentBackup, err := backupCurrent(path)
	if err != nil {
		return application.ApplyResult{}, err
	}
	if err = safefile.AtomicWrite(path, raw, 0600); err != nil {
		return application.ApplyResult{}, err
	}
	_ = safefile.Prune(path, ".airoute.bak.", 10)
	return application.ApplyResult{OK: true, Path: path, Backup: filepath.Base(currentBackup)}, nil
}
