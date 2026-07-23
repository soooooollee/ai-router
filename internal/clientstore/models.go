package clientstore

import (
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/protocol/ir"
)

const (
	DefaultTenantID  = "tenant_default"
	DefaultProjectID = "project_default"
)

var (
	ErrNotFound       = errors.New("client store record not found")
	ErrAlreadyExists  = errors.New("client store record already exists")
	ErrInvalidState   = errors.New("invalid client store state transition")
	ErrQuotaExhausted = errors.New("client quota exhausted")
)

type Scope struct {
	TenantID  string `json:"tenant_id"`
	ProjectID string `json:"project_id"`
}

var DefaultScope = Scope{TenantID: DefaultTenantID, ProjectID: DefaultProjectID}

func (s Scope) Normalize() Scope {
	if strings.TrimSpace(s.TenantID) == "" {
		s.TenantID = DefaultTenantID
	}
	if strings.TrimSpace(s.ProjectID) == "" {
		s.ProjectID = DefaultProjectID
	}
	return s
}

func (s Scope) Valid() bool {
	s = s.Normalize()
	return s.TenantID != "" && s.ProjectID != ""
}

type ClientStatus string

const (
	ClientActive   ClientStatus = "active"
	ClientDisabled ClientStatus = "disabled"
	ClientDeleted  ClientStatus = "deleted"
)

