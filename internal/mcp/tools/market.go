package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/indexer"
)

// -- list_markets -------------------------------------------------------

type ListMarketsInput struct{}

// ListMarketsOutput is a pass-through of the indexer's response.
type ListMarketsOutput struct {
	Markets map[string]indexer.PerpetualMarket `json:"markets" jsonschema:"map of ticker to perpetual market"`
}

func (h *Handlers) ListMarkets(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ ListMarketsInput,
) (*mcp.CallToolResult, ListMarketsOutput, error) {
	if _, err := h.authorize(ctx, "list_markets"); err != nil {
		return nil, ListMarketsOutput{}, err
	}
	resp, err := h.Deps.Indexer.ListPerpetualMarkets(ctx)
	if err != nil {
		return nil, ListMarketsOutput{}, err
	}
	return nil, ListMarketsOutput{Markets: resp.Markets}, nil
}

// -- get_market ---------------------------------------------------------

type GetMarketInput struct {
	Ticker string `json:"ticker" jsonschema:"perpetual market ticker, e.g. BTC-USD"`
}
type GetMarketOutput struct {
	Market indexer.PerpetualMarket `json:"market"`
}

func (h *Handlers) GetMarket(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in GetMarketInput,
) (*mcp.CallToolResult, GetMarketOutput, error) {
	if _, err := h.authorize(ctx, "get_market"); err != nil {
		return nil, GetMarketOutput{}, err
	}
	m, err := h.Deps.Indexer.GetPerpetualMarket(ctx, in.Ticker)
	if err != nil {
		return nil, GetMarketOutput{}, err
	}
	return nil, GetMarketOutput{Market: *m}, nil
}

// -- get_orderbook ------------------------------------------------------

type GetOrderbookInput struct {
	Ticker string `json:"ticker" jsonschema:"perpetual market ticker"`
}
type GetOrderbookOutput struct {
	Orderbook indexer.Orderbook `json:"orderbook"`
}

func (h *Handlers) GetOrderbook(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in GetOrderbookInput,
) (*mcp.CallToolResult, GetOrderbookOutput, error) {
	if _, err := h.authorize(ctx, "get_orderbook"); err != nil {
		return nil, GetOrderbookOutput{}, err
	}
	ob, err := h.Deps.Indexer.GetOrderbook(ctx, in.Ticker)
	if err != nil {
		return nil, GetOrderbookOutput{}, err
	}
	return nil, GetOrderbookOutput{Orderbook: *ob}, nil
}
