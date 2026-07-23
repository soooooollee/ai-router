package clientauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/clientstore"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
)

func testManager(t *testing.T, policy clientstore.ClientPolicy) (*Manager, *config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := clientstore.Open(filepath.Join(dir, "gateway-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	t.Setenv(masterKeyEnvironment, "")
	t.Setenv(previousKeysEnv, "")
	ring, err := LoadOrCreateKeyRing(filepath.Join(dir, "credential-master.key"))
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "policy_a"
	client := clientstore.Client{ID: "client_a", Name: "Codex", Status: clientstore.ClientActive, PolicyID: policy.ID}
	if err = store.CreateClient(context.Background(), clientstore.DefaultScope, client, policy); err != nil {
		t.Fatal(err)
	}
	credential, secret, err := ring.Generate(client.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateCredential(context.Background(), clientstore.DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, ring)
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	return manager, &config.Config{Auth: config.Auth{Enabled: true}}, secret
}

func authenticatedRequest(secret string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://router/v1/responses", nil)
	request.Header.Set("authorization", "Bearer "+secret)
	request.RemoteAddr = "127.0.0.1:1234"
	return request
}

func TestManagerRejectsPreviousCredentialFormat(t *testing.T) {
	manager, cfg, _ := testManager(t, clientstore.ClientPolicy{})
	if _, accessErr := manager.Authenticate(authenticatedRequest("air_sk_live_old_secret"), cfg); accessErr == nil || accessErr.Code != "authentication_error" {
		t.Fatalf("previous credential format was accepted: %v", accessErr)
	}
}

func TestManagerAuthenticatesManagedAndLegacyCredentials(t *testing.T) {
	manager, cfg, secret := testManager(t, clientstore.ClientPolicy{})
	principal, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil || principal.ClientID != "client_a" || principal.CredentialID == "" || principal.Legacy {
		t.Fatalf("managed authentication failed: %#v %v", principal, accessErr)
	}
	request := authenticatedRequest(secret)
	request.Header.Set("x-api-key", "different")
	if _, accessErr = manager.Authenticate(request, cfg); accessErr == nil || accessErr.Status != http.StatusBadRequest {
		t.Fatalf("conflicting credentials were accepted: %v", accessErr)
	}
	query := httptest.NewRequest(http.MethodGet, "http://router/v1/models?key="+secret, nil)
	if _, accessErr = manager.Authenticate(query, cfg); accessErr == nil || accessErr.Code != "query_key_not_allowed" {
		t.Fatalf("query credential was accepted without opt-in: %v", accessErr)
	}
	cfg.Auth.AllowQueryKey = true
	if principal, accessErr = manager.Authenticate(query, cfg); accessErr != nil || principal.ClientID != "client_a" {
		t.Fatalf("query credential was rejected after opt-in: %#v %v", principal, accessErr)
	}
	cfg.Auth.AllowQueryKey = false
	cfg.Auth.Keys = []config.APIKey{{ID: "legacy", Value: "legacy-secret"}}
	legacy := authenticatedRequest("legacy-secret")
	principal, accessErr = manager.Authenticate(legacy, cfg)
	if accessErr != nil || !principal.Legacy || principal.ClientID != "legacy" {
		t.Fatalf("legacy authentication failed: %#v %v", principal, accessErr)
	}
}

func TestManagerRejectsCredentialAndClientLifecycleStates(t *testing.T) {
	manager, cfg, secret := testManager(t, clientstore.ClientPolicy{})
	principal, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	credential, err := manager.Store.GetCredential(context.Background(), principal.Scope(), principal.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		status clientstore.CredentialStatus
		code   string
	}{
		{clientstore.CredentialDisabled, "credential_disabled"},
		{clientstore.CredentialRevoked, "credential_revoked"},
	} {
		if err = manager.Store.UpdateCredentialStatus(context.Background(), principal.Scope(), credential.ID, test.status, manager.now()); err != nil {
			t.Fatal(err)
		}
		if _, accessErr = manager.Authenticate(authenticatedRequest(secret), cfg); accessErr == nil || accessErr.Code != test.code {
			t.Fatalf("credential state %s was accepted: %v", test.status, accessErr)
		}
		if test.status == clientstore.CredentialRevoked {
			break
		}
		if err = manager.Store.UpdateCredentialStatus(context.Background(), principal.Scope(), credential.ID, clientstore.CredentialActive, manager.now()); err != nil {
			t.Fatal(err)
		}
	}

	manager, cfg, secret = testManager(t, clientstore.ClientPolicy{})
	principal, accessErr = manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	client, err := manager.Store.GetClient(context.Background(), principal.Scope(), principal.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	client.Status = clientstore.ClientDisabled
	if err = manager.Store.UpdateClient(context.Background(), principal.Scope(), client); err != nil {
		t.Fatal(err)
	}
	if _, accessErr = manager.Authenticate(authenticatedRequest(secret), cfg); accessErr == nil || accessErr.Code != "credential_disabled" {
		t.Fatalf("disabled client was accepted: %v", accessErr)
	}
}

func TestManagerExpiresCredentialAndDoesNotEstimateFailedRequests(t *testing.T) {
	manager, cfg, secret := testManager(t, clientstore.ClientPolicy{})
	principal, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	credential, err := manager.Store.GetCredential(context.Background(), principal.Scope(), principal.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	past := manager.now().Add(-time.Minute)
	credential.ExpiresAt = &past
	// Expiry is immutable after creation, so seed a second credential with a
	// past expiry to exercise the authentication-time state transition.
	credential.ID = "expired_credential"
	credential.Prefix = "air_sk_live_expired"
	credential.CreatedAt = manager.now().Add(-time.Hour)
	credential.Status = clientstore.CredentialActive
	credential.SecretHMAC, credential.HMACKeyID, err = manager.Keys.Digest("expired-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Store.CreateCredential(context.Background(), principal.Scope(), credential); err != nil {
		t.Fatal(err)
	}
	if _, accessErr = manager.Authenticate(authenticatedRequest("expired-secret"), cfg); accessErr == nil || accessErr.Code != "credential_expired" {
		t.Fatalf("expired credential was accepted: %v", accessErr)
	}

	request := &ir.Request{Model: "any", Messages: []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: "hello"}}}}}
	lease, accessErr := manager.BeginRequest(context.Background(), principal, request, "failed_request")
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	if err = lease.Finish(context.Background(), ir.Usage{}, true); err != nil {
		t.Fatal(err)
	}
	usage, err := manager.Store.GetUsage(context.Background(), principal.Scope(), principal.ClientID, clientstore.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total.InputTokens != 0 || usage.Total.OutputTokens != 0 || usage.Total.Errors != 1 || usage.Total.Estimated != 0 {
		t.Fatalf("failed request was assigned fabricated token usage: %#v", usage.Total)
	}
}

func TestManagerPolicyPrecheckAndRequestLease(t *testing.T) {
	policy := clientstore.ClientPolicy{
		AllowedModels: []string{"allowed-*"}, AllowedProtocols: []ir.Protocol{ir.OpenAIResponses}, AllowedCIDRs: []string{"127.0.0.0/8"},
		RequestsPerMinute: 1, Burst: 1, MaxConcurrent: 1, DailyRequestLimit: 2, DailyInputTokens: 10000, DailyOutputTokens: 100,
		MaxOutputTokens: 50,
	}
	manager, cfg, secret := testManager(t, policy)
	principal, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	if accessErr = manager.Precheck(context.Background(), principal, ir.OpenAIChat, "127.0.0.1:1"); accessErr == nil || accessErr.Code != "protocol_not_allowed" {
		t.Fatalf("protocol policy was not enforced: %v", accessErr)
	}
	if accessErr = manager.Precheck(context.Background(), principal, ir.OpenAIResponses, "192.0.2.1:1"); accessErr == nil || accessErr.Code != "source_ip_not_allowed" {
		t.Fatalf("CIDR policy was not enforced: %v", accessErr)
	}
	if accessErr = manager.Precheck(context.Background(), principal, ir.OpenAIResponses, "127.0.0.1:1"); accessErr != nil {
		t.Fatal(accessErr)
	}
	if accessErr = manager.Precheck(context.Background(), principal, ir.OpenAIResponses, "127.0.0.1:1"); accessErr == nil || accessErr.Code != "rate_limited" {
		t.Fatalf("RPM policy was not enforced: %v", accessErr)
	}
	maxOutput := 40
	request := &ir.Request{Model: "allowed-model", Sampling: ir.SamplingOptions{MaxOutputTokens: &maxOutput}, Messages: []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: "hello"}}}}}
	lease, accessErr := manager.BeginRequest(context.Background(), principal, request, "req_1")
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	if _, secondErr := manager.BeginRequest(context.Background(), principal, request, "req_2"); secondErr == nil || secondErr.Code != "concurrency_limited" {
		t.Fatalf("concurrency policy was not enforced: %v", secondErr)
	}
	if err := lease.Finish(context.Background(), ir.Usage{InputTokens: 7, OutputTokens: 9}, false); err != nil {
		t.Fatal(err)
	}
	usage, err := manager.Store.GetUsage(context.Background(), principal.Scope(), principal.ClientID, clientstore.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total.Requests != 1 || usage.Total.InputTokens != 7 || usage.Total.OutputTokens != 9 {
		t.Fatalf("request usage was not settled: %#v", usage.Total)
	}
	denied := &ir.Request{Model: "other-model"}
	if _, accessErr = manager.BeginRequest(context.Background(), principal, denied, "req_3"); accessErr == nil || accessErr.Code != "model_not_allowed" {
		t.Fatalf("model policy was not enforced: %v", accessErr)
	}
	tooLarge := &ir.Request{Model: "allowed-model", Sampling: ir.SamplingOptions{MaxOutputTokens: func() *int { value := 51; return &value }()}}
	if _, accessErr = manager.BeginRequest(context.Background(), principal, tooLarge, "req_4"); accessErr == nil || accessErr.Code != "max_output_tokens_exceeded" {
		t.Fatalf("max output policy was not enforced: %v", accessErr)
	}
}

