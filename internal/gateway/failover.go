package gateway

import (
	"sync"
	"time"

	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
	"github.com/soooooollee/ai-router/internal/routing"
)

// breakerRegistry remembers consecutive provider failures so that an unhealthy
// target is tried last instead of first. It is deliberately in-memory: the
// gateway is a single process and a restart is a reasonable time to forget.
type breakerRegistry struct {
	mu     sync.Mutex
	states map[string]*breakerState
}

type breakerState struct {
	failures  int
	openUntil time.Time
}

func (b *breakerRegistry) open(providerID string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[providerID]
	if state == nil || state.openUntil.IsZero() {
		return false
	}
	return now.Before(state.openUntil)
}

func (b *breakerRegistry) record(providerID string, ok bool, threshold int, cooldown time.Duration, now time.Time) {
	if providerID == "" || threshold <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.states == nil {
		b.states = map[string]*breakerState{}
	}
	state := b.states[providerID]
	if state == nil {
		state = &breakerState{}
		b.states[providerID] = state
	}
	if ok {
		state.failures = 0
		state.openUntil = time.Time{}
		return
	}
	state.failures++
	if state.failures >= threshold {
		state.openUntil = now.Add(cooldown)
	}
}

// noteProviderResult updates the breaker after one upstream attempt.
func (g *Gateway) noteProviderResult(c *config.Config, providerID string, ok bool) {
	breaker := c.Failover.Breaker
	g.breakers.record(providerID, ok, breaker.FailureThreshold, breaker.Cooldown, time.Now())
	if !ok && breaker.FailureThreshold > 0 && g.Logger != nil {
		g.Logger.Warn("provider attempt failed", "provider_id", providerID, "failure_threshold", breaker.FailureThreshold, "cooldown", breaker.Cooldown.String())
	}
}

// orderTargets moves providers with an open breaker behind the healthy ones.
// Nothing is dropped: if every target is unhealthy the request still tries them
// in their configured order.
func (g *Gateway) orderTargets(c *config.Config, targets []routing.ResolvedTarget) []routing.ResolvedTarget {
	if c.Failover.Strategy != "health" || len(targets) < 2 {
		return targets
	}
	now := time.Now()
	ordered := make([]routing.ResolvedTarget, 0, len(targets))
	var degraded []routing.ResolvedTarget
	for _, target := range targets {
		if g.breakers.open(target.Provider.ID, now) {
			degraded = append(degraded, target)
			continue
		}
		ordered = append(ordered, target)
	}
	return append(ordered, degraded...)
}

func findRoute(c *config.Config, routeID string) *config.Route {
	if routeID == "" || routeID == "default" {
		return nil
	}
	for i := range c.Routes {
		if c.Routes[i].ID == routeID {
			return &c.Routes[i]
		}
	}
	return nil
}

// effectiveRetry returns the retry policy for a resolved route. A route that
// sets retry replaces the global policy instead of merging with it, so the
// config stays predictable.
func effectiveRetry(c *config.Config, routeID string) config.Retry {
	if route := findRoute(c, routeID); route != nil && route.Retry != nil {
		return *route.Retry
	}
	return c.Retry
}

func effectiveFallback(c *config.Config, routeID string) config.Fallback {
	if route := findRoute(c, routeID); route != nil && route.Fallback != nil {
		return *route.Fallback
	}
	return c.Fallback
}

// providerTimeout caps the provider timeout with the route timeout, whichever
// is shorter. A zero route timeout means "no route-level cap".
func providerTimeout(c *config.Config, routeID string, provider config.Provider) time.Duration {
	timeout := provider.Timeout
	route := findRoute(c, routeID)
	if route == nil || route.Timeout <= 0 {
		return timeout
	}
	if timeout <= 0 || route.Timeout < timeout {
		return route.Timeout
	}
	return timeout
}

func providerPricing(c *config.Config, providerID string) config.Pricing {
	for i := range c.Providers {
		if c.Providers[i].ID == providerID {
			return c.Providers[i].Pricing
		}
	}
	return config.Pricing{}
}

// routeCostMicros prices one settled request against the provider that served
// it. Unknown providers (or providers without pricing) cost nothing.
func routeCostMicros(c *config.Config, providerID string, usage ir.Usage) int64 {
	return providerPricing(c, providerID).CostMicros(usage)
}
