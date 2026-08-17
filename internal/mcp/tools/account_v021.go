package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/indexer"
)

// v0.2.1 owner-scoped read extensions. Every handler validates the
// requested address against the tenant owner before calling the indexer,
// matching the v0.1 get_subaccount pattern.

// -- get_orders --------------------------------------------------------

type GetOrdersInput struct {
	Address          string `json:"address" jsonschema:"svp1... bech32 owner address"`
	SubaccountNumber uint32 `json:"subaccount_number"`

	// Optional filters — passed through to Comlink verbatim.
	Status             string `json:"status,omitempty" jsonschema:"e.g. OPEN | FILLED | CANCELED | BEST_EFFORT_OPENED | UNTRIGGERED"`
	Side               string `json:"side,omitempty" jsonschema:"BUY | SELL"`
	Type               string `json:"type,omitempty" jsonschema:"LIMIT | STOP_LIMIT | TRAILING_STOP | TAKE_PROFIT etc."`
	Ticker             string `json:"ticker,omitempty"`
	GoodTilBlockBefore string `json:"good_til_block_before,omitempty"`
	ReturnLatestOrders string `json:"return_latest_orders,omitempty" jsonschema:"true | false"`
}
type GetOrdersOutput struct {
	Orders []indexer.Order `json:"orders"`
}

func (h *Handlers) GetOrders(
	ctx context.Context, _ *mcp.CallToolRequest, in GetOrdersInput,
) (*mcp.CallToolResult, GetOrdersOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_orders", in.Address)
	if err != nil {
		return nil, GetOrdersOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetOrdersOutput{}, err
	}
	filters := map[string]string{
		"status":             in.Status,
		"side":               in.Side,
		"type":               in.Type,
		"ticker":             in.Ticker,
		"goodTilBlockBefore": in.GoodTilBlockBefore,
		"returnLatestOrders": in.ReturnLatestOrders,
	}
	out, err := h.Deps.Indexer.GetOrders(ctx, in.Address, in.SubaccountNumber, filters)
	if err != nil {
		if errors.Is(err, indexer.ErrNotFound) {
			// Fresh subaccount with no open orders — indexer returns 404
			// rather than an empty list. Surface as an empty result.
			// Non-nil empty slice: encoding/json marshals nil as `null`
			// but the MCP SDK's reflection-derived schema requires `array`.
			return nil, GetOrdersOutput{Orders: []indexer.Order{}}, nil
		}
		return nil, GetOrdersOutput{}, err
	}
	return nil, GetOrdersOutput{Orders: out}, nil
}

// -- get_order ---------------------------------------------------------

type GetOrderInput struct {
	OrderID string `json:"order_id" jsonschema:"on-chain order id (hex)"`
}
type GetOrderOutput struct {
	Order indexer.Order `json:"order"`
}

func (h *Handlers) GetOrder(
	ctx context.Context, _ *mcp.CallToolRequest, in GetOrderInput,
) (*mcp.CallToolResult, GetOrderOutput, error) {
	// get_order is a single-id lookup; we don't know whose order it is until
	// after the fetch, so no upfront owner / subaccount check. The result
	// includes the owner if the agent wants to cross-check.
	if _, err := h.authorize(ctx, "get_order"); err != nil {
		return nil, GetOrderOutput{}, err
	}
	out, err := h.Deps.Indexer.GetOrder(ctx, in.OrderID)
	if err != nil {
		return nil, GetOrderOutput{}, err
	}
	return nil, GetOrderOutput{Order: out}, nil
}

// -- get_fills ---------------------------------------------------------

type GetFillsInput struct {
	Address          string `json:"address"`
	SubaccountNumber uint32 `json:"subaccount_number"`
	Market           string `json:"market,omitempty"`
}
type GetFillsOutput struct {
	Fills indexer.FillsResponse `json:"fills"`
}

