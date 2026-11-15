package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/soooooollee/ai-router/internal/clientstore"
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
