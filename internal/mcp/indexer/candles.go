package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// CandlesResponse wraps Comlink's GET /v4/candles/perpetualMarkets/:ticker
// response. Individual candles are passed through as untyped objects
// (map[string]any) so the agent sees Comlink's fields as-is and we don't have
// to track field drift.
//
// The element type must be map[string]any, not json.RawMessage or a bare any:
//   - json.RawMessage ([]byte) reflects to a "type: array" schema, which the
//     go-sdk then rejects real object candles against (server-side output
//     validation fails).
//   - a bare any reflects to the boolean schema `true`, which the go-sdk emits
//     at the property/items position; strict MCP clients reject a boolean there
//     ("Invalid input") and refuse the whole tools/list.
//
// map[string]any reflects to {"type":"object","additionalProperties":true} — a
// valid schema object that still accepts any candle shape.
type CandlesResponse struct {
	Candles []map[string]any `json:"candles"`
}

// GetCandlesArgs are the query params accepted by Comlink (all optional).
type GetCandlesArgs struct {
	Resolution string // e.g. "1MIN", "5MINS", "1HOUR", "1DAY"
	Limit      uint32
	FromISO    string // RFC3339
	ToISO      string // RFC3339
}

// GetCandles fetches GET /v4/candles/perpetualMarkets/:ticker.
func (c *Client) GetCandles(ctx context.Context, ticker string, args GetCandlesArgs) (*CandlesResponse, error) {
	q := url.Values{}
	if args.Resolution != "" {
		q.Set("resolution", args.Resolution)
	}
	if args.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", args.Limit))
	}
	if args.FromISO != "" {
		q.Set("fromISO", args.FromISO)
	}
	if args.ToISO != "" {
		q.Set("toISO", args.ToISO)
	}
	var resp CandlesResponse
	if err := c.get(ctx, "/candles/perpetualMarkets/"+ticker, q, &resp); err != nil {
		return nil, fmt.Errorf("GetCandles %q: %w", ticker, err)
	}
	// Non-nil so an empty result marshals to [] not null (fails "type":"array").
	if resp.Candles == nil {
		resp.Candles = []map[string]any{}
	}
	return &resp, nil
}
