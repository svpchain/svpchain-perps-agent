package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveSendAmount_KnownDenomHuman covers the human-amount path for the
// denoms build_bank_send knows: SVP (18 dp) and USDC (6 dp).
func TestResolveSendAmount_KnownDenomHuman(t *testing.T) {
	coin, label, err := resolveSendAmount("asvp", "0.01")
	require.NoError(t, err)
	require.Equal(t, "asvp", coin.Denom)
	require.Equal(t, "10000000000000000", coin.Amount.String()) // 0.01 * 1e18
	require.Equal(t, "0.01 SVP", label)

	coin, label, err = resolveSendAmount("erc20/usdc", "1.5")
	require.NoError(t, err)
	require.Equal(t, "1500000", coin.Amount.String()) // 1.5 * 1e6
	require.Equal(t, "1.5 USDC", label)
}

// TestResolveSendAmount_UnknownDenomBaseUnits: unknown denoms take a base-unit
// integer and reject human decimals (we don't know their scale).
func TestResolveSendAmount_UnknownDenomBaseUnits(t *testing.T) {
	coin, label, err := resolveSendAmount("ibc/ABC", "42")
	require.NoError(t, err)
	require.Equal(t, "ibc/ABC", coin.Denom)
	require.Equal(t, "42", coin.Amount.String())
	require.Equal(t, "42 ibc/ABC", label)

	_, _, err = resolveSendAmount("ibc/ABC", "1.5")
	require.Error(t, err)
	require.Contains(t, err.Error(), "base-unit integer")
}

// TestResolveSendAmount_RejectsExcessPrecision: an amount finer than the denom
// allows is an error, never a silent truncation.
func TestResolveSendAmount_RejectsExcessPrecision(t *testing.T) {
	_, _, err := resolveSendAmount("erc20/usdc", "0.0000001") // 7 dp on a 6 dp denom
	require.Error(t, err)
	require.Contains(t, err.Error(), "precision")
}

func TestResolveSendAmount_RejectsBadInputs(t *testing.T) {
	_, _, err := resolveSendAmount("", "1")
	require.Error(t, err) // invalid denom

	_, _, err = resolveSendAmount("asvp", "0")
	require.Error(t, err) // non-positive
}
