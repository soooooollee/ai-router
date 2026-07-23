package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/clientstore"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/safefile"
	"gopkg.in/yaml.v3"
)

func loopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	return address != nil && address.IsLoopback()
}

func entityID(prefix string) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(raw)
}

func (s *Server) clientManagementAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("cache-control", "no-store")
	if s.ClientStore == nil || s.CredentialKeys == nil {
		jsonOut(w, http.StatusServiceUnavailable, map[string]any{"error": "client credential management is unavailable"})
		return
	}
	switch {
	case r.URL.Path == "/api/clients" && r.Method == http.MethodGet:
		s.listClients(w, r)
	case r.URL.Path == "/api/clients" && r.Method == http.MethodPost:
		s.createClient(w, r)
	case r.URL.Path == "/api/clients/migrate-legacy" && r.Method == http.MethodPost:
		s.migrateLegacyKey(w, r)
	case r.URL.Path == "/api/clients/enable-auth" && r.Method == http.MethodPost:
		s.enableManagedAuthentication(w, r)
	case r.URL.Path == "/api/client-audit" && r.Method == http.MethodGet:
		s.listClientAudit(w, r)
	case r.URL.Path == "/api/client-state/backups" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.clientStateBackups(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/credentials/"):
		s.credentialAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/clients/"):
		s.clientAPI(w, r)
	default:
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "client management endpoint not found"})
	}
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	filter := clientstore.ClientFilter{IncludeDeleted: r.URL.Query().Get("include_deleted") == "true", Search: r.URL.Query().Get("search")}
	if value := r.URL.Query().Get("status"); value != "" {
		filter.Status = clientstore.ClientStatus(value)
	}
	clients, err := s.ClientStore.ListClients(r.Context(), clientstore.DefaultScope, filter)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	legacy := make([]map[string]string, 0, len(s.Config.Get().Auth.Keys))
	for _, key := range s.Config.Get().Auth.Keys {
		legacy = append(legacy, map[string]string{"id": key.ID})
	}
	jsonOut(w, http.StatusOK, map[string]any{
		"clients": clients, "authentication_enabled": s.Config.Get().Auth.Enabled,
		"managed_store": s.Config.Get().Auth.ManagedStoreEnabled(), "legacy_keys": legacy,
		"gateway_public": gatewayListenerIsPublic(s.Config.Get().Server.Listen),
	})
}

func gatewayListenerIsPublic(listen string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	return address == nil || !address.IsLoopback()
}

type createClientInput struct {
	Name             string                   `json:"name"`
	Description      string                   `json:"description"`
	Status           clientstore.ClientStatus `json:"status"`
	Policy           clientstore.ClientPolicy `json:"policy"`
	CreateCredential bool                     `json:"create_credential"`
	ExpiresAt        *time.Time               `json:"expires_at"`
	Test             bool                     `json:"test"`
	ConfirmUnlimited bool                     `json:"confirm_unlimited"`
}

func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	var input createClientInput
	if err := decodeLimitedJSON(r, &input); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": "client name is required"})
		return
	}
	if gatewayListenerIsPublic(s.Config.Get().Server.Listen) &&
		input.Policy.RequestsPerMinute == 0 && input.Policy.MaxConcurrent == 0 && input.Policy.DailyRequestLimit == 0 &&
		input.Policy.DailyInputTokens == 0 && input.Policy.DailyOutputTokens == 0 && !input.ConfirmUnlimited {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": "confirm_unlimited is required when creating an unlimited client on a non-loopback gateway"})
		return
	}
	clientID := entityID("client_")
	policyID := entityID("policy_")
	status := input.Status
	if status == "" {
		status = clientstore.ClientActive
	}
	client := clientstore.Client{ID: clientID, Name: input.Name, Description: strings.TrimSpace(input.Description), Status: status, PolicyID: policyID}
	input.Policy.ID = policyID
	if err := s.ClientStore.CreateClient(r.Context(), clientstore.DefaultScope, client, input.Policy); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	created, _ := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, clientID)
	_ = s.audit(r.Context(), "client.created", "client", clientID, map[string]string{"client_name": input.Name})
	response := map[string]any{"client": created, "policy": input.Policy}
	if input.CreateCredential {
		credential, secret, err := s.generateCredential(r.Context(), clientID, input.ExpiresAt, input.Test)
		if err != nil {
			apiError(w, statusForStoreError(err), err)
			return
		}
		response["credential"] = credential.View()
		response["secret"] = secret
		response["secret_available_once"] = true
	}
	jsonOut(w, http.StatusCreated, response)
}

