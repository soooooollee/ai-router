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
	"strings"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/safefile"
)

const backupMarker = ".airoute.bak."
const backupBundleFormat = "airoute.codex.backup.v1"

type fileSnapshot struct {
	Exists  bool   `json:"exists"`
	Content []byte `json:"content,omitempty"`
}

type backupBundle struct {
	Format  string       `json:"format"`
	Config  fileSnapshot `json:"config"`
	Catalog fileSnapshot `json:"catalog"`
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
