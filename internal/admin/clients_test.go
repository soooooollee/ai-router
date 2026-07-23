package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/clientauth"
	"github.com/zbss/airoute/internal/clientstore"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/gateway"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"gopkg.in/yaml.v3"
)

type credentialDeploymentAdapter struct {
	previewErr error
	applied    int
	verified   int
	config     json.RawMessage
}

func (a *credentialDeploymentAdapter) Manifest() application.Manifest {
	return application.Manifest{
		ID: "test-app", Name: "Test App", Description: "Test application adapter", Status: "stable", ConfigFormat: "json",
		Capabilities: []application.Capability{application.CapabilityPreview, application.CapabilityConfigure, application.CapabilityVerify},
	}
}
func (a *credentialDeploymentAdapter) Detect(context.Context) (application.Detection, error) {
	return application.Detection{Installed: true}, nil
}
func (a *credentialDeploymentAdapter) Read(context.Context) (application.State, error) {
	managed := map[string]any{}
	if len(a.config) > 0 {
		_ = json.Unmarshal(a.config, &managed)
	}
	return application.State{Managed: managed}, nil
}
func (a *credentialDeploymentAdapter) Preview(_ context.Context, raw json.RawMessage) (application.Preview, error) {
	a.config = append(a.config[:0], raw...)
	return application.Preview{Path: "/tmp/test-app.json", Content: append(json.RawMessage(nil), raw...), WillCreateBackup: true}, a.previewErr
}
func (a *credentialDeploymentAdapter) Apply(_ context.Context, raw json.RawMessage) (application.ApplyResult, error) {
	a.applied++
	a.config = append(a.config[:0], raw...)
	return application.ApplyResult{OK: true, Path: "/tmp/test-app.json", Backup: "/tmp/test-app.json.bak"}, nil
}
func (a *credentialDeploymentAdapter) Verify(context.Context, application.VerifyOptions) (application.VerifyResult, error) {
	a.verified++
	return application.VerifyResult{OK: true, Verified: time.Now()}, nil
}
func (a *credentialDeploymentAdapter) Backups(context.Context) ([]application.Backup, error) {
	return nil, nil
}
func (a *credentialDeploymentAdapter) DeleteBackup(context.Context, string) error { return nil }
func (a *credentialDeploymentAdapter) Rollback(context.Context, string) (application.ApplyResult, error) {
	return application.ApplyResult{OK: true}, nil
}

