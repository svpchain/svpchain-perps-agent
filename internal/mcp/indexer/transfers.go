package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// TransfersResponse wraps Comlink's TransferResponse. Transfers are passed
// through as untyped objects (map[string]any) so the MCP output schema is a
// valid object schema — see CandlesResponse.
type TransfersResponse struct {
	Transfers    []map[string]any `json:"transfers"`
	PageSize     uint32           `json:"pageSize,omitempty"`
	TotalResults uint32           `json:"totalResults,omitempty"`
	Offset       uint32           `json:"offset,omitempty"`
}

// GetTransfers fetches GET /v4/transfers?address=&subaccountNumber=
func (c *Client) GetTransfers(ctx context.Context, address string, subaccountNumber uint32) (*TransfersResponse, error) {
	q := url.Values{}
	q.Set("address", address)
	q.Set("subaccountNumber", fmt.Sprintf("%d", subaccountNumber))
	var resp TransfersResponse
	if err := c.get(ctx, "/transfers", q, &resp); err != nil {
		return nil, fmt.Errorf("GetTransfers %s/%d: %w", address, subaccountNumber, err)
	}
	// Non-nil so an empty result marshals to [] not null (fails "type":"array").
	if resp.Transfers == nil {
		resp.Transfers = []map[string]any{}
	}
	return &resp, nil
}
