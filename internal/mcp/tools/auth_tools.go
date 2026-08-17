package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/auth"
)

// auth_challenge and auth_verify are the two tools that DO NOT require
// a TenantContext — they ARE the auth mechanism. The HTTP middleware
// passes requests through with no TenantContext when no bearer is
// present; all other tools error via authorize* on the missing context,
// but these two run anyway.

// -- auth_challenge ----------------------------------------------------

type AuthChallengeInput struct {
	Owner string `json:"owner" jsonschema:"the svp1… bech32 address claiming ownership; signature in auth_verify must recover to this address"`
}

type AuthChallengeOutput struct {
	// Challenge is the full text the agent passes to sign_challenge on
	// the local signer. It already embeds chain_id + nonce + expires_at;
	// the agent doesn't need to assemble anything.
	Challenge string `json:"challenge"`
	// Nonce is the hex nonce embedded in Challenge. The agent echoes it
	// back to auth_verify so the server can look up the issued state.
	Nonce string `json:"nonce"`
	// ExpiresAt is the unix timestamp after which the nonce is invalid.
	// Informational — auth_verify rejects expired nonces internally.
	ExpiresAt int64 `json:"expires_at"`
}

func (h *Handlers) AuthChallenge(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in AuthChallengeInput,
) (*mcp.CallToolResult, AuthChallengeOutput, error) {
	// Per-IP rate limit before any state mutation — burns no nonce on
	// rejected requests. The auth tools run without a TenantContext so
	// the per-tenant rate limiter doesn't apply.
	if !h.Deps.IPChallengeLimit.Allow(IPFrom(ctx)) {
		return nil, AuthChallengeOutput{}, userErrf("rate limit exceeded")
	}
	if in.Owner == "" {
		return nil, AuthChallengeOutput{}, fmt.Errorf("owner is required")
	}
	nonce, exp, err := h.Deps.NonceStore.Issue(in.Owner)
	if err != nil {
		return nil, AuthChallengeOutput{}, fmt.Errorf("issue nonce: %w", err)
	}
	return nil, AuthChallengeOutput{
		Challenge: auth.BuildChallenge(h.ChainID, nonce, exp),
		Nonce:     nonce,
		ExpiresAt: exp.Unix(),
	}, nil
}

// -- auth_verify -------------------------------------------------------

type AuthVerifyInput struct {
	Nonce     string `json:"nonce" jsonschema:"the nonce echoed back from auth_challenge"`
	Signature string `json:"signature" jsonschema:"base64-encoded 65-byte eth_secp256k1 signature returned by sign_challenge on the local signer"`
}

type AuthVerifyOutput struct {
	// BearerToken is what the agent puts in Authorization: Bearer …
	// for every subsequent request. Valid for 24h by default.
	BearerToken string `json:"bearer_token"`
	// Owner is the recovered + verified bech32 address. Should match
	// what the agent originally claimed in auth_challenge.
	Owner string `json:"owner"`
	// ExpiresAt is the unix timestamp at which the bearer becomes
	// invalid; the agent should call auth_challenge again before then.
	ExpiresAt int64 `json:"expires_at"`
}

func (h *Handlers) AuthVerify(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in AuthVerifyInput,
) (*mcp.CallToolResult, AuthVerifyOutput, error) {
	// Consume the nonce (single-use, gives us back the bound owner +
	// the expires_at we recorded at issue time — both needed to rebuild
	// the canonical challenge text the signer was supposed to have
	// signed).
	boundOwner, expiresAt, err := h.Deps.NonceStore.Consume(in.Nonce)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrNonceNotFound):
			return nil, AuthVerifyOutput{}, fmt.Errorf("nonce not found or already used")
		case errors.Is(err, auth.ErrNonceExpired):
			return nil, AuthVerifyOutput{}, fmt.Errorf("nonce expired; re-run auth_challenge")
		default:
			return nil, AuthVerifyOutput{}, err
		}
	}

	sig, err := base64.StdEncoding.DecodeString(in.Signature)
	if err != nil {
		return nil, AuthVerifyOutput{}, fmt.Errorf("decode signature base64: %w", err)
	}
	// Rebuild the canonical challenge text from our stored state. The
	// agent doesn't get to choose what was signed; we know what we
	// issued, and the signature must verify against that exact text.
	challenge := auth.BuildChallenge(h.ChainID, in.Nonce, expiresAt)
	recovered, err := auth.RecoverOwner(challenge, sig)
	if err != nil {
		return nil, AuthVerifyOutput{}, fmt.Errorf("recover address from signature: %w", err)
	}
	if recovered != boundOwner {
		// Recovered address doesn't match the owner the nonce was
		// issued to. Surface the mismatch without disclosing both
		// sides — gives the caller enough to diagnose without
		// confirming whether their bound owner was right.
		return nil, AuthVerifyOutput{}, fmt.Errorf(
			"recovered address does not match the owner this nonce was issued to",
		)
	}

	bearer, _, exp, err := h.Deps.DynamicTenants.Mint(boundOwner)
	if err != nil {
		return nil, AuthVerifyOutput{}, fmt.Errorf("mint tenant: %w", err)
	}
	// Bind the bearer to the MCP session id so subsequent requests on
	// the same session resolve to the issued tenant without needing to
	// send the Authorization header (the MCP client can't update it
	// from a tool response). Bind is a no-op if no session id is
	// attached — typical for tests that bypass the HTTP layer.
	if h.Deps.SessionBearers != nil {
		h.Deps.SessionBearers.Bind(SessionIDFrom(ctx), bearer)
	}
	return nil, AuthVerifyOutput{
		BearerToken: bearer,
		Owner:       boundOwner,
		ExpiresAt:   exp.Unix(),
	}, nil
}
