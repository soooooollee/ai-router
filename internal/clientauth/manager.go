package clientauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/clientstore"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/protocol/ir"
	"github.com/zbss/airoute/internal/ratelimit"
	"github.com/zbss/airoute/internal/tokencount"
)

type Principal struct {
	TenantID         string                   `json:"tenant_id"`
	ProjectID        string                   `json:"project_id"`
	ClientID         string                   `json:"client_id"`
	ClientName       string                   `json:"client_name"`
	CredentialID     string                   `json:"credential_id,omitempty"`
	CredentialPrefix string                   `json:"credential_prefix,omitempty"`
	Policy           clientstore.ClientPolicy `json:"policy"`
	Legacy           bool                     `json:"legacy"`
	Anonymous        bool                     `json:"anonymous"`
}

func (p Principal) Scope() clientstore.Scope {
	return clientstore.Scope{TenantID: p.TenantID, ProjectID: p.ProjectID}.Normalize()
}

type AccessError struct {
	Status  int
	Code    string
	Message string
}

func (e *AccessError) Error() string { return e.Message }

type Manager struct {
	Store   clientstore.Store
	Keys    *KeyRing
	Limiter *ratelimit.Limiter
	Now     func() time.Time
}

func NewManager(store clientstore.Store, keys *KeyRing) *Manager {
	return &Manager{Store: store, Keys: keys, Limiter: ratelimit.New(), Now: time.Now}
}

func (m *Manager) now() time.Time {
	if m.Now == nil {
		return time.Now().UTC()
	}
	return m.Now().UTC()
}

func (m *Manager) Authenticate(r *http.Request, c *config.Config) (Principal, *AccessError) {
	if !c.Auth.Enabled {
		return Principal{TenantID: clientstore.DefaultTenantID, ProjectID: clientstore.DefaultProjectID, ClientID: "anonymous", ClientName: "Anonymous", Anonymous: true}, nil
	}
	value, accessErr := extractCredential(r, c.Auth.AllowQueryKey)
	if accessErr != nil {
		return Principal{}, accessErr
	}
	if value == "" {
		return Principal{}, authenticationError("invalid or missing API key")
	}
	if strings.HasPrefix(value, "air_sk_") {
		return Principal{}, authenticationError("invalid or missing API key")
	}
	if c.Auth.ManagedStoreEnabled() && m != nil && m.Store != nil && m.Keys != nil {
		for keyID, digest := range m.Keys.Digests(value) {
			credential, storeErr := m.Store.GetCredentialByHMAC(r.Context(), clientstore.DefaultScope, keyID, digest)
			if errors.Is(storeErr, clientstore.ErrNotFound) {
				continue
			}
			if storeErr != nil {
				return Principal{}, &AccessError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "credential store is unavailable"}
			}
			principal, err := m.authenticateManaged(r.Context(), credential, value)
			if err == nil {
				return principal, nil
			}
			if err.Status != http.StatusUnauthorized || err.Code != "authentication_error" {
				return Principal{}, err
			}
		}
	}
	for _, key := range c.Auth.Keys {
		if secureEqual(value, key.Value) {
			return Principal{
				TenantID: clientstore.DefaultTenantID, ProjectID: clientstore.DefaultProjectID,
				ClientID: key.ID, ClientName: key.ID, Legacy: true,
			}, nil
		}
	}
	return Principal{}, authenticationError("invalid or missing API key")
}

func extractCredential(r *http.Request, allowQuery bool) (string, *AccessError) {
	values := make([]string, 0, 4)
	for _, header := range r.Header.Values("Authorization") {
		if value := bearer(header); value != "" {
			values = append(values, value)
		}
	}
	for _, name := range []string{"x-api-key", "x-goog-api-key"} {
		for _, value := range r.Header.Values(name) {
			if value = strings.TrimSpace(value); value != "" {
				values = append(values, value)
			}
		}
	}
	query := strings.TrimSpace(r.URL.Query().Get("key"))
	if query != "" {
		if !allowQuery {
			return "", &AccessError{Status: http.StatusUnauthorized, Code: "query_key_not_allowed", Message: "API keys in query parameters are disabled"}
		}
		values = append(values, query)
	}
	if len(values) == 0 {
		return "", nil
	}
	selected := values[0]
	for _, value := range values[1:] {
		if value != selected {
			return "", &AccessError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "conflicting API keys were provided"}
		}
	}
	return selected, nil
}

