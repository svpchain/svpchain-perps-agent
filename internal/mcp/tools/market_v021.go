package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/indexer"
)

// v0.2.1 read-catalog extensions. Tools that need a per-owner check stay
// in account_v021.go; market-data tools (no owner involved) live here.

// -- get_candles -------------------------------------------------------

type GetCandlesInput struct {
	Ticker     string `json:"ticker" jsonschema:"perpetual market ticker, e.g. BTC-USD"`
	Resolution string `json:"resolution,omitempty" jsonschema:"e.g. 1MIN | 5MINS | 15MINS | 30MINS | 1HOUR | 4HOURS | 1DAY"`
	Limit      uint32 `json:"limit,omitempty"`
	FromISO    string `json:"from_iso,omitempty" jsonschema:"RFC3339 lower bound, inclusive"`
	ToISO      string `json:"to_iso,omitempty" jsonschema:"RFC3339 upper bound, exclusive"`
}
type GetCandlesOutput struct {
	Candles indexer.CandlesResponse `json:"candles"`
}

func (h *Handlers) GetCandles(
	ctx context.Context, _ *mcp.CallToolRequest, in GetCandlesInput,
) (*mcp.CallToolResult, GetCandlesOutput, error) {
	if _, err := h.authorize(ctx, "get_candles"); err != nil {
		return nil, GetCandlesOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetCandles(ctx, in.Ticker, indexer.GetCandlesArgs{
		Resolution: in.Resolution,
		Limit:      in.Limit,
		FromISO:    in.FromISO,
		ToISO:      in.ToISO,
	})
	if err != nil {
		return nil, GetCandlesOutput{}, err
	}
	return nil, GetCandlesOutput{Candles: *resp}, nil
}

// -- get_trades --------------------------------------------------------

type GetTradesInput struct {
	Ticker string `json:"ticker"`
	Limit  uint32 `json:"limit,omitempty"`
}
type GetTradesOutput struct {
	Trades indexer.TradesResponse `json:"trades"`
}

func (h *Handlers) GetTrades(
	ctx context.Context, _ *mcp.CallToolRequest, in GetTradesInput,
) (*mcp.CallToolResult, GetTradesOutput, error) {
	if _, err := h.authorize(ctx, "get_trades"); err != nil {
		return nil, GetTradesOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetTrades(ctx, in.Ticker, in.Limit)
	if err != nil {
		return nil, GetTradesOutput{}, err
	}
	return nil, GetTradesOutput{Trades: *resp}, nil
}

// -- get_sparklines ----------------------------------------------------

type GetSparklinesInput struct {
	TimePeriod string `json:"time_period,omitempty" jsonschema:"ONE_DAY | SEVEN_DAYS (defaults to indexer's default when empty)"`
}
type GetSparklinesOutput struct {
	Sparklines indexer.SparklinesResponse `json:"sparklines"`
}

func (h *Handlers) GetSparklines(
	ctx context.Context, _ *mcp.CallToolRequest, in GetSparklinesInput,
) (*mcp.CallToolResult, GetSparklinesOutput, error) {
	if _, err := h.authorize(ctx, "get_sparklines"); err != nil {
		return nil, GetSparklinesOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetSparklines(ctx, in.TimePeriod)
	if err != nil {
		return nil, GetSparklinesOutput{}, err
	}
	return nil, GetSparklinesOutput{Sparklines: resp}, nil
}

// -- get_historical_funding --------------------------------------------

type GetHistoricalFundingInput struct {
	Ticker string `json:"ticker"`
}
type GetHistoricalFundingOutput struct {
	HistoricalFunding indexer.HistoricalFundingResponse `json:"historical_funding"`
}

func (h *Handlers) GetHistoricalFunding(
	ctx context.Context, _ *mcp.CallToolRequest, in GetHistoricalFundingInput,
) (*mcp.CallToolResult, GetHistoricalFundingOutput, error) {
	if _, err := h.authorize(ctx, "get_historical_funding"); err != nil {
		return nil, GetHistoricalFundingOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetHistoricalFunding(ctx, in.Ticker)
	if err != nil {
		return nil, GetHistoricalFundingOutput{}, err
	}
	return nil, GetHistoricalFundingOutput{HistoricalFunding: *resp}, nil
}

// -- get_height --------------------------------------------------------

type GetHeightInput struct{}
type GetHeightOutput struct {
	Height indexer.HeightResponse `json:"height"`
}

func (h *Handlers) GetHeight(
	ctx context.Context, _ *mcp.CallToolRequest, _ GetHeightInput,
) (*mcp.CallToolResult, GetHeightOutput, error) {
	if _, err := h.authorize(ctx, "get_height"); err != nil {
		return nil, GetHeightOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetHeight(ctx)
	if err != nil {
		return nil, GetHeightOutput{}, err
	}
	return nil, GetHeightOutput{Height: *resp}, nil
}

// -- get_time ----------------------------------------------------------

type GetTimeInput struct{}
type GetTimeOutput struct {
	Time indexer.TimeResponse `json:"time"`
}

func (h *Handlers) GetTime(
	ctx context.Context, _ *mcp.CallToolRequest, _ GetTimeInput,
) (*mcp.CallToolResult, GetTimeOutput, error) {
	if _, err := h.authorize(ctx, "get_time"); err != nil {
		return nil, GetTimeOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetTime(ctx)
	if err != nil {
		return nil, GetTimeOutput{}, err
	}
	return nil, GetTimeOutput{Time: *resp}, nil
}
