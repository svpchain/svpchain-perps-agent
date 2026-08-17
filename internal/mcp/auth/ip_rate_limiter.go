package auth

import (
	"sync"
	"time"
)

// DefaultIPChallengeRate is the per-IP cap on auth_challenge calls.
// 10 / minute strikes the balance noted in the v0.3 plan: doesn't block
// accidental floods or casual abuse, doesn't pretend to defeat a serious
// attacker (network-layer defenses are still required for that).
const (
	DefaultIPChallengeRate   = 10
	DefaultIPChallengeWindow = time.Minute
)

// IPRateLimiter is a fixed-window per-IP counter. Each Allow() call
// increments a counter for the IP; the counter resets when the window
// rolls over. Safe for concurrent use; old entries are evicted by
// Sweep().
//
// Fixed-window (vs sliding) is deliberate: simpler to reason about,
// the small inaccuracy at window boundaries doesn't matter for our
// threat model (casual abuse, not adversarial precision).
type IPRateLimiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]*ipEntry
}

type ipEntry struct {
	count       int
	windowStart time.Time
}

// NewIPRateLimiter constructs a limiter with the supplied per-window max
// and window duration. now defaults to time.Now when nil.
func NewIPRateLimiter(max int, window time.Duration, now func() time.Time) *IPRateLimiter {
	if now == nil {
		now = time.Now
	}
	return &IPRateLimiter{
		max:     max,
		window:  window,
		now:     now,
		entries: map[string]*ipEntry{},
	}
}

// Allow returns true if the IP is under the per-window cap; the counter
// is incremented on every call regardless of return value (so a denied
// request still consumes a slot, mirroring fixed-window semantics).
// Empty ip is treated as "no IP available" and always allowed (typical
// for tests that bypass the HTTP layer).
func (l *IPRateLimiter) Allow(ip string) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[ip]
	if !ok || now.Sub(e.windowStart) >= l.window {
		l.entries[ip] = &ipEntry{count: 1, windowStart: now}
		return true
	}
	e.count++
	return e.count <= l.max
}

// Sweep drops entries whose window has elapsed. Driven by a background
// goroutine; callable from tests for determinism.
func (l *IPRateLimiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for ip, e := range l.entries {
		if now.Sub(e.windowStart) >= l.window {
			delete(l.entries, ip)
		}
	}
}

// Len reports the live entry count (test helper).
func (l *IPRateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
