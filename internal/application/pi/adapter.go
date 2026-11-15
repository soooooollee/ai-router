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
	AgentDir   string
	HTTPClient *http.Client
	LookPath   func(string) (string, error)
	WriteFile  func(string, []byte) error
}

func New() *Adapter {
	return &Adapter{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		LookPath:   exec.LookPath,
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

func (a *Adapter) Detect(_ context.Context) (application.Detection, error) {
	path, err := a.executable()
	if err != nil {
		return application.Detection{Message: "未检测到 Pi Coding Agent 命令"}, nil
	}
	// Pi is normally discovered as a Node.js-generated .cmd shim on Windows.
	// Application-page reads and preview refreshes are passive operations, so
	// never launch the interactive CLI merely to display its version: each
	// probe would create a cmd.exe window and can wait for the agent startup.
	return application.Detection{Installed: true, Executable: path, Message: "Pi Coding Agent 已检测到"}, nil
}

func (a *Adapter) write(path string, content []byte) error {
	if a.WriteFile != nil {
		return a.WriteFile(path, content)
	}
	return safefile.AtomicWrite(path, content, 0600)
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
