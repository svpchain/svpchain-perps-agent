package tools

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/faucet"
)

func TestFaucetTokenSymbol(t *testing.T) {
	// Native sentinel (zero / empty / explicit 0x0) -> SVP.
	require.Equal(t, "SVP", faucetTokenSymbol(""))
	require.Equal(t, "SVP", faucetTokenSymbol("0x0000000000000000000000000000000000000000"))
	// Known ERC-20 -> its symbol, regardless of input casing.
	require.Equal(t, "USDV", faucetTokenSymbol("0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951"))
	require.Equal(t, "USDV", faucetTokenSymbol("0x013a61e622e6abfcab64f52d274c3fc0aa37f951"))
	// Bank-linked tokens still get labeled by symbol in faucet output.
	require.Equal(t, "USDC", faucetTokenSymbol("0x732F6Ea7AfD5EdC02e7ba052075dd0780e285489"))
	// Unknown ERC-20 -> empty (so the JSON field is omitted).
	require.Equal(t, "", faucetTokenSymbol("0x1111111111111111111111111111111111111111"))
}

func TestFaucetTokensWithSymbols(t *testing.T) {
	in := []faucet.TokenInfo{
		{Address: "0x0000000000000000000000000000000000000000", AmountAllowed: "1000", Enabled: true},
		{Address: "0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951", AmountAllowed: "500", Enabled: true},
		{Address: "0x1111111111111111111111111111111111111111", AmountAllowed: "1", Enabled: false},
	}
	out := faucetTokensWithSymbols(in)
	require.Len(t, out, 3)

	require.Equal(t, "SVP", out[0].Symbol)
	require.Equal(t, "USDV", out[1].Symbol)
	require.Equal(t, "", out[2].Symbol)

	// Passthrough fields are preserved.
	require.Equal(t, in[1].Address, out[1].Address)
	require.Equal(t, "500", out[1].AmountAllowed)
	require.True(t, out[1].Enabled)
	require.False(t, out[2].Enabled)
}
