package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/zbss/airoute/internal/admin"
	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/gateway"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
	"github.com/zbss/airoute/internal/protocol/anthropic"
	"github.com/zbss/airoute/internal/protocol/gemini"
	"github.com/zbss/airoute/internal/protocol/ir"
	"github.com/zbss/airoute/internal/protocol/openaichat"
	"github.com/zbss/airoute/internal/protocol/openairesponses"
	"github.com/zbss/airoute/internal/routing"
	"github.com/zbss/airoute/internal/secure"
)

var version = "dev"
var commit = "none"
var builtAt = "unknown"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "air:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "start", "serve":
		return serve(args)
	case "init":
		return initConfig(args)
	case "check":
		return check(args)
	case "convert":
		return convert(args)
	case "doctor":
		return doctor(args)
	case "models":
		return models(args)
	case "routes":
		return routes(args)
	case "probe":
		return probe(args)
	case "status":
		return status(args)
	case "ui":
		return ui(args)
	case "version", "--version", "-v":
		fmt.Printf("air %s commit=%s built=%s go=%s\n", version, commit, builtAt, runtime.Version())
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}
func usage() {
	fmt.Print(`AI Router — compact AI protocol conversion gateway

Usage:
  air init     [--config airoute.yaml]
  air start    [--config airoute.yaml]
  air check    [--config airoute.yaml] [--json]
  air doctor   [--config airoute.yaml] [--json]
  air status   [--url http://127.0.0.1:12667] [--token TOKEN]
  air ui       [--url http://127.0.0.1:12667]
  air models   [--config airoute.yaml] [--json]
  air routes   [--config airoute.yaml] [--json]
  air probe    [--config airoute.yaml] --provider ID [--json]
  air convert  --from openai-chat --to anthropic-messages [file]
  air version
`)
}

const minimalConfig = `version: 1

server:
  listen: 127.0.0.1:12666
  admin_listen: 127.0.0.1:12667
  max_concurrent: 256
  max_headers: 100
  max_header_bytes: 1048576

admin:
  enabled: true

auth:
  enabled: false

providers: []
routes: []

conversion:
  unsupported_fields: warn

logging:
  level: info
  request_history: 50

metrics:
  enabled: true
  path: /metrics
`

func initConfig(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	path := fs.String("config", "airoute.yaml", "configuration file")
	force := fs.Bool("force", false, "replace an existing configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if *force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	file, err := os.OpenFile(*path, flags, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("configuration already exists: %s (use --force to replace it)", *path)
		}
		return fmt.Errorf("create configuration: %w", err)
	}
	if _, err = io.WriteString(file, minimalConfig); err != nil {
		_ = file.Close()
		return fmt.Errorf("write configuration: %w", err)
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync configuration: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close configuration: %w", err)
	}
	fmt.Printf("Created %s\n", *path)
	fmt.Println("Run 'air start' and open http://127.0.0.1:12667")
	return nil
}
func registry() *protocol.Registry {
	return protocol.NewRegistry(openaichat.New(), openairesponses.New(), anthropic.New(), gemini.New())
}
func configFlag(name string, args []string) (*config.Config, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	path := fs.String("config", "airoute.yaml", "configuration file")
	jsonMode := fs.Bool("json", false, "JSON output")
	_ = jsonMode
	if err := fs.Parse(args); err != nil {
		return nil, fs, err
	}
	c, err := config.Load(*path)
	return c, fs, err
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	path := fs.String("config", "airoute.yaml", "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err = os.Chmod(*path, 0600); err != nil {
		return fmt.Errorf("secure configuration permissions: %w", err)
	}
	store := config.NewStore(c)
	logs := observe.NewStore(c.Logging.RequestHistory)
	logs.SetFile(c.Logging.File)
	metrics := &observe.Metrics{}
	logger, levelController := newLogger(c)
	reg := registry()
	gw := gateway.New(store, reg, logs, metrics, logger)
	gw.SetLogLevelController(levelController)
	gatewayURL := "http://" + externalHost(c.Server.Listen)
	adm := admin.New(store, reg, logs, metrics, version, gatewayURL)
	adm.SetGatewayControl(gw)
	gatewayServer := &http.Server{Addr: c.Server.Listen, Handler: gw, ReadHeaderTimeout: c.Server.ReadHeaderTimeout, IdleTimeout: 2 * time.Minute}
	gatewayServer.MaxHeaderBytes = c.Server.MaxHeaderBytes
	adminServer := &http.Server{Addr: c.Server.AdminListen, Handler: adm, ReadHeaderTimeout: c.Server.ReadHeaderTimeout, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: c.Server.MaxHeaderBytes}
	errCh := make(chan error, 2)
	go func() {
		logger.Info("gateway listening", "address", c.Server.Listen)
		e := gatewayServer.ListenAndServe()
		if !errors.Is(e, http.ErrServerClosed) {
			errCh <- e
		}
	}()
	if c.Admin.Enabled {
		go func() {
			logger.Info("admin listening", "address", c.Server.AdminListen)
			e := adminServer.ListenAndServe()
			if !errors.Is(e, http.ErrServerClosed) {
				errCh <- e
			}
		}()
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	go watchConfig(watchCtx, *path, store, logger, gw.ApplyRuntimeConfig)
	for {
		select {
		case e := <-errCh:
			return e
		case s := <-sig:
			if s == syscall.SIGHUP {
				previous := store.Get()
				next, e := config.Load(*path)
				if e != nil {
					store.SetError(e)
					logger.Error("config reload failed", "error", e)
				} else {
					gw.ApplyRuntimeConfig(previous, next)
					store.Replace(next)
					logger.Info("configuration reloaded", "hash", next.Hash)
				}
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = gatewayServer.Shutdown(ctx)
			_ = adminServer.Shutdown(ctx)
			gw.CloseIdleConnections()
			adm.CloseIdleConnections()
			return nil
		}
	}
}
func watchConfig(ctx context.Context, path string, store *config.Store, logger *slog.Logger, apply ...func(*config.Config, *config.Config)) {
	watchConfigEvery(ctx, path, store, logger, 2*time.Second, apply...)
}
func watchConfigEvery(ctx context.Context, path string, store *config.Store, logger *slog.Logger, interval time.Duration, apply ...func(*config.Config, *config.Config)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	last := store.Get().Hash
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c, e := config.Load(path)
			if e != nil {
				store.SetError(e)
				continue
			}
			if c.Hash != last {
				previous := store.Get()
				for _, applyRuntime := range apply {
					if applyRuntime != nil {
						applyRuntime(previous, c)
					}
				}
				store.Replace(c)
				last = c.Hash
				logger.Info("configuration hot reloaded", "hash", c.Hash)
			}
		}
	}
}

