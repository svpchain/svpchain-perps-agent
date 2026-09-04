package a2aserver

import (
	"context"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/auth"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"
	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// AuthResolver maps an A2A request onto the tenant/IP/session context the MCP
// tool handlers read. It owns no verification logic — challenges, signatures,
// and bearer minting live in the auth_challenge / auth_verify tools — it only
// resolves an already-minted bearer to its tenant and stamps the context.
type AuthResolver struct {
	Tenants  *auth.DynamicTenantStore
	Sessions *auth.SessionBearers
}

// Attach returns ctx annotated for the tool handlers:
//
//   - Bearer, resolved in precedence order: Authorization header, envelope
//     field, then the bearer bound to this A2A context id by a previous
//     auth_verify on the same conversation. A resolved bearer becomes a
//     tools.TenantContext; an unknown or expired one is simply absent, and
//     the gated handler refuses with its own message.
//   - The A2A context id rides as the session id, so auth_verify can bind its
//     minted bearer to the conversation (the role Mcp-Session-Id plays on the
//     MCP transport).
//   - The client IP (via X-Forwarded-For when present) feeds auth_challenge's
//     per-IP rate limit; absent is fine — the limiter passes empty keys.
func (r *AuthResolver) Attach(ctx context.Context, execCtx *a2asrv.ExecutorContext, req *Request) context.Context {
	if execCtx.ContextID != "" {
		ctx = tools.WithSessionID(ctx, execCtx.ContextID)
	}
	if ip := clientIP(execCtx); ip != "" {
		ctx = tools.WithIP(ctx, ip)
	}

	bearer := bearerFromHeader(execCtx)
	if bearer == "" {
		bearer = req.Bearer
	}
	if bearer == "" && r.Sessions != nil && execCtx.ContextID != "" {
		bearer = r.Sessions.Lookup(execCtx.ContextID)
	}

	// The raw bearer, for operations that call the remote MCP server as this
	// caller rather than resolving it to a local tenant. Stamped even when it
	// is empty, so an unauthenticated request reaches those operations as
	// nobody and is refused by the server rather than by absence.
	ctx = mcpclient.WithCaller(ctx, mcpclient.Identity{
		Bearer:    bearer,
		ContextID: execCtx.ContextID,
		ClientIP:  clientIP(execCtx),
	})

	if bearer == "" || r.Tenants == nil {
		return ctx
	}
	rec, err := r.Tenants.LookupByBearer(bearer)
	if err != nil {
		return ctx
	}
	return tools.WithTenant(ctx, tools.TenantContext{TenantID: rec.TenantID, Owner: rec.Owner})
}

func bearerFromHeader(execCtx *a2asrv.ExecutorContext) string {
	v := headerValue(execCtx, "authorization")
	if v == "" {
		return ""
	}
	const prefix = "bearer "
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return strings.TrimSpace(v[len(prefix):])
	}
	return ""
}

func headerValue(execCtx *a2asrv.ExecutorContext, key string) string {
	if execCtx == nil || execCtx.ServiceParams == nil {
		return ""
	}
	vals, ok := execCtx.ServiceParams.Get(key)
	if !ok || len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// clientIP is the caller's address, taken from X-Forwarded-For's first hop.
func clientIP(execCtx *a2asrv.ExecutorContext) string {
	ip := headerValue(execCtx, "x-forwarded-for")
	if ip == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(ip, ",")[0])
}
