package mimocode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zbss/airoute/internal/application"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPreviewApplyPreservesConfigurationAndManagesModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mimocode.json")
	original := []byte(`{"theme":"dark","provider":{"existing":{"name":"Keep Me"}}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ConfigPath = path
	a.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:12666", APIKey: "local-key", Model: "fast", Models: []string{"fast", "coding"}})
	preview, err := a.Preview(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(preview.Content, []byte(`"existing"`)) || !bytes.Contains(preview.Content, []byte(`"coding"`)) || !bytes.Contains(preview.Content, []byte(`"model": "airoute/fast"`)) {
		t.Fatalf("unexpected preview: %s", preview.Content)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(unchanged, original) {
		t.Fatal("preview modified the MiMo Code configuration")
	}
	first, err := a.Apply(context.Background(), desired)
	if err != nil || first.Backup == "" {
		t.Fatalf("apply=%#v err=%v", first, err)
	}
	desired2, _ := json.Marshal(DesiredConfig{BaseURL: "http://127.0.0.1:12666", APIKey: "new-key", Model: "coding", Models: []string{"fast", "coding"}})
	if _, err = a.Apply(context.Background(), desired2); err != nil {
		t.Fatal(err)
	}
	backups, err := a.Backups(context.Background())
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
	for _, backup := range backups {
		if backup.Name != first.Backup {
			if err = a.DeleteBackup(context.Background(), backup.Name); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if _, err = a.Rollback(context.Background(), first.Backup); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(restored, original) {
		t.Fatalf("rollback did not restore original config: %s", restored)
	}
}

func TestReadAndVerifyChatCompletionsEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mimocode.json")
	raw := []byte(`{"model":"airoute/mimo","provider":{"airoute":{"api":"http://router/v1","options":{"apiKey":"secret"}}}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ConfigPath = path
	a.LookPath = func(string) (string, error) { return "/bin/echo", nil }
	a.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://router/v1/chat/completions" || request.Header.Get("authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %#v", request.URL, request.Header)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`)), Header: http.Header{}}, nil
	})}
	state, err := a.Read(context.Background())
	if err != nil || !state.Synced || state.Managed["api_key"] != "secret" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	desired, _ := json.Marshal(DesiredConfig{BaseURL: "http://router", APIKey: "secret", Model: "mimo"})
	result, err := a.Verify(context.Background(), application.VerifyOptions{Config: desired})
	if err != nil || !result.OK || len(result.Stages) != 2 {
		t.Fatalf("verify=%#v err=%v", result, err)
	}
}