func clientManagementServer(t *testing.T, rawConfig string) (*Server, *clientauth.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "airoute.yaml")
	if err := os.WriteFile(configPath, []byte(rawConfig), 0600); err != nil {
		t.Fatal(err)
	}
	current, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := clientstore.Open(filepath.Join(dir, "gateway-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	t.Setenv("AIROUTE_CREDENTIAL_MASTER_KEY", "")
	t.Setenv("AIROUTE_CREDENTIAL_PREVIOUS_KEYS", "")
	keys, err := clientauth.LoadOrCreateKeyRing(filepath.Join(dir, "credential-master.key"))
	if err != nil {
		t.Fatal(err)
	}
	manager := clientauth.NewManager(store, keys)
	server := New(config.NewStore(current), protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", "http://127.0.0.1:12666", configPath)
	server.SetClientManagement(manager, store, keys, dir)
	return server, manager, configPath
}

func clientAPIRequest(t *testing.T, server http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(raw))
	request.RemoteAddr = "127.0.0.1:40000"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func baseClientConfig() string {
	return `version: 1
server:
  listen: 127.0.0.1:12666
  admin_listen: 127.0.0.1:12667
admin:
  enabled: true
auth:
  enabled: false
providers: []
routes: []
`
}

func TestClientManagementLifecycleDoesNotRedisplaySecret(t *testing.T) {
	server, _, _ := clientManagementServer(t, baseClientConfig())
	created := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{
		"name": "Codex laptop", "create_credential": true,
		"policy": map[string]any{"allowed_models": []string{"gpt-*"}, "requests_per_minute": 30, "max_concurrent": 2},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", created.Code, created.Body.String())
	}
	var createBody struct {
		Client     clientstore.Client         `json:"client"`
		Credential clientstore.CredentialView `json:"credential"`
		Secret     string                     `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createBody); err != nil {
		t.Fatal(err)
	}
	if createBody.Client.ID == "" || createBody.Credential.ID == "" || !createBody.Credential.Recoverable || createBody.Credential.Kind != clientstore.CredentialManaged || !strings.HasPrefix(createBody.Secret, "sk-") {
		t.Fatalf("create response is incomplete: %s", created.Body.String())
	}
	acknowledged := clientAPIRequest(t, server, http.MethodPost, "/api/credentials/"+createBody.Credential.ID+"/secret-acknowledged", map[string]any{})
	if acknowledged.Code != http.StatusOK {
		t.Fatalf("secret acknowledgement failed: %d %s", acknowledged.Code, acknowledged.Body.String())
	}
	listed := clientAPIRequest(t, server, http.MethodGet, "/api/clients", nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), createBody.Secret) || strings.Contains(listed.Body.String(), "secret_hmac") || strings.Contains(listed.Body.String(), "secret_ciphertext") || strings.Contains(listed.Body.String(), "secret_nonce") || strings.Contains(listed.Body.String(), "hmac_key_id") {
		t.Fatalf("client list leaked verification data: %s", listed.Body.String())
	}
	rotated := clientAPIRequest(t, server, http.MethodPost, "/api/credentials/"+createBody.Credential.ID+"/rotate", map[string]any{})
	if rotated.Code != http.StatusCreated || !strings.Contains(rotated.Body.String(), `"secret_available_once":true`) {
		t.Fatalf("rotation failed: %d %s", rotated.Code, rotated.Body.String())
	}
	disabled := clientAPIRequest(t, server, http.MethodPatch, "/api/credentials/"+createBody.Credential.ID, map[string]any{"status": "disabled"})
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable failed: %d %s", disabled.Code, disabled.Body.String())
	}
	revoked := clientAPIRequest(t, server, http.MethodPost, "/api/credentials/"+createBody.Credential.ID+"/revoke", map[string]any{})
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), `"status":"revoked"`) {
		t.Fatalf("revoke failed: %d %s", revoked.Code, revoked.Body.String())
	}
	deleted := clientAPIRequest(t, server, http.MethodDelete, "/api/credentials/"+createBody.Credential.ID, nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", deleted.Code, deleted.Body.String())
	}
	audit := clientAPIRequest(t, server, http.MethodGet, "/api/client-audit?client_id="+createBody.Client.ID, nil)
	if audit.Code != http.StatusOK || strings.Contains(audit.Body.String(), createBody.Secret) || !strings.Contains(audit.Body.String(), "credential.created") || !strings.Contains(audit.Body.String(), "credential.secret_acknowledged") || !strings.Contains(audit.Body.String(), "credential.deleted") {
		t.Fatalf("audit is incomplete or leaked a secret: %s", audit.Body.String())
	}
}

func TestApplicationCanSelectManagedCredentialWithoutExposingSecret(t *testing.T) {
	server, _, _ := clientManagementServer(t, baseClientConfig())
	adapter := &credentialDeploymentAdapter{}
	server.Applications = application.NewRegistry(adapter)
	created := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{
		"name": "Selectable key", "create_credential": true,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", created.Code, created.Body.String())
	}
	var body struct {
		Credential clientstore.CredentialView `json:"credential"`
		Secret     string                     `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"credential_id": body.Credential.ID, "model": "fast", "base_url": "http://127.0.0.1:12666"}
	adapter.previewErr = errors.New("rejected credential " + body.Secret)
	rejected := clientAPIRequest(t, server, http.MethodPost, "/api/apps/test-app/preview", request)
	if rejected.Code != http.StatusUnprocessableEntity || strings.Contains(rejected.Body.String(), body.Secret) || !strings.Contains(rejected.Body.String(), "[REDACTED]") {
		t.Fatalf("selected credential error leaked the secret: %d %s", rejected.Code, rejected.Body.String())
	}
	adapter.previewErr = nil
	preview := clientAPIRequest(t, server, http.MethodPost, "/api/apps/test-app/preview", request)
	if preview.Code != http.StatusOK || strings.Contains(preview.Body.String(), body.Secret) || !strings.Contains(preview.Body.String(), webRedactionMask) {
		t.Fatalf("selected credential preview leaked or failed: %d %s", preview.Code, preview.Body.String())
	}
	applied := clientAPIRequest(t, server, http.MethodPut, "/api/apps/test-app/config", request)
	if applied.Code != http.StatusOK || strings.Contains(applied.Body.String(), body.Secret) {
		t.Fatalf("selected credential apply leaked or failed: %d %s", applied.Code, applied.Body.String())
	}
	if !bytes.Contains(adapter.config, []byte(body.Secret)) || bytes.Contains(adapter.config, []byte("credential_id")) {
		t.Fatalf("adapter did not receive the selected credential: %s", adapter.config)
	}
	state := clientAPIRequest(t, server, http.MethodGet, "/api/apps/test-app", nil)
	if state.Code != http.StatusOK || strings.Contains(state.Body.String(), body.Secret) || !strings.Contains(state.Body.String(), `"airoute_client_credential_id":"`+body.Credential.ID+`"`) || !strings.Contains(state.Body.String(), `"airoute_client_credential_recoverable":true`) || !strings.Contains(state.Body.String(), `"airoute_client_credential_name":"Selectable key"`) {
		t.Fatalf("application credential metadata is incomplete or leaked: %d %s", state.Code, state.Body.String())
	}
}

func TestPublicGatewayRequiresUnlimitedPolicyConfirmation(t *testing.T) {
	raw := strings.Replace(baseClientConfig(), "listen: 127.0.0.1:12666", "listen: 0.0.0.0:12666", 1)
	server, _, _ := clientManagementServer(t, raw)
	denied := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{"name": "Public unlimited"})
	if denied.Code != http.StatusBadRequest || !strings.Contains(denied.Body.String(), "confirm_unlimited") {
		t.Fatalf("unconfirmed unlimited public client returned %d: %s", denied.Code, denied.Body.String())
	}
	confirmed := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{"name": "Public unlimited", "confirm_unlimited": true})
	if confirmed.Code != http.StatusCreated {
		t.Fatalf("confirmed unlimited public client returned %d: %s", confirmed.Code, confirmed.Body.String())
	}
	limited := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{"name": "Public limited", "policy": map[string]any{"requests_per_minute": 60}})
	if limited.Code != http.StatusCreated {
		t.Fatalf("limited public client returned %d: %s", limited.Code, limited.Body.String())
	}
}

