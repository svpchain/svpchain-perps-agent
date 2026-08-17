// Package tools holds the MCP tool handlers. market.go / account.go /
// trade.go / funds.go / cross.go group them by capability area plus the
// cross-cutting ones (broadcast_signed_tx, get_tx_status, whoami); the
// _v021/_v022 files add later handlers to the same Handlers type.
//
// Each handler:
//  1. Extracts TenantContext from the request context (set by the caller's
//     auth layer — here internal/a2aserver).
//  2. Calls policy.Engine.Check.
//  3. Dispatches to internal/mcp/chain, internal/mcp/indexer, or
//     internal/mcp/builder.
//  4. Maps backend errors to user-visible MCP errors (policy reject →
//     plain text; chain reject → Code + RawLog).
//
// Upstream also carried registry.go, which registered every handler with an
// MCP server via the mcp.AddTool generic and its reflection-derived JSON
// schemas. That file did not come across in the absorption (see
// internal/mcp/doc.go): this binary serves A2A, and internal/toolbridge binds
// these same handlers to A2A operations instead. Handlers and New — all
// anything here used it for — live in handlers.go.
package tools
