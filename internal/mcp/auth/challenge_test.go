package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChallengeRoundTrip(t *testing.T) {
	expires := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	text := BuildChallenge("localsvp-1", "abc123def456", expires)
	require.True(t,
		len(text) > len(ChallengePrefix) && text[:len(ChallengePrefix)] == ChallengePrefix,
		"output must start with the canonical prefix")

	parsed, err := ParseChallenge(text)
	require.NoError(t, err)
	require.Equal(t, "localsvp-1", parsed.ChainID)
	require.Equal(t, "abc123def456", parsed.NonceHex)
	require.True(t, parsed.ExpiresAt.Equal(expires), "want %v got %v", expires, parsed.ExpiresAt)
}

func TestParseChallenge_Rejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"wrong prefix", "wrong-prefix:localsvp-1:abc:123", "must start with"},
		{"no chain id", "svpchain-mcp-auth-v1::abc:123", "chain_id is empty"},
		{"no nonce", "svpchain-mcp-auth-v1:localsvp-1::123", "nonce is empty"},
		{"non-numeric expires_at", "svpchain-mcp-auth-v1:localsvp-1:abc:notanint", "parse expires_at"},
		{"missing field", "svpchain-mcp-auth-v1:localsvp-1:abc", "malformed"},
		{"empty", "", "must start with"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseChallenge(tc.in)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
