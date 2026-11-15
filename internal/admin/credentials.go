package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/soooooollee/ai-router/internal/clientstore"
	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/safefile"
	"gopkg.in/yaml.v3"
)

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
	credential, secret, err := s.prepareCredential(ctx, clientID, expiresAt, test)
	if err != nil {
		return clientstore.Credential{}, "", err
	}
	if err = s.persistCredential(ctx, credential); err != nil {
		return clientstore.Credential{}, "", err
	}
	return credential, secret, nil
}

// prepareCredential mints a credential without storing it, so a caller can
// validate a whole deployment before any secret becomes usable.
func (s *Server) prepareCredential(ctx context.Context, clientID string, expiresAt *time.Time, test bool) (clientstore.Credential, string, error) {
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
	return s.CredentialKeys.GenerateManaged(clientID, expiresAt, test)
}

func (s *Server) persistCredential(ctx context.Context, credential clientstore.Credential) error {
	if err := s.ClientStore.CreateCredential(ctx, clientstore.DefaultScope, credential); err != nil {
		return err
	}
	_ = s.audit(ctx, "credential.created", "credential", credential.ID, map[string]string{"client_id": credential.ClientID, "credential_prefix": credential.Prefix})
	return nil
}

// releaseCredential removes a credential whose deployment was fully undone, so
// a failed write cannot leave a live but unused key in the store.
func (s *Server) releaseCredential(ctx context.Context, credential clientstore.Credential) error {
	if err := s.ClientStore.UpdateCredentialStatus(ctx, clientstore.DefaultScope, credential.ID, clientstore.CredentialRevoked, time.Now()); err != nil {
		return err
	}
	if err := s.ClientStore.DeleteCredential(ctx, clientstore.DefaultScope, credential.ID); err != nil {
		return err
	}
	_ = s.audit(ctx, "credential.released", "credential", credential.ID, map[string]string{"client_id": credential.ClientID, "reason": "deployment_rolled_back"})
	return nil
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