func TestManagerUsesSocketPeerForIPv4AndIPv6CIDRPolicy(t *testing.T) {
	policy := clientstore.ClientPolicy{AllowedCIDRs: []string{"127.0.0.0/8", "2001:db8::/32"}}
	manager, cfg, secret := testManager(t, policy)
	principal, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	for _, remote := range []string{"127.0.0.1:1234", "[2001:db8::1]:443"} {
		if accessErr = manager.Precheck(context.Background(), principal, ir.OpenAIResponses, remote); accessErr != nil {
			t.Fatalf("allowed peer %s was rejected: %v", remote, accessErr)
		}
	}
	request := authenticatedRequest(secret)
	request.RemoteAddr = "192.0.2.20:1234"
	request.Header.Set("x-forwarded-for", "127.0.0.1")
	if accessErr = manager.Precheck(context.Background(), principal, ir.OpenAIResponses, request.RemoteAddr); accessErr == nil || accessErr.Code != "source_ip_not_allowed" {
		t.Fatalf("forged proxy header bypassed the socket peer policy: %v", accessErr)
	}
}

func TestManagerFailsClosedWhenCredentialStoreIsUnavailable(t *testing.T) {
	manager, cfg, secret := testManager(t, clientstore.ClientPolicy{})
	if err := manager.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg); accessErr == nil || accessErr.Status != http.StatusServiceUnavailable || accessErr.Code != "authentication_unavailable" {
		t.Fatalf("closed credential store did not fail closed: %v", accessErr)
	}
}

