package tools

import (
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
)

// ★ Rescued verbatim from upstream during the absorption of svpchain-mcp
// v0.1.0 (see internal/mcp/doc.go). knownToken/knownSwapTokens/
// knownTokenSymbol/parseSwapToken come from tools/swap.go, ownerEthAddress
// from tools/evm.go — both files carried unregistered tool families and were
// pruned, but the faucet family (list_faucet_tokens, faucet_claim) and the
// transfer-out asset registry in assets.go still reach these symbols.
//
// Kept byte-identical to upstream so the re-sync recipe in internal/mcp/doc.go
// still diffs cleanly against the tag; if you touch them, say so there.

// bankLinked tokens (e.g. USDC, the EVM side of the erc20/usdc trading
// collateral) already surface through get_balance's bank read, so they are NOT
// additionally contract-read there — that would double-count the same balance.
// Pure ERC-20s (USDV) have no bank denom and ARE contract-read. The distinction
// only affects get_balance; swap aliases and faucet labels use every entry.
type knownToken struct {
	address    common.Address
	bankLinked bool
}

// knownSwapTokens maps lower-case symbol aliases to this deployment's ERC-20s,
// so an agent can pass token_in/token_out="usdv" / "usdc" instead of the raw 0x
// address (native SVP is named separately, in parseSwapToken). These are
// convenience aliases only — a caller can always pass any 0x address, or
// discover faucet-dispensed tokens via list_faucet_tokens. Hardcoded like
// knownDenoms in account.go; decimals are still read on chain at call time. Also
// the source for labeling known ERC-20s by symbol in faucet output (faucet.go)
// and for the transfer-out cap registry in assets.go.
var knownSwapTokens = map[string]knownToken{
	"usdv": {address: common.HexToAddress("0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951")},
	"usdc": {address: common.HexToAddress("0x732F6Ea7AfD5EdC02e7ba052075dd0780e285489"), bankLinked: true},
}

// knownTokenSymbol reverse-maps an ERC-20 address to its upper-cased symbol
// alias, if one is registered in knownSwapTokens.
func knownTokenSymbol(addr common.Address) (string, bool) {
	for sym, kt := range knownSwapTokens {
		if kt.address == addr {
			return strings.ToUpper(sym), true
		}
	}
	return "", false
}

// parseSwapToken resolves a tool's token argument to either native SVP or an
// ERC-20 address. Empty, "native", "svp", or the zero address all mean native;
// a known symbol (see knownSwapTokens) resolves to its address; anything else
// must be a valid 0x address.
func parseSwapToken(s string) (addr common.Address, native bool, err error) {
	t := strings.TrimSpace(s)
	key := strings.ToLower(t)
	switch key {
	case "", "native", "svp":
		return common.Address{}, true, nil
	}
	if kt, ok := knownSwapTokens[key]; ok {
		return kt.address, false, nil
	}
	if !common.IsHexAddress(t) {
		return common.Address{}, false, fmt.Errorf(
			"invalid token %q: use a 0x address, a known symbol (usdv), or empty/\"native\"/\"svp\" for native SVP", s)
	}
	addr = common.HexToAddress(t)
	if addr == (common.Address{}) {
		return common.Address{}, true, nil // 0x0 is the native sentinel
	}
	return addr, false, nil
}

// ownerEthAddress converts a tenant's bech32 owner (svp1…) to its 0x EVM
// address. Both are the same 20 underlying bytes — the same identity the auth
// handshake recovers (see internal/mcp/auth/recover.go), just rendered as hex.
func ownerEthAddress(owner string) (common.Address, error) {
	acc, err := sdk.AccAddressFromBech32(owner)
	if err != nil {
		return common.Address{}, fmt.Errorf("parse owner %q: %w", owner, err)
	}
	return common.BytesToAddress(acc.Bytes()), nil
}
