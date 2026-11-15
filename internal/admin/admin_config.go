package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/safefile"
	"gopkg.in/yaml.v3"
)

const webRedactionMask = "••••••••"

func sensitiveWebKey(key string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
	compact := strings.ReplaceAll(normalized, "_", "")
	return normalized == "key" || normalized == "token" || strings.HasSuffix(normalized, "_key") || strings.HasSuffix(normalized, "_token") || strings.HasSuffix(compact, "apikey") || strings.HasSuffix(compact, "accesstoken") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "authorization") || strings.Contains(normalized, "cookie") || strings.Contains(normalized, "password")
}

func collectSensitiveWebValues(value any, parentKey string, values map[string]struct{}) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			collectSensitiveWebValues(child, key, values)
		}
	case []any:
		for _, child := range item {
			collectSensitiveWebValues(child, parentKey, values)
		}
	case string:
		if sensitiveWebKey(parentKey) && item != "" && item != webRedactionMask {
			values[item] = struct{}{}
		}
	}
}

func redactWebValue(value any) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if sensitiveWebKey(key) {
				item[key] = webRedactionMask
			} else {
				redactWebValue(child)
			}
		}
	case []any:
		for _, child := range item {
			redactWebValue(child)
		}
	}
}

func redactConfigYAML(raw string) string {
	var document any
	if yaml.Unmarshal([]byte(raw), &document) != nil {
		return raw
	}
	redactWebValue(document)
	redacted, err := yaml.Marshal(document)
	if err != nil {
		return raw
	}
	return string(redacted)
}

func restoreMaskedConfigYAML(masked, original string) string {
	var edited, source any
	if yaml.Unmarshal([]byte(masked), &edited) != nil || yaml.Unmarshal([]byte(original), &source) != nil {
		return masked
	}
	restoreMaskedValue(edited, source)
	restored, err := yaml.Marshal(edited)
	if err != nil {
		return masked
	}
	return string(restored)
}

// maskedSecretRemains reports whether a sensitive field still holds the
// redaction mask after restoring from disk, which means it has no original
// value (for example a provider added in the raw editor) and would be written
// to disk verbatim.
func maskedSecretRemains(raw string) bool {
	var document any
	if yaml.Unmarshal([]byte(raw), &document) != nil {
		return false
	}
	return containsMaskedSecret(document)
}

func containsMaskedSecret(value any) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if text, ok := child.(string); ok && sensitiveWebKey(key) && text == webRedactionMask {
				return true
			}
			if containsMaskedSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsMaskedSecret(child) {
				return true
			}
		}
	}
	return false
}

