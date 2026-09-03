// Package mcpclient is the agent's connection to the remote MCP servers that
// implement its operations.
//
// This is what replaces the in-process tool handlers. The agent used to carry
// a fork of svpchain-mcp's lib/mcp and call it as Go code; it now speaks the
// Model Context Protocol over Streamable HTTP to a deployed server
// (svpchain-dex-mcp) that owns the chain clients, the tx builders, the policy
// engine and the tenant stores. The agent keeps no chain connection and no
// auth state of its own.
//
// # Identity is per call, not per connection
//
// Every A2A caller carries its own bearer, so one agent process serves many
// tenants. The Go SDK's Streamable HTTP transport sets headers on the
// *connection*, not on the call, which looks at first like it forces one
// connection per caller.
//
// It does not. The transport builds each POST with
// http.NewRequestWithContext(ctx, …) from the ctx passed to CallTool, so a
// RoundTripper can read per-call credentials off the request context and stamp
// them on that request alone. bearerTransport does exactly that, and
// TestPerCallCredentialsReachTheWire pins the behaviour against the vendored
// SDK so an upgrade that decoupled the two contexts would fail loudly rather
// than silently sending every call unauthenticated.
//
// # ★ Why sessions are pooled by identity and not shared
//
// The obvious design — one session, per-call bearers — is unsafe against this
// server, and the reason is worth stating because nothing in the transport
// hints at it.
//
// svpchain-dex-mcp resolves a caller two ways: an explicit Authorization
// header, or, failing that, the bearer previously bound to the request's
// Mcp-Session-Id. auth_verify creates that binding unconditionally when it
// mints a bearer. So a single shared session is a single shared binding: after
// any one caller authenticates over it, a *different* caller's unauthenticated
// call on the same session resolves to the first caller's tenant and acts as
// them.
//
// Sessions are therefore pooled under a key that is never shared by two
// identities:
//
//   - a caller carrying a bearer keys on the bearer, so one tenant's calls
//     share one session and the fallback binding can only ever resolve to that
//     same tenant;
//   - a caller mid-handshake (auth_challenge / auth_verify, no bearer yet)
//     keys on its A2A context id, which is unique per conversation — so the
//     binding auth_verify leaves behind belongs to that conversation alone,
//     and reproduces the "bearer bound to this A2A context" behaviour the
//     agent card advertises without the agent storing bearers itself;
//   - a caller with neither gets a single-use session that is closed when the
//     call returns, because there is no key that could safely be reused.
//
// Idle sessions are swept, so the pool tracks live tenants rather than growing
// without bound.
package mcpclient
