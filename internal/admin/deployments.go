package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/soooooollee/ai-router/internal/application"
	"github.com/soooooollee/ai-router/internal/clientstore"
)

func (s *Server) getPolicy(w http.ResponseWriter, r *http.Request, clientID string) {
	client, err := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, clientID)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	policy, err := s.ClientStore.GetPolicy(r.Context(), clientstore.DefaultScope, client.PolicyID)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	jsonOut(w, http.StatusOK, policy)
}

func (s *Server) updatePolicy(w http.ResponseWriter, r *http.Request, clientID string) {
	client, err := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, clientID)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	var policy clientstore.ClientPolicy
	if err = decodeLimitedJSON(r, &policy); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	policy.ID = client.PolicyID
	if err = s.ClientStore.UpdatePolicy(r.Context(), clientstore.DefaultScope, policy); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	if s.ClientAccess != nil {
		s.ClientAccess.Limiter.Forget(clientID)
	}
	_ = s.audit(r.Context(), "policy.updated", "client", clientID, nil)
	jsonOut(w, http.StatusOK, policy)
}

func (s *Server) clientUsage(w http.ResponseWriter, r *http.Request, clientID string) {
	query := clientstore.UsageQuery{}
	if value := r.URL.Query().Get("from"); value != "" {
		query.From, _ = time.Parse(time.RFC3339, value)
	}
	if value := r.URL.Query().Get("to"); value != "" {
		query.To, _ = time.Parse(time.RFC3339, value)
	}
	usage, err := s.ClientStore.GetUsage(r.Context(), clientstore.DefaultScope, clientID, query)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, usage)
}

func (s *Server) clientLogs(w http.ResponseWriter, r *http.Request, clientID string) {
	limit := 100
	_, _ = fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	logs := s.Logs.List(0)
	out := make([]any, 0, min(limit, len(logs)))
	for _, record := range logs {
		if record.ClientID == clientID || record.ClientKeyID == clientID {
			record.RequestBody = ""
			record.ResponseBody = ""
			out = append(out, record)
			if len(out) == limit {
				break
			}
		}
	}
	jsonOut(w, http.StatusOK, map[string]any{"logs": out})
}

func (s *Server) listClientAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	_, _ = fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	events, err := s.ClientStore.ListAudit(r.Context(), clientstore.DefaultScope, clientstore.AuditFilter{ClientID: r.URL.Query().Get("client_id"), Limit: limit})
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) clientStateBackups(w http.ResponseWriter, r *http.Request) {
	backupRoot := filepath.Join(s.StateDirectory, "backups")
	if r.Method == http.MethodPost {
		directory := filepath.Join(backupRoot, time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+entityID(""))
		databasePath := filepath.Join(directory, "gateway-state.db")
		manifest, err := s.ClientStore.Backup(r.Context(), databasePath, s.CredentialKeys.IDs(), s.CredentialKeys.BackupPath())
		if err != nil {
			apiError(w, http.StatusInternalServerError, err)
			return
		}
		_ = s.audit(r.Context(), "client_state.backup_created", "client_state", manifest.Database, nil)
		jsonOut(w, http.StatusCreated, map[string]any{"backup": map[string]any{"id": filepath.Base(directory), "manifest": manifest}})
		return
	}
	matches, err := filepath.Glob(filepath.Join(backupRoot, "*", "gateway-state.db.manifest.json"))
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	backups := make([]map[string]any, 0, len(matches))
	for index := len(matches) - 1; index >= 0; index-- {
		manifest, readErr := clientstore.ReadBackupManifest(matches[index])
		if readErr != nil {
			continue
		}
		backups = append(backups, map[string]any{"id": filepath.Base(filepath.Dir(matches[index])), "manifest": manifest})
	}
	jsonOut(w, http.StatusOK, map[string]any{"backups": backups})
}

type deploymentTarget struct {
	ID     string         `json:"id"`
	Config map[string]any `json:"config"`
	Verify bool           `json:"verify"`
}

func deploymentError(err error, secret string) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if secret != "" {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return message
}