func (s *Server) clientAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/clients/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "client not found"})
		return
	}
	clientID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		s.getClient(w, r, clientID)
	case action == "" && r.Method == http.MethodPatch:
		s.updateClient(w, r, clientID)
	case action == "" && r.Method == http.MethodDelete:
		s.deleteClient(w, r, clientID)
	case action == "credentials" && r.Method == http.MethodGet:
		s.listCredentials(w, r, clientID)
	case action == "credentials" && r.Method == http.MethodPost:
		s.createCredential(w, r, clientID)
	case action == "policy" && r.Method == http.MethodGet:
		s.getPolicy(w, r, clientID)
	case action == "policy" && r.Method == http.MethodPut:
		s.updatePolicy(w, r, clientID)
	case action == "usage" && r.Method == http.MethodGet:
		s.clientUsage(w, r, clientID)
	case action == "logs" && r.Method == http.MethodGet:
		s.clientLogs(w, r, clientID)
	case action == "credential-deployments" && r.Method == http.MethodPost:
		s.deployCredential(w, r, clientID)
	default:
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "client endpoint not found"})
	}
}

func (s *Server) getClient(w http.ResponseWriter, r *http.Request, clientID string) {
	clients, err := s.ClientStore.ListClients(r.Context(), clientstore.DefaultScope, clientstore.ClientFilter{IncludeDeleted: true})
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	for _, client := range clients {
		if client.Client.ID == clientID {
			jsonOut(w, http.StatusOK, client)
			return
		}
	}
	jsonOut(w, http.StatusNotFound, map[string]any{"error": "client not found"})
}

func (s *Server) updateClient(w http.ResponseWriter, r *http.Request, clientID string) {
	var input struct {
		Name        *string                   `json:"name"`
		Description *string                   `json:"description"`
		Status      *clientstore.ClientStatus `json:"status"`
	}
	if err := decodeLimitedJSON(r, &input); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	client, err := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, clientID)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	if input.Name != nil {
		client.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		client.Description = strings.TrimSpace(*input.Description)
	}
	if input.Status != nil {
		client.Status = *input.Status
	}
	if err = s.ClientStore.UpdateClient(r.Context(), clientstore.DefaultScope, client); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	updated, _ := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, clientID)
	_ = s.audit(r.Context(), "client.updated", "client", clientID, map[string]string{"status": string(updated.Status)})
	jsonOut(w, http.StatusOK, updated)
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request, clientID string) {
	client, err := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, clientID)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	client.Status = clientstore.ClientDeleted
	if err = s.ClientStore.UpdateClient(r.Context(), clientstore.DefaultScope, client); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	_ = s.audit(r.Context(), "client.deleted", "client", clientID, nil)
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) listCredentials(w http.ResponseWriter, r *http.Request, clientID string) {
	credentials, err := s.ClientStore.ListCredentials(r.Context(), clientstore.DefaultScope, clientID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]clientstore.CredentialView, 0, len(credentials))
	for _, credential := range credentials {
		views = append(views, credential.View())
	}
	jsonOut(w, http.StatusOK, map[string]any{"credentials": views})
}

type credentialInput struct {
	ExpiresAt *time.Time `json:"expires_at"`
	Test      bool       `json:"test"`
}

func (s *Server) createCredential(w http.ResponseWriter, r *http.Request, clientID string) {
	var input credentialInput
	if err := decodeLimitedJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	credential, secret, err := s.generateCredential(r.Context(), clientID, input.ExpiresAt, input.Test)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	jsonOut(w, http.StatusCreated, map[string]any{"credential": credential.View(), "secret": secret, "secret_available_once": true})
}

