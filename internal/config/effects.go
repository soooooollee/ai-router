package config

import "reflect"

type Effects struct {
	HotReloaded    []string `json:"hot_reloaded"`
	RuntimeRebuilt []string `json:"runtime_rebuilt"`
	RestartNeeded  []string `json:"restart_required"`
}

// CompareEffects classifies configuration changes by their real runtime
// behavior. The field names are stable API values used by the control plane.
func CompareEffects(previous, next *Config) Effects {
	var effects Effects
	if previous == nil || next == nil {
		return effects
	}
	hot := func(changed bool, field string) {
		if changed {
			effects.HotReloaded = append(effects.HotReloaded, field)
		}
	}
	runtime := func(changed bool, field string) {
		if changed {
			effects.RuntimeRebuilt = append(effects.RuntimeRebuilt, field)
		}
	}
	restart := func(changed bool, field string) {
		if changed {
			effects.RestartNeeded = append(effects.RestartNeeded, field)
		}
	}

	hot(!reflect.DeepEqual(previous.Providers, next.Providers), "providers")
	hot(!reflect.DeepEqual(previous.Routes, next.Routes), "routes")
	hot(!reflect.DeepEqual(previous.DefaultRoute, next.DefaultRoute), "default_route")
	hot(!reflect.DeepEqual(previous.Retry, next.Retry), "retry")
	hot(!reflect.DeepEqual(previous.Fallback, next.Fallback), "fallback")
	hot(!reflect.DeepEqual(previous.Conversion, next.Conversion), "conversion")
	hot(previous.Server.MaxHeaders != next.Server.MaxHeaders, "server.max_headers")
	hot(previous.Server.MaxBodySize != next.Server.MaxBodySize, "server.max_body_size")
	hot(previous.Server.RequestTimeout != next.Server.RequestTimeout, "server.request_timeout")
	hot(!reflect.DeepEqual(previous.Auth, next.Auth), "auth")
	hot(previous.Admin.Token != next.Admin.Token, "admin.token")
	hot(!reflect.DeepEqual(previous.Admin.AllowedHosts, next.Admin.AllowedHosts), "admin.allowed_hosts")
	hot(previous.Logging.RequestHistory != next.Logging.RequestHistory, "logging.request_history")
	hot(previous.Logging.CaptureBodies != next.Logging.CaptureBodies, "logging.capture_bodies")
	hot(previous.Logging.File != next.Logging.File, "logging.file")
	hot(previous.Logging.Level != next.Logging.Level, "logging.level")
	hot(!reflect.DeepEqual(previous.Metrics, next.Metrics), "metrics")

	runtime(previous.Server.MaxConcurrent != next.Server.MaxConcurrent, "server.max_concurrent")

	restart(previous.Server.Listen != next.Server.Listen, "server.listen")
	restart(previous.Server.AdminListen != next.Server.AdminListen, "server.admin_listen")
	restart(previous.Server.MaxHeaderBytes != next.Server.MaxHeaderBytes, "server.max_header_bytes")
	restart(previous.Server.ReadHeaderTimeout != next.Server.ReadHeaderTimeout, "server.read_header_timeout")
	restart(previous.Admin.Enabled != next.Admin.Enabled, "admin.enabled")
	restart(previous.Logging.Format != next.Logging.Format, "logging.format")
	return effects
}
