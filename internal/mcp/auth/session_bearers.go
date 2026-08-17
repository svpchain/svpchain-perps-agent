package auth

import (
	"sync"
	"time"
)

// SessionBearers binds an MCP session id (set by the server during the
// initialize handshake, echoed by the client on every subsequent
// request via the Mcp-Session-Id header) to the bearer token issued by
// auth_verify. The auth middleware uses this to resolve a tenant
// context when the request omits an explicit Authorization header —
// MCP clients (Claude Desktop, Cursor, etc.) configure headers
// statically and can't update them from a tool response, so binding by
// session is what makes self-service auth actually usable end-to-end.
//
// Entries inherit the bearer's TTL (24h by default) plus a small
// grace period; Sweep evicts on a background goroutine.
type SessionBearers struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.RWMutex
	entries map[string]sessionEntry // session_id → entry
}

type sessionEntry struct {
	bearer    string
	expiresAt time.Time
}

// NewSessionBearers constructs a store with the supplied TTL. now
// defaults to time.Now when nil.
func NewSessionBearers(ttl time.Duration, now func() time.Time) *SessionBearers {
	if now == nil {
		now = time.Now
	}
	return &SessionBearers{
		ttl:     ttl,
		now:     now,
		entries: map[string]sessionEntry{},
	}
}

// Bind records that sessionID now uses bearer. Overwrites any previous
// binding for the same session — re-running the auth flow on an
// existing session just rotates the bearer.
func (s *SessionBearers) Bind(sessionID, bearer string) {
	if sessionID == "" || bearer == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[sessionID] = sessionEntry{
		bearer:    bearer,
		expiresAt: s.now().Add(s.ttl).UTC(),
	}
}

// Lookup returns the bearer bound to sessionID, or "" if none / expired.
func (s *SessionBearers) Lookup(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[sessionID]
	if !ok {
		return ""
	}
	if !s.now().Before(e.expiresAt) {
		return ""
	}
	return e.bearer
}

// Sweep removes expired entries. Driven by a background goroutine;
// callable from tests for determinism.
func (s *SessionBearers) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.entries {
		if !now.Before(v.expiresAt) {
			delete(s.entries, k)
		}
	}
}

// Len reports the live entry count (test helper).
func (s *SessionBearers) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}