func (h *Handlers) GetFills(
	ctx context.Context, _ *mcp.CallToolRequest, in GetFillsInput,
) (*mcp.CallToolResult, GetFillsOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_fills", in.Address)
	if err != nil {
		return nil, GetFillsOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetFillsOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetFills(ctx, in.Address, in.SubaccountNumber, in.Market)
	if err != nil {
		return nil, GetFillsOutput{}, err
	}
	return nil, GetFillsOutput{Fills: *resp}, nil
}

// -- get_transfers -----------------------------------------------------

type GetTransfersInput struct {
	Address          string `json:"address"`
	SubaccountNumber uint32 `json:"subaccount_number"`
}
type GetTransfersOutput struct {
	Transfers indexer.TransfersResponse `json:"transfers"`
}

func (h *Handlers) GetTransfers(
	ctx context.Context, _ *mcp.CallToolRequest, in GetTransfersInput,
) (*mcp.CallToolResult, GetTransfersOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_transfers", in.Address)
	if err != nil {
		return nil, GetTransfersOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetTransfersOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetTransfers(ctx, in.Address, in.SubaccountNumber)
	if err != nil {
		return nil, GetTransfersOutput{}, err
	}
	return nil, GetTransfersOutput{Transfers: *resp}, nil
}

// -- get_pnl -----------------------------------------------------------

type GetPnlInput struct {
	Address          string `json:"address"`
	SubaccountNumber uint32 `json:"subaccount_number"`
}
type GetPnlOutput struct {
	Pnl indexer.PnlResponse `json:"pnl"`
}

func (h *Handlers) GetPnl(
	ctx context.Context, _ *mcp.CallToolRequest, in GetPnlInput,
) (*mcp.CallToolResult, GetPnlOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_pnl", in.Address)
	if err != nil {
		return nil, GetPnlOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetPnlOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetPnl(ctx, in.Address, in.SubaccountNumber)
	if err != nil {
		if errors.Is(err, indexer.ErrNotFound) {
			return nil, GetPnlOutput{Pnl: indexer.PnlResponse{
				HistoricalPnl: []map[string]any{},
			}}, nil
		}
		return nil, GetPnlOutput{}, err
	}
	return nil, GetPnlOutput{Pnl: *resp}, nil
}

// -- get_historical_pnl ------------------------------------------------

type GetHistoricalPnlInput struct {
	Address          string `json:"address"`
	SubaccountNumber uint32 `json:"subaccount_number"`
}
type GetHistoricalPnlOutput struct {
	HistoricalPnl indexer.HistoricalPnlResponse `json:"historical_pnl"`
}

func (h *Handlers) GetHistoricalPnl(
	ctx context.Context, _ *mcp.CallToolRequest, in GetHistoricalPnlInput,
) (*mcp.CallToolResult, GetHistoricalPnlOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_historical_pnl", in.Address)
	if err != nil {
		return nil, GetHistoricalPnlOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetHistoricalPnlOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetHistoricalPnl(ctx, in.Address, in.SubaccountNumber)
	if err != nil {
		if errors.Is(err, indexer.ErrNotFound) {
			return nil, GetHistoricalPnlOutput{HistoricalPnl: indexer.HistoricalPnlResponse{
				HistoricalPnl: []map[string]any{},
			}}, nil
		}
		return nil, GetHistoricalPnlOutput{}, err
	}
	return nil, GetHistoricalPnlOutput{HistoricalPnl: *resp}, nil
}

// -- get_funding_payments ----------------------------------------------

type GetFundingPaymentsInput struct {
	Address          string `json:"address"`
	SubaccountNumber uint32 `json:"subaccount_number"`
}
type GetFundingPaymentsOutput struct {
	FundingPayments indexer.FundingPaymentsResponse `json:"funding_payments"`
}

func (h *Handlers) GetFundingPayments(
	ctx context.Context, _ *mcp.CallToolRequest, in GetFundingPaymentsInput,
) (*mcp.CallToolResult, GetFundingPaymentsOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_funding_payments", in.Address)
	if err != nil {
		return nil, GetFundingPaymentsOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetFundingPaymentsOutput{}, err
	}
	resp, err := h.Deps.Indexer.GetFundingPayments(ctx, in.Address, in.SubaccountNumber)
	if err != nil {
		if errors.Is(err, indexer.ErrNotFound) {
			return nil, GetFundingPaymentsOutput{FundingPayments: indexer.FundingPaymentsResponse{
				FundingPayments: []map[string]any{},
			}}, nil
		}
		return nil, GetFundingPaymentsOutput{}, err
	}
	return nil, GetFundingPaymentsOutput{FundingPayments: *resp}, nil
}
