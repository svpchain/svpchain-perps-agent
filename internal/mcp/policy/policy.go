package policy

import (
	"fmt"
	"sync"
)

// TenantPolicy is the per-tenant guardrail configuration loaded from
// [[tenants]] in the server config. v0.1 carries owner + subaccount
// allowlist + kill switch only; v0.2 will add per-tool caps, withdraw
// destination allowlist, and daily withdraw cap.
type TenantPolicy struct {
	TenantID           string // free-form id used for audit logs
	Owner              string
	AllowedSubaccounts map[uint32]struct{}
	KillSwitch         bool
}

// DynamicSource is an optional fallback the Engine consults when a
// tenant id isn't in its static table — implemented by the self-service
// store (v0.3) so auto-issued tenants resolve to a TenantPolicy with
// the same shape. Returning ok=false means "not a dynamic tenant
// either", and the Engine surfaces an unknown-tenant error.
type DynamicSource interface {
	LookupTenantPolicy(tenantID string) (TenantPolicy, bool)
}

// Engine enforces guardrails. All methods accept a tenant id (typically
// derived from the bearer token in HTTP middleware) and return a
// user-visible error on rejection.
type Engine struct {
	mu        sync.RWMutex
	perTenant map[string]TenantPolicy
	dynamic   DynamicSource // optional; set via SetDynamicSource
}

// NewEngine builds an Engine indexed by tenant id.
func NewEngine(tenants []TenantPolicy) *Engine {
	e := &Engine{perTenant: make(map[string]TenantPolicy, len(tenants))}
	for _, t := range tenants {
		e.perTenant[t.TenantID] = t
	}
	return e
}

// SetDynamicSource attaches a dynamic fallback. Safe to call once at
// wire time (no need to lock — Engine isn't yet handling requests).
func (e *Engine) SetDynamicSource(src DynamicSource) {
	e.dynamic = src
}

// Tenant returns the policy for tenantID, consulting the static table
// first and the dynamic source second. Returns an error if neither
// resolves it.
func (e *Engine) Tenant(tenantID string) (TenantPolicy, error) {
	e.mu.RLock()
	tp, ok := e.perTenant[tenantID]
	e.mu.RUnlock()
	if ok {
		return tp, nil
	}
	if e.dynamic != nil {
		if tp, ok := e.dynamic.LookupTenantPolicy(tenantID); ok {
			return tp, nil
		}
	}
	return TenantPolicy{}, fmt.Errorf("unknown tenant %q", tenantID)
}

// CheckTenant asserts the tenant exists and the kill switch is off — a
// blanket precondition every tool call must satisfy.
func (e *Engine) CheckTenant(tenantID string) error {
	tp, err := e.Tenant(tenantID)
	if err != nil {
		return err
	}
	if tp.KillSwitch {
		return fmt.Errorf("kill switch active for tenant %s", tenantID)
	}
	return nil
}

// CheckOwner asserts that requestedOwner (from tool args) matches the
// tenant's configured owner. An empty requestedOwner is allowed and means
// "use the tenant's owner" — handlers should fall back to the tenant's
// owner when args omit it.
func (e *Engine) CheckOwner(tenantID, requestedOwner string) error {
	tp, err := e.Tenant(tenantID)
	if err != nil {
		return err
	}
	if requestedOwner != "" && requestedOwner != tp.Owner {
		return fmt.Errorf("owner %s not allowed for tenant %s (allowed: %s)",
			requestedOwner, tenantID, tp.Owner)
	}
	return nil
}

// CheckSubaccount asserts that subaccount is in the tenant's allowlist.
func (e *Engine) CheckSubaccount(tenantID string, subaccount uint32) error {
	tp, err := e.Tenant(tenantID)
	if err != nil {
		return err
	}
	if _, ok := tp.AllowedSubaccounts[subaccount]; !ok {
		return fmt.Errorf("subaccount %d not allowed for tenant %s", subaccount, tenantID)
	}
	return nil
}