func check(args []string) error {
	c, fs, err := configFlag("check", args)
	if err != nil {
		return err
	}
	if boolFlag(fs, "json") {
		return printJSON(map[string]any{"valid": true, "config_version": c.Hash, "providers": len(c.Providers), "routes": len(c.Routes)})
	}
	fmt.Printf("valid configuration %s (%d providers, %d routes)\n", c.Hash, len(c.Providers), len(c.Routes))
	return nil
}
func convert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	from := fs.String("from", "", "source protocol")
	to := fs.String("to", "", "target protocol")
	kind := fs.String("kind", "request", "request or response")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind != "request" && *kind != "response" {
		return fmt.Errorf("kind must be request or response")
	}
	fp, tp := ir.Protocol(*from), ir.Protocol(*to)
	reg := registry()
	fa, e := reg.Get(fp)
	if e != nil {
		return e
	}
	ta, e := reg.Get(tp)
	if e != nil {
		return e
	}
	var rd io.Reader = os.Stdin
	if fs.NArg() > 0 {
		f, e := os.Open(fs.Arg(0))
		if e != nil {
			return e
		}
		defer f.Close()
		rd = f
	}
	raw, e := io.ReadAll(io.LimitReader(rd, 32<<20))
	if e != nil {
		return e
	}
	ctx := context.Background()
	var out []byte
	var d []ir.Diagnostic
	if *kind == "request" {
		v, dx, e := fa.DecodeRequest(ctx, raw)
		if e != nil {
			return e
		}
		out, d, e = ta.EncodeRequest(ctx, v)
		d = append(dx, d...)
		if e != nil {
			return e
		}
	} else {
		v, dx, e := fa.DecodeResponse(ctx, raw)
		if e != nil {
			return e
		}
		out, d, e = ta.EncodeResponse(ctx, v)
		d = append(dx, d...)
		if e != nil {
			return e
		}
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") != nil {
		fmt.Println(string(out))
	} else {
		fmt.Println(pretty.String())
	}
	if len(d) > 0 {
		b, _ := json.Marshal(d)
		fmt.Fprintln(os.Stderr, string(b))
	}
	return nil
}

