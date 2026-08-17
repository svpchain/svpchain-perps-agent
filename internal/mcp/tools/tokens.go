package tools

import (
	"github.com/ethereum/go-ethereum/common"
)

// ★ Rescued from upstream during the absorption of svpchain-mcp v0.1.0 (see
// internal/mcp/doc.go). knownToken/knownSwapTokens come from tools/swap.go,
// which carried an unregistered tool family and was pruned; the transfer-out
// asset registry in assets.go still reaches them.
//
// The rest of the rescue — knownTokenSymbol, parseSwapToken and ownerEthAddress
// — went with the faucet family, which was their last caller. So this file is
// no longer byte-identical to the tag; internal/mcp/doc.go records that.

// bankLinked tokens (e.g. USDC, the EVM side of the erc20/usdc trading
// collateral) already surface through get_balance's bank read, so they are NOT
// additionally contract-read there — that would double-count the same balance.
// Pure ERC-20s (USDV) have no bank denom. The distinction only affects
// get_balance, which in this binary contract-reads nothing at all.
type knownToken struct {
	address    common.Address
	bankLinked bool
}

// knownSwapTokens maps lower-case symbol aliases to this deployment's ERC-20s.
// Named for the swap family it was written for; what survives here is its role
// as the source for the transfer-out cap registry in assets.go. Hardcoded like
// knownDenoms in account.go; decimals are still read on chain at call time.
var knownSwapTokens = map[string]knownToken{
	"usdv": {address: common.HexToAddress("0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951")},
	"usdc": {address: common.HexToAddress("0x732F6Ea7AfD5EdC02e7ba052075dd0780e285489"), bankLinked: true},
}
