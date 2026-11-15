package pi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

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
