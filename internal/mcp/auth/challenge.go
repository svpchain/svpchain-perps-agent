package auth

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ChallengePrefix locks the challenge format to the svpchain self-service-
// auth flow. Mirrored verbatim in the signer's prefix guard (now the
// independent svpchain-signer-mcp repo) — the two MUST stay in sync.
const ChallengePrefix = "svpchain-mcp-auth-v1:"

// BuildChallenge formats a challenge text from its three fields:
//
//	svpchain-mcp-auth-v1:<chain_id>:<nonce_hex>:<expires_at_unix>
//
// nonceHex is expected to be the lowercase hex encoding of a random
// nonce; the caller (NonceStore.Issue) generates it.
func BuildChallenge(chainID, nonceHex string, expiresAt time.Time) string {
	return fmt.Sprintf("%s%s:%s:%d", ChallengePrefix, chainID, nonceHex, expiresAt.Unix())
}

// ParsedChallenge is the decoded view of a challenge text.
type ParsedChallenge struct {
	ChainID   string
	NonceHex  string
	ExpiresAt time.Time
}

// ParseChallenge decodes a challenge text into its fields. Refuses any
// input that doesn't start with ChallengePrefix or that omits one of
// the three trailing fields. ExpiresAt is decoded from a unix timestamp;
// no clock check — the caller decides whether to enforce expiry.
func ParseChallenge(text string) (ParsedChallenge, error) {
	if !strings.HasPrefix(text, ChallengePrefix) {
		return ParsedChallenge{}, fmt.Errorf(
			"challenge must start with %q", ChallengePrefix,
		)
	}
	// Split on ':' into exactly 4 parts. SplitN with n=4 leaves any
	// extra colons in the final field (defensive, though chain ids /
	// hex nonces / unix timestamps don't contain ':').
	parts := strings.SplitN(text, ":", 4)
	if len(parts) != 4 {
		return ParsedChallenge{}, fmt.Errorf(
			"challenge malformed: expected %s<chain_id>:<nonce>:<expires_at>",
			ChallengePrefix,
		)
	}
	chainID := parts[1]
	nonceHex := parts[2]
	expiresUnix, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return ParsedChallenge{}, fmt.Errorf("parse expires_at %q: %w", parts[3], err)
	}
	if chainID == "" {
		return ParsedChallenge{}, fmt.Errorf("challenge chain_id is empty")
	}
	if nonceHex == "" {
		return ParsedChallenge{}, fmt.Errorf("challenge nonce is empty")
	}
	return ParsedChallenge{
		ChainID:   chainID,
		NonceHex:  nonceHex,
		ExpiresAt: time.Unix(expiresUnix, 0).UTC(),
	}, nil
}