func TestCredentialDeploymentPreviewsAppliesVerifiesAndReturnsSecretOnce(t *testing.T) {
	server, manager, _ := clientManagementServer(t, baseClientConfig())
	adapter := &credentialDeploymentAdapter{}
	server.Applications = application.NewRegistry(adapter)
	created := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{"name": "Configured client"})
	if created.Code != http.StatusCreated {
		t.Fatalf("client create failed: %s", created.Body.String())
	}
	var clientBody struct {
		Client clientstore.Client `json:"client"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &clientBody); err != nil {
		t.Fatal(err)
	}
	initial := clientAPIRequest(t, server, http.MethodPost, "/api/clients/"+clientBody.Client.ID+"/credentials", map[string]any{})
	var initialBody struct {
		Credential clientstore.CredentialView `json:"credential"`
	}
	if initial.Code != http.StatusCreated {
		t.Fatalf("initial credential failed: %s", initial.Body.String())
	}
	if err := json.Unmarshal(initial.Body.Bytes(), &initialBody); err != nil {
		t.Fatal(err)
	}
	deployed := clientAPIRequest(t, server, http.MethodPost, "/api/clients/"+clientBody.Client.ID+"/credential-deployments", map[string]any{
		"previous_credential_id": initialBody.Credential.ID,
		"revoke_previous":        true,
		"applications":           []map[string]any{{"id": "test-app", "verify": true, "config": map[string]any{"model": "fast"}}},
	})
	if deployed.Code != http.StatusOK || adapter.applied != 1 || adapter.verified != 1 {
		t.Fatalf("credential deployment failed: %d %s applied=%d verified=%d", deployed.Code, deployed.Body.String(), adapter.applied, adapter.verified)
	}
	var deployment struct {
		OK         bool                       `json:"ok"`
		Secret     string                     `json:"secret"`
		Credential clientstore.CredentialView `json:"credential"`
	}
	if err := json.Unmarshal(deployed.Body.Bytes(), &deployment); err != nil {
		t.Fatal(err)
	}
	if !deployment.OK || !strings.HasPrefix(deployment.Secret, "sk-") || !bytes.Contains(adapter.config, []byte(deployment.Secret)) || !bytes.Contains(adapter.config, []byte(server.GatewayURL)) {
		t.Fatalf("deployment did not pass the generated credential to the adapter: %s config=%s", deployed.Body.String(), adapter.config)
	}
	request := authenticatedRequestForAdmin(deployment.Secret)
	principal, accessErr := manager.Authenticate(request, &config.Config{Auth: config.Auth{Enabled: true}})
	if accessErr != nil || principal.ClientID != clientBody.Client.ID {
		t.Fatalf("deployed key cannot authenticate: %#v %v", principal, accessErr)
	}
	previous, err := server.ClientStore.GetCredential(context.Background(), clientstore.DefaultScope, initialBody.Credential.ID)
	if err != nil || previous.Status != clientstore.CredentialRevoked {
		t.Fatalf("previous credential was not revoked after deployment: %#v %v", previous, err)
	}
	listed := clientAPIRequest(t, server, http.MethodGet, "/api/clients", nil)
	if strings.Contains(listed.Body.String(), deployment.Secret) || strings.Contains(listed.Body.String(), "secret_hmac") {
		t.Fatalf("client list redisplayed credential material: %s", listed.Body.String())
	}
	applicationState := clientAPIRequest(t, server, http.MethodGet, "/api/apps/test-app", nil)
	if applicationState.Code != http.StatusOK || strings.Contains(applicationState.Body.String(), deployment.Secret) || !strings.Contains(applicationState.Body.String(), deployment.Credential.Prefix) || !strings.Contains(applicationState.Body.String(), webRedactionMask) {
		t.Fatalf("application state did not safely identify the managed credential: %d %s", applicationState.Code, applicationState.Body.String())
	}
}

func authenticatedRequestForAdmin(secret string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://router/v1/models", nil)
	request.Header.Set("authorization", "Bearer "+secret)
	return request
}

func TestCredentialDeploymentStopsBeforeWritesWhenAnyPreviewFails(t *testing.T) {
	server, _, _ := clientManagementServer(t, baseClientConfig())
	good := &credentialDeploymentAdapter{}
	bad := &credentialDeploymentAdapter{previewErr: errors.New("preview rejected")}
	server.Applications = application.NewRegistry(good)
	// Register a second adapter with a distinct manifest through a small wrapper.
	server.Applications = application.NewRegistry(&namedDeploymentAdapter{credentialDeploymentAdapter: bad, id: "bad-app"}, good)
	created := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{"name": "Preview client"})
	var body struct {
		Client clientstore.Client `json:"client"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	deployed := clientAPIRequest(t, server, http.MethodPost, "/api/clients/"+body.Client.ID+"/credential-deployments", map[string]any{
		"applications": []map[string]any{{"id": "test-app", "config": map[string]any{}}, {"id": "bad-app", "config": map[string]any{}}},
	})
	if deployed.Code != http.StatusOK || !strings.Contains(deployed.Body.String(), `"ok":false`) || good.applied != 0 || bad.applied != 0 {
		t.Fatalf("preview failure allowed an application write: %d %s", deployed.Code, deployed.Body.String())
	}
	credentials, err := server.ClientStore.ListCredentials(context.Background(), clientstore.DefaultScope, body.Client.ID)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credential was not retained after partial deployment: %#v %v", credentials, err)
	}
}

