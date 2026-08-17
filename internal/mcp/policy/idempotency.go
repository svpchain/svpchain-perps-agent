package policy

import (
	"fmt"
	"sync"
	"time"
)

// DefaultIdempotencyTTL is how long a payload-level client_id is reserved
// after a successful broadcast. After this, the same client_id may be used
// again (e.g. for a follow-up trade with a similar shape) — long enough
// that an in-flight retry can't slip through twice, short enough that the
// in-memory table doesn't grow unbounded.
const DefaultIdempotencyTTL = 10 * time.Minute

// Idempotency tracks claimed broadcast-idempotency keys (the payload-level
// uuid client_id) so a buggy / replaying client cannot accidentally send
// the same tx twice. Keys are also per-tenant so collisions across tenants
// do not interact.
type Idempotency struct {
	ttl  time.Duration
	mu   sync.Mutex
	seen map[string]time.Time // key: tenantID + "|" + clientID
}

// NewIdempotency returns a tracker with the given TTL (defaults to
// DefaultIdempotencyTTL when zero).
func NewIdempotency(ttl time.Duration) *Idempotency {
	if ttl == 0 {
		ttl = DefaultIdempotencyTTL
	}
	return &Idempotency{
		ttl:  ttl,
		seen: make(map[string]time.Time),
	}
}

// Claim records (tenantID, clientID) as in-use and returns nil if it has
// not been claimed within the TTL; otherwise returns a duplicate-broadcast
// error.
//
// v0.1 does not garbage-collect expired entries proactively — at low
// traffic the table stays small. v0.2 should run a periodic sweeper.
func (i *Idempotency) Claim(tenantID, clientID string) error {
	if clientID == "" {
		return fmt.Errorf("missing client_id")
	}
	key := tenantID + "|" + clientID
	now := time.Now()
	i.mu.Lock()
	defer i.mu.Unlock()
	if exp, ok := i.seen[key]; ok && exp.After(now) {
		return fmt.Errorf("duplicate broadcast: client_id %s already used for tenant %s", clientID, tenantID)
	}
	i.seen[key] = now.Add(i.ttl)
	return nil
}
