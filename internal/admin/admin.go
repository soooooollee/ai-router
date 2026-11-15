package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zbss/airoute/internal/application"
	"github.com/zbss/airoute/internal/application/claudeapp"
	"github.com/zbss/airoute/internal/application/claudecode"
	"github.com/zbss/airoute/internal/application/codex"
	"github.com/zbss/airoute/internal/application/mimocode"
	"github.com/zbss/airoute/internal/application/pi"
	"github.com/zbss/airoute/internal/auth"
	"github.com/zbss/airoute/internal/clientauth"
	"github.com/zbss/airoute/internal/clientstore"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/safefile"
	"github.com/zbss/airoute/internal/secure"
)

type Server struct {
	Config            *config.Store
	Registry          *protocol.Registry
	Logs              *observe.Store
	Metrics           *observe.Metrics
	Started           time.Time
	Version           string
	GatewayURL        string
	ReleaseURL        string
	Client            *http.Client
	RestrictedClient  *http.Client
	Applications      *application.Registry
	ClientAccess      *clientauth.Manager
	ClientStore       clientstore.Store
	CredentialKeys    *clientauth.KeyRing
	StateDirectory    string
	failureMu         sync.Mutex
	failures          map[string][]time.Time
	healthMu          sync.RWMutex
	health            map[string]map[string]any
	updateMu          sync.Mutex
	updateCached      UpdateInfo
	updateCachedUntil time.Time
	probeMu           sync.Mutex
	probeCache        map[string]providerProbeCacheEntry
	probeInFlight     map[string]*providerProbeCall
	GatewayControl    interface {
		SetEnabled(bool)
		IsEnabled() bool
		ApplyRuntimeConfig(*config.Config, *config.Config)
	}
}

func New(c *config.Store, r *protocol.Registry, l *observe.Store, m *observe.Metrics, version, gatewayURL string, configPaths ...string) *Server {
	restrictedTransport := &http.Transport{Proxy: nil, DialContext: secure.PublicDialContext, ResponseHeaderTimeout: 30 * time.Second}
	releaseURL := os.Getenv("AIROUTE_RELEASE_API_URL")
	if releaseURL == "" {
		releaseURL = releaseAPIURL()
	}
	codexAdapter := codex.New()
	if len(configPaths) > 0 {
		codexAdapter.RouterConfigPath = configPaths[0]
	}
	return &Server{Config: c, Registry: r, Logs: l, Metrics: m, Started: time.Now(), Version: version, GatewayURL: gatewayURL, ReleaseURL: releaseURL, Client: &http.Client{Timeout: 30 * time.Second}, RestrictedClient: &http.Client{Transport: restrictedTransport, Timeout: 30 * time.Second}, Applications: application.NewRegistry(claudeapp.New(), claudecode.New(), codexAdapter, mimocode.New(), pi.New()), failures: map[string][]time.Time{}, health: map[string]map[string]any{}, probeCache: map[string]providerProbeCacheEntry{}, probeInFlight: map[string]*providerProbeCall{}}
}

func (s *Server) CloseIdleConnections() {
	s.Client.CloseIdleConnections()
	s.RestrictedClient.CloseIdleConnections()
}

func (s *Server) SetGatewayControl(control interface {
	SetEnabled(bool)
	IsEnabled() bool
	ApplyRuntimeConfig(*config.Config, *config.Config)
}) {
	s.GatewayControl = control
}

