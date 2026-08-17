package tools

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/auth"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/signer"
)

// newTestAuthHandlers wires a Handlers with just enough plumbing to
// exercise auth_challenge + auth_verify. No chain, no policy engine,
// no rate limit on the per-tenant rate limiter — pure auth focus.
func newTestAuthHandlers(t *testing.T) *Handlers {
	t.Helper()
	return &Handlers{
		ChainID: "localsvp-1",
		Deps: Deps{
			NonceStore: auth.NewNonceStore(auth.DefaultChallengeTTL, nil),
			DynamicTenants: auth.NewDynamicTenantStore(auth.DynamicTenantStoreConfig{
				BearerTTL:                 auth.DefaultBearerTTL,
				DefaultAllowedSubaccounts: []uint32{0, 1},
			}, nil),
			IPChallengeLimit: auth.NewIPRateLimiter(100, time.Minute, nil),
			SessionBearers:   auth.NewSessionBearers(auth.DefaultBearerTTL, nil),
		},
	}
}

func newRandomPriv(t *testing.T) *ethsecp256k1.PrivKey {
	t.Helper()
	bz := make([]byte, 32)
	_, err := rand.Read(bz)
	require.NoError(t, err)
	return &ethsecp256k1.PrivKey{Key: bz}
}

// TestAuthFlow_EndToEnd is the critical happy path:
// AuthChallenge → sign with priv → AuthVerify → bearer. Proves the
// recovered-address-equals-bound-owner property holds across the real
// signing primitive.
func TestAuthFlow_EndToEnd(t *testing.T) {
	priv := newRandomPriv(t)
	owner := signer.DeriveAddress(priv)
	h := newTestAuthHandlers(t)

	_, ch, err := h.AuthChallenge(context.Background(), nil, AuthChallengeInput{
		Owner: owner,
	})
	require.NoError(t, err)
	require.NotEmpty(t, ch.Challenge)
	require.NotEmpty(t, ch.Nonce)
	require.True(t, ch.ExpiresAt > 0)

	sig, err := priv.Sign([]byte(ch.Challenge))
	require.NoError(t, err)

	_, vf, err := h.AuthVerify(context.Background(), nil, AuthVerifyInput{
		Nonce:     ch.Nonce,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	require.NoError(t, err)
	require.NotEmpty(t, vf.BearerToken)
	require.Equal(t, owner, vf.Owner)
	require.True(t, vf.ExpiresAt > 0)
}

func TestAuthVerify_RejectsSignatureFromDifferentKey(t *testing.T) {
	// Alice issues a challenge for her address; Bob signs it. The
	// recovered address won't match Alice, so verify must fail.
	alicePriv := newRandomPriv(t)
	aliceOwner := signer.DeriveAddress(alicePriv)
	bobPriv := newRandomPriv(t)
	h := newTestAuthHandlers(t)

	_, ch, err := h.AuthChallenge(context.Background(), nil, AuthChallengeInput{
		Owner: aliceOwner,
	})
	require.NoError(t, err)

	bobSig, err := bobPriv.Sign([]byte(ch.Challenge))
	require.NoError(t, err)

	_, _, err = h.AuthVerify(context.Background(), nil, AuthVerifyInput{
		Nonce:     ch.Nonce,
		Signature: base64.StdEncoding.EncodeToString(bobSig),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match")
}

func TestAuthVerify_NonceSingleUse(t *testing.T) {
	priv := newRandomPriv(t)
	owner := signer.DeriveAddress(priv)
	h := newTestAuthHandlers(t)

	_, ch, _ := h.AuthChallenge(context.Background(), nil, AuthChallengeInput{Owner: owner})
	sig, _ := priv.Sign([]byte(ch.Challenge))
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	_, _, err := h.AuthVerify(context.Background(), nil, AuthVerifyInput{
		Nonce: ch.Nonce, Signature: sigB64,
	})
	require.NoError(t, err)

	// Same nonce + same valid signature; second verify must fail because
	// the nonce was consumed on the first success.
	_, _, err = h.AuthVerify(context.Background(), nil, AuthVerifyInput{
		Nonce: ch.Nonce, Signature: sigB64,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonce")
}

func TestAuthChallenge_RateLimitedPerIP(t *testing.T) {
	h := &Handlers{
		ChainID: "localsvp-1",
		Deps: Deps{
			NonceStore:       auth.NewNonceStore(auth.DefaultChallengeTTL, nil),
			DynamicTenants:   auth.NewDynamicTenantStore(auth.DynamicTenantStoreConfig{}, nil),
			IPChallengeLimit: auth.NewIPRateLimiter(2, time.Hour, nil), // cap of 2 per hour
		},
	}
	ctx := WithIP(context.Background(), "1.2.3.4")
	for range 2 {
		_, _, err := h.AuthChallenge(ctx, nil, AuthChallengeInput{Owner: "svp1alice"})
		require.NoError(t, err)
	}
	_, _, err := h.AuthChallenge(ctx, nil, AuthChallengeInput{Owner: "svp1alice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "rate limit")
}

func TestAuthChallenge_EmptyOwnerRejected(t *testing.T) {
	h := newTestAuthHandlers(t)
	_, _, err := h.AuthChallenge(context.Background(), nil, AuthChallengeInput{Owner: ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "owner is required")
}

func TestAuthVerify_BindsSession(t *testing.T) {
	// When the request carries an Mcp-Session-Id, auth_verify must bind
	// the issued bearer to it — otherwise subsequent requests on the
	// same session can't resolve tenant without the client (which can't
	// update its Authorization header) re-sending the bearer.
	priv := newRandomPriv(t)
	owner := signer.DeriveAddress(priv)
	h := newTestAuthHandlers(t)

	// Bake a session id into the context like the real middleware does.
	ctx := WithSessionID(context.Background(), "session-xyz")

	_, ch, _ := h.AuthChallenge(ctx, nil, AuthChallengeInput{Owner: owner})
	sig, _ := priv.Sign([]byte(ch.Challenge))

	_, vf, err := h.AuthVerify(ctx, nil, AuthVerifyInput{
		Nonce:     ch.Nonce,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	require.NoError(t, err)
	require.Equal(t, vf.BearerToken, h.Deps.SessionBearers.Lookup("session-xyz"),
		"session must now be bound to the issued bearer")
}

func TestAuthVerify_BadBase64Sig(t *testing.T) {
	h := newTestAuthHandlers(t)
	// First issue a nonce so the verify path passes the consume step.
	_, ch, _ := h.AuthChallenge(context.Background(), nil, AuthChallengeInput{Owner: "svp1alice"})

	_, _, err := h.AuthVerify(context.Background(), nil, AuthVerifyInput{
		Nonce: ch.Nonce, Signature: "not!valid!base64",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "base64")
}
