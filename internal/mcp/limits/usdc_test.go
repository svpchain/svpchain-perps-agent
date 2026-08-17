package limits

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dydxprotocol/v4-chain/protocol/lib"
)

// Guards the limits package's hard-coded 10^6 against drift from
// lib.QuoteCurrencyAtomicResolution — if the chain ever changes USDC's
// resolution, this test breaks loudly instead of silently miscapping.
func TestQuantumsPerUSDC_MatchesProtocolConstant(t *testing.T) {
	require.Equal(t, int32(-6), lib.QuoteCurrencyAtomicResolution)
	require.Equal(t, uint64(1_000_000), uint64(quantumsPerUSDC))
}

func TestHumanToQuantums(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"1", 1_000_000},
		{"1.0", 1_000_000},
		{"1.5", 1_500_000},
		{"0.000001", 1},
		{"0.5", 500_000},
		{".5", 500_000},
		{"5000", 5_000_000_000},
		{"0", 0},
		{"0.000000", 0},
	}
	for _, tc := range cases {
		got, err := HumanToQuantums(tc.in)
		require.NoError(t, err, "in=%s", tc.in)
		require.Equal(t, tc.want, got, "in=%s", tc.in)
	}
}

func TestHumanToQuantums_Rejects(t *testing.T) {
	cases := []struct {
		in      string
		wantErr string
	}{
		{"", "empty"},
		{"-5", "non-negative"},
		{"1.2345678", "more than 6"},
		{"abc", "invalid"},
		{"1.5e3", "invalid"},
	}
	for _, tc := range cases {
		_, err := HumanToQuantums(tc.in)
		require.Error(t, err, "in=%s", tc.in)
		require.Contains(t, err.Error(), tc.wantErr, "in=%s", tc.in)
	}
}

func TestQuantumsToHuman(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{1_000_000, "1.000000"},
		{1_500_000, "1.500000"},
		{1, "0.000001"},
		{0, "0.000000"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, QuantumsToHuman(tc.in), "in=%d", tc.in)
	}
}
