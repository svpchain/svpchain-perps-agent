package units_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/units"
)

// Reuses btcMeta() from units_test.go (same package units_test):
// AtomicResolution=-10, QuantumConversionExponent=-9,
// StepBaseQuantums=1_000_000, SubticksPerTick=1_000.
// PriceToSubticks: subticks = price * 10^(9 - 10 + 6) = price * 10^5,
// so oracle=65000 → 6_500_000_000 subticks before tick-snap.

func TestAggressiveSubticks_Buy(t *testing.T) {
	meta := btcMeta()
	cases := []struct {
		name    string
		oracle  string
		bps     uint32
		wantMin uint64 // we assert a range because of tick-snap
		wantMax uint64
	}{
		// 65000 + 1% = 65650 → 6_565_000_000 subticks (snap DOWN to multiple of 1000).
		{name: "1% slippage", oracle: "65000.00", bps: 100, wantMin: 6_565_000_000, wantMax: 6_565_000_000},
		// 65000 + 0.1% = 65065 → 6_506_500_000 → snap DOWN to 6_506_500_000.
		{name: "0.1% slippage", oracle: "65000.00", bps: 10, wantMin: 6_506_500_000, wantMax: 6_506_500_000},
		// 65000 + 10% = 71500 → 7_150_000_000 → snap DOWN.
		{name: "10% slippage", oracle: "65000.00", bps: 1000, wantMin: 7_150_000_000, wantMax: 7_150_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := units.AggressiveSubticks("BUY", tc.oracle, tc.bps, meta)
			require.NoError(t, err)
			require.GreaterOrEqual(t, got, tc.wantMin)
			require.LessOrEqual(t, got, tc.wantMax)
			require.Equal(t, uint64(0), got%uint64(meta.SubticksPerTick), "must be a multiple of SubticksPerTick")
		})
	}
}

func TestAggressiveSubticks_Sell(t *testing.T) {
	meta := btcMeta()
	// 65000 - 1% = 64350 → 6_435_000_000 (snap UP to multiple of 1000).
	got, err := units.AggressiveSubticks("SELL", "65000.00", 100, meta)
	require.NoError(t, err)
	require.Equal(t, uint64(6_435_000_000), got)
	require.Equal(t, uint64(0), got%uint64(meta.SubticksPerTick))
}

func TestAggressiveSubticks_Rejects(t *testing.T) {
	meta := btcMeta()
	cases := []struct {
		name    string
		side    string
		oracle  string
		bps     uint32
		wantErr string
	}{
		{name: "bad side", side: "HOLD", oracle: "65000", bps: 100, wantErr: "invalid side"},
		{name: "bad oracle", side: "BUY", oracle: "not-a-price", bps: 100, wantErr: "invalid oracle price"},
		{name: "SELL slippage ≥ 100%", side: "SELL", oracle: "65000", bps: 10_000, wantErr: "negative for SELL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := units.AggressiveSubticks(tc.side, tc.oracle, tc.bps, meta)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