func doctor(args []string) error {
	c, fs, err := configFlag("doctor", args)
	result := map[string]any{"configuration": err == nil, "go": runtime.Version()}
	if err == nil {
		listener, gerr := net.Listen("tcp", c.Server.Listen)
		if gerr == nil {
			result["gateway_port"] = "available"
			_ = listener.Close()
		} else {
			result["gateway_port"] = "in use"
		}
		result["providers"] = len(c.Providers)
	} else {
		result["error"] = err.Error()
	}
	if boolFlag(fs, "json") {
		return printJSON(result)
	}
	for k, v := range result {
		fmt.Printf("%-18s %v\n", k, v)
	}
	return err
}
func models(args []string) error {
	c, fs, err := configFlag("models", args)
	if err != nil {
		return err
	}
	data := map[string][]string{}
	for _, p := range c.Providers {
		data[p.ID] = p.Models
	}
	if boolFlag(fs, "json") {
		return printJSON(data)
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s\t%s\n", k, strings.Join(data[k], ", "))
	}
	return nil
}
func routes(args []string) error {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	path := fs.String("config", "airoute.yaml", "configuration file")
	jsonMode := fs.Bool("json", false, "JSON output")
	model := fs.String("model", "", "explain a model route")
	protocolName := fs.String("protocol", string(ir.OpenAIChat), "source protocol")
	tools := fs.Bool("tools", false, "request contains tools")
	stream := fs.Bool("stream", false, "streaming request")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *model != "" {
		req := &ir.Request{Model: *model, Stream: *stream}
		if *tools {
			req.Tools = []ir.Tool{{Name: "tool"}}
		}
		decision, e := routing.Resolve(c, routing.Input{Request: req, Protocol: ir.Protocol(*protocolName), Headers: map[string]string{}})
		if e != nil {
			return e
		}
		if *jsonMode {
			return printJSON(decision)
		}
		fmt.Printf("route=%s\n", decision.RouteID)
		for i, t := range decision.Targets {
			fmt.Printf("%d\t%s\t%s\n", i+1, t.Provider.ID, t.Model)
		}
		for _, line := range decision.Explanation {
			fmt.Println("-", line)
		}
		return nil
	}
	if *jsonMode {
		return printJSON(map[string]any{"routes": c.Routes, "default": c.DefaultRoute})
	}
	for _, r := range c.Routes {
		fmt.Printf("%d\t%s\t%s\n", r.Priority, r.ID, r.Match.Model)
	}
	if c.DefaultRoute != nil {
		fmt.Println("default\t", c.DefaultRoute.Targets)
	}
	return nil
}
func probe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	path := fs.String("config", "airoute.yaml", "configuration file")
	id := fs.String("provider", "", "provider id")
	jsonMode := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	var p *config.Provider
	for i := range c.Providers {
		if c.Providers[i].ID == *id {
			p = &c.Providers[i]
		}
	}
	if p == nil {
		return fmt.Errorf("provider %q not found", *id)
	}
	u := modelsURL(*p)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	if p.Protocol == ir.Anthropic {
		req.Header.Set("x-api-key", p.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if p.Protocol == ir.Gemini {
		q := req.URL.Query()
		q.Set("key", p.APIKey)
		req.URL.RawQuery = q.Encode()
	} else {
		req.Header.Set("authorization", "Bearer "+p.APIKey)
	}
	start := time.Now()
	if e := secure.ValidatePublicTarget(context.Background(), req.URL.String(), p.AllowPrivateURL); e != nil {
		return e
	}
	transport := &http.Transport{Proxy: nil, DialContext: secure.PublicDialContext, ResponseHeaderTimeout: 30 * time.Second}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if p.AllowPrivateURL {
		client.Transport = http.DefaultTransport
	}
	resp, e := client.Do(req)
	out := map[string]any{"provider": p.ID, "protocol": p.Protocol, "latency_ms": time.Since(start).Milliseconds()}
	if e != nil {
		out["ok"] = false
		out["error"] = e.Error()
	} else {
		defer resp.Body.Close()
		out["ok"] = resp.StatusCode >= 200 && resp.StatusCode < 300
		out["status"] = resp.StatusCode
	}
	if *jsonMode {
		return printJSON(out)
	}
	fmt.Println(out)
	return nil
}
func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	u := fs.String("url", "http://127.0.0.1:12667", "admin URL")
	token := fs.String("token", os.Getenv("AIROUTE_ADMIN_TOKEN"), "admin token")
	jsonMode := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(*u, "/")+"/api/status", nil)
	if *token != "" {
		req.Header.Set("authorization", "Bearer "+*token)
	}
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if e != nil {
		return e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("admin returned HTTP %d", resp.StatusCode)
	}
	if *jsonMode {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("admin returned invalid JSON")
		}
		return printJSON(value)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return fmt.Errorf("admin returned invalid JSON")
	}
	fmt.Printf("status=%v version=%v uptime_seconds=%v config=%v\n", value["status"], value["version"], value["uptime_seconds"], value["config_version"])
	return nil
}
func ui(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	u := fs.String("url", "http://127.0.0.1:12667", "admin URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", *u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", *u)
	default:
		cmd = exec.Command("xdg-open", *u)
	}
	return cmd.Start()
}
func newLogger(c *config.Config) (*slog.Logger, *slog.LevelVar) {
	level := slog.LevelInfo
	switch c.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	levelController := &slog.LevelVar{}
	levelController.Set(level)
	var h slog.Handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: levelController})
	if c.Logging.Format == "text" {
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelController})
	}
	return slog.New(h), levelController
}
func externalHost(addr string) string {
	h, p, e := net.SplitHostPort(addr)
	if e != nil {
		return addr
	}
	if h == "0.0.0.0" || h == "::" || h == "" {
		h = "127.0.0.1"
	}
	return net.JoinHostPort(h, p)
}
func modelsURL(p config.Provider) string {
	base := strings.TrimRight(p.BaseURL, "/")
	if p.Protocol == ir.Gemini {
		if !strings.Contains(base, "/v1beta") {
			base += "/v1beta"
		}
		return base + "/models"
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}
func boolFlag(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	return f != nil && f.Value.String() == "true"
}
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
