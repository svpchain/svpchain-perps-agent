package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestErrNoTenant_IsActionableHandshake locks in the contract that a missing
// tenant surfaces as an actionable "authenticate first" instruction rather
// than the old "internal: missing tenant context" message. The go-sdk relays
// this string to the calling agent verbatim, so the handshake steps must stay
// present and in order: signer whoami → auth_challenge → sign_challenge →
// auth_verify.
func TestErrNoTenant_IsActionableHandshake(t *testing.T) {
	msg := ErrNoTenant.Error()

	require.Contains(t, msg, "authentication required")
	require.NotContains(t, msg, "internal:")

	// The four handshake steps, in the order the agent must run them.
	steps := []string{"whoami", "auth_challenge", "sign_challenge", "auth_verify"}
	last := -1
	for _, s := range steps {
		i := strings.Index(msg, s)
		require.NotEqual(t, -1, i, "handshake message must mention %q", s)
		require.Greater(t, i, last, "handshake step %q is out of order", s)
		last = i
	}

	// Names the signer that holds the key, not a vague "local signer".
	require.Contains(t, msg, "svpchain-signer")
}
