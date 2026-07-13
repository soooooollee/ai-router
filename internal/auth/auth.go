package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/zbss/airoute/internal/config"
)

func ClientKey(r *http.Request, c *config.Config) (string, bool) {
	if !c.Auth.Enabled {
		return "anonymous", true
	}
	got := bearer(r.Header.Get("Authorization"))
	if got == "" {
		got = r.Header.Get("x-api-key")
	}
	if got == "" {
		got = r.Header.Get("x-goog-api-key")
	}
	if got == "" {
		got = r.URL.Query().Get("key")
	}
	for _, k := range c.Auth.Keys {
		if secureEqual(got, k.Value) {
			return k.ID, true
		}
	}
	return "", false
}
func AdminOK(r *http.Request, c *config.Config) bool {
	if !c.Admin.Enabled {
		return false
	}
	got := bearer(r.Header.Get("Authorization"))
	if got == "" {
		got = r.Header.Get("x-airoute-admin-token")
	}
	return c.Admin.Token == "" && loopback(r.RemoteAddr) || secureEqual(got, c.Admin.Token)
}
func bearer(v string) string {
	if len(v) > 7 && strings.EqualFold(v[:7], "Bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}
func secureEqual(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func loopback(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "[::1]:")
}