func (s *Server) generateCredential(ctx context.Context, clientID string, expiresAt *time.Time, test bool) (clientstore.Credential, string, error) {
	client, err := s.ClientStore.GetClient(ctx, clientstore.DefaultScope, clientID)
	if err != nil {
		return clientstore.Credential{}, "", err
	}
	if client.Status == clientstore.ClientDeleted {
		return clientstore.Credential{}, "", clientstore.ErrInvalidState
	}
	if expiresAt != nil {
		value := expiresAt.UTC()
		if !value.After(time.Now()) {
			return clientstore.Credential{}, "", errors.New("credential expiration must be in the future")
		}
		expiresAt = &value
	}
	credential, secret, err := s.CredentialKeys.GenerateManaged(clientID, expiresAt, test)
	if err != nil {
		return clientstore.Credential{}, "", err
	}
	if err = s.ClientStore.CreateCredential(ctx, clientstore.DefaultScope, credential); err != nil {
		return clientstore.Credential{}, "", err
	}
	_ = s.audit(ctx, "credential.created", "credential", credential.ID, map[string]string{"client_id": clientID, "credential_prefix": credential.Prefix})
	return credential, secret, nil
}

func (s *Server) credentialAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/credentials/"), "/")
	credentialID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch {
	case action == "" && r.Method == http.MethodPatch:
		var input struct {
			Status clientstore.CredentialStatus `json:"status"`
		}
		if err := decodeLimitedJSON(r, &input); err != nil {
			apiError(w, http.StatusBadRequest, err)
			return
		}
		if input.Status != clientstore.CredentialActive && input.Status != clientstore.CredentialDisabled {
			jsonOut(w, http.StatusBadRequest, map[string]any{"error": "credential status must be active or disabled"})
			return
		}
		s.setCredentialStatus(w, r, credentialID, input.Status, "credential.status_changed")
	case action == "revoke" && r.Method == http.MethodPost:
		s.setCredentialStatus(w, r, credentialID, clientstore.CredentialRevoked, "credential.revoked")
	case action == "" && r.Method == http.MethodDelete:
		s.deleteCredential(w, r, credentialID)
	case action == "rotate" && r.Method == http.MethodPost:
		s.rotateCredential(w, r, credentialID)
	case action == "secret-acknowledged" && r.Method == http.MethodPost:
		credential, err := s.ClientStore.GetCredential(r.Context(), clientstore.DefaultScope, credentialID)
		if err != nil {
			apiError(w, statusForStoreError(err), err)
			return
		}
		_ = s.audit(r.Context(), "credential.secret_acknowledged", "credential", credentialID, map[string]string{"client_id": credential.ClientID})
		jsonOut(w, http.StatusOK, map[string]any{"ok": true})
	default:
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "credential endpoint not found"})
	}
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request, credentialID string) {
	credential, err := s.ClientStore.GetCredential(r.Context(), clientstore.DefaultScope, credentialID)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	if err = s.ClientStore.DeleteCredential(r.Context(), clientstore.DefaultScope, credentialID); err != nil {
		if errors.Is(err, clientstore.ErrInvalidState) {
			jsonOut(w, http.StatusConflict, map[string]any{"error": "only revoked or expired credentials can be deleted"})
			return
		}
		apiError(w, statusForStoreError(err), err)
		return
	}
	remaining, listErr := s.ClientStore.ListCredentials(r.Context(), clientstore.DefaultScope, credential.ClientID)
	if listErr == nil && len(remaining) == 0 {
		if client, clientErr := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, credential.ClientID); clientErr == nil {
			client.Status = clientstore.ClientDeleted
			_ = s.ClientStore.UpdateClient(r.Context(), clientstore.DefaultScope, client)
			if s.ClientAccess != nil {
				s.ClientAccess.Limiter.Forget(client.ID)
			}
		}
	}
	_ = s.audit(r.Context(), "credential.deleted", "credential", credentialID, map[string]string{"client_id": credential.ClientID, "credential_prefix": credential.Prefix})
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) setCredentialStatus(w http.ResponseWriter, r *http.Request, credentialID string, status clientstore.CredentialStatus, action string) {
	if err := s.ClientStore.UpdateCredentialStatus(r.Context(), clientstore.DefaultScope, credentialID, status, time.Now()); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	credential, _ := s.ClientStore.GetCredential(r.Context(), clientstore.DefaultScope, credentialID)
	_ = s.audit(r.Context(), action, "credential", credentialID, map[string]string{"client_id": credential.ClientID, "status": string(status)})
	jsonOut(w, http.StatusOK, credential.View())
}

