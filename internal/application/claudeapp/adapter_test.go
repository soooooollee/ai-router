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
	if err != nil || !strings.Contains(string(preview.Content), "visible-key") || strings.Contains(string(preview.Content), "••••••••") {
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
}

func TestRouteIDRoundTrip(t *testing.T) {
	for _, alias := range []string{"qwen3", "供应商/模型-1"} {
		if got := decodeRouteID(routeID(alias)); got != alias {
			t.Fatalf("round trip = %q, want %q", got, alias)
		}
	}
}
