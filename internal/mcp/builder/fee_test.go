package builder_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
)

// TestAssemble_NonCLOBCarriesFee proves a funds-movement tx (not short-term
// CLOB) ships with the configured gas fee, so the chain doesn't reject it with
// code 13 (insufficient fee). This is the regression test for the empty-fee
// deposit rejection.
func TestAssemble_NonCLOBCarriesFee(t *testing.T) {
	_, p, err := builder.BuildDepositToSubaccount(builder.DepositToSubaccountInput{
		Owner:           testOwner,
		SubaccountNum:   0,
		HumanUSDC:       "100",
		PayloadClientID: "uuid-fee-dep",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.False(t, p.IsShortTermCLOB)

	require.Equal(t, []payload.Coin{{Denom: testFeeDenom, Amount: testFeeAmount}}, p.Fee.Amount,
		"non-CLOB txs must carry the configured fee")
	require.Equal(t, "1000000", p.Fee.GasLimit)
}

// TestAssemble_ShortTermCLOBIsGasFree proves short-term CLOB orders keep an
// empty fee — svpchain exempts them, and adding a fee would change their
// sign-bytes / be rejected.
func TestAssemble_ShortTermCLOBIsGasFree(t *testing.T) {
	cache := newBtcCache(t)
	_, p, err := builder.BuildPlaceLimitOrder(builder.PlaceLimitOrderInput{
		Owner:           testOwner,
		Ticker:          "BTC-USD",
		Side:            "BUY",
		HumanSize:       "0.05",
		HumanPrice:      "65000.00",
		GoodTilBlock:    100,
		OrderClientID:   1,
		PayloadClientID: "uuid-fee-limit",
	}, cache, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.True(t, p.IsShortTermCLOB)
	require.Empty(t, p.Fee.Amount, "short-term CLOB orders are gas-free")
}