func (s *Server) rotateCredential(w http.ResponseWriter, r *http.Request, credentialID string) {
	var input struct {
		ExpiresAt      *time.Time `json:"expires_at"`
		Test           bool       `json:"test"`
		RevokePrevious bool       `json:"revoke_previous"`
	}
	if err := decodeLimitedJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	previous, err := s.ClientStore.GetCredential(r.Context(), clientstore.DefaultScope, credentialID)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	next, secret, err := s.generateCredential(r.Context(), previous.ClientID, input.ExpiresAt, input.Test)
	if err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	if input.RevokePrevious {
		if err = s.ClientStore.UpdateCredentialStatus(r.Context(), clientstore.DefaultScope, previous.ID, clientstore.CredentialRevoked, time.Now()); err != nil {
			apiError(w, statusForStoreError(err), err)
			return
		}
	}
	_ = s.audit(r.Context(), "credential.rotated", "credential", next.ID, map[string]string{"client_id": previous.ClientID, "previous_credential_id": previous.ID})
	jsonOut(w, http.StatusCreated, map[string]any{"credential": next.View(), "previous_credential": previous.View(), "secret": secret, "secret_available_once": true})
}

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
	var err error
	if input.PreviousCredentialID != "" {
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
	credential, secret, err := s.generateCredential(r.Context(), clientID, input.ExpiresAt, input.Test)
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
	previewsOK := true
	for _, target := range input.Applications {
		adapter, adapterErr := s.Applications.Get(target.ID)
		if adapterErr != nil {
			results = append(results, map[string]any{"id": target.ID, "ok": false, "stage": "preview", "error": deploymentError(adapterErr, secret)})
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": target.ID, "stage": "preview"})
			previewsOK = false
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
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": target.ID, "stage": "preview"})
			previewsOK = false
			continue
		}
		preview, previewErr := adapter.Preview(r.Context(), raw)
		if previewErr != nil {
			results = append(results, map[string]any{"id": target.ID, "ok": false, "stage": "preview", "error": deploymentError(previewErr, secret)})
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": target.ID, "stage": "preview"})
			previewsOK = false
			continue
		}
		prepared = append(prepared, preparedTarget{input: target, adapter: adapter, raw: raw})
		results = append(results, map[string]any{"id": target.ID, "ok": true, "stage": "preview", "path": preview.Path})
	}
	if !previewsOK {
		jsonOut(w, http.StatusOK, map[string]any{"ok": false, "credential": credential.View(), "secret": secret, "secret_available_once": true, "applications": results})
		return
	}
	results = results[:0]
	allOK := true
	for _, target := range prepared {
		applied, applyErr := target.adapter.Apply(r.Context(), target.raw)
		result := map[string]any{"id": target.input.ID, "stage": "apply", "ok": applyErr == nil}
		if applyErr != nil {
			result["error"] = deploymentError(applyErr, secret)
			allOK = false
			results = append(results, result)
			_ = s.audit(r.Context(), "credential.application_configuration_failed", "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": target.input.ID, "stage": "apply"})
			continue
		}
		result["path"] = applied.Path
		result["backup"] = applied.Backup
		if target.input.Verify {
			verification, verifyErr := target.adapter.Verify(r.Context(), application.VerifyOptions{Config: target.raw})
			result["stage"] = "verify"
			result["verification"] = verification
			if verifyErr != nil || !verification.OK {
				result["ok"] = false
				allOK = false
				if verifyErr != nil {
					result["error"] = deploymentError(verifyErr, secret)
				}
			}
		}
		results = append(results, result)
		action := "credential.application_configured"
		if ok, _ := result["ok"].(bool); !ok {
			action = "credential.application_configuration_failed"
		}
		_ = s.audit(r.Context(), action, "credential", credential.ID, map[string]string{"client_id": clientID, "application_id": target.input.ID, "stage": fmt.Sprint(result["stage"])})
	}
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
	jsonOut(w, http.StatusOK, map[string]any{"ok": allOK, "credential": credential.View(), "secret": secret, "secret_available_once": true, "applications": results})
}

