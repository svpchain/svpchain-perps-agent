package builder

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/units"
)

// PlaceLimitOrderInput is the v0.1 input shape for build_place_limit_order.
// Only short-term orders are supported in v0.1; LongTerm / Conditional /
// TWAP / Scaled land in v0.2 / v0.3.
type PlaceLimitOrderInput struct {
	Owner         string
	SubaccountNum uint32

	Ticker      string
	Side        string // "BUY" | "SELL"
	HumanSize   string // e.g. "0.05"
	HumanPrice  string // e.g. "65000.00"
	TimeInForce string // "" (UNSPECIFIED → GTT), "IOC", or "POST_ONLY"
	ReduceOnly  bool

	// GoodTilBlock is the block height after which this short-term order
	// expires. Must be the current height + a few blocks; the agent reads
	// height from get_height and adds a small buffer.
	GoodTilBlock uint32

	// OrderClientID is the per-order uint32 client id that goes on-chain
	// (Order.ClientId). The agent picks this and is responsible for
	// avoiding collisions with currently open orders.
	OrderClientID uint32

	// PayloadClientID is the broadcast-idempotency uuid for the TxPayload
	// envelope. Distinct from OrderClientID.
	PayloadClientID string
}

// BuildPlaceLimitOrder turns a PlaceLimitOrderInput into a
// MsgPlaceOrder and an envelope TxPayload. It performs:
//
//   - ticker → MarketMeta resolution (markets.Cache)
//   - size → quantums and price → subticks conversion (units)
//   - Order construction with ShortTerm flags + GoodTilBlock
//   - MsgPlaceOrder.ValidateBasic so we surface chain rejects early
//   - TxPayload assembly via Assembler
//
// Caller is responsible for: (1) running policy checks BEFORE this, and
// (2) reading {AccountNumber, Sequence} via chain.AccountClient.Account.
func BuildPlaceLimitOrder(
	in PlaceLimitOrderInput,
	cache *markets.Cache,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*clobtypes.MsgPlaceOrder, *payload.TxPayload, error) {
	meta, ok := cache.ResolveTicker(in.Ticker)
	if !ok {
		return nil, nil, fmt.Errorf("unknown ticker %q (markets cache not populated?)", in.Ticker)
	}

	quantums, err := units.SizeToQuantums(in.HumanSize, meta)
	if err != nil {
		return nil, nil, fmt.Errorf("size: %w", err)
	}
	subticks, err := units.PriceToSubticks(in.HumanPrice, meta)
	if err != nil {
		return nil, nil, fmt.Errorf("price: %w", err)
	}

	side, err := parseSide(in.Side)
	if err != nil {
		return nil, nil, err
	}
	tif, err := parseTimeInForce(in.TimeInForce)
	if err != nil {
		return nil, nil, err
	}
	if in.GoodTilBlock == 0 {
		return nil, nil, fmt.Errorf("good_til_block is required for short-term orders")
	}

	order := clobtypes.Order{
		OrderId: clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{Owner: in.Owner, Number: in.SubaccountNum},
			ClientId:     in.OrderClientID,
			OrderFlags:   clobtypes.OrderIdFlags_ShortTerm,
			ClobPairId:   meta.ClobPairID,
		},
		Side:         side,
		Quantums:     quantums,
		Subticks:     subticks,
		TimeInForce:  tif,
		ReduceOnly:   in.ReduceOnly,
		GoodTilOneof: &clobtypes.Order_GoodTilBlock{GoodTilBlock: in.GoodTilBlock},
	}
	msg := clobtypes.NewMsgPlaceOrder(order)
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgPlaceOrder.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:      "build_place_limit_order",
		MsgTypeURL:    "/dydxprotocol.clob.MsgPlaceOrder",
		Subaccount:    payload.SubaccountRef{Owner: in.Owner, Number: in.SubaccountNum},
		Ticker:        in.Ticker,
		Side:          in.Side,
		SizeHuman:     in.HumanSize,
		PriceHuman:    in.HumanPrice,
		GoodTil:       fmt.Sprintf("block:%d", in.GoodTilBlock),
		ReduceOnly:    in.ReduceOnly,
		OrderClientID: in.OrderClientID,
	}
	txPayload, err := asm.Assemble(Args{
		Msgs:          []sdk.Msg{msg},
		SignerAddress: in.Owner,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		ClientID:      in.PayloadClientID,
		Summary:       summary,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assemble: %w", err)
	}
	return msg, txPayload, nil
}

// PlaceMarketOrderInput drives build_place_market_order. svpchain has no
// dedicated market-order msg; we place an IOC limit at the worst price
// the caller is willing to fill at. The worst price may be passed
// directly via WorstPrice, or derived from OraclePrice + SlippageBps
// using units.AggressiveSubticks.
type PlaceMarketOrderInput struct {
	Owner         string
	SubaccountNum uint32

	Ticker    string
	Side      string // "BUY" | "SELL"
	HumanSize string

	// Exactly one strategy for the IOC worst price:
	//   (a) WorstPrice set explicitly (takes precedence if both populated)
	//   (b) OraclePrice + SlippageBps to derive worst via AggressiveSubticks
	WorstPrice  string
	OraclePrice string
	SlippageBps uint32

	ReduceOnly      bool
	GoodTilBlock    uint32
	OrderClientID   uint32
	PayloadClientID string
}

