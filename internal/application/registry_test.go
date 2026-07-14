package application

import (
	"context"
	"encoding/json"
	"testing"
)

type testAdapter struct{ id string }

func (a testAdapter) Manifest() Manifest {
	return Manifest{ID: a.id, Name: a.id, Description: "test adapter", Status: "available", Capabilities: []Capability{CapabilityDetect}, ConfigFormat: "json"}
}
func (testAdapter) Detect(context.Context) (Detection, error) { return Detection{}, nil }
func (a testAdapter) Read(context.Context) (State, error) {
	return State{Manifest: a.Manifest()}, nil
}
func (testAdapter) Preview(context.Context, json.RawMessage) (Preview, error) {
	return Preview{}, nil
}
func (testAdapter) Apply(context.Context, json.RawMessage) (ApplyResult, error) {
	return ApplyResult{}, nil
}
func (testAdapter) Verify(context.Context, VerifyOptions) (VerifyResult, error) {
	return VerifyResult{}, nil
}
func (testAdapter) Backups(context.Context) ([]Backup, error)  { return nil, nil }
func (testAdapter) DeleteBackup(context.Context, string) error { return nil }
func (testAdapter) Rollback(context.Context, string) (ApplyResult, error) {
	return ApplyResult{}, nil
}

func TestRegistrySupportsAdditionalAdaptersWithoutAPISpecialCases(t *testing.T) {
	r := NewRegistry(testAdapter{id: "one"}, testAdapter{id: "two"})
	if got := len(r.List()); got != 2 {
		t.Fatalf("got %d adapters", got)
	}
	if _, err := r.Get("two"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testAdapter{id: "one"}); err == nil {
		t.Fatal("duplicate adapter accepted")
	}
	invalid := testAdapter{id: "invalid"}
	invalidRegistry := NewRegistry()
	if err := invalidRegistry.Register(adapterWithManifest{testAdapter: invalid, manifest: Manifest{ID: "invalid", Name: "invalid", Status: "available"}}); err == nil {
		t.Fatal("incomplete manifest was accepted")
	}
}

type adapterWithManifest struct {
	testAdapter
	manifest Manifest
}

func (a adapterWithManifest) Manifest() Manifest { return a.manifest }
