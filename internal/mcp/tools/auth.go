package tools

import (
	"context"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/policy"
)

// authorize is the base auth prelude every tool handler runs: extract the
// tenant context, confirm it isn't kill-switched, retrieve its policy, and
// consult the rate limiter under a tool-prefixed key. Returns the
// TenantPolicy so write handlers have Owner / AllowedSubaccounts in hand
// without a second lookup.
//
// Centralising the prelude here closes a real correctness gap: previously
// every new handler hand-rolled the same 8-13 lines and a missing
// RateLimit.Allow call would have shipped silently. Now the contract is
// one function to audit.
func (h *Handlers) authorize(ctx context.Context, tool string) (*policy.TenantPolicy, error) {
	tc, ok := TenantFrom(ctx)
	if !ok {
		return nil, ErrNoTenant
	}
	if err := h.Deps.Policy.CheckTenant(tc.TenantID); err != nil {
		return nil, err
	}
	tp, err := h.Deps.Policy.Tenant(tc.TenantID)
	if err != nil {
		return nil, err
	}
	if !h.Deps.RateLimit.Allow(tool + ":" + tc.TenantID) {
		return nil, userErrf("rate limit exceeded")
	}
	return &tp, nil
}

// authorizeOwner adds an explicit owner-address check on top of authorize.
// Used by read handlers where the caller passes the address as a tool
// argument (e.g. get_subaccount, get_orders) — guards against one tenant
// peeking at another's owner.
func (h *Handlers) authorizeOwner(ctx context.Context, tool, ownerAddr string) (*policy.TenantPolicy, error) {
	tp, err := h.authorize(ctx, tool)
	if err != nil {
		return nil, err
	}
	if err := h.Deps.Policy.CheckOwner(tp.TenantID, ownerAddr); err != nil {
		return nil, err
	}
	return tp, nil
}

// authorizeSubaccount adds a subaccount-allowlist check on top of authorize.
// Used by write handlers scoped to a specific subaccount of the tenant's
// owner (all trading + funds builders). The owner is implicit (tp.Owner);
// only the subaccount number comes from the request.
func (h *Handlers) authorizeSubaccount(ctx context.Context, tool string, sub uint32) (*policy.TenantPolicy, error) {
	tp, err := h.authorize(ctx, tool)
	if err != nil {
		return nil, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, sub); err != nil {
		return nil, err
	}
	return tp, nil
}
