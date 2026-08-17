package builder_test

import (
	"context"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"

	// testOwner, newTestAsm, and the app/config blank-import live in
	// testutil_test.go (same package).
	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
	perptypes "github.com/dydxprotocol/v4-chain/protocol/x/perpetuals/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
)

// stubChain implements both chain.ClobQueryClient and
// chain.PerpetualsQueryClient off fixed in-memory slices, so the markets
// cache can be driven without a live chain.
type stubChain struct {
	pairs []clobtypes.ClobPair
	perps []perptypes.Perpetual
}

func (s *stubChain) ClobPairAll(_ context.Context) ([]clobtypes.ClobPair, error) {
	return s.pairs, nil
}

func (s *stubChain) AllPerpetuals(_ context.Context) ([]perptypes.Perpetual, error) {
	return s.perps, nil
}

func newBtcCache(t *testing.T) *markets.Cache {
	t.Helper()
	stub := &stubChain{
		pairs: []clobtypes.ClobPair{{
			Id: 0,
			Metadata: &clobtypes.ClobPair_PerpetualClobMetadata{
				PerpetualClobMetadata: &clobtypes.PerpetualClobMetadata{PerpetualId: 0},
			},
			StepBaseQuantums:          1_000_000,
			SubticksPerTick:           1_000,
			QuantumConversionExponent: -9,
		}},
		perps: []perptypes.Perpetual{{
			Params: perptypes.PerpetualParams{Id: 0, Ticker: "BTC-USD", AtomicResolution: -10},
		}},
	}
	c := markets.NewCache(stub, stub, time.Hour, log.NewNopLogger())
	require.NoError(t, c.Refresh(context.Background()))
	return c
}

func TestBuildPlaceLimitOrder_Happy(t *testing.T) {
	cache := newBtcCache(t)
	msg, p, err := builder.BuildPlaceLimitOrder(builder.PlaceLimitOrderInput{
		Owner:           testOwner,
		Ticker:          "BTC-USD",
		Side:            "BUY",
		HumanSize:       "0.05",
		HumanPrice:      "65000.00",
		TimeInForce:     "",
		GoodTilBlock:    100,
		OrderClientID:   1,
		PayloadClientID: "uuid-limit",
	}, cache, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, clobtypes.Order_SIDE_BUY, msg.Order.Side)
	require.Equal(t, clobtypes.OrderIdFlags_ShortTerm, msg.Order.OrderId.OrderFlags)
	require.Equal(t, uint32(100), msg.Order.GetGoodTilBlock())
	require.True(t, p.IsShortTermCLOB)
}

func TestBuildPlaceMarketOrder_ExplicitWorstPrice(t *testing.T) {
	cache := newBtcCache(t)
	msg, p, err := builder.BuildPlaceMarketOrder(builder.PlaceMarketOrderInput{
		Owner:           testOwner,
		Ticker:          "BTC-USD",
		Side:            "BUY",
		HumanSize:       "0.05",
		WorstPrice:      "66000.00",
		GoodTilBlock:    100,
		OrderClientID:   2,
		PayloadClientID: "uuid-market-1",
	}, cache, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, clobtypes.Order_TIME_IN_FORCE_IOC, msg.Order.TimeInForce, "market = IOC limit")
	require.True(t, p.IsShortTermCLOB)
	// 66000.00 → 6_600_000_000 subticks, already a multiple of SubticksPerTick=1000.
	require.Equal(t, uint64(6_600_000_000), msg.Order.Subticks)
}

func TestBuildPlaceMarketOrder_OraclePlusSlippage(t *testing.T) {
	cache := newBtcCache(t)
	msg, _, err := builder.BuildPlaceMarketOrder(builder.PlaceMarketOrderInput{
		Owner:           testOwner,
		Ticker:          "BTC-USD",
		Side:            "BUY",
		HumanSize:       "0.05",
		OraclePrice:     "65000.00",
		SlippageBps:     100, // 1%
		GoodTilBlock:    100,
		OrderClientID:   3,
		PayloadClientID: "uuid-market-2",
	}, cache, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	// 65000 * 1.01 = 65650 → 6_565_000_000 subticks (tick-snap exact).
	require.Equal(t, uint64(6_565_000_000), msg.Order.Subticks)
	require.Equal(t, clobtypes.Order_TIME_IN_FORCE_IOC, msg.Order.TimeInForce)
}

func TestBuildPlaceMarketOrder_RequiresPriceStrategy(t *testing.T) {
	cache := newBtcCache(t)
	_, _, err := builder.BuildPlaceMarketOrder(builder.PlaceMarketOrderInput{
		Owner:           testOwner,
		Ticker:          "BTC-USD",
		Side:            "BUY",
		HumanSize:       "0.05",
		GoodTilBlock:    100,
		OrderClientID:   4,
		PayloadClientID: "uuid-market-3",
	}, cache, newTestAsm(t), 7, 17)
	require.Error(t, err)
	require.Contains(t, err.Error(), "worst_price")
}

func TestBuildPlaceConditionalOrder_StopLoss(t *testing.T) {
	cache := newBtcCache(t)
	msg, p, err := builder.BuildPlaceConditionalOrder(builder.PlaceConditionalOrderInput{
		Owner:            testOwner,
		Ticker:           "BTC-USD",
		Side:             "SELL",
		HumanSize:        "0.05",
		HumanPrice:       "60000.00", // limit when triggered
		ConditionType:    "STOP_LOSS",
		TriggerPrice:     "60500.00",
		GoodTilBlockTime: 1780000000,
		OrderClientID:    5,
		PayloadClientID:  "uuid-cond",
	}, cache, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, clobtypes.OrderIdFlags_Conditional, msg.Order.OrderId.OrderFlags)
	require.Equal(t, clobtypes.Order_CONDITION_TYPE_STOP_LOSS, msg.Order.ConditionType)
	require.Equal(t, uint64(6_050_000_000), msg.Order.ConditionalOrderTriggerSubticks)
	require.Equal(t, uint32(1780000000), msg.Order.GetGoodTilBlockTime())
	require.False(t, p.IsShortTermCLOB, "conditional orders are stateful")
}

func TestBuildPlaceConditionalOrder_BadConditionType(t *testing.T) {
	cache := newBtcCache(t)
	_, _, err := builder.BuildPlaceConditionalOrder(builder.PlaceConditionalOrderInput{
		Owner:            testOwner,
		Ticker:           "BTC-USD",
		Side:             "SELL",
		HumanSize:        "0.05",
		HumanPrice:       "60000.00",
		ConditionType:    "TRAILING_STOP",
		TriggerPrice:     "60500.00",
		GoodTilBlockTime: 1780000000,
		OrderClientID:    6,
		PayloadClientID:  "uuid-cond-bad",
	}, cache, newTestAsm(t), 7, 17)
	require.Error(t, err)
	require.Contains(t, err.Error(), "condition_type")
}
