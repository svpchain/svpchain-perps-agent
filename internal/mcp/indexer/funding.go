package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// HistoricalFundingResponse wraps Comlink's HistoricalFundingResponse. Entries
// are passed through as untyped objects (map[string]any) so the MCP output
// schema is a valid object schema — see CandlesResponse.
type HistoricalFundingResponse struct {
	HistoricalFunding []map[string]any `json:"historicalFunding"`
}

// FundingPaymentsResponse wraps Comlink's FundingPaymentResponse.
type FundingPaymentsResponse struct {
	FundingPayments []map[string]any `json:"fundingPayments"`
}

// GetHistoricalFunding fetches GET /v4/historicalFunding/:ticker.
func (c *Client) GetHistoricalFunding(ctx context.Context, ticker string) (*HistoricalFundingResponse, error) {
	var resp HistoricalFundingResponse
	if err := c.get(ctx, "/historicalFunding/"+ticker, nil, &resp); err != nil {
		return nil, fmt.Errorf("GetHistoricalFunding %q: %w", ticker, err)
	}
	// Non-nil so an empty result marshals to [] not null (fails "type":"array").
	if resp.HistoricalFunding == nil {
		resp.HistoricalFunding = []map[string]any{}
	}
	return &resp, nil
}

// GetFundingPayments fetches GET /v4/fundingPayments?address=&subaccountNumber=
func (c *Client) GetFundingPayments(ctx context.Context, address string, subaccountNumber uint32) (*FundingPaymentsResponse, error) {
	q := url.Values{}
	q.Set("address", address)
	q.Set("subaccountNumber", fmt.Sprintf("%d", subaccountNumber))
	var resp FundingPaymentsResponse
	if err := c.get(ctx, "/fundingPayments", q, &resp); err != nil {
		return nil, fmt.Errorf("GetFundingPayments %s/%d: %w", address, subaccountNumber, err)
	}
	// Non-nil so an empty result marshals to [] not null (fails "type":"array").
	if resp.FundingPayments == nil {
		resp.FundingPayments = []map[string]any{}
	}
	return &resp, nil
}
