package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestUpdateCheckDetectsAndCachesNewRelease(t *testing.T) {
	var calls atomic.Int32
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.3"}`))
	}))
	defer releases.Close()

	server := &Server{
		Version:    "0.2.2",
		ReleaseURL: releases.URL,
		Client:     releases.Client(),
	}
	first := server.checkUpdate(context.Background())
	second := server.checkUpdate(context.Background())
	if !first.Checked || !first.UpdateAvailable || first.LatestVersion != "0.2.3" {
		t.Fatalf("unexpected update result: %#v", first)
	}
	if second != first || calls.Load() != 1 {
		t.Fatalf("update result was not cached: second=%#v calls=%d", second, calls.Load())
	}
}

func TestUpdateCheckSkipsDevelopmentVersions(t *testing.T) {
	server := &Server{Version: "dev"}
	result := server.checkUpdate(context.Background())
	if result.Checked || result.UpdateAvailable || result.CheckUnavailable {
		t.Fatalf("development build should not check for updates: %#v", result)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, test := range []struct {
		candidate string
		current   string
		want      bool
	}{
		{"0.2.3", "0.2.2", true},
		{"v1.0.0", "0.9.9", true},
		{"0.2.2", "0.2.2", false},
		{"0.2.1", "0.2.2", false},
		{"1.0.0", "1.0.0-beta.1", true},
		{"dev", "0.2.2", false},
	} {
		if got := newerVersion(test.candidate, test.current); got != test.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", test.candidate, test.current, got, test.want)
		}
	}
}
