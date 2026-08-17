package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// PerpetualMarket mirrors Comlink's PerpetualMarketResponseObject
// (indexer/services/comlink/src/types.ts). Field names match the on-wire
// JSON exactly so we can pass-through responses to MCP clients verbatim
// while still letting Go code consume the conversion constants the markets
// cache and unit-conversion helpers need.
type PerpetualMarket struct {
	ClobPairID                string `json:"clobPairId"`
	Ticker                    string `json:"ticker"`
	Status                    string `json:"status"`
	OraclePrice               string `json:"oraclePrice"`
	PriceChange24H            string `json:"priceChange24H"`
	Volume24H                 string `json:"volume24H"`
	Trades24H                 int64  `json:"trades24H"`
	NextFundingRate           string `json:"nextFundingRate"`
	InitialMarginFraction     string `json:"initialMarginFraction"`
	MaintenanceMarginFraction string `json:"maintenanceMarginFraction"`
	OpenInterest              string `json:"openInterest"`
	AtomicResolution          int32  `json:"atomicResolution"`
	QuantumConversionExponent int32  `json:"quantumConversionExponent"`
	TickSize                  string `json:"tickSize"`
	StepSize                  string `json:"stepSize"`
	StepBaseQuantums          uint64 `json:"stepBaseQuantums"`
	SubticksPerTick           uint32 `json:"subticksPerTick"`
	MarketType                string `json:"marketType"`
	OpenInterestLowerCap      string `json:"openInterestLowerCap,omitempty"`
	OpenInterestUpperCap      string `json:"openInterestUpperCap,omitempty"`
	BaseOpenInterest          string `json:"baseOpenInterest"`
	DefaultFundingRate1H      string `json:"defaultFundingRate1H,omitempty"`
}

// PerpetualMarketsResponse is the wrapper returned by GET /v4/perpetualMarkets
// — a map of ticker → market.
type PerpetualMarketsResponse struct {
	Markets map[string]PerpetualMarket `json:"markets"`
}

// ListPerpetualMarkets fetches GET /v4/perpetualMarkets (no filter). Used
// by the list_markets MCP tool and by the markets cache to populate
// ticker↔clobPairId.
func (c *Client) ListPerpetualMarkets(ctx context.Context) (*PerpetualMarketsResponse, error) {
	var resp PerpetualMarketsResponse
	if err := c.get(ctx, "/perpetualMarkets", nil, &resp); err != nil {
		return nil, fmt.Errorf("ListPerpetualMarkets: %w", err)
	}
	// Non-nil so an empty map marshals to {} not null (fails "type":"object").
	if resp.Markets == nil {
		resp.Markets = map[string]PerpetualMarket{}
	}
	return &resp, nil
}

// GetPerpetualMarket fetches GET /v4/perpetualMarkets?ticker=<ticker> and
// returns the single matching market (or an error if the indexer returns
// an empty or multi-entry map).
func (c *Client) GetPerpetualMarket(ctx context.Context, ticker string) (*PerpetualMarket, error) {
	q := url.Values{}
	q.Set("ticker", ticker)
	var resp PerpetualMarketsResponse
	if err := c.get(ctx, "/perpetualMarkets", q, &resp); err != nil {
		return nil, fmt.Errorf("GetPerpetualMarket %q: %w", ticker, err)
	}
	mkt, ok := resp.Markets[ticker]
	if !ok {
		return nil, fmt.Errorf("GetPerpetualMarket %q: not found in response", ticker)
	}
	return &mkt, nil
}
