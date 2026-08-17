package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// FillsResponse wraps Comlink's FillResponse. Fills are passed through as
// untyped objects (map[string]any) so the MCP output schema is a valid object
// schema — see CandlesResponse.
type FillsResponse struct {
	Fills        []map[string]any `json:"fills"`
	PageSize     uint32           `json:"pageSize,omitempty"`
	TotalResults uint32           `json:"totalResults,omitempty"`
	Offset       uint32           `json:"offset,omitempty"`
}

// GetFills fetches GET /v4/fills?address=&subaccountNumber=&market=
func (c *Client) GetFills(ctx context.Context, address string, subaccountNumber uint32, market string) (*FillsResponse, error) {
	q := url.Values{}
	q.Set("address", address)
	q.Set("subaccountNumber", fmt.Sprintf("%d", subaccountNumber))
	if market != "" {
		q.Set("market", market)
	}
	var resp FillsResponse
	if err := c.get(ctx, "/fills", q, &resp); err != nil {
		return nil, fmt.Errorf("GetFills %s/%d: %w", address, subaccountNumber, err)
	}
	// A nil slice marshals to JSON null, which fails the MCP output schema's
	// "type":"array" (Comlink returns null/omits the key when there are none).
	if resp.Fills == nil {
		resp.Fills = []map[string]any{}
	}
	return &resp, nil
}
