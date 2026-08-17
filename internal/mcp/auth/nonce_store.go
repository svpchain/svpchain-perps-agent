package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultChallengeTTL is how long an issued nonce stays valid. Five
// minutes is long enough for slow user interaction (signer-side
// approval prompt, hardware wallet confirm) and short enough that
// a stolen nonce isn't useful for long.
const DefaultChallengeTTL = 5 * time.Minute

// nonceLen is the byte length of generated nonces (16 = 128-bit
// collision-resistant for our use; encoded as 32 hex chars).
const nonceLen = 16

// ErrNonceNotFound — nonce was never issued or was already consumed.
var ErrNonceNotFound = errors.New("nonce not found or already consumed")

// ErrNonceExpired — nonce was issued but its TTL elapsed.
var ErrNonceExpired = errors.New("nonce expired")

// NonceStore tracks issued challenge nonces. Each nonce is single-use
// (Consume removes it on success) and TTL-bounded (a background sweeper
// removes expired entries).
//
// Concurrency: every public method is safe for concurrent use.
type NonceStore struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]nonceEntry // nonceHex → entry
}

type nonceEntry struct {
	owner     string // the address the nonce was issued for
	expiresAt time.Time
}

// NewNonceStore constructs a store with the given TTL. now defaults to
// time.Now when nil (the test path injects a fake clock).
func NewNonceStore(ttl time.Duration, now func() time.Time) *NonceStore {
	if now == nil {
		now = time.Now
	}
	return &NonceStore{
		ttl:     ttl,
		now:     now,
		entries: map[string]nonceEntry{},
	}
}

// Issue generates a fresh nonce, records it bound to owner, and returns
// both the nonce hex and the expiry time. The owner binding lets Consume
// guard against an attacker swapping owners between Issue and the
// auth_verify call.
func (s *NonceStore) Issue(owner string) (nonceHex string, expiresAt time.Time, err error) {
	if owner == "" {
		return "", time.Time{}, fmt.Errorf("owner is required to issue a nonce")
	}
	buf := make([]byte, nonceLen)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, fmt.Errorf("generate nonce: %w", err)
	}
	nonceHex = hex.EncodeToString(buf)
	expiresAt = s.now().Add(s.ttl).UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[nonceHex] = nonceEntry{owner: owner, expiresAt: expiresAt}
	return nonceHex, expiresAt, nil
}

// Consume looks up the nonce and verifies it isn't expired. Returns the
// owner the nonce was bound to AND the expires_at the issue recorded,
// then removes the nonce so it can't be reused. The verifier needs
// both: owner to compare against the recovered address, expires_at to
// rebuild the canonical challenge text. Returns ErrNonceNotFound if the
// nonce was never issued or already consumed, ErrNonceExpired if the
// TTL elapsed.
func (s *NonceStore) Consume(nonceHex string) (owner string, expiresAt time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[nonceHex]
	if !ok {
		return "", time.Time{}, ErrNonceNotFound
	}
	delete(s.entries, nonceHex) // single-use: consume regardless of expiry
	if !s.now().Before(entry.expiresAt) {
		return "", time.Time{}, ErrNonceExpired
	}
	return entry.owner, entry.expiresAt, nil
}

// Sweep removes every expired entry. The server starts a background
// goroutine that calls this periodically; callers can also drive it
// from tests for deterministic behavior.
func (s *NonceStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.entries {
		if !now.Before(v.expiresAt) {
			delete(s.entries, k)
		}
	}
}

// Len reports the current entry count (test helper).
func (s *NonceStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
