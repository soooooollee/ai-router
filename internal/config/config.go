package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/ir"
	configschemas "github.com/zbss/airoute/schemas"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Version      int              `yaml:"version" json:"version"`
	Server       Server           `yaml:"server" json:"server"`
	Admin        Admin            `yaml:"admin" json:"admin"`
	Auth         Auth             `yaml:"auth" json:"auth"`
	Providers    []Provider       `yaml:"providers" json:"providers"`
	Routes       []Route          `yaml:"routes" json:"routes"`
	DefaultRoute *RouteTargetList `yaml:"default_route" json:"default_route,omitempty"`
	Conversion   Conversion       `yaml:"conversion" json:"conversion"`
	Retry        Retry            `yaml:"retry" json:"retry"`
	Fallback     Fallback         `yaml:"fallback" json:"fallback"`
	Logging      Logging          `yaml:"logging" json:"logging"`
	Metrics      Metrics          `yaml:"metrics" json:"metrics"`
	SourcePath   string           `yaml:"-" json:"-"`
	Hash         string           `yaml:"-" json:"hash"`
}

type Server struct {
	Listen            string        `yaml:"listen" json:"listen"`
	AdminListen       string        `yaml:"admin_listen" json:"admin_listen"`
	MaxConcurrent     int           `yaml:"max_concurrent" json:"max_concurrent"`
	MaxHeaders        int           `yaml:"max_headers" json:"max_headers"`
	MaxHeaderBytes    int           `yaml:"max_header_bytes" json:"max_header_bytes"`
	ReadHeaderTimeout time.Duration `yaml:"-" json:"read_header_timeout"`
	RequestTimeout    time.Duration `yaml:"-" json:"request_timeout"`
	MaxBodySize       int64         `yaml:"-" json:"max_body_size"`
	ReadHeaderText    string        `yaml:"read_header_timeout" json:"-"`
	RequestText       string        `yaml:"request_timeout" json:"-"`
	MaxBodyText       string        `yaml:"max_body_size" json:"-"`
}

type Admin struct {
	Enabled      bool     `yaml:"enabled" json:"enabled"`
	Token        string   `yaml:"token" json:"-"`
	AllowedHosts []string `yaml:"allowed_hosts" json:"allowed_hosts,omitempty"`
}
type Auth struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Keys    []APIKey `yaml:"keys" json:"keys"`
}
type APIKey struct {
	ID    string `yaml:"id" json:"id"`
	Value string `yaml:"value" json:"-"`
}

type Provider struct {
	ID              string            `yaml:"id" json:"id"`
	Name            string            `yaml:"name" json:"name"`
	Profile         string            `yaml:"profile" json:"profile,omitempty"`
	ReasoningMode   string            `yaml:"reasoning_mode" json:"reasoning_mode,omitempty"`
	MaxOutputTokens int               `yaml:"max_output_tokens" json:"max_output_tokens,omitempty"`
	Protocol        ir.Protocol       `yaml:"protocol" json:"protocol"`
	BaseURL         string            `yaml:"base_url" json:"base_url"`
	APIKey          string            `yaml:"api_key" json:"-"`
	Models          []string          `yaml:"models" json:"models"`
	DynamicModels   bool              `yaml:"dynamic_models" json:"dynamic_models"`
	Headers         map[string]string `yaml:"headers" json:"-"`
	RequestFields   map[string]any    `yaml:"request_fields" json:"-"`
	TimeoutText     string            `yaml:"timeout" json:"-"`
	Timeout         time.Duration     `yaml:"-" json:"timeout"`
	AllowPrivateURL bool              `yaml:"allow_private_url" json:"allow_private_url"`
}