func restoreMaskedValue(edited, source any) {
	switch current := edited.(type) {
	case map[string]any:
		original, _ := source.(map[string]any)
		for key, value := range current {
			originalValue := original[key]
			text, isText := value.(string)
			if sensitiveWebKey(key) && isText && text == webRedactionMask {
				if originalValue != nil {
					current[key] = originalValue
				}
				continue
			}
			restoreMaskedValue(value, originalValue)
		}
	case []any:
		original, _ := source.([]any)
		for index, value := range current {
			var originalValue any
			if item, ok := value.(map[string]any); ok {
				if id, ok := item["id"].(string); ok {
					for _, candidate := range original {
						if candidateMap, ok := candidate.(map[string]any); ok && candidateMap["id"] == id {
							originalValue = candidate
							break
						}
					}
				}
			}
			if originalValue == nil && index < len(original) {
				originalValue = original[index]
			}
			restoreMaskedValue(value, originalValue)
		}
	}
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request, save bool) {
	previousConfig := s.Config.Get()
	var in struct {
		YAML         string `json:"yaml"`
		ExpectedHash string `json:"expected_hash"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in) != nil {
		jsonOut(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if save && in.ExpectedHash != "" && in.ExpectedHash != s.Config.Get().Hash {
		jsonOut(w, 409, map[string]any{"error": "configuration changed since it was loaded", "code": "configuration_conflict", "current_hash": s.Config.Get().Hash})
		return
	}
	if strings.Contains(in.YAML, webRedactionMask) {
		original, readErr := os.ReadFile(previousConfig.SourcePath)
		if readErr != nil {
			apiError(w, 500, readErr)
			return
		}
		in.YAML = restoreMaskedConfigYAML(in.YAML, string(original))
		if maskedSecretRemains(in.YAML) {
			jsonOut(w, http.StatusUnprocessableEntity, map[string]any{
				"error": "a masked secret has no original value; enter the secret explicitly",
				"code":  "unrestorable_secret",
			})
			return
		}
	}
	dir := filepath.Dir(s.Config.Get().SourcePath)
	tmp, e := os.CreateTemp(dir, ".airoute-validate-*.yaml")
	if e != nil {
		apiError(w, 500, e)
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if e = tmp.Chmod(0600); e == nil {
		_, e = tmp.WriteString(in.YAML)
	}
	if e == nil {
		e = tmp.Sync()
	}
	if closeErr := tmp.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		apiError(w, 500, e)
		return
	}
	c, e := config.Load(name)
	if e != nil {
		jsonOut(w, 422, map[string]any{"valid": false, "error": e.Error()})
		return
	}
	if !save {
		jsonOut(w, 200, map[string]any{"valid": true})
		return
	}
	target := s.Config.Get().SourcePath
	backup, e := safefile.Backup(target, ".bak.")
	if e != nil {
		apiError(w, 500, e)
		return
	}
	if e = safefile.AtomicWrite(target, []byte(in.YAML), 0600); e != nil {
		apiError(w, 500, e)
		return
	}
	c, e = config.Load(target)
	if e != nil {
		if backup != "" {
			if previous, readErr := os.ReadFile(backup); readErr == nil {
				_ = safefile.AtomicWrite(target, previous, 0600)
			}
		}
		apiError(w, 500, fmt.Errorf("saved configuration failed verification and was restored: %w", e))
		return
	}
	c.SourcePath = target
	effects := config.CompareEffects(previousConfig, c)
	if s.GatewayControl != nil {
		s.GatewayControl.ApplyRuntimeConfig(previousConfig, c)
	}
	if len(effects.RuntimeRebuilt) > 0 {
		if s.GatewayControl == nil {
			effects.RestartNeeded = append(effects.RestartNeeded, effects.RuntimeRebuilt...)
			effects.RuntimeRebuilt = nil
		}
	}
	s.Config.Replace(c)
	_ = safefile.Prune(target, ".bak.", 10)
	jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash, "backup": filepath.Base(backup), "hot_reloaded": effects.HotReloaded, "runtime_rebuilt": effects.RuntimeRebuilt, "restart_required": effects.RestartNeeded})
}

func (s *Server) backups(w http.ResponseWriter) {
	target := s.Config.Get().SourcePath
	entries, _ := safefile.List(target, ".bak.")
	var out []any
	for _, entry := range entries {
		out = append(out, map[string]any{"name": entry.Name, "size": entry.Size, "modified": entry.ModifiedAt, "contains_sensitive_config": true})
	}
	jsonOut(w, 200, map[string]any{"backups": out})
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	previousConfig := s.Config.Get()
	var in struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.Contains(in.Name, "/") {
		jsonOut(w, 400, map[string]any{"error": "invalid backup name"})
		return
	}
	target := s.Config.Get().SourcePath
	source, ok := safefile.ResolveBackup(target, ".bak.", in.Name)
	if !ok {
		jsonOut(w, 400, map[string]any{"error": "invalid backup name"})
		return
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		apiError(w, 404, e)
		return
	}
	c, e := config.Load(source)
	if e != nil {
		apiError(w, 422, e)
		return
	}
	if _, e = safefile.Backup(target, ".bak."); e != nil {
		apiError(w, 500, e)
		return
	}
	if e = safefile.AtomicWrite(target, raw, 0600); e != nil {
		apiError(w, 500, e)
		return
	}
	c.SourcePath = target
	effects := config.CompareEffects(previousConfig, c)
	if s.GatewayControl != nil {
		s.GatewayControl.ApplyRuntimeConfig(previousConfig, c)
	} else if len(effects.RuntimeRebuilt) > 0 {
		effects.RestartNeeded = append(effects.RestartNeeded, effects.RuntimeRebuilt...)
		effects.RuntimeRebuilt = nil
	}
	s.Config.Replace(c)
	_ = safefile.Prune(target, ".bak.", 10)
	jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash, "hot_reloaded": effects.HotReloaded, "runtime_rebuilt": effects.RuntimeRebuilt, "restart_required": effects.RestartNeeded})
}

func redactLogBody(raw string) string {
	if raw == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	redactWebValue(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(redacted)
}
