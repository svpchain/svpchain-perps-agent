package units

import (
	"fmt"
	"math/big"

	"github.com/dydxprotocol/v4-chain/protocol/lib"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
)

// AggressiveSubticks derives the worst-price subticks for an IOC "market"
// order. svpchain (and dYdX) has no native market-order type — a market
// order is an IOC limit order placed at a worst price the agent is
// willing to fill at. We push the worst price far enough that the order
// matches against the resting book up to the slippage cap:
//
//	BUY market:  worst = oraclePx * (1 + slippageBps/10_000)   (price up)
//	SELL market: worst = oraclePx * (1 - slippageBps/10_000)   (price down)
//
// The result is converted to subticks via PriceToSubticks, then snapped
// down to a multiple of SubticksPerTick for BUYs (so the limit doesn't
// exceed the worst we committed to) and snapped up for SELLs.
//
// slippageBps is bounded by the caller — typical values are 10 (0.1%) to
// 1000 (10%); 100 (1%) is a reasonable default for spot-equivalent CEX
// behaviour. A 0 slippage means the order must fill at exactly oracle
// price (rarely useful for IOC).
//
// side must be "BUY" or "SELL"; any other value is an input error.
func AggressiveSubticks(side, oraclePx string, slippageBps uint32, meta markets.MarketMeta) (uint64, error) {
	if side != "BUY" && side != "SELL" {
		return 0, fmt.Errorf("invalid side %q (want BUY or SELL)", side)
	}
	rat, ok := new(big.Rat).SetString(oraclePx)
	if !ok {
		return 0, fmt.Errorf("invalid oracle price %q", oraclePx)
	}
	// worst = oraclePx * (10_000 ± slippageBps) / 10_000
	const bpsDenom = int64(10_000)
	var factor *big.Rat
	if side == "BUY" {
		factor = new(big.Rat).SetFrac64(bpsDenom+int64(slippageBps), bpsDenom)
	} else {
		if int64(slippageBps) >= bpsDenom {
			return 0, fmt.Errorf("slippage_bps %d would push worst price negative for SELL", slippageBps)
		}
		factor = new(big.Rat).SetFrac64(bpsDenom-int64(slippageBps), bpsDenom)
	}
	worst := new(big.Rat).Mul(rat, factor)

	// Convert worst (big.Rat) to subticks using the same formula as
	// PriceToSubticks: subticks = worst * 10^(-QuantumConversionExponent +
	// AtomicResolution - QuoteAtomicResolution).
	exponent := -meta.QuantumConversionExponent + meta.AtomicResolution - lib.QuoteCurrencyAtomicResolution
	num := lib.BigIntMulPow10(new(big.Int).Set(worst.Num()), exponent, false)
	num.Quo(num, worst.Denom())

	if num.Sign() < 0 || !num.IsUint64() {
		return 0, fmt.Errorf("aggressive subticks out of uint64 range for oracle=%q slippage=%d bps",
			oraclePx, slippageBps)
	}
	subticks := num.Uint64()

	// Snap to a multiple of SubticksPerTick. BUYs snap DOWN (don't exceed
	// the agent-declared worst), SELLs snap UP (don't undercut). This
	// matches the chain's MsgPlaceOrder.ValidateBasic alignment check
	// while preserving the worst-price guarantee.
	if meta.SubticksPerTick > 0 {
		tick := uint64(meta.SubticksPerTick)
		if side == "BUY" {
			subticks = (subticks / tick) * tick
		} else {
			rem := subticks % tick
			if rem != 0 {
				subticks += tick - rem
			}
		}
	}
	if subticks == 0 {
		return 0, fmt.Errorf("aggressive subticks rounded to 0 (oracle=%q slippage=%d bps too small relative to SubticksPerTick=%d)",
			oraclePx, slippageBps, meta.SubticksPerTick)
	}
	return subticks, nil
}
