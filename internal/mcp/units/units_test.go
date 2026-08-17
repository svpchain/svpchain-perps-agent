package units_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/units"
)

// btcMeta mirrors a typical BTC-USD perp on svpchain: AtomicResolution=-10,
// QuantumConversionExponent=-9, StepBaseQuantums=1_000_000, SubticksPerTick=1_000.
// Note: lib.QuoteCurrencyAtomicResolution = -6 (USDC).
func btcMeta() markets.MarketMeta {
	return markets.MarketMeta{
		Ticker:                    "BTC-USD",
		ClobPairID:                0,
		AtomicResolution:          -10,
		QuantumConversionExponent: -9,
		StepBaseQuantums:          1_000_000,
		SubticksPerTick:           1_000,
	}
}

func TestSizeToQuantums(t *testing.T) {
	meta := btcMeta()
	cases := []struct {
		name    string
		size    string
		want    uint64
		wantErr string
	}{
		// quantums = size * 10^(-AtomicResolution) = size * 10^10
		// 0.001 BTC = 10_000_000 quantums; multiple of step=1_000_000.
		{name: "0.001 BTC aligned", size: "0.001", want: 10_000_000},
		// 0.05 BTC = 500_000_000 quantums.
		{name: "0.05 BTC aligned", size: "0.05", want: 500_000_000},
		// 0.0001 BTC = 1_000_000 quantums (right at step boundary).
		{name: "step boundary", size: "0.0001", want: 1_000_000},
		// Misaligned: 0.0005 BTC = 5_000_000 quantums but step is 1_000_000.
		// Wait - 5_000_000 / 1_000_000 = 5, exact. So 0.0005 is aligned.
		// Try 0.000055 → 550_000 quantums, not a multiple of 1_000_000.
		{name: "below step granularity", size: "0.000055", wantErr: "StepBaseQuantums"},
		// Garbage input.
		{name: "non-numeric", size: "abc", wantErr: "invalid size"},
		// Negative.
		{name: "negative", size: "-0.001", wantErr: "out of uint64 range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := units.SizeToQuantums(tc.size, meta)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestPriceToSubticks(t *testing.T) {
	meta := btcMeta()
	// Formula: subticks = price * 10^(-QuantumConversionExponent + AtomicResolution - QuoteAtomicResolution)
	//                   = price * 10^(-(-9) + (-10) - (-6))
	//                   = price * 10^(9 - 10 + 6)
	//                   = price * 10^5
	// SubticksPerTick = 1_000, so subticks must be a multiple of 1_000.
	cases := []struct {
		name    string
		price   string
		want    uint64
		wantErr string
	}{
		// 65000.00 * 10^5 = 6_500_000_000; multiple of 1_000.
		{name: "65000.00 aligned", price: "65000.00", want: 6_500_000_000},
		// 1.00 * 10^5 = 100_000; multiple of 1_000.
		{name: "1.00 aligned", price: "1.00", want: 100_000},
		// 0.01 * 10^5 = 1_000 (right at tick).
		{name: "tick boundary", price: "0.01", want: 1_000},
		// 0.001 * 10^5 = 100; below the tick → error.
		{name: "below tick granularity", price: "0.001", wantErr: "SubticksPerTick"},
		// Garbage input.
		{name: "non-numeric", price: "not-a-price", wantErr: "invalid price"},
		// Negative.
		{name: "negative", price: "-1.00", wantErr: "out of uint64 range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := units.PriceToSubticks(tc.price, meta)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestUnits_ZeroStepBypassesAlignment(t *testing.T) {
	// If StepBaseQuantums == 0 (unset / unknown market), the alignment
	// check is skipped — useful for synthetic tests against partial metadata.
	meta := btcMeta()
	meta.StepBaseQuantums = 0
	_, err := units.SizeToQuantums("0.000055", meta)
	require.NoError(t, err, "size alignment check must be skipped when StepBaseQuantums == 0")

	meta = btcMeta()
	meta.SubticksPerTick = 0
	_, err = units.PriceToSubticks("0.001", meta)
	require.NoError(t, err, "price alignment check must be skipped when SubticksPerTick == 0")
}
