package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/soooooollee/ai-router/internal/application"
	"github.com/soooooollee/ai-router/internal/clientstore"
)

func applicationConfigurationCounts(ctx context.Context, registry *application.Registry) (configured, total int) {
	if registry == nil {
		return 0, 0
	}
	for _, adapter := range registry.List() {
		total++
		reader, ok := adapter.(application.ConfigurationStatusReader)
		if !ok {
			continue
		}
		synced, err := reader.ConfigurationSynced(ctx)
		if err == nil && synced {
			configured++
		}
	}
	return configured, total
}

func currentApplicationKey(state application.State) string {
	for _, key := range []string{"api_key", "ANTHROPIC_API_KEY", "inferenceGatewayApiKey"} {
		if value, ok := state.Managed[key].(string); ok && value != "" && value != webRedactionMask {
			return value
		}
	}
	values := map[string]struct{}{}
	collectSensitiveWebValues(state.Managed, "", values)
	for value := range values {
		return value
	}
	return ""
}

func (s *Server) applicationCredential(ctx context.Context, key string) (clientstore.Credential, bool) {
	key = strings.TrimSpace(key)
	if key == "" || s.ClientStore == nil || s.CredentialKeys == nil {
		return clientstore.Credential{}, false
	}
	for keyID, digest := range s.CredentialKeys.Digests(key) {
		credential, err := s.ClientStore.GetCredentialByHMAC(ctx, clientstore.DefaultScope, keyID, digest)
		if err == nil && s.CredentialKeys.Verify(credential.HMACKeyID, key, credential.SecretHMAC) {
			return credential, true
		}
	}
	return clientstore.Credential{}, false
}

func (s *Server) managedApplicationCredential(ctx context.Context, key string) bool {
	credential, ok := s.applicationCredential(ctx, key)
	return ok && credential.Kind == clientstore.CredentialManaged
}

func (s *Server) attachApplicationCredentialMetadata(ctx context.Context, state *application.State, key string) {
	credential, ok := s.applicationCredential(ctx, key)
	if !ok {
		return
	}
	if state.Managed == nil {
		state.Managed = map[string]any{}
	}
	state.Managed["airoute_client_credential_prefix"] = credential.Prefix
	state.Managed["airoute_client_credential_status"] = credential.Status
	state.Managed["airoute_client_credential_id"] = credential.ID
	state.Managed["airoute_client_credential_recoverable"] = credential.View().Recoverable
	if client, clientErr := s.ClientStore.GetClient(ctx, clientstore.DefaultScope, credential.ClientID); clientErr == nil {
		state.Managed["airoute_client_credential_name"] = client.Name
	}
}

func (s *Server) resolveApplicationCredential(ctx context.Context, raw json.RawMessage) (json.RawMessage, string, error) {
	var desired map[string]any
	if err := json.Unmarshal(raw, &desired); err != nil {
		return raw, "", nil
	}
	credentialID, _ := desired["credential_id"].(string)
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		delete(desired, "credential_id")
		return raw, "", nil
	}
	if s.ClientStore == nil || s.CredentialKeys == nil {
		return nil, "", errors.New("managed credential selection is unavailable")
	}
	credential, err := s.ClientStore.GetCredential(ctx, clientstore.DefaultScope, credentialID)
	if err != nil {
		return nil, "", errors.New("selected managed credential was not found")
	}
	view := credential.View()
	if view.Status != clientstore.CredentialActive {
		return nil, "", fmt.Errorf("selected managed credential is %s", view.Status)
	}
	client, err := s.ClientStore.GetClient(ctx, clientstore.DefaultScope, credential.ClientID)
	if err != nil || client.Status != clientstore.ClientActive {
		return nil, "", errors.New("selected managed credential is disabled")
	}
	secret, err := s.CredentialKeys.Reveal(credential)
	if err != nil {
		return nil, "", errors.New("selected credential cannot be written to an application")
	}
	desired["api_key"] = secret
	delete(desired, "credential_id")
	resolved, err := json.Marshal(desired)
	if err != nil {
		return nil, "", err
	}
	return resolved, secret, nil
}

func restoreApplicationMaskedKey(ctx context.Context, adapter application.Adapter, raw json.RawMessage) json.RawMessage {
	var desired map[string]any
	if json.Unmarshal(raw, &desired) != nil || desired["api_key"] != webRedactionMask {
		return raw
	}
	state, err := adapter.Read(ctx)
	if err != nil {
		return raw
	}
	if key := currentApplicationKey(state); key != "" {
		desired["api_key"] = key
		restored, marshalErr := json.Marshal(desired)
		if marshalErr == nil {
			return restored
		}
	}
	return raw
}