// BuildPlaceMarketOrder constructs a short-term IOC MsgPlaceOrder placed
// at the worst price the caller commits to. The size→quantums and
// price→subticks conversions go through the same units helpers as the
// limit-order path; the only differences are TimeInForce=IOC and the
// price-derivation strategy.
func BuildPlaceMarketOrder(
	in PlaceMarketOrderInput,
	cache *markets.Cache,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*clobtypes.MsgPlaceOrder, *payload.TxPayload, error) {
	meta, ok := cache.ResolveTicker(in.Ticker)
	if !ok {
		return nil, nil, fmt.Errorf("unknown ticker %q (markets cache not populated?)", in.Ticker)
	}
	quantums, err := units.SizeToQuantums(in.HumanSize, meta)
	if err != nil {
		return nil, nil, fmt.Errorf("size: %w", err)
	}
	side, err := parseSide(in.Side)
	if err != nil {
		return nil, nil, err
	}

	var subticks uint64
	var worstHuman string
	switch {
	case in.WorstPrice != "":
		// Explicit worst price.
		subticks, err = units.PriceToSubticks(in.WorstPrice, meta)
		if err != nil {
			return nil, nil, fmt.Errorf("worst_price: %w", err)
		}
		worstHuman = in.WorstPrice
	case in.OraclePrice != "" && in.SlippageBps > 0:
		subticks, err = units.AggressiveSubticks(in.Side, in.OraclePrice, in.SlippageBps, meta)
		if err != nil {
			return nil, nil, fmt.Errorf("aggressive subticks: %w", err)
		}
		worstHuman = fmt.Sprintf("oracle=%s slip=%dbps", in.OraclePrice, in.SlippageBps)
	default:
		return nil, nil, fmt.Errorf("market order requires either worst_price or (oracle_price + slippage_bps)")
	}
	if in.GoodTilBlock == 0 {
		return nil, nil, fmt.Errorf("good_til_block is required for short-term orders")
	}

	order := clobtypes.Order{
		OrderId: clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{Owner: in.Owner, Number: in.SubaccountNum},
			ClientId:     in.OrderClientID,
			OrderFlags:   clobtypes.OrderIdFlags_ShortTerm,
			ClobPairId:   meta.ClobPairID,
		},
		Side:         side,
		Quantums:     quantums,
		Subticks:     subticks,
		TimeInForce:  clobtypes.Order_TIME_IN_FORCE_IOC,
		ReduceOnly:   in.ReduceOnly,
		GoodTilOneof: &clobtypes.Order_GoodTilBlock{GoodTilBlock: in.GoodTilBlock},
	}
	msg := clobtypes.NewMsgPlaceOrder(order)
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgPlaceOrder.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:      "build_place_market_order",
		MsgTypeURL:    "/dydxprotocol.clob.MsgPlaceOrder",
		Subaccount:    payload.SubaccountRef{Owner: in.Owner, Number: in.SubaccountNum},
		Ticker:        in.Ticker,
		Side:          in.Side,
		SizeHuman:     in.HumanSize,
		PriceHuman:    worstHuman,
		GoodTil:       fmt.Sprintf("block:%d", in.GoodTilBlock),
		ReduceOnly:    in.ReduceOnly,
		OrderClientID: in.OrderClientID,
	}
	txPayload, err := asm.Assemble(Args{
		Msgs:          []sdk.Msg{msg},
		SignerAddress: in.Owner,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		ClientID:      in.PayloadClientID,
		Summary:       summary,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assemble: %w", err)
	}
	return msg, txPayload, nil
}

// PlaceConditionalOrderInput drives build_place_conditional_order.
// Conditional orders are stateful (long-term in chain terms): they sit on
// the chain until either the trigger condition fires (oracle crosses the
// trigger price) or the good-til-time elapses. When triggered, they
// become an ordinary limit order at HumanPrice.
type PlaceConditionalOrderInput struct {
	Owner         string
	SubaccountNum uint32

	Ticker     string
	Side       string // "BUY" | "SELL"
	HumanSize  string
	HumanPrice string // limit price applied when the trigger fires

	// ConditionType: "STOP_LOSS" or "TAKE_PROFIT". Trigger semantics
	// follow the chain (clob/order.proto:209):
	//   STOP_LOSS  : BUY triggers when oracle >= trigger_price;
	//                SELL triggers when oracle <= trigger_price.
	//   TAKE_PROFIT: BUY triggers when oracle <= trigger_price;
	//                SELL triggers when oracle >= trigger_price.
	ConditionType string
	TriggerPrice  string

	TimeInForce string // "" | "IOC" | "POST_ONLY"
	ReduceOnly  bool

	// GoodTilBlockTime is the unix timestamp (seconds) after which the
	// order is treated as expired. Required for stateful orders.
	GoodTilBlockTime uint32

	OrderClientID   uint32
	PayloadClientID string
}