type Client struct {
	ID          string       `json:"id"`
	TenantID    string       `json:"tenant_id"`
	ProjectID   string       `json:"project_id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Status      ClientStatus `json:"status"`
	PolicyID    string       `json:"policy_id"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type CredentialKind string

const (
	CredentialStandard CredentialKind = "standard"
	CredentialManaged  CredentialKind = "managed"
)

type CredentialStatus string

const (
	CredentialActive   CredentialStatus = "active"
	CredentialDisabled CredentialStatus = "disabled"
	CredentialExpired  CredentialStatus = "expired"
	CredentialRevoked  CredentialStatus = "revoked"
)

type Credential struct {
	ID               string           `json:"id"`
	ClientID         string           `json:"client_id"`
	Kind             CredentialKind   `json:"kind"`
	Prefix           string           `json:"prefix"`
	SecretHMAC       []byte           `json:"secret_hmac"`
	HMACKeyID        string           `json:"hmac_key_id"`
	SecretCiphertext []byte           `json:"secret_ciphertext,omitempty"`
	SecretNonce      []byte           `json:"secret_nonce,omitempty"`
	Status           CredentialStatus `json:"status"`
	CreatedAt        time.Time        `json:"created_at"`
	ExpiresAt        *time.Time       `json:"expires_at,omitempty"`
	LastUsedAt       *time.Time       `json:"last_used_at,omitempty"`
	RevokedAt        *time.Time       `json:"revoked_at,omitempty"`
}

type CredentialView struct {
	ID          string           `json:"id"`
	ClientID    string           `json:"client_id"`
	Kind        CredentialKind   `json:"kind"`
	Prefix      string           `json:"prefix"`
	Recoverable bool             `json:"recoverable"`
	Status      CredentialStatus `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	ExpiresAt   *time.Time       `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time       `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time       `json:"revoked_at,omitempty"`
}

func (c Credential) View() CredentialView {
	view := CredentialView{
		ID: c.ID, ClientID: c.ClientID, Kind: c.Kind, Prefix: c.Prefix,
		Recoverable: c.Kind == CredentialManaged && len(c.SecretCiphertext) > 0 && len(c.SecretNonce) > 0,
		Status:      c.Status, CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt,
		LastUsedAt: c.LastUsedAt, RevokedAt: c.RevokedAt,
	}
	if view.Status == CredentialActive && view.ExpiresAt != nil && !view.ExpiresAt.After(time.Now()) {
		view.Status = CredentialExpired
	}
	return view
}

type ClientPolicy struct {
	ID                string        `json:"id"`
	ProjectID         string        `json:"project_id"`
	AllowedModels     []string      `json:"allowed_models,omitempty"`
	AllowedProtocols  []ir.Protocol `json:"allowed_protocols,omitempty"`
	AllowedCIDRs      []string      `json:"allowed_cidrs,omitempty"`
	RequestsPerMinute int           `json:"requests_per_minute,omitempty"`
	Burst             int           `json:"burst,omitempty"`
	MaxConcurrent     int           `json:"max_concurrent,omitempty"`
	DailyRequestLimit int64         `json:"daily_request_limit,omitempty"`
	DailyInputTokens  int64         `json:"daily_input_tokens,omitempty"`
	DailyOutputTokens int64         `json:"daily_output_tokens,omitempty"`
	MaxOutputTokens   int           `json:"max_output_tokens,omitempty"`
}

func (p ClientPolicy) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("policy id is required")
	}
	for _, value := range []int64{int64(p.RequestsPerMinute), int64(p.Burst), int64(p.MaxConcurrent), p.DailyRequestLimit, p.DailyInputTokens, p.DailyOutputTokens, int64(p.MaxOutputTokens)} {
		if value < 0 {
			return errors.New("policy limits cannot be negative")
		}
	}
	seenModels := map[string]bool{}
	for _, model := range p.AllowedModels {
		model = strings.TrimSpace(model)
		if model == "" || seenModels[model] {
			return errors.New("allowed models must be unique and non-empty")
		}
		seenModels[model] = true
	}
	seenProtocols := map[ir.Protocol]bool{}
	for _, protocol := range p.AllowedProtocols {
		switch protocol {
		case ir.OpenAIChat, ir.OpenAIResponses, ir.Anthropic, ir.Gemini:
		default:
			return errors.New("allowed protocols must use a supported client protocol")
		}
		if seenProtocols[protocol] {
			return errors.New("allowed protocols must be unique")
		}
		seenProtocols[protocol] = true
	}
	for _, cidr := range p.AllowedCIDRs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(cidr)); err != nil {
			return errors.New("allowed CIDRs must be valid network prefixes")
		}
	}
	return nil
}

type ClientFilter struct {
	IncludeDeleted bool
	Status         ClientStatus
	Search         string
}

type ClientSummary struct {
	Client            Client           `json:"client"`
	Policy            ClientPolicy     `json:"policy"`
	Credentials       []CredentialView `json:"credentials"`
	ActiveCredentials int              `json:"active_credentials"`
	Today             UsageBucket      `json:"today"`
}

type UsageBucket struct {
	TenantID             string    `json:"tenant_id"`
	ProjectID            string    `json:"project_id"`
	ClientID             string    `json:"client_id"`
	WindowStart          time.Time `json:"window_start"`
	WindowKind           string    `json:"window_kind"`
	Requests             int64     `json:"requests"`
	Errors               int64     `json:"errors"`
	Rejected             int64     `json:"rejected"`
	InputTokens          int64     `json:"input_tokens"`
	OutputTokens         int64     `json:"output_tokens"`
	ReservedInputTokens  int64     `json:"reserved_input_tokens,omitempty"`
	ReservedOutputTokens int64     `json:"reserved_output_tokens,omitempty"`
	Estimated            int64     `json:"estimated_requests,omitempty"`
}

type UsageReservation struct {
	RequestID    string    `json:"request_id"`
	TenantID     string    `json:"tenant_id"`
	ProjectID    string    `json:"project_id"`
	ClientID     string    `json:"client_id"`
	Day          time.Time `json:"day"`
	Minute       time.Time `json:"minute"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CreatedAt    time.Time `json:"created_at"`
}

type UsageDelta struct {
	RequestID    string
	InputTokens  int64
	OutputTokens int64
	Error        bool
	Rejected     bool
	Estimated    bool
}

type UsageQuery struct {
	From time.Time
	To   time.Time
}

type UsageSummary struct {
	ClientID string        `json:"client_id"`
	Total    UsageBucket   `json:"total"`
	Daily    []UsageBucket `json:"daily"`
	Minute   []UsageBucket `json:"minute"`
}

type AuditEvent struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenant_id"`
	ProjectID    string            `json:"project_id"`
	ActorType    string            `json:"actor_type"`
	ActorID      string            `json:"actor_id"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

type AuditFilter struct {
	ClientID string
	Limit    int
}
