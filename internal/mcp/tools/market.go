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

// -- get_oracle_price ---------------------------------------------------

type GetOraclePriceInput struct{}

// GetOraclePriceOutput surfaces the EVM aggregator's latest price as both a
// decimal-adjusted human string and the raw int256, plus the feed metadata
// needed to interpret it.
type GetOraclePriceOutput struct {
	Oracle      string `json:"oracle"`      // 0x aggregator address read
	Description string `json:"description"` // feed label, e.g. "BTC / USD"
	Decimals    int64  `json:"decimals"`    // feed decimals
	Price       string `json:"price"`       // decimal-adjusted answer
	PriceRaw    string `json:"price_raw"`   // raw int256 answer (base units)
	RoundID     string `json:"round_id"`    // latest round id
	UpdatedAt   int64  `json:"updated_at"`  // unix seconds the round was last updated
}

// GetOraclePrice upstream reads an OffChainAggregator price feed over read-only
// eth_calls. This binary has no EVM configuration at all — internal/config
// carries no [evm] section, so Deps.Chain.EVM was always nil and upstream's
// requireOracle could only ever return this same refusal. The tool stays
// registered and advertised because it is on the served agent card, whose
// sha256 is published on chain by agent_self_register; dropping it would force
// an agent_self_update on every deployment for no behavioural gain.
//
// The authorize call must stay ahead of the refusal: an unauthenticated caller
// gets ErrNoTenant, exactly as before. That ordering is also why this stays a
// handler rather than a refusing entry in internal/toolbridge, which would
// refuse before authorizing.
func (h *Handlers) GetOraclePrice(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ GetOraclePriceInput,
) (*mcp.CallToolResult, GetOraclePriceOutput, error) {
	if _, err := h.authorize(ctx, "get_oracle_price"); err != nil {
		return nil, GetOraclePriceOutput{}, err
	}
	return nil, GetOraclePriceOutput{}, userErrf("EVM is not enabled on this server (no evm_rpc_url configured)")
}
