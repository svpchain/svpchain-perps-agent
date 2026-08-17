package units

import (
	"fmt"
	"math/big"

	"github.com/dydxprotocol/v4-chain/protocol/lib"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
)

// SizeToQuantums converts a human-readable base-currency size (e.g. "0.05" BTC)
// to the chain's quantums. The result must be an exact multiple of
// meta.StepBaseQuantums — fractional residue is reported as an error rather
// than silently rounded, because the chain will reject misaligned orders
// at MsgPlaceOrder.ValidateBasic time.
//
// Formula (mirrors testutil/perpetuals.MustHumanSizeToBaseQuantums):
//
//	quantums = humanSize * 10^(-AtomicResolution)
func SizeToQuantums(humanSize string, meta markets.MarketMeta) (uint64, error) {
	rat, ok := new(big.Rat).SetString(humanSize)
	if !ok {
		return 0, fmt.Errorf("invalid size %q", humanSize)
	}
	// numerator * 10^(-AtomicResolution), then divide by denominator.
	result := lib.BigIntMulPow10(new(big.Int).Set(rat.Num()), -meta.AtomicResolution, false)
	result.Quo(result, rat.Denom())
	if result.Sign() < 0 || !result.IsUint64() {
		return 0, fmt.Errorf("size %q out of uint64 range after conversion", humanSize)
	}
	quantums := result.Uint64()
	if meta.StepBaseQuantums > 0 && quantums%meta.StepBaseQuantums != 0 {
		return 0, fmt.Errorf("size %q (%d quantums) is not a multiple of StepBaseQuantums (%d)",
			humanSize, quantums, meta.StepBaseQuantums)
	}
	return quantums, nil
}

// PriceToSubticks converts a human-readable price (e.g. "65000.00") to the
// chain's subticks. The result must be an exact multiple of
// meta.SubticksPerTick.
//
// Derivation: starting from x/clob/types/price_to_subticks.go's
//
//	subticks = marketPrice.Price * 10^(marketPrice.Exponent
//	          - QuantumConversionExponent + AtomicResolution - QuoteAtomicResolution)
//
// and substituting humanPrice = marketPrice.Price * 10^marketPrice.Exponent,
// the marketPrice exponent terms cancel and we get:
//
//	subticks = humanPrice * 10^(-QuantumConversionExponent
//	                          + AtomicResolution - QuoteAtomicResolution)
//
// where QuoteAtomicResolution = lib.QuoteCurrencyAtomicResolution (= -6 for USDC).
func PriceToSubticks(humanPrice string, meta markets.MarketMeta) (uint64, error) {
	rat, ok := new(big.Rat).SetString(humanPrice)
	if !ok {
		return 0, fmt.Errorf("invalid price %q", humanPrice)
	}
	exponent := -meta.QuantumConversionExponent + meta.AtomicResolution - lib.QuoteCurrencyAtomicResolution
	result := lib.BigIntMulPow10(new(big.Int).Set(rat.Num()), exponent, false)
	result.Quo(result, rat.Denom())
	if result.Sign() < 0 || !result.IsUint64() {
		return 0, fmt.Errorf("price %q out of uint64 range after conversion", humanPrice)
	}
	subticks := result.Uint64()
	if meta.SubticksPerTick > 0 && subticks%uint64(meta.SubticksPerTick) != 0 {
		return 0, fmt.Errorf("price %q (%d subticks) is not a multiple of SubticksPerTick (%d)",
			humanPrice, subticks, meta.SubticksPerTick)
	}
	return subticks, nil
}
