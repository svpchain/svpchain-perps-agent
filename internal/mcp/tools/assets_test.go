package tools

import (
	"testing"

	"github.com/stretchr/testify/require"

	assettypes "github.com/dydxprotocol/v4-chain/protocol/x/assets/types"
)

func TestTransferOutAssets_DecimalsKnown(t *testing.T) {
	// Decimals are hardcoded so cap parsing works without an EVM read; pin them
	// so a registry edit can't silently change a cap's scale.
	a, ok := assetForSymbol("SVP") // case-insensitive
	require.True(t, ok)
	require.EqualValues(t, 18, a.decimals)
	require.Equal(t, nativeBankDenom, a.bankDenom)
	require.True(t, a.native)

	a, ok = assetForSymbol("usdc")
	require.True(t, ok)
	require.EqualValues(t, 6, a.decimals)
	require.Equal(t, assettypes.UusdcDenom, a.bankDenom)
	require.NotEqual(t, "", a.erc20.Hex())

	a, ok = assetForSymbol("usdv")
	require.True(t, ok)
	require.EqualValues(t, 6, a.decimals)
	require.Equal(t, "", a.bankDenom, "usdv is a pure ERC-20, no bank denom")
}

func TestSymbolForDenomAndToken(t *testing.T) {
	sym, ok := symbolForDenom("asvp")
	require.True(t, ok)
	require.Equal(t, "svp", sym)

	sym, ok = symbolForDenom(assettypes.UusdcDenom)
	require.True(t, ok)
	require.Equal(t, "usdc", sym)

	_, ok = symbolForDenom("ibc/ABCDEF")
	require.False(t, ok, "unknown denom is uncapped")

	usdc, _ := assetForSymbol("usdc")
	sym, ok = symbolForToken(usdc.erc20)
	require.True(t, ok)
	require.Equal(t, "usdc", sym)

	usdv, _ := assetForSymbol("usdv")
	sym, ok = symbolForToken(usdv.erc20)
	require.True(t, ok)
	require.Equal(t, "usdv", sym)

	sym, ok = symbolForNative()
	require.True(t, ok)
	require.Equal(t, "svp", sym)
}