type Route struct {
	ID       string        `yaml:"id" json:"id"`
	Priority int           `yaml:"priority" json:"priority"`
	Match    RouteMatch    `yaml:"match" json:"match"`
	Targets  []RouteTarget `yaml:"targets" json:"targets"`
}
type RouteMatch struct {
	Model    string            `yaml:"model" json:"model"`
	Protocol ir.Protocol       `yaml:"protocol" json:"protocol,omitempty"`
	Stream   *bool             `yaml:"stream" json:"stream,omitempty"`
	Tools    *bool             `yaml:"tools" json:"tools,omitempty"`
	Image    *bool             `yaml:"image" json:"image,omitempty"`
	Headers  map[string]string `yaml:"headers" json:"headers,omitempty"`
}
type RouteTarget struct {
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model" json:"model"`
}
type RouteTargetList struct {
	Targets []RouteTarget `yaml:"targets" json:"targets"`
}
type Conversion struct {
	UnsupportedFields  string `yaml:"unsupported_fields" json:"unsupported_fields"`
	PreserveExtensions bool   `yaml:"preserve_extensions" json:"preserve_extensions"`
	RemoteImagePolicy  string `yaml:"remote_image_policy" json:"remote_image_policy"`
}
type Retry struct {
	MaxAttempts   int           `yaml:"max_attempts" json:"max_attempts"`
	BaseDelayText string        `yaml:"base_delay" json:"-"`
	MaxDelayText  string        `yaml:"max_delay" json:"-"`
	BaseDelay     time.Duration `yaml:"-" json:"base_delay"`
	MaxDelay      time.Duration `yaml:"-" json:"max_delay"`
	OnStatus      []int         `yaml:"on_status" json:"on_status"`
}
type Fallback struct {
	OnNetworkError *bool    `yaml:"on_network_error" json:"on_network_error,omitempty"`
	OnTimeout      *bool    `yaml:"on_timeout" json:"on_timeout,omitempty"`
	OnStatus       []int    `yaml:"on_status" json:"on_status,omitempty"`
	OnErrorCodes   []string `yaml:"on_error_codes" json:"on_error_codes,omitempty"`
}
type Logging struct {
	Level          string `yaml:"level" json:"level"`
	Format         string `yaml:"format" json:"format"`
	RequestHistory int    `yaml:"request_history" json:"request_history"`
	CaptureBodies  bool   `yaml:"capture_bodies" json:"capture_bodies"`
	Persist        bool   `yaml:"persist" json:"persist"`
	WebRedaction   bool   `yaml:"web_redaction" json:"web_redaction"`
	File           string `yaml:"file" json:"file,omitempty"`
}
type Metrics struct {
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	Path        string `yaml:"path" json:"path"`
	ModelLabels bool   `yaml:"model_labels" json:"model_labels"`
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)
var secretRefPattern = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

func SecretStorage(value string) (mode, reference string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "missing", ""
	}
	if secretRefPattern.MatchString(value) {
		return "environment", strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	}
	return "plaintext", ""
}

func ResolveSecretInput(value string) (string, error) {
	mode, reference := SecretStorage(value)
	if mode != "environment" {
		return value, nil
	}
	resolved, ok := os.LookupEnv(reference)
	if !ok || resolved == "" {
		return "", fmt.Errorf("missing environment variable: %s", reference)
	}
	return resolved, nil
}

type SecretInfo struct {
	Mode      string
	Reference string
}

func ProviderSecretStorage(path string) (map[string]SecretInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document struct {
		Providers []struct {
			ID     string `yaml:"id"`
			APIKey string `yaml:"api_key"`
		} `yaml:"providers"`
	}
	if err = yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	out := make(map[string]SecretInfo, len(document.Providers))
	for _, provider := range document.Providers {
		mode, reference := SecretStorage(provider.APIKey)
		out[provider.ID] = SecretInfo{Mode: mode, Reference: reference}
	}
	return out, nil
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = validateSecretReferences(raw); err != nil {
		return nil, err
	}
	if err = validatePublishedSchema(raw); err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := interpolateValue(reflect.ValueOf(&c)); err != nil {
		return nil, err
	}
	c.SourcePath = path
	h := sha256.Sum256(raw)
	c.Hash = hex.EncodeToString(h[:8])
	if err := c.prepare(); err != nil {
		return nil, err
	}
	return &c, nil
}

var (
	schemaOnce sync.Once
	schemaV1   *jsonschema.Schema
	schemaErr  error
)