// deployCredential writes a freshly minted client credential into one or more
// application configurations. The operation is safe to retry: previews run
// before anything is persisted or written, and a failed apply rolls this
// request's writes back instead of stranding applications on a key that was
// never activated.
func (s *Server) deployCredential(w http.ResponseWriter, r *http.Request, clientID string) {
	var input struct {
		ExpiresAt            *time.Time         `json:"expires_at"`
		Test                 bool               `json:"test"`
		PreviousCredentialID string             `json:"previous_credential_id"`
		RevokePrevious       bool               `json:"revoke_previous"`
		Applications         []deploymentTarget `json:"applications"`
	}
	if err := decodeLimitedJSON(r, &input); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	if len(input.Applications) == 0 {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": "at least one application is required"})
		return
	}
	var previous clientstore.Credential
	if input.PreviousCredentialID != "" {
		var err error
		previous, err = s.ClientStore.GetCredential(r.Context(), clientstore.DefaultScope, input.PreviousCredentialID)
		if err != nil {
			apiError(w, statusForStoreError(err), err)
			return
		}
		if previous.ClientID != clientID || previous.Status == clientstore.CredentialRevoked || previous.Status == clientstore.CredentialExpired {
			apiError(w, http.StatusConflict, clientstore.ErrInvalidState)
			return
		}
	}
	// Mint the credential in memory only. A preview rejection must not leave a
	// live but unused key in the store, so nothing is persisted until every
	// target has accepted the configuration.
	credential, secret, err := s.prepareCredential(r.Context(), clientID, input.ExpiresAt, input.Test)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}

	type preparedTarget struct {
		input   deploymentTarget
		adapter application.Adapter
		raw     json.RawMessage
	}
	prepared := make([]preparedTarget, 0, len(input.Applications))
	results := make([]map[string]any, 0, len(input.Applications))
	for _, target := range input.Applications {
		adapter, adapterErr := s.Applications.Get(target.ID)
		if adapterErr != nil {
			results = append(results, map[string]any{"id": target.ID, "ok": false, "stage": "preview", "error": deploymentError(adapterErr, secret)})
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "client", clientID, map[string]string{"application_id": target.ID, "stage": "preview"})
			continue
		}
		if target.Config == nil {
			target.Config = map[string]any{}
		}
		target.Config["api_key"] = secret
		if strings.TrimSpace(fmt.Sprint(target.Config["base_url"])) == "" || target.Config["base_url"] == nil {
			target.Config["base_url"] = strings.TrimRight(s.GatewayURL, "/")
		}
		raw, marshalErr := json.Marshal(target.Config)
		if marshalErr != nil {
			results = append(results, map[string]any{"id": target.ID, "ok": false, "stage": "preview", "error": deploymentError(marshalErr, secret)})
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "client", clientID, map[string]string{"application_id": target.ID, "stage": "preview"})
			continue
		}
		preview, previewErr := adapter.Preview(r.Context(), raw)
		if previewErr != nil {
			results = append(results, map[string]any{"id": target.ID, "ok": false, "stage": "preview", "error": deploymentError(previewErr, secret)})
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "client", clientID, map[string]string{"application_id": target.ID, "stage": "preview"})
			continue
		}
		prepared = append(prepared, preparedTarget{input: target, adapter: adapter, raw: raw})
		results = append(results, map[string]any{"id": target.ID, "ok": true, "stage": "preview", "path": preview.Path})
	}
	if len(prepared) != len(input.Applications) {
		_ = s.audit(r.Context(), "credential.deployment_rejected", "client", clientID, map[string]string{"stage": "preview"})
		jsonOut(w, http.StatusOK, map[string]any{"ok": false, "stage": "preview", "credential": nil, "credential_created": false, "secret": "", "secret_available_once": false, "applications": results})
		return
	}
	if err = s.persistCredential(r.Context(), credential); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}

	type writtenTarget struct {
		input   deploymentTarget
		adapter application.Adapter
		result  application.ApplyResult
	}
	written := make([]writtenTarget, 0, len(prepared))
	results = results[:0]
	failed := false
	for _, target := range prepared {
		applied, applyErr := target.adapter.Apply(r.Context(), target.raw)
		result := map[string]any{"id": target.input.ID, "stage": "apply", "ok": applyErr == nil}
		if applyErr != nil {
			result["error"] = deploymentError(applyErr, secret)
		} else {
			written = append(written, writtenTarget{input: target.input, adapter: target.adapter, result: applied})
			result["path"] = applied.Path
			result["backup"] = applied.Backup
			if target.input.Verify {
				verification, verifyErr := target.adapter.Verify(r.Context(), application.VerifyOptions{Config: target.raw})
				result["stage"] = "verify"
				result["verification"] = verification
				if verifyErr != nil || !verification.OK {
					result["ok"] = false
					if verifyErr != nil {
						result["error"] = deploymentError(verifyErr, secret)
					}
				}
			}
		}
		stage := fmt.Sprint(result["stage"])
		if ok, _ := result["ok"].(bool); !ok {
			failed = true
			results = append(results, result)
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": target.input.ID, "stage": stage})
			break
		}
		results = append(results, result)
		_ = s.audit(r.Context(), "credential.application_configured", "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": target.input.ID, "stage": stage})
	}

	if failed {
		// Undo this request's writes, newest first, so a failed deployment does
		// not leave applications pointing at a key that will be retired.
		rolledBack := true
		for i := len(written) - 1; i >= 0; i-- {
			item := written[i]
			rollback := map[string]any{"id": item.input.ID, "stage": "rollback", "ok": true}
			switch {
			case !supportsRollback(item.adapter):
				rollback["ok"] = false
				rollback["error"] = "application does not support rollback"
			case strings.TrimSpace(item.result.Backup) == "":
				rollback["ok"] = false
				rollback["error"] = "no backup was recorded for this write"
			default:
				restored, rollbackErr := item.adapter.Rollback(r.Context(), item.result.Backup)
				if rollbackErr != nil {
					rollback["ok"] = false
					rollback["error"] = deploymentError(rollbackErr, secret)
				} else {
					rollback["path"] = restored.Path
				}
			}
			action := "credential.application_rolled_back"
			if ok, _ := rollback["ok"].(bool); !ok {
				rolledBack = false
				action = "credential.application_rollback_failed"
			}
			results = append(results, rollback)
			_ = s.audit(r.Context(), action, "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": item.input.ID})
		}
		released := false
		if rolledBack {
			if releaseErr := s.releaseCredential(r.Context(), credential); releaseErr == nil {
				released = true
			}
		}
		_ = s.audit(r.Context(), "credential.deployment_failed", "credential", credential.ID, map[string]string{"client_id": clientID})
		out := map[string]any{"ok": false, "credential": credential.View(), "rolled_back": rolledBack, "credential_released": released, "applications": results}
		if released {
			// The key no longer exists, so returning it would invite the user to
			// copy a secret that can never authenticate.
			out["secret"] = ""
			out["secret_available_once"] = false
		} else {
			out["secret"] = secret
			out["secret_available_once"] = true
		}
		jsonOut(w, http.StatusOK, out)
		return
	}

	// Only a fully written and verified deployment may retire the previous key:
	// revoking it earlier would break the client that still uses it.
	allOK := true
	if input.PreviousCredentialID != "" {
		if input.RevokePrevious {
			if revokeErr := s.ClientStore.UpdateCredentialStatus(r.Context(), clientstore.DefaultScope, previous.ID, clientstore.CredentialRevoked, time.Now()); revokeErr != nil {
				allOK = false
				results = append(results, map[string]any{"id": "previous-credential", "stage": "revoke", "ok": false, "error": revokeErr.Error()})
			}
		}
		_ = s.audit(r.Context(), "credential.rotated", "credential", credential.ID, map[string]string{"client_id": clientID, "previous_credential_id": previous.ID})
	}
	_ = s.audit(r.Context(), "credential.deployed", "credential", credential.ID, map[string]string{"client_id": clientID})
	jsonOut(w, http.StatusOK, map[string]any{"ok": allOK, "credential": credential.View(), "secret": secret, "secret_available_once": true, "rolled_back": false, "credential_released": false, "applications": results})
}

// supportsRollback reports whether an adapter can undo a write it made.
func supportsRollback(adapter application.Adapter) bool {
	for _, capability := range adapter.Manifest().Capabilities {
		if capability == application.CapabilityRollback {
			return true
		}
	}
	return false
}