func bearer(value string) string {
	if len(value) > 7 && strings.EqualFold(value[:7], "Bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return ""
}

func secureEqual(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func authenticationError(message string) *AccessError {
	return &AccessError{Status: http.StatusUnauthorized, Code: "authentication_error", Message: message}
}

func (m *Manager) authenticateManaged(ctx context.Context, credential clientstore.Credential, completeKey string) (Principal, *AccessError) {
	scope := clientstore.DefaultScope
	if credential.ID == "" {
		return Principal{}, authenticationError("invalid or missing API key")
	}
	if credential.Kind == clientstore.CredentialManaged && !ValidManagedCredentialKey(completeKey) {
		return Principal{}, authenticationError("invalid or missing API key")
	}
	if !m.Keys.Verify(credential.HMACKeyID, completeKey, credential.SecretHMAC) {
		return Principal{}, authenticationError("invalid or missing API key")
	}
	now := m.now()
	switch credential.Status {
	case clientstore.CredentialDisabled:
		return Principal{}, &AccessError{Status: http.StatusForbidden, Code: "credential_disabled", Message: "credential is disabled"}
	case clientstore.CredentialExpired:
		return Principal{}, &AccessError{Status: http.StatusForbidden, Code: "credential_expired", Message: "credential has expired"}
	case clientstore.CredentialRevoked:
		return Principal{}, &AccessError{Status: http.StatusForbidden, Code: "credential_revoked", Message: "credential has been revoked"}
	case clientstore.CredentialActive:
	default:
		return Principal{}, &AccessError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "credential has an invalid state"}
	}
	if credential.ExpiresAt != nil && !credential.ExpiresAt.After(now) {
		_ = m.Store.UpdateCredentialStatus(ctx, scope, credential.ID, clientstore.CredentialExpired, now)
		return Principal{}, &AccessError{Status: http.StatusForbidden, Code: "credential_expired", Message: "credential has expired"}
	}
	client, err := m.Store.GetClient(ctx, scope, credential.ClientID)
	if err != nil {
		return Principal{}, &AccessError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "credential client is unavailable"}
	}
	if client.Status != clientstore.ClientActive {
		return Principal{}, &AccessError{Status: http.StatusForbidden, Code: "credential_disabled", Message: "client is disabled"}
	}
	policy, err := m.Store.GetPolicy(ctx, scope, client.PolicyID)
	if err != nil {
		return Principal{}, &AccessError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "client policy is unavailable"}
	}
	if credential.LastUsedAt != nil && now.Before(credential.LastUsedAt.Add(-time.Second)) {
		_ = m.Store.AppendAudit(ctx, scope, clientstore.AuditEvent{
			ID:        "audit_clock_" + credential.ID + "_" + now.Format("20060102T150405.000000000"),
			ActorType: "system", ActorID: "gateway", Action: "system.clock_rollback_detected",
			ResourceType: "credential", ResourceID: credential.ID,
			Metadata: map[string]string{"client_id": client.ID}, CreatedAt: now,
		})
	}
	if credential.LastUsedAt == nil || now.Sub(*credential.LastUsedAt) >= time.Minute {
		if err = m.Store.TouchCredential(ctx, scope, credential.ID, now); err != nil {
			return Principal{}, &AccessError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "credential store is unavailable"}
		}
	}
	return Principal{
		TenantID: scope.TenantID, ProjectID: scope.ProjectID, ClientID: client.ID, ClientName: client.Name,
		CredentialID: credential.ID, CredentialPrefix: credential.Prefix, Policy: policy,
	}, nil
}

func (m *Manager) Precheck(ctx context.Context, principal Principal, protocol ir.Protocol, remoteAddr string) *AccessError {
	if principal.Anonymous || principal.Legacy {
		return nil
	}
	policy := principal.Policy
	if len(policy.AllowedProtocols) > 0 {
		allowed := false
		for _, candidate := range policy.AllowedProtocols {
			if candidate == protocol {
				allowed = true
				break
			}
		}
		if !allowed {
			m.reject(ctx, principal)
			return &AccessError{Status: http.StatusForbidden, Code: "protocol_not_allowed", Message: "client protocol is not allowed"}
		}
	}
	if len(policy.AllowedCIDRs) > 0 && !sourceAllowed(remoteAddr, policy.AllowedCIDRs) {
		m.reject(ctx, principal)
		return &AccessError{Status: http.StatusForbidden, Code: "source_ip_not_allowed", Message: "source IP is not allowed"}
	}
	if !m.Limiter.Allow(principal.ClientID, policy.RequestsPerMinute, policy.Burst, m.now()) {
		m.reject(ctx, principal)
		return &AccessError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "client request rate limit reached"}
	}
	return nil
}