func validatePublishedSchema(raw []byte) error {
	schemaOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		compiler.Draft = jsonschema.Draft2020
		if err := compiler.AddResource("config.v1.schema.json", io.NopCloser(bytes.NewReader(configschemas.ConfigV1))); err != nil {
			schemaErr = err
			return
		}
		schemaV1, schemaErr = compiler.Compile("config.v1.schema.json")
	})
	if schemaErr != nil {
		return fmt.Errorf("compile config schema: %w", schemaErr)
	}
	var yamlValue any
	if err := yaml.Unmarshal(raw, &yamlValue); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	encoded, err := json.Marshal(yamlValue)
	if err != nil {
		return fmt.Errorf("encode config for schema: %w", err)
	}
	var document any
	if err = json.Unmarshal(encoded, &document); err != nil {
		return fmt.Errorf("decode config for schema: %w", err)
	}
	if err = schemaV1.Validate(document); err != nil {
		return fmt.Errorf("config schema: %w", err)
	}
	return nil
}

func interpolateValue(v reflect.Value) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return interpolateValue(v.Elem())
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := interpolateValue(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if v.CanSet() {
			expanded, err := expandEnv(v.String())
			if err != nil {
				return err
			}
			v.SetString(expanded)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if err := interpolateValue(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(iter.Value())
			if err := interpolateValue(value); err != nil {
				return err
			}
			v.SetMapIndex(iter.Key(), value)
		}
	}
	return nil
}

