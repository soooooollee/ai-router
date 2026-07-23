package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
	rate   int
	burst  int
}

type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	inflight map[string]int
}

func New() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, inflight: map[string]int{}}
}

func (l *Limiter) Allow(clientID string, requestsPerMinute, burst int, now time.Time) bool {
	if requestsPerMinute <= 0 {
		return true
	}
	if burst <= 0 {
		burst = requestsPerMinute
	}
	if burst < 1 {
		burst = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[clientID]
	if b == nil || b.rate != requestsPerMinute || b.burst != burst {
		b = &bucket{tokens: float64(burst), last: now, rate: requestsPerMinute, burst: burst}
		l.buckets[clientID] = b
	}
	if now.Before(b.last) {
		b.last = now
	}
	elapsed := now.Sub(b.last).Minutes()
	if elapsed > 0 {
		b.tokens += elapsed * float64(requestsPerMinute)
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *Limiter) Acquire(clientID string, maxConcurrent int) (func(), bool) {
	if maxConcurrent <= 0 {
		return func() {}, true
	}
	l.mu.Lock()
	if l.inflight[clientID] >= maxConcurrent {
		l.mu.Unlock()
		return nil, false
	}
	l.inflight[clientID]++
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.inflight[clientID] <= 1 {
				delete(l.inflight, clientID)
			} else {
				l.inflight[clientID]--
			}
			l.mu.Unlock()
		})
	}, true
}

func (l *Limiter) Forget(clientID string) {
	l.mu.Lock()
	delete(l.buckets, clientID)
	if l.inflight[clientID] == 0 {
		delete(l.inflight, clientID)
	}
	l.mu.Unlock()
}