func (s *Server) migrateLegacyKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		KeyID       string                   `json:"key_id"`
		Name        string                   `json:"name"`
		Description string                   `json:"description"`
		Policy      clientstore.ClientPolicy `json:"policy"`
	}
	if err := decodeLimitedJSON(r, &input); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	var legacy config.APIKey
	found := false
	for _, key := range s.Config.Get().Auth.Keys {
		if key.ID == input.KeyID {
			legacy, found = key, true
			break
		}
	}
	if !found {
		jsonOut(w, http.StatusNotFound, map[string]any{"error": "legacy key not found"})
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		input.Name = legacy.ID
	}
	clientID, policyID := entityID("client_"), entityID("policy_")
	client := clientstore.Client{ID: clientID, Name: input.Name, Description: input.Description, Status: clientstore.ClientActive, PolicyID: policyID}
	input.Policy.ID = policyID
	if err := s.ClientStore.CreateClient(r.Context(), clientstore.DefaultScope, client, input.Policy); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	digest, hmacKeyID, err := s.CredentialKeys.Digest(legacy.Value)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	credentialID := entityID("legacy_")
	credential := clientstore.Credential{
		ID: credentialID, ClientID: clientID, Kind: clientstore.CredentialStandard,
		Prefix: "legacy_" + legacy.ID, SecretHMAC: digest, HMACKeyID: hmacKeyID,
		Status: clientstore.CredentialActive, CreatedAt: time.Now().UTC(),
	}
	if err = s.ClientStore.CreateCredential(r.Context(), clientstore.DefaultScope, credential); err != nil {
		apiError(w, statusForStoreError(err), err)
		return
	}
	if err = s.removeLegacyKeyFromConfig(legacy.ID); err != nil {
		_ = s.ClientStore.UpdateCredentialStatus(r.Context(), clientstore.DefaultScope, credential.ID, clientstore.CredentialRevoked, time.Now())
		client.Status = clientstore.ClientDeleted
		_ = s.ClientStore.UpdateClient(r.Context(), clientstore.DefaultScope, client)
		_ = s.audit(r.Context(), "legacy.migration_rolled_back", "credential", credential.ID, map[string]string{"client_id": clientID, "legacy_key_id": legacy.ID})
		apiError(w, http.StatusInternalServerError, fmt.Errorf("legacy migration was rolled back and the original configuration remains active: %w", err))
		return
	}
	_ = s.audit(r.Context(), "legacy.migrated", "credential", credential.ID, map[string]string{"client_id": clientID, "legacy_key_id": legacy.ID})
	created, _ := s.ClientStore.GetClient(r.Context(), clientstore.DefaultScope, clientID)
	jsonOut(w, http.StatusOK, map[string]any{"client": created, "credential": credential.View(), "legacy_key_removed": true})
}

func (s *Server) removeLegacyKeyFromConfig(keyID string) error {
	path := s.Config.Get().SourcePath
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err = yaml.Unmarshal(raw, &document); err != nil {
		return err
	}
	if !removeYAMLAuthKey(&document, keyID) {
		return clientstore.ErrNotFound
	}
	nextRaw, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(path), ".airoute-migrate-"+entityID("")+".yaml")
	if err = os.WriteFile(temporary, nextRaw, 0600); err != nil {
		return err
	}
	validated, validateErr := config.Load(temporary)
	_ = os.Remove(temporary)
	if validateErr != nil {
		return validateErr
	}
	backup, err := safefile.BackupData(path, ".airoute.bak.", raw)
	if err != nil {
		return err
	}
	if err = safefile.AtomicWrite(path, nextRaw, 0600); err != nil {
		return err
	}
	next, err := config.Load(path)
	if err != nil {
		_ = safefile.AtomicWrite(path, raw, 0600)
		return err
	}
	_ = validated
	s.Config.Replace(next)
	_ = backup
	return nil
}

