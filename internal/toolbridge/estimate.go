package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/svpchain/svpchain-perps-agent/internal/marketdata"
	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// EstimateClearingPrice is the one operation this agent computes rather than
// proxies. It reads the book through the MCP server, as the caller, and walks
// it locally.
const EstimateClearingPrice = "estimate_clearing_price"

// EstimateInput is the tool's arguments.
type EstimateInput struct {
	Ticker string `json:"ticker" jsonschema:"market to price against, e.g. \"BTC-USD\""`
	Side   string `json:"side" jsonschema:"which way the taker order crosses the book: \"buy\" or \"sell\""`
	Size   string `json:"size" jsonschema:"base size to price, as a decimal string, e.g. \"2.5\""`
}

// registerEstimate adds the clearing-price estimate to the market-data family.
//
// ★ It is advertised on the card alongside the proxied market-data tools, but
// no MCP server serves it, so agentOwnedTools has to name it or the boot-time
// catalog check reads it as an operation the card promises and the server
// cannot answer.
func (r *Registry) registerEstimate(mcp *mcpclient.Client) {
	r.add(SkillMarketData, EstimateClearingPrice, Bound{
		InputSchema: schemaFor[EstimateInput](),
		Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in EstimateInput
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			side, err := parseSide(in.Side)
			if err != nil {
				return nil, err
			}
			if mcp == nil {
				return nil, fmt.Errorf("%s: this registry has no MCP server behind it", EstimateClearingPrice)
			}

			// The book comes from the server as the caller, so this estimate
			// is gated exactly like the tool it reads.
			id, _ := mcpclient.CallerFrom(ctx)
			args, err := json.Marshal(map[string]string{"ticker": in.Ticker})
			if err != nil {
				return nil, err
			}
			res, err := mcp.CallTool(ctx, mcpclient.Call{
				Tool: "get_orderbook", Args: args, Identity: id, Idempotent: true,
			})
			if err != nil {
				return nil, err
			}
			if res.IsError || len(res.Structured) == 0 {
				if res.Text != "" {
					return nil, fmt.Errorf("%s", res.Text)
				}
				return nil, fmt.Errorf("get_orderbook returned no result")
			}

			var book struct {
				Orderbook marketdata.Orderbook `json:"orderbook"`
			}
			if err := json.Unmarshal(res.Structured, &book); err != nil {
				return nil, fmt.Errorf("decode orderbook: %w", err)
			}
			return marketdata.EstimateClearing(in.Ticker, side, &book.Orderbook, in.Size)
		},
	})
}

func parseSide(s string) (marketdata.Side, error) {
	switch marketdata.Side(s) {
	case marketdata.Buy:
		return marketdata.Buy, nil
	case marketdata.Sell:
		return marketdata.Sell, nil
	default:
		return "", fmt.Errorf("side must be %q or %q, got %q", marketdata.Buy, marketdata.Sell, s)
	}
}