func TestConcurrentAuthenticationAndRevocation(t *testing.T) {
	manager, cfg, secret := testManager(t, clientstore.ClientPolicy{})
	principal, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for attempt := 0; attempt < 25; attempt++ {
				_, _ = manager.Authenticate(authenticatedRequest(secret), cfg)
			}
		}()
	}
	if err := manager.Store.UpdateCredentialStatus(context.Background(), principal.Scope(), principal.CredentialID, clientstore.CredentialRevoked, manager.now()); err != nil {
		t.Fatal(err)
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}
	if _, accessErr = manager.Authenticate(authenticatedRequest(secret), cfg); accessErr == nil || accessErr.Code != "credential_revoked" {
		t.Fatalf("revoked credential remained usable: %v", accessErr)
	}
}

func TestManagerAuditsSystemClockRollback(t *testing.T) {
	manager, cfg, secret := testManager(t, clientstore.ClientPolicy{})
	principal, accessErr := manager.Authenticate(authenticatedRequest(secret), cfg)
	if accessErr != nil {
		t.Fatal(accessErr)
	}
	if err := manager.Store.TouchCredential(context.Background(), principal.Scope(), principal.CredentialID, manager.now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, accessErr = manager.Authenticate(authenticatedRequest(secret), cfg); accessErr != nil {
		t.Fatal(accessErr)
	}
	events, err := manager.Store.ListAudit(context.Background(), principal.Scope(), clientstore.AuditFilter{ClientID: principal.ClientID})
	if err != nil || len(events) == 0 || events[0].Action != "system.clock_rollback_detected" {
		t.Fatalf("clock rollback was not audited: %#v %v", events, err)
	}
}
