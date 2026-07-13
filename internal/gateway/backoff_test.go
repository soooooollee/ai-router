package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/zbss/airoute/internal/config"
	"github.com/zbss/airoute/internal/observe"
	"github.com/zbss/airoute/internal/protocol"
)

func TestBackoffRespectsRetryAfterHTTPDate(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Retry-After": []string{time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)}}}
	d := backoff(config.Retry{BaseDelay: time.Millisecond, MaxDelay: time.Second}, 1, resp)
	if d < 500*time.Millisecond || d > 3*time.Second {
		t.Fatalf("HTTP-date Retry-After not respected: %s", d)
	}
}

func TestUpstreamPoolTracksConcurrencyLimit(t *testing.T) {
	c := &config.Config{Server: config.Server{MaxConcurrent: 128}}
	g := New(config.NewStore(c), protocol.NewRegistry(), observe.NewStore(1), &observe.Metrics{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	transport := g.Client.Transport.(*http.Transport)
	if transport.MaxIdleConnsPerHost != 128 || transport.MaxIdleConns < 256 {
		t.Fatalf("undersized upstream pool: per_host=%d total=%d", transport.MaxIdleConnsPerHost, transport.MaxIdleConns)
	}
}