// BuildPlaceConditionalOrder constructs a stateful (OrderIdFlags=Conditional)
// MsgPlaceOrder carrying the limit price + trigger condition. The chain
// activates it as a normal limit order once the oracle crosses the
// trigger; until then it's parked.
func BuildPlaceConditionalOrder(
	in PlaceConditionalOrderInput,
	cache *markets.Cache,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*clobtypes.MsgPlaceOrder, *payload.TxPayload, error) {
	meta, ok := cache.ResolveTicker(in.Ticker)
	if !ok {
		return nil, nil, fmt.Errorf("unknown ticker %q", in.Ticker)
	}
	quantums, err := units.SizeToQuantums(in.HumanSize, meta)
	if err != nil {
		return nil, nil, fmt.Errorf("size: %w", err)
	}
	subticks, err := units.PriceToSubticks(in.HumanPrice, meta)
	if err != nil {
		return nil, nil, fmt.Errorf("price: %w", err)
	}
	triggerSubticks, err := units.PriceToSubticks(in.TriggerPrice, meta)
	if err != nil {
		return nil, nil, fmt.Errorf("trigger_price: %w", err)
	}
	side, err := parseSide(in.Side)
	if err != nil {
		return nil, nil, err
	}
	tif, err := parseTimeInForce(in.TimeInForce)
	if err != nil {
		return nil, nil, err
	}
	cond, err := parseConditionType(in.ConditionType)
	if err != nil {
		return nil, nil, err
	}
	if in.GoodTilBlockTime == 0 {
		return nil, nil, fmt.Errorf("good_til_block_time is required for stateful (conditional) orders")
	}

	order := clobtypes.Order{
		OrderId: clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{Owner: in.Owner, Number: in.SubaccountNum},
			ClientId:     in.OrderClientID,
			OrderFlags:   clobtypes.OrderIdFlags_Conditional,
			ClobPairId:   meta.ClobPairID,
		},
		Side:                            side,
		Quantums:                        quantums,
		Subticks:                        subticks,
		TimeInForce:                     tif,
		ReduceOnly:                      in.ReduceOnly,
		ConditionType:                   cond,
		ConditionalOrderTriggerSubticks: triggerSubticks,
		GoodTilOneof: &clobtypes.Order_GoodTilBlockTime{
			GoodTilBlockTime: in.GoodTilBlockTime,
		},
	}
	msg := clobtypes.NewMsgPlaceOrder(order)
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgPlaceOrder.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:      "build_place_conditional_order",
		MsgTypeURL:    "/dydxprotocol.clob.MsgPlaceOrder",
		Subaccount:    payload.SubaccountRef{Owner: in.Owner, Number: in.SubaccountNum},
		Ticker:        in.Ticker,
		Side:          in.Side,
		SizeHuman:     in.HumanSize,
		PriceHuman:    fmt.Sprintf("%s (%s @ trigger %s)", in.HumanPrice, in.ConditionType, in.TriggerPrice),
		GoodTil:       fmt.Sprintf("time:%d", in.GoodTilBlockTime),
		ReduceOnly:    in.ReduceOnly,
		OrderClientID: in.OrderClientID,
	}
	txPayload, err := asm.Assemble(Args{
		Msgs:          []sdk.Msg{msg},
		SignerAddress: in.Owner,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		ClientID:      in.PayloadClientID,
		Summary:       summary,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assemble: %w", err)
	}
	return msg, txPayload, nil
}

func parseConditionType(s string) (clobtypes.Order_ConditionType, error) {
	switch s {
	case "STOP_LOSS":
		return clobtypes.Order_CONDITION_TYPE_STOP_LOSS, nil
	case "TAKE_PROFIT":
		return clobtypes.Order_CONDITION_TYPE_TAKE_PROFIT, nil
	default:
		return clobtypes.Order_CONDITION_TYPE_UNSPECIFIED, fmt.Errorf("invalid condition_type %q (want STOP_LOSS or TAKE_PROFIT)", s)
	}
}

func parseSide(s string) (clobtypes.Order_Side, error) {
	switch s {
	case "BUY":
		return clobtypes.Order_SIDE_BUY, nil
	case "SELL":
		return clobtypes.Order_SIDE_SELL, nil
	default:
		return clobtypes.Order_SIDE_UNSPECIFIED, fmt.Errorf("invalid side %q (want BUY or SELL)", s)
	}
}

func parseTimeInForce(s string) (clobtypes.Order_TimeInForce, error) {
	switch s {
	case "", "UNSPECIFIED":
		return clobtypes.Order_TIME_IN_FORCE_UNSPECIFIED, nil
	case "IOC":
		return clobtypes.Order_TIME_IN_FORCE_IOC, nil
	case "POST_ONLY":
		return clobtypes.Order_TIME_IN_FORCE_POST_ONLY, nil
	default:
		return clobtypes.Order_TIME_IN_FORCE_UNSPECIFIED, fmt.Errorf("invalid time_in_force %q", s)
	}
}
