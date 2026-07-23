package ratelimit

import (
	"testing"
	"time"
)

func TestTokenBucketRefillsAndPolicyChangesReset(t *testing.T) {
	limiter := New()
	now := time.Unix(100, 0)
	if !limiter.Allow("client", 60, 2, now) || !limiter.Allow("client", 60, 2, now) {
		t.Fatal("initial burst should be available")
	}
	if limiter.Allow("client", 60, 2, now) {
		t.Fatal("burst was exceeded")
	}
	if !limiter.Allow("client", 60, 2, now.Add(time.Second)) {
		t.Fatal("one token should refill after one second")
	}
	if !limiter.Allow("client", 120, 1, now.Add(time.Second)) {
		t.Fatal("policy change should reset the bucket")
	}
}

func TestConcurrencyReleaseIsIdempotent(t *testing.T) {
	limiter := New()
	release, ok := limiter.Acquire("client", 1)
	if !ok {
		t.Fatal("first acquire failed")
	}
	if _, ok = limiter.Acquire("client", 1); ok {
		t.Fatal("concurrency limit was not enforced")
	}
	release()
	release()
	if _, ok = limiter.Acquire("client", 1); !ok {
		t.Fatal("slot was not released")
	}
}