func sourceAllowed(remoteAddr string, cidrs []string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	address = address.Unmap()
	for _, value := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}

func modelAllowed(model string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == model || candidate == "*" {
			return true
		}
		if matched, err := path.Match(candidate, model); err == nil && matched {
			return true
		}
	}
	return false
}

type RequestLease struct {
	manager        *Manager
	principal      Principal
	requestID      string
	estimatedInput int64
	reserved       bool
	release        func()
}

func (m *Manager) BeginRequest(ctx context.Context, principal Principal, request *ir.Request, requestID string) (*RequestLease, *AccessError) {
	lease := &RequestLease{manager: m, principal: principal, requestID: requestID, release: func() {}}
	if principal.Anonymous || principal.Legacy {
		return lease, nil
	}
	policy := principal.Policy
	if !modelAllowed(request.Model, policy.AllowedModels) {
		m.reject(ctx, principal)
		return nil, &AccessError{Status: http.StatusForbidden, Code: "model_not_allowed", Message: "requested model is not allowed"}
	}
	requestedOutput := 0
	if request.Sampling.MaxOutputTokens != nil {
		requestedOutput = *request.Sampling.MaxOutputTokens
	}
	if policy.MaxOutputTokens > 0 && requestedOutput > policy.MaxOutputTokens {
		m.reject(ctx, principal)
		return nil, &AccessError{Status: http.StatusForbidden, Code: "max_output_tokens_exceeded", Message: "requested output token limit exceeds client policy"}
	}
	release, ok := m.Limiter.Acquire(principal.ClientID, policy.MaxConcurrent)
	if !ok {
		m.reject(ctx, principal)
		return nil, &AccessError{Status: http.StatusTooManyRequests, Code: "concurrency_limited", Message: "client concurrency limit reached"}
	}
	lease.release = release
	input := int64(tokencount.Heuristic{}.Count(request).InputTokens)
	output := int64(requestedOutput)
	if output == 0 && policy.MaxOutputTokens > 0 {
		output = int64(policy.MaxOutputTokens)
	}
	if output == 0 && policy.DailyOutputTokens > 0 {
		output = min64(4096, policy.DailyOutputTokens)
	}
	lease.estimatedInput = input
	if err := m.Store.ReserveUsage(ctx, principal.Scope(), clientstore.UsageReservation{
		RequestID: requestID, ClientID: principal.ClientID, Day: m.now(), InputTokens: input, OutputTokens: output,
	}, policy); err != nil {
		release()
		m.reject(ctx, principal)
		if errors.Is(err, clientstore.ErrQuotaExhausted) {
			return nil, &AccessError{Status: http.StatusTooManyRequests, Code: "quota_exhausted", Message: "client daily quota exhausted"}
		}
		return nil, &AccessError{Status: http.StatusServiceUnavailable, Code: "usage_unavailable", Message: "usage store is unavailable"}
	}
	lease.reserved = true
	return lease, nil
}

func (l *RequestLease) Finish(ctx context.Context, usage ir.Usage, requestFailed bool) error {
	if l == nil {
		return nil
	}
	l.release()
	if !l.reserved {
		return nil
	}
	input := int64(usage.InputTokens)
	output := int64(usage.OutputTokens)
	estimated := false
	if input == 0 && !requestFailed {
		input = l.estimatedInput
		estimated = true
	}
	if usage.OutputTokens == 0 && requestFailed {
		output = 0
	}
	return l.manager.Store.SettleUsage(ctx, l.principal.Scope(), clientstore.UsageDelta{
		RequestID: l.requestID, InputTokens: input, OutputTokens: output, Error: requestFailed, Estimated: estimated,
	})
}

func (m *Manager) reject(ctx context.Context, principal Principal) {
	if m == nil || m.Store == nil || principal.Anonymous || principal.Legacy {
		return
	}
	_ = m.Store.AddRejectedUsage(ctx, principal.Scope(), principal.ClientID, m.now())
}

func (m *Manager) FilterModels(principal Principal, models []string) []string {
	if principal.Anonymous || principal.Legacy || len(principal.Policy.AllowedModels) == 0 {
		return models
	}
	out := make([]string, 0, len(models))
	for _, model := range models {
		if modelAllowed(model, principal.Policy.AllowedModels) {
			out = append(out, model)
		}
	}
	return out
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