func (s *Server) SetClientManagement(access *clientauth.Manager, store clientstore.Store, keys *clientauth.KeyRing, stateDirectory string) {
	s.ClientAccess = access
	s.ClientStore = store
	s.CredentialKeys = keys
	s.StateDirectory = stateDirectory
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("x-airoute-request-id", adminRequestID())
	if s.ClientStore != nil && !loopbackRemote(r.RemoteAddr) {
		http.Error(w, "admin console is available from loopback only", http.StatusForbidden)
		return
	}
	if !allowedHost(r, s.Config.Get()) {
		http.Error(w, "forbidden host", 403)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !s.loginAllowed(r.RemoteAddr) {
			jsonOut(w, 429, map[string]any{"error": "too many failed authentication attempts"})
			return
		}
		if !allowedOrigin(r) || !auth.AdminOK(r, s.Config.Get()) {
			s.recordFailure(r.RemoteAddr)
			jsonOut(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		s.clearFailures(r.RemoteAddr)
		s.api(w, r)
		return
	}
	s.static(w, r)
}

func adminRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "adm_" + hex.EncodeToString(b)
}

func (s *Server) loginAllowed(addr string) bool {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	key := remoteHost(addr)
	cut := time.Now().Add(-time.Minute)
	recent := s.failures[key][:0]
	for _, at := range s.failures[key] {
		if at.After(cut) {
			recent = append(recent, at)
		}
	}
	s.failures[key] = recent
	return len(recent) < 10
}

func (s *Server) recordFailure(addr string) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	key := remoteHost(addr)
	s.failures[key] = append(s.failures[key], time.Now())
}

func (s *Server) clearFailures(addr string) {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()
	delete(s.failures, remoteHost(addr))
}