func (s *Server) enableManagedAuthentication(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Secret       string `json:"secret"`
		CredentialID string `json:"credential_id"`
	}
	if err := decodeLimitedJSON(r, &input); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	input.Secret = strings.TrimSpace(input.Secret)
	input.CredentialID = strings.TrimSpace(input.CredentialID)
	if input.Secret == "" && input.CredentialID != "" {
		credential, credentialErr := s.ClientStore.GetCredential(r.Context(), clientstore.DefaultScope, input.CredentialID)
		if credentialErr != nil {
			apiError(w, statusForStoreError(credentialErr), credentialErr)
			return
		}
		input.Secret, credentialErr = s.CredentialKeys.Reveal(credential)
		if credentialErr != nil {
			jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"error": "selected credential cannot enable authentication"})
			return
		}
	}
	if input.Secret == "" {
		jsonOut(w, http.StatusBadRequest, map[string]any{"error": "a newly created credential is required"})
		return
	}
	current := s.Config.Get()
	validationConfig := *current
	validationConfig.Auth.Enabled = true
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(s.GatewayURL, "/")+"/v1/models", nil)
	request.Header.Set("authorization", "Bearer "+input.Secret)
	principal, accessErr := s.ClientAccess.Authenticate(request, &validationConfig)
	if accessErr != nil || principal.Legacy || principal.Anonymous {
		jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"error": "credential could not be validated as a managed key"})
		return
	}
	path := current.SourcePath
	raw, err := os.ReadFile(path)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	nextRaw, err := setYAMLAuthEnabled(raw, true)
	if err != nil {
		apiError(w, http.StatusUnprocessableEntity, err)
		return
	}
	if _, err = safefile.BackupData(path, ".airoute.bak.", raw); err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	if err = safefile.AtomicWrite(path, nextRaw, 0600); err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	next, err := config.Load(path)
	if err != nil {
		_ = safefile.AtomicWrite(path, raw, 0600)
		apiError(w, http.StatusUnprocessableEntity, err)
		return
	}
	s.Config.Replace(next)
	response, verifyErr := s.Client.Do(request)
	verified := verifyErr == nil && response.StatusCode >= 200 && response.StatusCode < 300
	if response != nil {
		_ = response.Body.Close()
	}
	if !verified {
		_ = safefile.AtomicWrite(path, raw, 0600)
		if restored, restoreErr := config.Load(path); restoreErr == nil {
			s.Config.Replace(restored)
		}
		message := "gateway rejected the credential after authentication was enabled"
		if verifyErr != nil {
			message = verifyErr.Error()
		}
		jsonOut(w, http.StatusBadGateway, map[string]any{"error": message, "rolled_back": true})
		return
	}
	_ = s.audit(r.Context(), "authentication.enabled", "client", principal.ClientID, nil)
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "authentication_enabled": true, "client_id": principal.ClientID})
}

func setYAMLAuthEnabled(raw []byte, enabled bool) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("configuration root must be a mapping")
	}
	root := document.Content[0]
	var authNode *yaml.Node
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == "auth" {
			authNode = root.Content[index+1]
			break
		}
	}
	if authNode == nil {
		authNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "auth"}, authNode)
	}
	if authNode.Kind != yaml.MappingNode {
		return nil, errors.New("auth configuration must be a mapping")
	}
	setYAMLScalar(authNode, "enabled", fmt.Sprint(enabled), "!!bool")
	setYAMLScalar(authNode, "managed_store", "true", "!!bool")
	setYAMLScalar(authNode, "allow_query_key", "false", "!!bool")
	return yaml.Marshal(&document)
}

func setYAMLScalar(mapping *yaml.Node, key, value, tag string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1].Kind = yaml.ScalarNode
			mapping.Content[index+1].Tag = tag
			mapping.Content[index+1].Value = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	)
}

func removeYAMLAuthKey(document *yaml.Node, keyID string) bool {
	if document == nil || len(document.Content) == 0 {
		return false
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}
	var authNode *yaml.Node
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == "auth" {
			authNode = root.Content[index+1]
			break
		}
	}
	if authNode == nil || authNode.Kind != yaml.MappingNode {
		return false
	}
	for index := 0; index+1 < len(authNode.Content); index += 2 {
		if authNode.Content[index].Value != "keys" {
			continue
		}
		sequence := authNode.Content[index+1]
		if sequence.Kind != yaml.SequenceNode {
			return false
		}
		for itemIndex, item := range sequence.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			for field := 0; field+1 < len(item.Content); field += 2 {
				if item.Content[field].Value == "id" && item.Content[field+1].Value == keyID {
					sequence.Content = append(sequence.Content[:itemIndex], sequence.Content[itemIndex+1:]...)
					return true
				}
			}
		}
	}
	return false
}

func (s *Server) audit(ctx context.Context, action, resourceType, resourceID string, metadata map[string]string) error {
	return s.ClientStore.AppendAudit(ctx, clientstore.DefaultScope, clientstore.AuditEvent{
		ID: entityID("audit_"), ActorType: "admin", ActorID: "local-admin", Action: action,
		ResourceType: resourceType, ResourceID: resourceID, Metadata: metadata, CreatedAt: time.Now().UTC(),
	})
}

func decodeLimitedJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func statusForStoreError(err error) int {
	switch {
	case errors.Is(err, clientstore.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, clientstore.ErrAlreadyExists), errors.Is(err, clientstore.ErrInvalidState):
		return http.StatusConflict
	case errors.Is(err, clientstore.ErrQuotaExhausted):
		return http.StatusTooManyRequests
	default:
		return http.StatusUnprocessableEntity
	}
}
