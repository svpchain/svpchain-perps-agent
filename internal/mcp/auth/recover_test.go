package auth_test

import (
	"crypto/rand"
	"testing"

	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/auth"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/signer"
)

func newRandomPriv(t *testing.T) *ethsecp256k1.PrivKey {
	t.Helper()
	bz := make([]byte, 32)
	_, err := rand.Read(bz)
	require.NoError(t, err)
	return &ethsecp256k1.PrivKey{Key: bz}
}

// TestRecoverOwner_RoundTrip is the critical compatibility test between
// the two crypto libraries: cosmos/evm signs, go-ethereum recovers, and
// the recovered bech32 address must equal the signer.DeriveAddress side.
// If hashing or signing conventions diverge between the two, this test
// is where it'd surface.
func TestRecoverOwner_RoundTrip(t *testing.T) {
	priv := newRandomPriv(t)
	expectedOwner := signer.DeriveAddress(priv)

	message := "svpchain-mcp-auth-v1:localsvp-1:abc123:1780000000"
	sig, err := priv.Sign([]byte(message))
	require.NoError(t, err)
	require.Len(t, sig, 65, "eth_secp256k1 sigs are R||S||V (65 bytes)")

	recovered, err := auth.RecoverOwner(message, sig)
	require.NoError(t, err)
	require.Equal(t, expectedOwner, recovered,
		"recovered address must match signer-side DeriveAddress")
}

func TestRecoverOwner_DifferentMessageMismatches(t *testing.T) {
	priv := newRandomPriv(t)
	signedMessage := "svpchain-mcp-auth-v1:localsvp-1:abc123:1780000000"
	otherMessage := "svpchain-mcp-auth-v1:localsvp-1:def456:1780000000"
	sig, err := priv.Sign([]byte(signedMessage))
	require.NoError(t, err)

	// Recovery on a different message recovers SOME address (recovery
	// always returns a candidate pubkey) — but it MUST NOT equal the
	// real signer's address.
	recovered, err := auth.RecoverOwner(otherMessage, sig)
	require.NoError(t, err)
	require.NotEqual(t, signer.DeriveAddress(priv), recovered,
		"recovery on mismatched message must not return the original signer")
}

func TestRecoverOwner_BadSignatureLength(t *testing.T) {
	_, err := auth.RecoverOwner("anything", []byte{1, 2, 3})
	require.Error(t, err)
	require.Contains(t, err.Error(), "65 bytes")
}

func TestRecoverOwner_GarbageSignature(t *testing.T) {
	garbage := make([]byte, 65) // all zeros
	_, err := auth.RecoverOwner("anything", garbage)
	require.Error(t, err) // crypto.SigToPub rejects invalid signatures
}

func TestRecoverOwner_SvpPrefix(t *testing.T) {
	priv := newRandomPriv(t)
	message := "svpchain-mcp-auth-v1:localsvp-1:abc:1780000000"
	sig, _ := priv.Sign([]byte(message))
	recovered, err := auth.RecoverOwner(message, sig)
	require.NoError(t, err)
	require.True(t, len(recovered) > 4 && recovered[:4] == "svp1",
		"recovered address must carry the svp1 prefix, got %s", recovered)
}
