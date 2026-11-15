package codex

import (
	"encoding/json"
	"strings"
)

const catalogName = "airoute-model-catalog.json"

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
