package pi

import (
	"encoding/json"
	"sort"
	"strings"
)

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
