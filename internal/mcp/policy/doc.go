// Package policy is the per-tenant guardrail layer for every MCP tool call.
//
// Engine.Check(toolName, args, tenantCtx) → Decision enforces: owner +
// subaccount allowlist (v0.1); per-tool notional & frequency caps,
// withdraw destination allowlist, daily withdraw cap (v0.2+); global kill
// switch.
//
// audit.go is the append-only audit log for every signed/broadcast tx
// (v0.1: stdout-structured; v0.2: rotating file). idempotency.go dedupes
// repeat broadcasts by the payload-level client_id. rate.go provides per-
// (tool, owner) rate.Limiter wrappers from golang.org/x/time/rate.
//
// All state is keyed by tenant so multi-tenancy from v0.1 doesn't leak
// guardrails between tenants.
package policy
