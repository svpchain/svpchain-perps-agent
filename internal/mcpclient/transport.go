package mcpclient

import (
	"context"
	"net/http"
)

// Identity is who a single tool call is made as. It is carried on the call's
// context rather than on the connection, so one pooled session can serve many
// calls without any of them inheriting another's credentials.
type Identity struct {
	// Bearer authenticates the caller to the MCP server. Empty during the
	// auth_challenge / auth_verify handshake, which needs no credential.
	Bearer string

	// ContextID is the A2A conversation this call belongs to. It selects the
	// pooled session for an unauthenticated caller; see the package doc.
	ContextID string

	// ClientIP is the A2A caller's address, forwarded so the server can rate
	// limit auth_challenge per caller.
	//
	// ★ The deployed server does not read it yet: its ipMiddleware overwrites
	// the header it trusts with the TCP peer address, which is this agent. So
	// the per-IP challenge limit currently counts every A2A caller as one.
	// Forwarding it anyway costs nothing and is what the server needs when it
	// grows an X-Forwarded-For path.
	ClientIP string
}

type identityKey struct{}

// withIdentity returns ctx carrying the credentials for one call. The
// RoundTripper reads them back off the outgoing request's context.
func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

func identityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// bearerTransport stamps per-call credentials onto each outgoing MCP request.
//
// It reads them from the *request* context, which the Streamable HTTP
// transport derives from the ctx passed to CallTool. That is what lets a
// single connection carry a different caller on every call.
type bearerTransport struct{ base http.RoundTripper }

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	id, ok := identityFrom(req.Context())
	if !ok {
		return t.base.RoundTrip(req)
	}

	// Clone before mutating: RoundTrippers must not modify the request they
	// are given, and the SDK reuses request objects across retries.
	req = req.Clone(req.Context())
	if id.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+id.Bearer)
	}
	if id.ClientIP != "" {
		req.Header.Set("X-Forwarded-For", id.ClientIP)
	}
	return t.base.RoundTrip(req)
}

// callerKey carries the identity of the A2A caller that a request is being
// served for, as distinct from identityKey above, which carries the
// credentials for one outgoing HTTP request.
//
// They hold the same type and usually the same value. They are separate
// because they answer different questions: one is "who asked", set once when
// the A2A request arrives, and the other is "who is this call made as", set by
// CallTool immediately before the transport reads it. Anything that fans a
// single request out into several tool calls -- the assistant's planning loop
// -- reads the first and passes it as the second.
type callerKey struct{}

// WithCaller returns ctx carrying the identity of the A2A caller.
func WithCaller(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, callerKey{}, id)
}

// CallerFrom returns the A2A caller's identity, if one was attached.
func CallerFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(callerKey{}).(Identity)
	return id, ok
}