type namedDeploymentAdapter struct {
	*credentialDeploymentAdapter
	id string
}

func (a *namedDeploymentAdapter) Manifest() application.Manifest {
	manifest := a.credentialDeploymentAdapter.Manifest()
	manifest.ID = a.id
	manifest.Name = a.id
	return manifest
}

func TestLegacyMigrationKeepsExistingKeyUsableAndRemovesYAMLKey(t *testing.T) {
	t.Setenv("LEGACY_CLIENT_KEY", "legacy-secret-value")
	raw := `version: 1
server:
  listen: 127.0.0.1:12666
  admin_listen: 127.0.0.1:12667
admin:
  enabled: true
auth:
  enabled: true
  keys:
    - id: old-codex
      value: ${LEGACY_CLIENT_KEY}
providers: []
routes: []
`
	server, manager, configPath := clientManagementServer(t, raw)
	migrated := clientAPIRequest(t, server, http.MethodPost, "/api/clients/migrate-legacy", map[string]any{"key_id": "old-codex", "name": "Migrated Codex"})
	if migrated.Code != http.StatusOK {
		t.Fatalf("migration failed: %d %s", migrated.Code, migrated.Body.String())
	}
	configRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configRaw), "old-codex") || strings.Contains(string(configRaw), "LEGACY_CLIENT_KEY") {
		t.Fatalf("legacy key entry remained in configuration:\n%s", configRaw)
	}
	request := httptest.NewRequest(http.MethodGet, "http://router/v1/models", nil)
	request.Header.Set("authorization", "Bearer legacy-secret-value")
	principal, accessErr := manager.Authenticate(request, server.Config.Get())
	if accessErr != nil || principal.Legacy || principal.ClientName != "Migrated Codex" {
		t.Fatalf("migrated key is not managed and usable: %#v %v", principal, accessErr)
	}
}

