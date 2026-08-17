// Package auth implements the self-service tenant authentication flow
// described in docs/mcp-agent-service-design.md §9 — a wallet-signature
// challenge-response that lets any user with a svpchain key mint a
// bearer token without operator pre-provisioning.
//
// The flow has three RPCs:
//
//  1. auth_challenge(owner)  → {nonce, challenge_text, expires_at}
//  2. sign_challenge(text)   → {signature, owner}   (on the local signer)
//  3. auth_verify(nonce, signature, owner) → {bearer_token, expires_at}
//
// The package is pure logic — no MCP wiring lives here. Callers in
// internal/mcp/tools/ and internal/a2aserver/ adapt it to the SDK's request/
// response types and HTTP middleware. Two in-memory stores back the
// flow (NonceStore, DynamicTenantStore); both are TTL-bounded with
// background sweepers. Server restart invalidates all bearers — the
// durable backend is deferred to v0.4 (planned alongside the durable
// withdraw ledger).
//
// The challenge text is anchored to a chain id and a versioned prefix:
//
//	svpchain-mcp-auth-v1:<chain_id>:<nonce_hex>:<expires_at_unix>
//
// That byte stream is structurally distinct from any cosmos
// SIGN_MODE_DIRECT TxBody+AuthInfo+SignDoc — a challenge signature can
// never be reused as a tx signature, and vice versa.
package auth
