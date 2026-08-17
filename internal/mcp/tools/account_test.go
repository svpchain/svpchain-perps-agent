package tools

import (
	"encoding/json"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/dydxprotocol/v4-chain/protocol/dtypes"
	assettypes "github.com/dydxprotocol/v4-chain/protocol/x/assets/types"
	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"
)

// TestLiveSubaccountFromChain_JSONShape proves that a populated
// satypes.Subaccount round-trips into JSON where SerializableInt fields
// land as decimal strings — the shape the MCP-SDK schema reflector
// generates from LiveSubaccountDTO. Without the DTO the raw proto would
// emit Quantums as a Go struct view (object), and any populated
// subaccount returned by get_live_subaccount would trip the SDK's
// runtime output validator.
//
// This is the regression test for the live-fire failure on flow0's
// 100k USDC subaccount.
func TestLiveSubaccountFromChain_JSONShape(t *testing.T) {
	sub := satypes.Subaccount{
		Id: &satypes.SubaccountId{Owner: "svp1owner", Number: 0},
		AssetPositions: []*satypes.AssetPosition{
			{AssetId: 0, Quantums: dtypes.NewInt(100_000_000_000), Index: 0},
		},
		PerpetualPositions: []*satypes.PerpetualPosition{
			{
				PerpetualId:  1,
				Quantums:     dtypes.NewInt(-500),
				FundingIndex: dtypes.NewInt(0),
				QuoteBalance: dtypes.NewInt(1234),
			},
		},
		MarginEnabled: true,
	}

	dto := liveSubaccountFromChain(sub)
	b, err := json.Marshal(dto)
	require.NoError(t, err)

	// Decode opaquely to verify the JSON types — not the Go types — match
	// the schema reflector's expectation (string, not object).
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))

	aps, _ := raw["asset_positions"].([]any)
	require.Len(t, aps, 1)
	ap, _ := aps[0].(map[string]any)
	require.IsType(t, "", ap["quantums"], "asset_positions[].quantums must be a JSON string")
	require.Equal(t, "100000000000", ap["quantums"])

	pps, _ := raw["perpetual_positions"].([]any)
	require.Len(t, pps, 1)
	pp, _ := pps[0].(map[string]any)
	require.IsType(t, "", pp["quantums"])
	require.IsType(t, "", pp["funding_index"])
	require.IsType(t, "", pp["quote_balance"])
	require.Equal(t, "-500", pp["quantums"])
}

// TestBalancesFromCoins_KnownAndUnknownDenoms proves the bank-coin projection:
// known denoms (USDC, native SVP) get a ticker + decimal-adjusted Display, the
// raw base-unit Amount is always preserved, and an unknown denom passes through
// with no Symbol/Display rather than being dropped.
func TestBalancesFromCoins_KnownAndUnknownDenoms(t *testing.T) {
	coins := sdk.NewCoins(
		sdk.NewCoin(assettypes.UusdcDenom, math.NewInt(100_500_000)), // 100.5 USDC at 6 dp
		sdk.NewCoin("asvp", math.NewIntWithDecimal(5, 18)),           // 5 SVP at 18 dp
		sdk.NewCoin("ibc/SOMETHINGUNKNOWN", math.NewInt(42)),
	)

	got := balancesFromCoins(coins)
	byDenom := map[string]BalanceDTO{}
	for _, b := range got {
		byDenom[b.Denom] = b
	}

	// Raw base-unit amount preserved; Display is decimal-adjusted with
	// trailing zeros trimmed (so 100.5, not 100.500000000000000000).
	usdc := byDenom[assettypes.UusdcDenom]
	require.Equal(t, "100500000", usdc.Amount)
	require.Equal(t, "USDC", usdc.Symbol)
	require.Equal(t, "100.5", usdc.Display)

	svp := byDenom["asvp"]
	require.Equal(t, "5000000000000000000", svp.Amount)
	require.Equal(t, "SVP", svp.Symbol)
	require.Equal(t, "5", svp.Display)

	unknown := byDenom["ibc/SOMETHINGUNKNOWN"]
	require.Equal(t, "42", unknown.Amount)
	require.Empty(t, unknown.Symbol)
	require.Empty(t, unknown.Display)
}

// TestBalancesFromCoins_EmptyIsEmptySlice ensures an empty wallet marshals to
// [] rather than null (clients that reject null arrays).
func TestBalancesFromCoins_EmptyIsEmptySlice(t *testing.T) {
	b, err := json.Marshal(GetBalanceOutput{Owner: "svp1empty", Balances: balancesFromCoins(sdk.NewCoins())})
	require.NoError(t, err)
	require.Contains(t, string(b), `"balances":[]`)
}

func TestLiveSubaccountFromChain_EmptyPositions(t *testing.T) {
	// Clients prefer [] over null on empty arrays.
	dto := liveSubaccountFromChain(satypes.Subaccount{
		Id: &satypes.SubaccountId{Owner: "svp1empty", Number: 0},
	})
	b, err := json.Marshal(dto)
	require.NoError(t, err)
	require.Contains(t, string(b), `"asset_positions":[]`)
	require.Contains(t, string(b), `"perpetual_positions":[]`)
}
