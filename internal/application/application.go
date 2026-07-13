package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Capability string

const (
	CapabilityDetect    Capability = "detect"
	CapabilityConfigure Capability = "configure"
	CapabilityPreview   Capability = "preview"
	CapabilityVerify    Capability = "verify"
	CapabilityRollback  Capability = "rollback"
)

type Manifest struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Status       string       `json:"status"`
	Capabilities []Capability `json:"capabilities"`
	ConfigFormat string       `json:"config_format"`
}

type Detection struct {
	Installed  bool   `json:"installed"`
	Executable string `json:"executable,omitempty"`
	Version    string `json:"version,omitempty"`
	Message    string `json:"message,omitempty"`
}

type State struct {
	Manifest        Manifest       `json:"manifest"`
	Detection       Detection      `json:"detection"`
	Path            string         `json:"path"`
	Exists          bool           `json:"exists"`
	Managed         map[string]any `json:"managed"`
	PreservedFields int            `json:"preserved_fields"`
	Synced          bool           `json:"synced"`
}

type Preview struct {
	Path             string          `json:"path"`
	Content          json.RawMessage `json:"content"`
	Diff             string          `json:"diff"`
	PreservedFields  int             `json:"preserved_fields"`
	WillCreateBackup bool            `json:"will_create_backup"`
}

type ApplyResult struct {
	OK     bool   `json:"ok"`
	Path   string `json:"path"`
	Backup string `json:"backup,omitempty"`
}

type VerifyOptions struct {
	Config json.RawMessage `json:"config,omitempty"`
	RunCLI bool            `json:"run_cli,omitempty"`
}

type VerifyStage struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	Latency int64  `json:"latency_ms,omitempty"`
}

type VerifyResult struct {
	OK       bool          `json:"ok"`
	Verified time.Time     `json:"verified_at"`
	Stages   []VerifyStage `json:"stages"`
}

type Backup struct {
	Name              string    `json:"name"`
	Path              string    `json:"path"`
	Size              int64     `json:"size"`
	ModifiedAt        time.Time `json:"modified_at"`
	ContainsSensitive bool      `json:"contains_sensitive_config"`
}

type Adapter interface {
	Manifest() Manifest
	Detect(context.Context) (Detection, error)
	Read(context.Context) (State, error)
	Preview(context.Context, json.RawMessage) (Preview, error)
	Apply(context.Context, json.RawMessage) (ApplyResult, error)
	Verify(context.Context, VerifyOptions) (VerifyResult, error)
	Backups(context.Context) ([]Backup, error)
	Rollback(context.Context, string) (ApplyResult, error)
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{adapters: map[string]Adapter{}}
	for _, adapter := range adapters {
		if err := r.Register(adapter); err != nil {
			panic(err)
		}
	}
	return r
}

func (r *Registry) Register(adapter Adapter) error {
	if adapter == nil {
		return fmt.Errorf("application adapter is nil")
	}
	manifest := adapter.Manifest()
	if manifest.ID == "" || manifest.Name == "" || manifest.Description == "" {
		return fmt.Errorf("application adapter requires id, name and description")
	}
	if manifest.Status == "" {
		return fmt.Errorf("application adapter %q requires status", manifest.ID)
	}
	if manifest.ConfigFormat == "" {
		return fmt.Errorf("application adapter %q requires config_format", manifest.ID)
	}
	if len(manifest.Capabilities) == 0 {
		return fmt.Errorf("application adapter %q requires capabilities", manifest.ID)
	}
	seenCapabilities := make(map[Capability]struct{}, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		switch capability {
		case CapabilityDetect, CapabilityConfigure, CapabilityPreview, CapabilityVerify, CapabilityRollback:
		default:
			return fmt.Errorf("application adapter %q declares unknown capability %q", manifest.ID, capability)
		}
		if _, exists := seenCapabilities[capability]; exists {
			return fmt.Errorf("application adapter %q declares duplicate capability %q", manifest.ID, capability)
		}
		seenCapabilities[capability] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[manifest.ID]; exists {
		return fmt.Errorf("application adapter %q is already registered", manifest.ID)
	}
	r.adapters[manifest.ID] = adapter
	return nil
}

func (r *Registry) Get(id string) (Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[id]
	if !ok {
		return nil, fmt.Errorf("unknown application %q", id)
	}
	return adapter, nil
}

func (r *Registry) List() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Adapter, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		out = append(out, adapter)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest().ID < out[j].Manifest().ID
	})
	return out
}
