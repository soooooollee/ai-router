package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/zbss/airoute/internal/config"
)

func TestClientAndAdminAuthentication(t *testing.T) {
	c := &config.Config{Auth: config.Auth{Enabled: true, Keys: []config.APIKey{{ID: "one", Value: "secret-value"}}}, Admin: config.Admin{Enabled: true, Token: "admin-secret"}}
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	r.Header.Set("authorization", "Bearer secret-value")
	if id, ok := ClientKey(r, c); !ok || id != "one" {
		t.Fatalf("bearer auth failed %q %v", id, ok)
	}
	r.Header.Del("authorization")
	r.Header.Set("x-api-key", "secret-value")
	if _, ok := ClientKey(r, c); !ok {
		t.Fatal("x-api-key auth failed")
	}
	r.Header.Set("x-api-key", "wrong")
	if _, ok := ClientKey(r, c); ok {
		t.Fatal("invalid client key accepted")
	}
	r.Header.Del("x-api-key")
	r.Header.Set("x-goog-api-key", "secret-value")
	if _, ok := ClientKey(r, c); !ok {
		t.Fatal("Gemini header auth failed")
	}
	r.Header.Del("x-api-key")
	r.Header.Set("authorization", "Bearer admin-secret")
	if !AdminOK(r, c) {
		t.Fatal("admin token rejected")
	}
	r.Header.Set("authorization", "Bearer wrong")
	if AdminOK(r, c) {
		t.Fatal("invalid admin token accepted")
	}
	c.Auth.Enabled = false
	if id, ok := ClientKey(r, c); !ok || id != "anonymous" {
		t.Fatal("disabled client auth should allow anonymous")
	}
	c.Admin.Token = ""
	r.RemoteAddr = "127.0.0.1:1234"
	if !AdminOK(r, c) {
		t.Fatal("local tokenless admin should be allowed")
	}
}