func redactApplicationPreview(ctx context.Context, adapter application.Adapter, preview *application.Preview) {
	values := map[string]struct{}{}
	type textPreview struct {
		target *json.RawMessage
		value  string
	}
	textPreviews := make([]textPreview, 0, 2)
	for _, target := range []*json.RawMessage{&preview.Current, &preview.Content} {
		var content any
		if json.Unmarshal(*target, &content) == nil {
			if text, ok := content.(string); ok {
				textPreviews = append(textPreviews, textPreview{target: target, value: text})
				continue
			}
			collectSensitiveWebValues(content, "", values)
			redactWebValue(content)
			if redacted, err := json.Marshal(content); err == nil {
				*target = redacted
			}
		}
	}
	if state, err := adapter.Read(ctx); err == nil {
		collectSensitiveWebValues(state.Managed, "", values)
	}
	for _, item := range textPreviews {
		lines := strings.Split(item.value, "\n")
		for index, line := range lines {
			position := strings.Index(line, "=")
			if position >= 0 && sensitiveWebKey(strings.TrimSpace(line[:position])) {
				lines[index] = line[:position+1] + " \"" + webRedactionMask + "\""
			}
		}
		redacted := strings.Join(lines, "\n")
		for value := range values {
			redacted = strings.ReplaceAll(redacted, value, webRedactionMask)
		}
		if raw, err := json.Marshal(redacted); err == nil {
			*item.target = raw
		}
	}
	for value := range values {
		preview.Diff = strings.ReplaceAll(preview.Diff, value, webRedactionMask)
	}
}

