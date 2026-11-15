package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/safefile"
)

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
	} else if len(a.DesktopExecutables) == 0 {
		// Store-distributed desktop applications do not necessarily expose an
		// executable that accepts Codex CLI arguments. Detect their platform
		// registration separately instead of trying to launch the GUI app.
		desktop = detectPlatformDesktopApplication(ctx)
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

func looksLikeChatGPTWindowsPackage(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(name, "chatgpt") &&
		(strings.Contains(name, "openai") || strings.HasPrefix(name, "chatgpt")) {
		return true
	}
	// The current Microsoft Store ChatGPT application is registered as
	// OpenAI.Codex even though its Start menu display name remains ChatGPT.
	// Match the official Store package-family publisher ID to avoid treating an
	// unrelated third-party package containing "Codex" as the OpenAI app.
	return strings.HasPrefix(name, "openai.codex_") && strings.HasSuffix(name, "_2p2nqsd0c76g0")
}

func looksLikeOfficialChatGPTWindowsAppID(appID string) bool {
	appID = strings.ToLower(strings.TrimSpace(appID))
	return strings.HasSuffix(appID, "_2p2nqsd0c76g0!app") &&
		(strings.HasPrefix(appID, "openai.codex_") || strings.HasPrefix(appID, "openai.chatgpt"))
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
