package policy

import (
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter applies a token-bucket limit per (tool, tenant) key so a
// single tenant cannot starve others. v0.1 default: 10 RPS, burst 5; the
// server can override per-tenant in v0.2.
type RateLimiter struct {
	rps    float64
	burst  int
	mu     sync.Mutex
	perKey map[string]*rate.Limiter
}

// NewRateLimiter constructs a RateLimiter. Pass zero for sensible defaults.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	if rps == 0 {
		rps = 10
	}
	if burst == 0 {
		burst = 5
	}
	return &RateLimiter{
		rps:    rps,
		burst:  burst,
		perKey: make(map[string]*rate.Limiter),
	}
}

// Allow reports whether one token is available right now for key. Keys
// are arbitrary — the typical convention is "<tool>:<tenant>".
func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	lim, ok := r.perKey[key]
	if !ok {
		lim = rate.NewLimiter(rate.Limit(r.rps), r.burst)
		r.perKey[key] = lim
	}
	r.mu.Unlock()
	return lim.Allow()
}
