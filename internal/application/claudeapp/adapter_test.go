package claudeapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zbss/airoute/internal/application"
)

func desiredJSON(baseURL string) json.RawMessage {
	raw, _ := json.Marshal(DesiredConfig{BaseURL: baseURL, APIKey: "visible-key", Model: "qwen3", SonnetModel: "qwen3", OpusModel: "opus-route", HaikuModel: "fast-route"})
	return raw
}

func TestApplyReadPreviewAndRollback(t *testing.T) {
	appPath := filepath.Join(t.TempDir(), "Claude.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message"}`))
	}))
	defer server.Close()
	adapter := &Adapter{Candidates: []string{appPath}, DataDir: dataDir, HTTPClient: server.Client()}

	detection, err := adapter.Detect(context.Background())
	if err != nil || !detection.Installed || detection.Executable != appPath {
		t.Fatalf("unexpected detection: %#v err=%v", detection, err)
	}
	preview, err := adapter.Preview(context.Background(), desiredJSON(server.URL))
	if err != nil || string(preview.Current) != "{}" || !strings.Contains(string(preview.Content), "visible-key") || strings.Contains(string(preview.Content), "••••••••") {
		t.Fatalf("unexpected preview: %#v err=%v", preview, err)
	}
	result, err := adapter.Apply(context.Background(), desiredJSON(server.URL))
	if err != nil || !result.OK || result.Backup == "" {
		t.Fatalf("unexpected apply: %#v err=%v", result, err)
	}
	state, err := adapter.Read(context.Background())
	if err != nil || !state.Exists || !state.Synced || state.Managed["api_key"] != "visible-key" || state.Managed["model"] != "qwen3" {
		t.Fatalf("unexpected state: %#v err=%v", state, err)
	}
	verify, err := adapter.Verify(context.Background(), application.VerifyOptions{Config: desiredJSON(server.URL)})
	if err != nil || !verify.OK {
		t.Fatalf("unexpected verify: %#v err=%v", verify, err)
	}
	backups, err := adapter.Backups(context.Background())
	if err != nil || len(backups) != 1 {
		t.Fatalf("unexpected backups: %#v err=%v", backups, err)
	}
	if _, err = adapter.Rollback(context.Background(), backups[0].Name); err != nil {
		t.Fatal(err)
	}
	state, err = adapter.Read(context.Background())
	if err != nil || state.Exists {
		t.Fatalf("expected original empty state after rollback: %#v err=%v", state, err)
	}
	backups, err = adapter.Backups(context.Background())
	if err != nil || len(backups) < 1 {
		t.Fatalf("expected rollback backups: %#v err=%v", backups, err)
	}
	if err = adapter.DeleteBackup(context.Background(), backups[0].Name); err != nil {
		t.Fatal(err)
	}
}

func TestRouteIDRoundTrip(t *testing.T) {
	for _, alias := range []string{"qwen3", "供应商/模型-1"} {
		if got := decodeRouteID(routeID(alias)); got != alias {
			t.Fatalf("round trip = %q, want %q", got, alias)
		}
	}
}

func TestApplyRawAndCleanupManagedDesktopConfiguration(t *testing.T) {
	appPath := filepath.Join(t.TempDir(), "Claude.app")
	if err := os.MkdirAll(appPath, 0755); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	a := &Adapter{Candidates: []string{appPath}, DataDir: dataDir}
	desired := desiredJSON("http://127.0.0.1:12666")
	preview, err := a.Preview(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err = json.Unmarshal(preview.Content, &content); err != nil {
		t.Fatal(err)
	}
	content["customPreviewField"] = true
	edited, _ := json.MarshalIndent(content, "", "  ")
	if _, err = a.ApplyRaw(context.Background(), application.RawConfig{Content: string(edited), Config: desired}); err != nil {
		t.Fatal(err)
	}
	_, _, library, _, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(library)
	if !strings.Contains(string(written), "customPreviewField") {
		t.Fatalf("edited preview was not written: %s", written)
	}
	if _, err = a.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(library); !os.IsNotExist(err) {
		t.Fatalf("managed gateway library should be removed: %v", err)
	}
}
