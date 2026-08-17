package limits

import (
	"fmt"
	"math/big"
	"strings"
)

// quantumsPerUSDC is 10^6 — see lib.QuoteCurrencyAtomicResolution.
// Hard-coded here so the limits package stays a leaf with no protocol-side
// imports; the round-trip test in usdc_test.go guards against the two
// values drifting apart.
const quantumsPerUSDC = 1_000_000

// HumanToQuantums converts a USDC amount in human units (e.g. "1.50") to
// chain-side atomic quantums. Accepts up to 6 decimal places; rejects more
// (cosmos rounds down silently, which we don't want for cap math).
func HumanToQuantums(human string) (uint64, error) {
	s := strings.TrimSpace(human)
	if s == "" {
		return 0, fmt.Errorf("usdc amount is empty")
	}
	neg := strings.HasPrefix(s, "-")
	if neg {
		return 0, fmt.Errorf("usdc amount must be non-negative: %s", human)
	}
	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" && !hasFrac {
		return 0, fmt.Errorf("invalid usdc amount: %s", human)
	}
	if intPart == "" {
		intPart = "0"
	}
	if hasFrac && len(fracPart) > 6 {
		return 0, fmt.Errorf("usdc amount has more than 6 decimal places: %s", human)
	}
	// pad fractional part to exactly 6 digits, then concatenate.
	for len(fracPart) < 6 {
		fracPart += "0"
	}
	combined := intPart + fracPart
	n, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return 0, fmt.Errorf("invalid usdc amount: %s", human)
	}
	if !n.IsUint64() {
		return 0, fmt.Errorf("usdc amount overflows uint64: %s", human)
	}
	return n.Uint64(), nil
}

// QuantumsToHuman is the inverse of HumanToQuantums. Always renders with 6
// decimal places — callers can trim trailing zeros if they prefer.
func QuantumsToHuman(q uint64) string {
	whole := q / quantumsPerUSDC
	frac := q % quantumsPerUSDC
	return fmt.Sprintf("%d.%06d", whole, frac)
}
