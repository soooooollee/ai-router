package ratelimit

import (
	"fmt"
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

func TestLimiterEvictsFullyRefilledBuckets(t *testing.T) {
	limiter := New()
	now := time.Now()
	for i := 0; i < evictionSweepAtSize+20; i++ {
		if !limiter.Allow(fmt.Sprintf("idle-%d", i), 60, 60, now) {
			t.Fatalf("bucket %d rejected its first request", i)
		}
	}
	if !limiter.Allow("active", 60, 60, now) {
		t.Fatal("active client rejected")
	}
	limiter.mu.Lock()
	for _, b := range limiter.buckets {
		b.last = now.Add(-11 * time.Minute)
	}
	limiter.mu.Unlock()

	// A request that crosses the sweep threshold triggers the eviction pass.
	if !limiter.Allow("newcomer", 60, 60, now) {
		t.Fatal("newcomer rejected")
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if _, ok := limiter.buckets["idle-0"]; ok {
		t.Fatal("fully refilled idle bucket was not evicted")
	}
	if _, ok := limiter.buckets["newcomer"]; !ok {
		t.Fatal("new bucket was evicted by its own sweep")
	}
}

func TestLimiterKeepsBucketsThatWouldStillBeLimited(t *testing.T) {
	limiter := New()
	now := time.Now()
	for i := 0; i < evictionSweepAtSize+5; i++ {
		limiter.Allow(fmt.Sprintf("idle-%d", i), 60, 60, now)
	}
	// Drain a wide burst at one request per minute: eleven minutes of idling
	// refills only eleven of its hundred tokens.
	for i := 0; i < 100; i++ {
		if !limiter.Allow("slow", 1, 100, now) {
			t.Fatalf("slow bucket rejected request %d", i)
		}
	}
	limiter.mu.Lock()
	for _, b := range limiter.buckets {
		b.last = now.Add(-11 * time.Minute)
	}
	limiter.mu.Unlock()

	limiter.Allow("newcomer", 60, 60, now)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if _, ok := limiter.buckets["slow"]; !ok {
		t.Fatal("bucket that has not refilled was evicted, resetting its limit")
	}
	if _, ok := limiter.buckets["idle-3"]; ok {
		t.Fatal("refilled bucket survived the sweep")
	}
}