func remoteHost(addr string) string {
	h, _, e := net.SplitHostPort(addr)
	if e == nil {
		return h
	}
	return addr
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/status" && r.Method == "GET":
		c := s.Config.Get()
		status := "running"
		if s.GatewayControl != nil && !s.GatewayControl.IsEnabled() {
			status = "stopped"
		}
		configuredApps, totalApps := applicationConfigurationCounts(r.Context(), s.Applications)
		jsonOut(w, 200, map[string]any{"status": status, "runtime_state_persistent": false, "version": s.Version, "uptime_seconds": int(time.Since(s.Started).Seconds()), "config_version": c.Hash, "config_error": s.Config.LastError(), "gateway_url": s.GatewayURL, "providers": len(c.Providers), "models": configuredModelCount(c), "routes": len(c.Routes), "applications_configured": configuredApps, "applications_total": totalApps, "logs": len(s.Logs.List(0)), "logs_capacity": c.Logging.RequestHistory, "logging_persist": c.Logging.Persist, "logging_capture_bodies": c.Logging.CaptureBodies, "provider_health": s.providerHealth(), "metrics": summary(s.Metrics, s.Logs)})
	case r.URL.Path == "/api/update" && r.Method == "GET":
		jsonOut(w, 200, s.checkUpdate(r.Context()))
	case r.URL.Path == "/api/runtime" && r.Method == "PUT":
		if s.GatewayControl == nil {
			jsonOut(w, 501, map[string]any{"error": "gateway runtime control is unavailable"})
			return
		}
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in) != nil || in.Enabled == nil {
			jsonOut(w, 400, map[string]any{"error": "enabled must be a boolean"})
			return
		}
		s.GatewayControl.SetEnabled(*in.Enabled)
		jsonOut(w, 200, map[string]any{"ok": true, "status": map[bool]string{true: "running", false: "stopped"}[*in.Enabled], "persistent": false, "message": "运行状态只对当前进程有效，进程重启后恢复运行"})
	case r.URL.Path == "/api/config" && r.Method == "GET":
		current := s.Config.Get()
		info, statErr := os.Stat(current.SourcePath)
		if statErr != nil {
			apiError(w, 500, statErr)
			return
		}
		if !safefile.PermissionsPrivate(info) {
			jsonOut(w, http.StatusForbidden, map[string]any{"error": "configuration file permissions are too broad; require 0600"})
			return
		}
		raw, e := os.ReadFile(current.SourcePath)
		if e != nil {
			apiError(w, 500, e)
			return
		}
		// Secrets are always redacted here: the console only needs to render and
		// round-trip the document, and a masked value is restored from disk on
		// save. logging.web_redaction additionally redacts request bodies.
		yamlText := redactConfigYAML(string(raw))
		jsonOut(w, 200, map[string]any{"yaml": yamlText, "hash": current.Hash, "config": current})
	case r.URL.Path == "/api/config/validate" && r.Method == "POST":
		s.validate(w, r, false)
	case r.URL.Path == "/api/config" && r.Method == "PUT":
		s.validate(w, r, true)
	case r.URL.Path == "/api/config/reload" && r.Method == "POST":
		c, e := config.Load(s.Config.Get().SourcePath)
		if e != nil {
			s.Config.SetError(e)
			apiError(w, 422, e)
			return
		}
		s.Config.Replace(c)
		jsonOut(w, 200, map[string]any{"ok": true, "hash": c.Hash})
	case r.URL.Path == "/api/config/backups" && r.Method == "GET":
		s.backups(w)
	case r.URL.Path == "/api/config/rollback" && r.Method == "POST":
		s.rollback(w, r)
	case r.URL.Path == "/api/providers" && r.Method == "GET":
		current := s.Config.Get()
		jsonOut(w, 200, map[string]any{"providers": publicProviders(current, s.providerHealth(), true)})
	case r.URL.Path == "/api/providers/detect" && r.Method == "POST":
		s.detectProvider(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/providers/") && strings.HasSuffix(r.URL.Path, "/probe") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/probe")
		s.probe(w, r, id)
	case r.URL.Path == "/api/routes/explain" && (r.Method == "GET" || r.Method == "POST"):
		s.explain(w, r)
	case r.URL.Path == "/api/clients" || r.URL.Path == "/api/client-audit" || r.URL.Path == "/api/clients/migrate-legacy" || strings.HasPrefix(r.URL.Path, "/api/clients/") || strings.HasPrefix(r.URL.Path, "/api/credentials/") || strings.HasPrefix(r.URL.Path, "/api/client-state/"):
		s.clientManagementAPI(w, r)
	case r.URL.Path == "/api/apps" || strings.HasPrefix(r.URL.Path, "/api/apps/"):
		s.applicationAPI(w, r)
	case r.URL.Path == "/api/claude-code/config" && r.Method == "GET":
		s.legacyClaudeCodeConfig(w, r)
	case r.URL.Path == "/api/claude-code/config" && r.Method == "PUT":
		s.legacyClaudeCodeConfig(w, r)
	case r.URL.Path == "/api/playground/preview" && r.Method == "POST":
		s.preview(w, r)
	case r.URL.Path == "/api/logs" && r.Method == "GET":
		limit := 100
		fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
		logs := s.Logs.List(limit)
		for i := range logs {
			logs[i].RequestBody = ""
			logs[i].ResponseBody = ""
		}
		jsonOut(w, 200, map[string]any{"logs": logs})
	case strings.HasPrefix(r.URL.Path, "/api/logs/") && r.Method == "GET":
		id := strings.TrimPrefix(r.URL.Path, "/api/logs/")
		v, ok := s.Logs.Get(id)
		if !ok {
			jsonOut(w, 404, map[string]any{"error": "not found"})
			return
		}
		if s.Config.Get().Logging.WebRedaction {
			v.RequestBody = redactLogBody(v.RequestBody)
			v.ResponseBody = redactLogBody(v.ResponseBody)
		}
		jsonOut(w, 200, v)
	case r.URL.Path == "/api/metrics/summary" && r.Method == "GET":
		jsonOut(w, 200, summary(s.Metrics, s.Logs))
	case r.URL.Path == "/api/diagnostics" && r.Method == "GET":
		s.diagnostics(w)
	case r.URL.Path == "/api/playground/request" && r.Method == "POST":
		s.playground(w, r)
	default:
		jsonOut(w, 404, map[string]any{"error": "not found"})
	}
}

func allowedHost(r *http.Request, c *config.Config) bool {
	h := r.Host
	if x, _, e := net.SplitHostPort(h); e == nil {
		h = x
	}
	if h == "localhost" || net.ParseIP(strings.Trim(h, "[]")) != nil {
		return true
	}
	for _, allowed := range c.Admin.AllowedHosts {
		if strings.EqualFold(h, allowed) || strings.EqualFold(r.Host, allowed) {
			return true
		}
	}
	return false
}

func allowedOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, e := url.Parse(o)
	return e == nil && strings.EqualFold(u.Host, r.Host)
}

func apiError(w http.ResponseWriter, status int, e error) {
	jsonOut(w, status, map[string]any{"error": e.Error()})
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
