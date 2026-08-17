package tools

import (
	"errors"
	"fmt"
)

// ErrNoTenant is returned when a protected tool is called without the auth
// middleware having resolved a TenantContext — i.e. the caller is not
// authenticated. In v0.3 the only tenant source is the auth_challenge →
// auth_verify handshake, so a missing tenant always means "authenticate
// first," never an internal misconfiguration. The message is therefore an
// actionable, client-facing instruction: the go-sdk surfaces it to the
// calling agent as CallToolResult{IsError:true} text, and auth_verify binds
// the minted bearer to the MCP session, so the agent can simply retry the
// original tool once the handshake completes.
var ErrNoTenant = errors.New(
	"authentication required: this tool needs a bearer token. Complete the handshake, then retry:\n" +
		"  1) call the svpchain-signer MCP server's whoami tool to get your svp1… owner address\n" +
		"  2) call auth_challenge with that owner address\n" +
		"  3) sign the returned `challenge` with the svpchain-signer MCP server's sign_challenge tool\n" +
		"  4) call auth_verify with the nonce and the base64 signature\n" +
		"The minted bearer binds to this MCP session automatically — after auth_verify, just call this tool again.",
)

// userErrf wraps a sentinel cause with a user-visible message. Callers
// should prefer this over raw fmt.Errorf for errors that surface to the
// MCP client.
func userErrf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
