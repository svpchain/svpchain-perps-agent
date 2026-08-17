package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// TradesResponse mirrors Comlink's TradeResponse. Trades are passed through as
// untyped objects (map[string]any) so the MCP output schema is a valid object
// schema — see CandlesResponse.
type TradesResponse struct {
	Trades       []map[string]any `json:"trades"`
	PageSize     uint32           `json:"pageSize,omitempty"`
	TotalResults uint32           `json:"totalResults,omitempty"`
	Offset       uint32           `json:"offset,omitempty"`
}

// GetTrades fetches GET /v4/trades/perpetualMarket/:ticker.
func (c *Client) GetTrades(ctx context.Context, ticker string, limit uint32) (*TradesResponse, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	var resp TradesResponse
	if err := c.get(ctx, "/trades/perpetualMarket/"+ticker, q, &resp); err != nil {
		return nil, fmt.Errorf("GetTrades %q: %w", ticker, err)
	}
	// Non-nil so an empty result marshals to [] not null (fails "type":"array").
	if resp.Trades == nil {
		resp.Trades = []map[string]any{}
	}
	return &resp, nil
}
