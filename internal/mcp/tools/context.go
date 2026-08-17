package tools

import "context"

// ctxKey is the context-key type for TenantContext. Using a private type
// (rather than a string) prevents collisions with other packages.
type ctxKey int

const (
	tenantKey ctxKey = iota
	ipKey
	sessionIDKey
)

// TenantContext carries the per-request tenant identity decoded by the
// auth middleware (cmd/mcp-server/auth.go) from the bearer token. The
// MCP SDK propagates the http.Request context into each tool handler's
// ctx, so handlers read this via TenantFrom(ctx).
type TenantContext struct {
	TenantID string
	Owner    string
}

// WithTenant returns ctx with tc attached.
func WithTenant(ctx context.Context, tc TenantContext) context.Context {
	return context.WithValue(ctx, tenantKey, tc)
}

// TenantFrom extracts the TenantContext set by middleware. ok == false
// means the request reached a handler without auth — handlers must treat
// this as an internal error and refuse to act, EXCEPT auth_challenge /
// auth_verify which are the auth flow themselves and run without it.
func TenantFrom(ctx context.Context) (TenantContext, bool) {
	tc, ok := ctx.Value(tenantKey).(TenantContext)
	return tc, ok
}

// WithIP returns ctx with the client IP attached. Used by auth_challenge
// to rate-limit per IP (the auth tools don't run with a TenantContext,
// so the per-tenant rate limiter doesn't apply).
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipKey, ip)
}

// IPFrom extracts the client IP set by middleware. Empty string when
// the middleware didn't set one (tests that bypass the HTTP layer).
func IPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(ipKey).(string)
	return ip
}

// WithSessionID returns ctx with the MCP session id attached. Set by
// middleware when the client echoes Mcp-Session-Id; used by
// auth_verify to bind the issued bearer to the session so subsequent
// requests on the same session resolve to the same tenant without a
// fresh Authorization header.
func WithSessionID(ctx context.Context, sid string) context.Context {
	return context.WithValue(ctx, sessionIDKey, sid)
}

// SessionIDFrom extracts the MCP session id set by middleware. Empty
// string when no session is established yet (the initialize handshake
// has not completed).
func SessionIDFrom(ctx context.Context) string {
	sid, _ := ctx.Value(sessionIDKey).(string)
	return sid
}
