package a2aserver

import (
	"context"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// AuthResolver maps an A2A request onto the identity its operations run as.
//
// It owns no verification logic and no state. Challenges, signatures and
// bearer minting are the MCP server's — auth_challenge and auth_verify are two
// more proxied tools — so all this does is find the caller's bearer and stamp
// it on the context for the operation to carry.
//
// ★ It used to hold two stores: a tenant store to resolve a bearer locally,
// and a session store binding a minted bearer to an A2A conversation. Both
// went with the handlers. The tenant lookup is the server's job now, and the
// session binding is reproduced without any state here: mcpclient pools its
// MCP sessions by A2A context id for a caller that has no bearer yet, so a
// conversation that ran auth_verify keeps the binding the server made against
// that session. The card's promise that a bearer binds to the conversation
// still holds; nothing in this process remembers it.
type AuthResolver struct{}

// Attach returns ctx carrying the caller's identity.
//
// The bearer is resolved in precedence order: Authorization header, then the
// envelope's bearer field. A caller with neither is stamped as nobody rather
// than skipped, so the operations it reaches refuse at the server with the
// server's own handshake instructions — the same answer it would get calling
// that server directly.
func (r *AuthResolver) Attach(ctx context.Context, execCtx *a2asrv.ExecutorContext, req *Request) context.Context {
	bearer := bearerFromHeader(execCtx)
	if bearer == "" && req != nil {
		bearer = req.Bearer
	}
	return mcpclient.WithCaller(ctx, mcpclient.Identity{
		Bearer:    bearer,
		ContextID: execCtx.ContextID,
		ClientIP:  clientIP(execCtx),
	})
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

// clientIP is the caller's address, taken from X-Forwarded-For's first hop.
func clientIP(execCtx *a2asrv.ExecutorContext) string {
	ip := headerValue(execCtx, "x-forwarded-for")
	if ip == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(ip, ",")[0])
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
