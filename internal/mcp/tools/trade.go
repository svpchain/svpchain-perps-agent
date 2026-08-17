package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
)

// -- build_place_limit_order (short-term only in v0.1) ------------------

type BuildPlaceLimitOrderInput struct {
	SubaccountNumber uint32 `json:"subaccount_number"`
	Ticker           string `json:"ticker" jsonschema:"e.g. BTC-USD"`
	Side             string `json:"side" jsonschema:"BUY or SELL"`
	Size             string `json:"size" jsonschema:"human-readable base size, e.g. 0.05"`
	Price            string `json:"price" jsonschema:"human-readable price, e.g. 65000.00"`
	TimeInForce      string `json:"time_in_force,omitempty" jsonschema:"empty | IOC | POST_ONLY"`
	ReduceOnly       bool   `json:"reduce_only,omitempty"`
	GoodTilBlock     uint32 `json:"good_til_block" jsonschema:"block height after which the order expires"`
	OrderClientID    uint32 `json:"order_client_id" jsonschema:"on-chain Order.ClientId (uint32)"`
	PayloadClientID  string `json:"payload_client_id" jsonschema:"broadcast-idempotency uuid"`
}

type BuildPlaceLimitOrderOutput struct {
	Payload payload.TxPayload `json:"payload"`
}

func (h *Handlers) BuildPlaceLimitOrder(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BuildPlaceLimitOrderInput,
) (*mcp.CallToolResult, BuildPlaceLimitOrderOutput, error) {
	tp, err := h.authorizeSubaccount(ctx, "build_place_limit_order", in.SubaccountNumber)
	if err != nil {
		return nil, BuildPlaceLimitOrderOutput{}, err
	}

	acc, err := h.Deps.Chain.Account.Account(ctx, tp.Owner)
	if err != nil {
		return nil, BuildPlaceLimitOrderOutput{}, fmt.Errorf("read account state: %w", err)
	}

	_, p, err := builder.BuildPlaceLimitOrder(
		builder.PlaceLimitOrderInput{
			Owner:           tp.Owner,
			SubaccountNum:   in.SubaccountNumber,
			Ticker:          in.Ticker,
			Side:            in.Side,
			HumanSize:       in.Size,
			HumanPrice:      in.Price,
			TimeInForce:     in.TimeInForce,
			ReduceOnly:      in.ReduceOnly,
			GoodTilBlock:    in.GoodTilBlock,
			OrderClientID:   in.OrderClientID,
			PayloadClientID: in.PayloadClientID,
		},
		h.Deps.Markets,
		h.Deps.Builder,
		acc.AccountNumber,
		acc.Sequence,
	)
	if err != nil {
		return nil, BuildPlaceLimitOrderOutput{}, err
	}
	return nil, BuildPlaceLimitOrderOutput{Payload: *p}, nil
}
