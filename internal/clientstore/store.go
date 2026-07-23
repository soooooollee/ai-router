package clientstore

import (
	"context"
	"time"
)

type Store interface {
	CreateClient(context.Context, Scope, Client, ClientPolicy) error
	UpdateClient(context.Context, Scope, Client) error
	GetClient(context.Context, Scope, string) (Client, error)
	ListClients(context.Context, Scope, ClientFilter) ([]ClientSummary, error)

	CreateCredential(context.Context, Scope, Credential) error
	GetCredential(context.Context, Scope, string) (Credential, error)
	GetCredentialByHMAC(context.Context, Scope, string, []byte) (Credential, error)
	ListCredentials(context.Context, Scope, string) ([]Credential, error)
	UpdateCredentialStatus(context.Context, Scope, string, CredentialStatus, time.Time) error
	DeleteCredential(context.Context, Scope, string) error
	TouchCredential(context.Context, Scope, string, time.Time) error

	GetPolicy(context.Context, Scope, string) (ClientPolicy, error)
	UpdatePolicy(context.Context, Scope, ClientPolicy) error

	ReserveUsage(context.Context, Scope, UsageReservation, ClientPolicy) error
	SettleUsage(context.Context, Scope, UsageDelta) error
	AddRejectedUsage(context.Context, Scope, string, time.Time) error
	GetUsage(context.Context, Scope, string, UsageQuery) (UsageSummary, error)

	AppendAudit(context.Context, Scope, AuditEvent) error
	ListAudit(context.Context, Scope, AuditFilter) ([]AuditEvent, error)
	RequiredHMACKeyIDs(context.Context) ([]string, error)
	Backup(context.Context, string, []string, string) (BackupManifest, error)
	Close() error
}

type BackupManifest struct {
	Version           int       `json:"version"`
	CreatedAt         time.Time `json:"created_at"`
	Database          string    `json:"database"`
	SHA256            string    `json:"sha256"`
	HMACKeyIDs        []string  `json:"hmac_key_ids"`
	MasterKeyFile     string    `json:"master_key_file,omitempty"`
	MasterKeySHA256   string    `json:"master_key_sha256,omitempty"`
	ExternalMasterKey bool      `json:"external_master_key,omitempty"`
}
