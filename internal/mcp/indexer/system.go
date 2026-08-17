package indexer

import (
	"context"
	"fmt"
)

// HeightResponse mirrors Comlink's HeightResponse — the indexer's latest
// ingested block height and the corresponding block time.
type HeightResponse struct {
	Height string `json:"height"`
	Time   string `json:"time"` // RFC3339
}

// TimeResponse mirrors Comlink's TimeResponse — the indexer's wall-clock
// time, useful as a freshness sentinel.
type TimeResponse struct {
	ISO   string  `json:"iso"`
	Epoch float64 `json:"epoch"` // seconds since 1970, with millisecond fraction
}

// GetHeight fetches GET /v4/height.
func (c *Client) GetHeight(ctx context.Context) (*HeightResponse, error) {
	var resp HeightResponse
	if err := c.get(ctx, "/height", nil, &resp); err != nil {
		return nil, fmt.Errorf("GetHeight: %w", err)
	}
	return &resp, nil
}

// GetTime fetches GET /v4/time.
func (c *Client) GetTime(ctx context.Context) (*TimeResponse, error) {
	var resp TimeResponse
	if err := c.get(ctx, "/time", nil, &resp); err != nil {
		return nil, fmt.Errorf("GetTime: %w", err)
	}
	return &resp, nil
}