func (s *Server) applicationAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/apps" {
		if r.Method != http.MethodGet {
			jsonOut(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		detect := r.URL.Query().Get("detect") != "false"
		apps := make([]map[string]any, 0)
		for _, adapter := range s.Applications.List() {
			detection := application.Detection{}
			if detect {
				var err error
				detection, err = adapter.Detect(r.Context())
				if err != nil {
					detection.Message = err.Error()
				}
			}
			apps = append(apps, map[string]any{"manifest": adapter.Manifest(), "detection": detection})
		}
		jsonOut(w, http.StatusOK, map[string]any{"apps": apps})
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "apps" {
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "application endpoint not found"})
		return
	}
	adapter, err := s.Applications.Get(parts[2])
	if err != nil {
		apiError(w, http.StatusNotFound, err)
		return
	}
	action := ""
	if len(parts) > 3 {
		action = parts[3]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		state, readErr := adapter.Read(r.Context())
		if readErr != nil {
			apiError(w, http.StatusUnprocessableEntity, readErr)
			return
		}
		applicationKey := currentApplicationKey(state)
		s.attachApplicationCredentialMetadata(r.Context(), &state, applicationKey)
		if s.Config.Get().Logging.WebRedaction || s.managedApplicationCredential(r.Context(), applicationKey) {
			redactWebValue(state.Managed)
			// The prefix and lifecycle status are safe display metadata and must
			// remain available even when the full managed credential is hidden.
			s.attachApplicationCredentialMetadata(r.Context(), &state, applicationKey)
		}
		jsonOut(w, http.StatusOK, state)
	case action == "preview" && r.Method == http.MethodPost:
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if readErr != nil {
			apiError(w, http.StatusBadRequest, readErr)
			return
		}
		raw = restoreApplicationMaskedKey(r.Context(), adapter, raw)
		var selectedSecret string
		raw, selectedSecret, readErr = s.resolveApplicationCredential(r.Context(), raw)
		if readErr != nil {
			apiError(w, http.StatusUnprocessableEntity, readErr)
			return
		}
		preview, previewErr := adapter.Preview(r.Context(), raw)
		if previewErr != nil {
			apiError(w, http.StatusUnprocessableEntity, errors.New(deploymentError(previewErr, selectedSecret)))
			return
		}
		state, _ := adapter.Read(r.Context())
		if selectedSecret != "" || s.Config.Get().Logging.WebRedaction || s.managedApplicationCredential(r.Context(), currentApplicationKey(state)) {
			redactApplicationPreview(r.Context(), adapter, &preview)
		}
		jsonOut(w, http.StatusOK, preview)
	case action == "config" && r.Method == http.MethodPut:
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if readErr != nil {
			apiError(w, http.StatusBadRequest, readErr)
			return
		}
		raw = restoreApplicationMaskedKey(r.Context(), adapter, raw)
		var selectedSecret string
		raw, selectedSecret, readErr = s.resolveApplicationCredential(r.Context(), raw)
		if readErr != nil {
			apiError(w, http.StatusUnprocessableEntity, readErr)
			return
		}
		result, applyErr := adapter.Apply(r.Context(), raw)
		if applyErr != nil {
			apiError(w, http.StatusUnprocessableEntity, errors.New(deploymentError(applyErr, selectedSecret)))
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "raw-config" && r.Method == http.MethodPut:
		rawAdapter, ok := adapter.(application.RawConfigAdapter)
		if !ok {
			jsonOut(w, http.StatusNotImplemented, map[string]any{"error": "application does not support editable previews"})
			return
		}
		var input application.RawConfig
		if decodeErr := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&input); decodeErr != nil {
			apiError(w, http.StatusBadRequest, decodeErr)
			return
		}
		if strings.TrimSpace(input.Content) == "" {
			jsonOut(w, http.StatusBadRequest, map[string]any{"error": "configuration content is required"})
			return
		}
		if strings.Contains(input.Content, webRedactionMask) {
			state, stateErr := adapter.Read(r.Context())
			key := currentApplicationKey(state)
			if stateErr != nil || key == "" {
				jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"error": "masked application credential cannot be restored"})
				return
			}
			input.Content = strings.ReplaceAll(input.Content, webRedactionMask, key)
		}
		input.Config = restoreApplicationMaskedKey(r.Context(), adapter, input.Config)
		resolvedConfig, selectedSecret, resolveErr := s.resolveApplicationCredential(r.Context(), input.Config)
		if resolveErr != nil {
			apiError(w, http.StatusUnprocessableEntity, resolveErr)
			return
		}
		input.Config = resolvedConfig
		result, applyErr := rawAdapter.ApplyRaw(r.Context(), input)
		if applyErr != nil {
			apiError(w, http.StatusUnprocessableEntity, errors.New(deploymentError(applyErr, selectedSecret)))
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "cleanup" && r.Method == http.MethodPost:
		cleanupAdapter, ok := adapter.(application.CleanupAdapter)
		if !ok {
			jsonOut(w, http.StatusNotImplemented, map[string]any{"error": "application does not support configuration cleanup"})
			return
		}
		result, cleanupErr := cleanupAdapter.Cleanup(r.Context())
		if cleanupErr != nil {
			apiError(w, http.StatusUnprocessableEntity, cleanupErr)
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "verify" && r.Method == http.MethodPost:
		var options application.VerifyOptions
		decodeErr := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&options)
		if decodeErr != nil && decodeErr != io.EOF {
			apiError(w, http.StatusBadRequest, decodeErr)
			return
		}
		options.Config = restoreApplicationMaskedKey(r.Context(), adapter, options.Config)
		var selectedSecret string
		options.Config, selectedSecret, decodeErr = s.resolveApplicationCredential(r.Context(), options.Config)
		if decodeErr != nil {
			apiError(w, http.StatusUnprocessableEntity, decodeErr)
			return
		}
		result, verifyErr := adapter.Verify(r.Context(), options)
		if verifyErr != nil {
			apiError(w, http.StatusUnprocessableEntity, errors.New(deploymentError(verifyErr, selectedSecret)))
			return
		}
		jsonOut(w, http.StatusOK, result)
	case action == "backups" && r.Method == http.MethodGet:
		backups, backupErr := adapter.Backups(r.Context())
		if backupErr != nil {
			apiError(w, http.StatusInternalServerError, backupErr)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"backups": backups})
	case action == "backups" && r.Method == http.MethodDelete:
		var input struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input) != nil || input.Name == "" {
			jsonOut(w, http.StatusBadRequest, map[string]any{"error": "backup name is required"})
			return
		}
		if deleteErr := adapter.DeleteBackup(r.Context(), input.Name); deleteErr != nil {
			apiError(w, http.StatusUnprocessableEntity, deleteErr)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"ok": true, "name": input.Name})
	case action == "rollback" && r.Method == http.MethodPost:
		var input struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input) != nil || input.Name == "" {
			jsonOut(w, http.StatusBadRequest, map[string]any{"error": "backup name is required"})
			return
		}
		result, rollbackErr := adapter.Rollback(r.Context(), input.Name)
		if rollbackErr != nil {
			apiError(w, http.StatusUnprocessableEntity, rollbackErr)
			return
		}
		jsonOut(w, http.StatusOK, result)
	default:
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "application endpoint not found"})
	}
}

func (s *Server) legacyClaudeCodeConfig(w http.ResponseWriter, r *http.Request) {
	adapter, err := s.Applications.Get("claude-code")
	if err != nil {
		apiError(w, http.StatusNotFound, err)
		return
	}
	if r.Method == http.MethodGet {
		state, readErr := adapter.Read(r.Context())
		if readErr != nil {
			apiError(w, http.StatusUnprocessableEntity, readErr)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"path": state.Path, "exists": state.Exists, "router": state.Managed, "preserved_fields": state.PreservedFields})
		return
	}
	raw, readErr := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if readErr != nil {
		apiError(w, http.StatusBadRequest, readErr)
		return
	}
	result, applyErr := adapter.Apply(r.Context(), raw)
	if applyErr != nil {
		apiError(w, http.StatusUnprocessableEntity, applyErr)
		return
	}
	jsonOut(w, http.StatusOK, result)
}