func validateSecretReferences(raw []byte) error {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	check := func(path, value string) error {
		if value != "" && !secretRefPattern.MatchString(strings.TrimSpace(value)) {
			return fmt.Errorf("%s must use an environment variable reference", path)
		}
		return nil
	}
	if err := check("admin.token", c.Admin.Token); err != nil {
		return err
	}
	for i, k := range c.Auth.Keys {
		if err := check(fmt.Sprintf("auth.keys[%d].value", i), k.Value); err != nil {
			return err
		}
	}
	for i, p := range c.Providers {
		for name, value := range p.Headers {
			n := strings.ToLower(name)
			if strings.Contains(n, "authorization") || strings.Contains(n, "key") || strings.Contains(n, "token") || strings.Contains(n, "cookie") || strings.Contains(n, "secret") {
				if err := check(fmt.Sprintf("providers[%d].headers.%s", i, name), value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func expandEnv(s string) (string, error) {
	var missing []string
	out := envPattern.ReplaceAllStringFunc(s, func(m string) string {
		parts := envPattern.FindStringSubmatch(m)
		if v, ok := os.LookupEnv(parts[1]); ok {
			return v
		}
		if parts[2] != "" {
			return parts[2]
		}
		missing = append(missing, parts[1])
		return ""
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func (c *Config) prepare() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Server.Listen == "" {
		c.Server.Listen = "127.0.0.1:12666"
	}
	if c.Server.AdminListen == "" {
		c.Server.AdminListen = "127.0.0.1:12667"
	}
	if c.Server.MaxConcurrent == 0 {
		c.Server.MaxConcurrent = 256
	}
	if c.Server.MaxHeaderBytes == 0 {
		c.Server.MaxHeaderBytes = 1 << 20
	}
	if c.Server.MaxHeaders == 0 {
		c.Server.MaxHeaders = 100
	}
	if c.Server.MaxConcurrent < 1 {
		return errors.New("server.max_concurrent must be positive")
	}
	if c.Server.MaxHeaders < 1 {
		return errors.New("server.max_headers must be positive")
	}
	var err error
	if c.Server.ReadHeaderTimeout, err = duration(c.Server.ReadHeaderText, 10*time.Second); err != nil {
		return fmt.Errorf("server.read_header_timeout: %w", err)
	}
	if c.Server.RequestTimeout, err = duration(c.Server.RequestText, 10*time.Minute); err != nil {
		return fmt.Errorf("server.request_timeout: %w", err)
	}
	if c.Server.MaxBodySize, err = bytesize(c.Server.MaxBodyText, 32<<20); err != nil {
		return fmt.Errorf("server.max_body_size: %w", err)
	}
	if c.Retry.MaxAttempts == 0 {
		c.Retry.MaxAttempts = 2
	}
	if c.Retry.BaseDelay, err = duration(c.Retry.BaseDelayText, 500*time.Millisecond); err != nil {
		return err
	}
	if c.Retry.MaxDelay, err = duration(c.Retry.MaxDelayText, 5*time.Second); err != nil {
		return err
	}
	if len(c.Retry.OnStatus) == 0 {
		c.Retry.OnStatus = []int{429, 500, 502, 503, 504}
	}
	if c.Fallback.OnNetworkError == nil {
		v := true
		c.Fallback.OnNetworkError = &v
	}
	if c.Fallback.OnTimeout == nil {
		v := true
		c.Fallback.OnTimeout = &v
	}
	if len(c.Fallback.OnStatus) == 0 {
		c.Fallback.OnStatus = []int{429, 500, 502, 503, 504}
	}
	if c.Conversion.UnsupportedFields == "" {
		c.Conversion.UnsupportedFields = "warn"
	}
	if c.Conversion.RemoteImagePolicy == "" {
		c.Conversion.RemoteImagePolicy = "pass-through"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Logging.RequestHistory == 0 {
		c.Logging.RequestHistory = 50
	}
	if c.Metrics.Path == "" {
		c.Metrics.Path = "/metrics"
	}
	return c.Validate()
}

func (c *Config) Validate() error {
	var errs []error
	if _, _, err := net.SplitHostPort(c.Server.Listen); err != nil {
		errs = append(errs, fmt.Errorf("server.listen: %w", err))
	}
	adminHost, _, err := net.SplitHostPort(c.Server.AdminListen)
	if err != nil {
		errs = append(errs, fmt.Errorf("server.admin_listen: %w", err))
	}
	if c.Admin.Enabled && c.Server.Listen == c.Server.AdminListen {
		errs = append(errs, errors.New("server.listen and server.admin_listen must be different"))
	}
	if c.Admin.Enabled && !isLoopback(adminHost) && len(c.Admin.Token) < 24 {
		errs = append(errs, errors.New("admin.token must contain at least 24 characters when admin listens beyond loopback"))
	}
	if c.Conversion.UnsupportedFields != "strict" && c.Conversion.UnsupportedFields != "warn" && c.Conversion.UnsupportedFields != "drop" {
		errs = append(errs, errors.New("conversion.unsupported_fields must be strict, warn, or drop"))
	}
	if c.Conversion.RemoteImagePolicy != "pass-through" && c.Conversion.RemoteImagePolicy != "reject" {
		errs = append(errs, errors.New("conversion.remote_image_policy must be pass-through or reject"))
	}
	keyIDs := map[string]bool{}
	keyValues := map[string]bool{}
	for i, k := range c.Auth.Keys {
		if k.ID == "" || k.Value == "" {
			errs = append(errs, fmt.Errorf("auth.keys[%d] requires id and value", i))
		}
		if keyIDs[k.ID] {
			errs = append(errs, fmt.Errorf("duplicate auth key id %q", k.ID))
		}
		if k.Value != "" && keyValues[k.Value] {
			errs = append(errs, fmt.Errorf("auth.keys[%d] duplicates another key value", i))
		}
		keyIDs[k.ID] = true
		keyValues[k.Value] = true
	}
	providers := map[string]Provider{}
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.ID == "" {
			errs = append(errs, fmt.Errorf("providers[%d].id is required", i))
			continue
		}
		if p.Profile != "" && p.Profile != "generic" && p.Profile != "qwen3" && p.Profile != "xiaomi-mimo" {
			errs = append(errs, fmt.Errorf("providers[%d].profile must be generic, qwen3, or xiaomi-mimo", i))
		}
		if p.ReasoningMode != "" && p.ReasoningMode != "auto" && p.ReasoningMode != "enabled" && p.ReasoningMode != "disabled" {
			errs = append(errs, fmt.Errorf("providers[%d].reasoning_mode must be auto, enabled, or disabled", i))
		}
		if p.MaxOutputTokens < 0 {
			errs = append(errs, fmt.Errorf("providers[%d].max_output_tokens must be positive", i))
		}
		if _, ok := providers[p.ID]; ok {
			errs = append(errs, fmt.Errorf("duplicate provider id %q", p.ID))
		}
		if !protocol.Supported(p.Protocol) {
			errs = append(errs, fmt.Errorf("provider %q uses unsupported protocol %q", p.ID, p.Protocol))
		}
		if len(p.Models) == 0 && !p.DynamicModels {
			errs = append(errs, fmt.Errorf("provider %q requires models or dynamic_models", p.ID))
		}
		u, e := url.Parse(p.BaseURL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("provider %q has invalid base_url", p.ID))
		}
		p.Timeout, e = duration(p.TimeoutText, c.Server.RequestTimeout)
		if e != nil {
			errs = append(errs, fmt.Errorf("provider %q timeout: %w", p.ID, e))
		}
		providers[p.ID] = *p
	}
	routes := map[string]bool{}
	matchOwners := map[string]string{}
	validateTargets := func(owner string, targets []RouteTarget) {
		if len(targets) == 0 {
			errs = append(errs, fmt.Errorf("%s requires at least one target", owner))
		}
		seenTargets := map[string]bool{}
		for _, t := range targets {
			key := t.Provider + "\x00" + t.Model
			if seenTargets[key] {
				errs = append(errs, fmt.Errorf("%s contains duplicate target %q/%q", owner, t.Provider, t.Model))
			}
			seenTargets[key] = true
			p, ok := providers[t.Provider]
			if !ok {
				errs = append(errs, fmt.Errorf("%s references unknown provider %q", owner, t.Provider))
			}
			if t.Model == "" {
				errs = append(errs, fmt.Errorf("%s target model is required", owner))
			} else if ok && !p.DynamicModels && !contains(p.Models, t.Model) {
				errs = append(errs, fmt.Errorf("%s references undeclared model %q on provider %q", owner, t.Model, t.Provider))
			}
		}
	}
	for _, r := range c.Routes {
		if r.ID == "" {
			errs = append(errs, errors.New("route id is required"))
		} else if routes[r.ID] {
			errs = append(errs, fmt.Errorf("duplicate route id %q", r.ID))
		}
		routes[r.ID] = true
		matchJSON, _ := json.Marshal(r.Match)
		matchKey := string(matchJSON)
		if owner, exists := matchOwners[matchKey]; exists {
			errs = append(errs, fmt.Errorf("route %q is unreachable because it duplicates match conditions from route %q", r.ID, owner))
		} else {
			matchOwners[matchKey] = r.ID
		}
		if r.Match.Protocol != "" && !protocol.Supported(r.Match.Protocol) {
			errs = append(errs, fmt.Errorf("route %q uses unsupported protocol %q", r.ID, r.Match.Protocol))
		}
		validateTargets("route "+r.ID, r.Targets)
	}
	if c.DefaultRoute != nil {
		validateTargets("default_route", c.DefaultRoute.Targets)
	}
	return errors.Join(errs...)
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func duration(v string, d time.Duration) (time.Duration, error) {
	if v == "" {
		return d, nil
	}
	return time.ParseDuration(v)
}
func bytesize(v string, d int64) (int64, error) {
	if v == "" {
		return d, nil
	}
	units := map[string]int64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30}
	for u, m := range units {
		if strings.HasSuffix(v, u) {
			n, e := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(v, u)), 10, 64)
			return n * m, e
		}
	}
	return strconv.ParseInt(v, 10, 64)
}
func isLoopback(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

type loadError struct{ Message string }
type Store struct {
	value     atomic.Pointer[Config]
	lastError atomic.Pointer[loadError]
	loadOK    atomic.Uint64
	loadFail  atomic.Uint64
}

func NewStore(c *Config) *Store    { s := &Store{}; s.value.Store(c); s.loadOK.Store(1); return s }
func (s *Store) Get() *Config      { return s.value.Load() }
func (s *Store) Replace(c *Config) { s.value.Store(c); s.lastError.Store(nil); s.loadOK.Add(1) }
func (s *Store) SetError(err error) {
	if err == nil {
		s.lastError.Store(nil)
		return
	}
	s.lastError.Store(&loadError{Message: err.Error()})
	s.loadFail.Add(1)
}
func (s *Store) LoadCounts() (uint64, uint64) { return s.loadOK.Load(), s.loadFail.Load() }
func (s *Store) LastError() string {
	if e := s.lastError.Load(); e != nil {
		return e.Message
	}
	return ""
}
