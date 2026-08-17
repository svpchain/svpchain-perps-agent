// Package units handles the human-direction ↔ chain-direction unit
// conversion that every build_* trading tool needs: size ↔ quantums and
// price ↔ subticks.
//
// It is a thin wrapper over lib.BaseToQuoteQuantums / lib.QuoteToBaseQuantums
// (protocol/lib/quantums.go) and clobtypes.PriceToSubticks /
// SubticksToPrice (protocol/x/clob/types/price_to_subticks.go), with extra
// rounding/alignment to ClobPair.StepBaseQuantums and SubticksPerTick and
// clear error reporting when inputs cannot be safely aligned.
//
// market_order.go derives the aggressive IOC subticks used by
// build_place_market_order (slippageBps / worstPrice).
package units
