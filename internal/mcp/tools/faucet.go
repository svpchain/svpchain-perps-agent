package tools

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/faucet"
)

// faucet.go holds the HTTP faucet tools. Unlike the EVM build_* tools, these
// do not construct an on-chain transaction for the caller to sign: the faucet
// backend (faucet_base_url) runs its own operator that signs and submits the
// claim. The tools are thin wrappers over internal/mcp/faucet.Client — claim funds
// in one call, no signing / EVM RPC / contract address on the client side.

// nativeTokenAddress is the faucet's sentinel for the chain's native token
// (SVP). The faucet treats the zero address as "native" in /api/claim and
// /api/enabledTokens; an ERC-20 claim passes the token's real 0x address.
const nativeTokenAddress = "0x0000000000000000000000000000000000000000"

// faucetTokenSymbol labels a faucet token address with a human symbol: "SVP"
// for the native sentinel (zero / empty address), the registered alias for a
// known ERC-20 (e.g. "USDV" for 0x013a…f951; see knownSwapTokens), or "" when
// the address isn't recognized. Lets list_faucet_tokens / faucet_claim show a
// symbol instead of just a raw 0x address.
func faucetTokenSymbol(address string) string {
	addr := common.HexToAddress(address)
	if addr == (common.Address{}) {
		return "SVP"
	}
	if sym, ok := knownTokenSymbol(addr); ok {
		return sym
	}
	return ""
}

// FaucetTokenDTO is one claimable token plus its human symbol — the tool-layer
// projection of faucet.TokenInfo (which the HTTP backend returns without a
// symbol).
type FaucetTokenDTO struct {
	Address       string `json:"address"`
	AmountAllowed string `json:"amount_allowed"`
	Enabled       bool   `json:"enabled"`
	Symbol        string `json:"symbol,omitempty"` // "SVP", "USDV", … — omitted if unknown
}

// faucetTokensWithSymbols annotates each backend token with its symbol.
func faucetTokensWithSymbols(tokens []faucet.TokenInfo) []FaucetTokenDTO {
	out := make([]FaucetTokenDTO, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, FaucetTokenDTO{
			Address:       t.Address,
			AmountAllowed: t.AmountAllowed,
			Enabled:       t.Enabled,
			Symbol:        faucetTokenSymbol(t.Address),
		})
	}
	return out
}

// -- list_faucet_tokens ------------------------------------------------

type ListFaucetTokensInput struct{}

type ListFaucetTokensOutput struct {
	Tokens []FaucetTokenDTO `json:"tokens" jsonschema:"tokens the faucet will dispense, each with its symbol (SVP, USDV, …), 0x address, and per-claim amount (base units)"`
}

func (h *Handlers) ListFaucetTokens(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ ListFaucetTokensInput,
) (*mcp.CallToolResult, ListFaucetTokensOutput, error) {
	if _, err := h.authorize(ctx, "list_faucet_tokens"); err != nil {
		return nil, ListFaucetTokensOutput{}, err
	}
	if h.Deps.Faucet == nil {
		return nil, ListFaucetTokensOutput{}, userErrf("faucet is not enabled on this server (no faucet_base_url configured)")
	}
	tokens, err := h.Deps.Faucet.EnabledTokens(ctx)
	if err != nil {
		return nil, ListFaucetTokensOutput{}, err
	}
	return nil, ListFaucetTokensOutput{Tokens: faucetTokensWithSymbols(tokens)}, nil
}

// -- faucet_claim ------------------------------------------------------

type FaucetClaimInput struct {
	// Token is the token to claim: a 0x address, a known symbol ("usdv"), or
	// empty/"native"/"svp" (the default) for the native token (SVP). Use
	// list_faucet_tokens to discover claimable ERC-20 addresses.
	Token string `json:"token,omitempty" jsonschema:"token to claim: a 0x address, a known symbol (\"usdv\"), or omit/\"native\"/\"svp\" for the native token (SVP). See list_faucet_tokens."`
}

type FaucetClaimOutput struct {
	TxHash  string `json:"tx_hash"`          // on-chain tx the faucet operator submitted
	Amount  string `json:"amount"`           // amount dispensed, base units
	Token   string `json:"token"`            // token that was claimed (0x address)
	Symbol  string `json:"symbol,omitempty"` // "SVP", "USDV", … — omitted if unknown
	Address string `json:"address"`          // recipient (the caller's own EVM address)
}

func (h *Handlers) FaucetClaim(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in FaucetClaimInput,
) (*mcp.CallToolResult, FaucetClaimOutput, error) {
	tp, err := h.authorize(ctx, "faucet_claim")
	if err != nil {
		return nil, FaucetClaimOutput{}, err
	}
	if h.Deps.Faucet == nil {
		return nil, FaucetClaimOutput{}, userErrf("faucet is not enabled on this server (no faucet_base_url configured)")
	}

	// Recipient is always the caller's own EVM address (the same 20 bytes as
	// the bech32 owner the auth handshake recovered) — never a caller-supplied
	// address.
	addr, err := ownerEthAddress(tp.Owner)
	if err != nil {
		return nil, FaucetClaimOutput{}, err
	}
	address := addr.Hex()

	// Accept a 0x address, a known symbol ("usdv"), or native — same resolution
	// the swap tools use. The faucet backend wants the zero address for native.
	tokenAddr, native, err := parseSwapToken(in.Token)
	if err != nil {
		return nil, FaucetClaimOutput{}, err
	}
	token := nativeTokenAddress
	if !native {
		token = tokenAddr.Hex()
	}

	res, err := h.Deps.Faucet.Claim(ctx, token, address)
	if err != nil {
		return nil, FaucetClaimOutput{}, err
	}
	return nil, FaucetClaimOutput{
		TxHash:  res.TxHash,
		Amount:  res.Amount,
		Token:   res.Token,
		Symbol:  faucetTokenSymbol(res.Token),
		Address: address,
	}, nil
}
