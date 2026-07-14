package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	t.Setenv("TEST_KEY", "secret")
	d := t.TempDir()
	p := filepath.Join(d, "config.yaml")
	raw := `version: 1
providers:
  - id: p
    protocol: openai-chat
    base_url: https://example.com/v1
    api_key: ${TEST_KEY}
    models: [m]
default_route:
  targets: [{provider: p, model: m}]
`
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].APIKey != "secret" {
		t.Fatal("environment variable was not expanded")
	}
	storage, err := ProviderSecretStorage(p)
	if err != nil || storage["p"].Mode != "environment" || storage["p"].Reference != "TEST_KEY" {
		t.Fatalf("environment storage metadata is wrong: %#v (%v)", storage, err)
	}
	if c.Server.MaxBodySize != 32<<20 {
		t.Fatalf("unexpected max body size %d", c.Server.MaxBodySize)
	}
	if c.Server.Listen != "127.0.0.1:12666" || c.Server.AdminListen != "127.0.0.1:12667" {
		t.Fatalf("unexpected default listeners: gateway=%q admin=%q", c.Server.Listen, c.Server.AdminListen)
	}
	if c.Logging.RequestHistory != 50 {
		t.Fatalf("unexpected default request history %d", c.Logging.RequestHistory)
	}
}

func TestEmptyOnboardingConfigurationAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "version: 1\nproviders: []\nroutes: []\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) != 0 || len(c.Routes) != 0 || c.DefaultRoute != nil {
		t.Fatalf("unexpected onboarding configuration: %#v", c)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.yaml")
	_ = os.WriteFile(p, []byte("version: 1\nunknown: true\n"), 0600)
	if _, err := Load(p); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLiteralProviderAPIKeyAllowed(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.yaml")
	raw := `version: 1
providers:
  - id: p
    protocol: openai-chat
    base_url: https://example.com
    api_key: literal-secret
    models: [m]
default_route:
  targets: [{provider: p, model: m}]
`
	_ = os.WriteFile(p, []byte(raw), 0600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].APIKey != "literal-secret" {
		t.Fatal("literal provider API key was not loaded")
	}
	storage, err := ProviderSecretStorage(p)
	if err != nil || storage["p"].Mode != "plaintext" || storage["p"].Reference != "" {
		t.Fatalf("plaintext storage metadata is wrong: %#v (%v)", storage, err)
	}
}

func TestEnvironmentValuesCannotInjectYAML(t *testing.T) {
	value := ":\nmalicious: true"
	t.Setenv("SAFE_KEY", value)
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `version: 1
providers:
  - id: p
    protocol: openai-chat
    base_url: https://example.com
    api_key: ${SAFE_KEY}
    models: [m]
default_route:
  targets: [{provider: p, model: m}]
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].APIKey != value {
		t.Fatalf("environment value changed: %q", c.Providers[0].APIKey)
	}
}

func TestRuntimeAppliesPublishedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `version: 1
providers:
  - id: "invalid id with spaces"
    protocol: openai-chat
    base_url: https://example.com
    models: [m]
default_route:
  targets: [{provider: "invalid id with spaces", model: m}]
retry:
  max_attempts: 99
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("runtime accepted a document rejected by the published schema")
	}
}

func TestDuplicateFallbackTargetRejected(t *testing.T) {
	c := &Config{Version: 1, Providers: []Provider{{ID: "p", Protocol: "openai-chat", BaseURL: "https://example.com", Models: []string{"m"}}}, DefaultRoute: &RouteTargetList{Targets: []RouteTarget{{Provider: "p", Model: "m"}, {Provider: "p", Model: "m"}}}}
	if err := c.prepare(); err == nil {
		t.Fatal("duplicate fallback target was accepted")
	}
}

func TestUnreachableDuplicateRouteRejected(t *testing.T) {
	c := &Config{Version: 1, Providers: []Provider{{ID: "p", Protocol: "openai-chat", BaseURL: "https://example.com", Models: []string{"m"}}}, Routes: []Route{
		{ID: "first", Priority: 10, Match: RouteMatch{Model: "m"}, Targets: []RouteTarget{{Provider: "p", Model: "m"}}},
		{ID: "shadowed", Priority: 1, Match: RouteMatch{Model: "m"}, Targets: []RouteTarget{{Provider: "p", Model: "m"}}},
	}}
	if err := c.prepare(); err == nil {
		t.Fatal("unreachable duplicate route was accepted")
	}
}