func TestAdminManagementRejectsNonLoopbackSource(t *testing.T) {
	server, _, _ := clientManagementServer(t, baseClientConfig())
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/clients", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("non-loopback admin request returned %d", recorder.Code)
	}
}

func TestClientCredentialNeverAuthorizesManagementAPI(t *testing.T) {
	t.Setenv("CLIENT_SEPARATION_ADMIN_TOKEN", "admin-token-123456789012345678901234")
	raw := strings.Replace(baseClientConfig(), "admin:\n  enabled: true", "admin:\n  enabled: true\n  token: ${CLIENT_SEPARATION_ADMIN_TOKEN}", 1)
	server, manager, _ := clientManagementServer(t, raw)
	policy := clientstore.ClientPolicy{ID: "policy_separation"}
	client := clientstore.Client{ID: "client_separation", Name: "Separated client", Status: clientstore.ClientActive, PolicyID: policy.ID}
	if err := server.ClientStore.CreateClient(context.Background(), clientstore.DefaultScope, client, policy); err != nil {
		t.Fatal(err)
	}
	credential, secret, err := manager.Keys.Generate(client.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.ClientStore.CreateCredential(context.Background(), clientstore.DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/clients", nil)
	request.RemoteAddr = "127.0.0.1:40000"
	request.Header.Set("authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("client credential authorized the management API: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestClientStateBackupAPIProducesVerifiablePrivateBundle(t *testing.T) {
	server, _, _ := clientManagementServer(t, baseClientConfig())
	created := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{"name": "Backup client", "create_credential": true})
	if created.Code != http.StatusCreated {
		t.Fatalf("seed client failed: %s", created.Body.String())
	}
	var createdBody struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || createdBody.Secret == "" {
		t.Fatalf("seed response did not contain one-time secret: %v", err)
	}
	backup := clientAPIRequest(t, server, http.MethodPost, "/api/client-state/backups", map[string]any{})
	if backup.Code != http.StatusCreated {
		t.Fatalf("backup failed: %d %s", backup.Code, backup.Body.String())
	}
	var body struct {
		Backup struct {
			ID       string                     `json:"id"`
			Manifest clientstore.BackupManifest `json:"manifest"`
		} `json:"backup"`
	}
	if err := json.Unmarshal(backup.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(server.StateDirectory, "backups", body.Backup.ID, "gateway-state.db")
	manifestPath := databasePath + ".manifest.json"
	if _, err := clientstore.VerifyBackup(databasePath, manifestPath, server.CredentialKeys.IDs()); err != nil {
		t.Fatalf("admin backup is not verifiable: %v", err)
	}
	for _, path := range []string{databasePath, manifestPath, databasePath + ".master.key"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("backup file is missing or not private: %s %#v %v", path, info, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil || bytes.Contains(raw, []byte(createdBody.Secret)) {
			t.Fatalf("backup file contains a complete client secret: %s %v", path, err)
		}
	}
	listed := clientAPIRequest(t, server, http.MethodGet, "/api/client-state/backups", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), body.Backup.ID) {
		t.Fatalf("backup was not listed: %d %s", listed.Code, listed.Body.String())
	}
}

func TestEnableManagedAuthenticationVerifiesAndActivatesGateway(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "airoute.yaml")
	if err := os.WriteFile(configPath, []byte(baseClientConfig()), 0600); err != nil {
		t.Fatal(err)
	}
	current, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state, err := clientstore.Open(filepath.Join(dir, "gateway-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	t.Setenv("AIROUTE_CREDENTIAL_MASTER_KEY", "")
	t.Setenv("AIROUTE_CREDENTIAL_PREVIOUS_KEYS", "")
	keys, err := clientauth.LoadOrCreateKeyRing(filepath.Join(dir, "credential-master.key"))
	if err != nil {
		t.Fatal(err)
	}
	policy := clientstore.ClientPolicy{ID: "policy_a"}
	client := clientstore.Client{ID: "client_a", Name: "Client", Status: clientstore.ClientActive, PolicyID: policy.ID}
	if err = state.CreateClient(context.Background(), clientstore.DefaultScope, client, policy); err != nil {
		t.Fatal(err)
	}
	credential, secret, err := keys.GenerateManaged(client.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = state.CreateCredential(context.Background(), clientstore.DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	configStore := config.NewStore(current)
	manager := clientauth.NewManager(state, keys)
	gatewayHandler := gateway.New(configStore, protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gatewayHandler.SetClientAccess(manager)
	gatewayServer := httptest.NewServer(gatewayHandler)
	defer gatewayServer.Close()
	server := New(configStore, protocol.NewRegistry(), observe.NewStore(10), &observe.Metrics{}, "test", gatewayServer.URL, configPath)
	server.SetClientManagement(manager, state, keys, dir)
	enabled := clientAPIRequest(t, server, http.MethodPost, "/api/clients/enable-auth", map[string]any{"credential_id": credential.ID})
	if enabled.Code != http.StatusOK || !configStore.Get().Auth.Enabled {
		t.Fatalf("authentication was not enabled: %d %s", enabled.Code, enabled.Body.String())
	}
	raw, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(raw), "managed_store: true") || !strings.Contains(string(raw), "enabled: true") {
		t.Fatalf("authentication setting was not persisted:\n%s\n%v", raw, err)
	}
	validRequest, _ := http.NewRequest(http.MethodGet, gatewayServer.URL+"/v1/models", nil)
	validRequest.Header.Set("authorization", "Bearer "+secret)
	validResponse, err := http.DefaultClient.Do(validRequest)
	if err != nil {
		t.Fatal(err)
	}
	validResponse.Body.Close()
	if validResponse.StatusCode != http.StatusOK {
		t.Fatalf("valid key returned %d after enable", validResponse.StatusCode)
	}
	invalidRequest, _ := http.NewRequest(http.MethodGet, gatewayServer.URL+"/v1/models", nil)
	invalidRequest.Header.Set("authorization", "Bearer invalid")
	invalidResponse, err := http.DefaultClient.Do(invalidRequest)
	if err != nil {
		t.Fatal(err)
	}
	invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid key returned %d after enable", invalidResponse.StatusCode)
	}
}

func TestEnableManagedAuthenticationRollsBackWhenGatewayVerificationFails(t *testing.T) {
	server, _, configPath := clientManagementServer(t, baseClientConfig())
	created := clientAPIRequest(t, server, http.MethodPost, "/api/clients", map[string]any{"name": "Rollback client", "create_credential": true})
	var body struct {
		Secret     string                     `json:"secret"`
		Credential clientstore.CredentialView `json:"credential"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || body.Secret == "" {
		t.Fatalf("credential seed failed: %s %v", created.Body.String(), err)
	}
	badGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer badGateway.Close()
	server.GatewayURL = badGateway.URL
	enabled := clientAPIRequest(t, server, http.MethodPost, "/api/clients/enable-auth", map[string]any{"credential_id": body.Credential.ID})
	if enabled.Code != http.StatusBadGateway || !strings.Contains(enabled.Body.String(), `"rolled_back":true`) || server.Config.Get().Auth.Enabled {
		t.Fatalf("failed gateway verification did not roll back: %d %s", enabled.Code, enabled.Body.String())
	}
	raw, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(raw), "enabled: false") {
		t.Fatalf("authentication rollback was not persisted:\n%s\n%v", raw, err)
	}
}

func TestRemoveYAMLAuthKeyPreservesOtherConfiguration(t *testing.T) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte("auth:\n  enabled: true\n  keys:\n    - id: one\n      value: ${ONE}\n    - id: two\n      value: ${TWO}\nproviders: []\n"), &document); err != nil {
		t.Fatal(err)
	}
	if !removeYAMLAuthKey(&document, "one") || removeYAMLAuthKey(&document, "missing") {
		t.Fatal("YAML key removal result is wrong")
	}
	raw, _ := yaml.Marshal(&document)
	if strings.Contains(string(raw), "id: one") || !strings.Contains(string(raw), "id: two") || !strings.Contains(string(raw), "providers: []") {
		t.Fatalf("YAML key removal damaged configuration:\n%s", raw)
	}
}
