package builder

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
)

// CancelOrderInput drives build_cancel_order. The agent must declare
// order_flags explicitly — the server doesn't infer short-term vs
// stateful, because guessing the wrong class produces a silent
// misroute (short-term cancels go through one ante path, stateful
// through another). Mirror of the OrderId flags in
// protocol/x/clob/types/order_id.go (ShortTerm=0, Conditional=32,
// LongTerm=64).
type CancelOrderInput struct {
	Owner         string
	SubaccountNum uint32

	ClobPairID    uint32
	OrderClientID uint32
	OrderFlags    uint32 // 0=ShortTerm, 32=Conditional, 64=LongTerm

	// Exactly one of GoodTilBlock (short-term) / GoodTilBlockTime
	// (stateful) is set; the matching one must align with OrderFlags.
	GoodTilBlock     uint32
	GoodTilBlockTime uint32

	PayloadClientID string
}

// BuildCancelOrder produces a MsgCancelOrder + TxPayload. Short-term
// cancels go through the same short-term ante path as short-term place
// orders (no sequence consumption — see app/ante.go:331-342) and use
// GoodTilBlock; stateful cancels consume sequence and use
// GoodTilBlockTime. The caller (handler) is responsible for the policy
// + account-state preconditions.
func BuildCancelOrder(
	in CancelOrderInput,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*clobtypes.MsgCancelOrder, *payload.TxPayload, error) {
	if err := validateCancelFlags(in.OrderFlags, in.GoodTilBlock, in.GoodTilBlockTime); err != nil {
		return nil, nil, err
	}

	msg := &clobtypes.MsgCancelOrder{
		OrderId: clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{Owner: in.Owner, Number: in.SubaccountNum},
			ClientId:     in.OrderClientID,
			OrderFlags:   in.OrderFlags,
			ClobPairId:   in.ClobPairID,
		},
	}
	if in.GoodTilBlock > 0 {
		msg.GoodTilOneof = &clobtypes.MsgCancelOrder_GoodTilBlock{GoodTilBlock: in.GoodTilBlock}
	} else {
		msg.GoodTilOneof = &clobtypes.MsgCancelOrder_GoodTilBlockTime{GoodTilBlockTime: in.GoodTilBlockTime}
	}
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgCancelOrder.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:      "build_cancel_order",
		MsgTypeURL:    "/dydxprotocol.clob.MsgCancelOrder",
		Subaccount:    payload.SubaccountRef{Owner: in.Owner, Number: in.SubaccountNum},
		OrderClientID: in.OrderClientID,
		GoodTil:       describeGoodTil(in.GoodTilBlock, in.GoodTilBlockTime),
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

// OrderBatchInput is one (clob_pair_id, client_ids) tuple for a batch cancel.
type OrderBatchInput struct {
	ClobPairID uint32
	ClientIDs  []uint32
}

// BatchCancelOrdersInput drives build_batch_cancel_orders. The chain
// (clob/tx.proto:107-120) accepts MsgBatchCancel only for short-term
// orders; long-term / conditional cancels must use BuildCancelOrder one
// at a time. GoodTilBlock applies to the whole batch.
type BatchCancelOrdersInput struct {
	Owner         string
	SubaccountNum uint32

	Batches      []OrderBatchInput
	GoodTilBlock uint32

	PayloadClientID string
}

// BuildBatchCancelOrders produces a MsgBatchCancel + TxPayload. The chain
// requires at least one (clob_pair_id, client_ids) tuple with at least
// one client_id per tuple; MsgBatchCancel.ValidateBasic enforces both,
// and we surface that as an early error rather than a CheckTx reject.
func BuildBatchCancelOrders(
	in BatchCancelOrdersInput,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*clobtypes.MsgBatchCancel, *payload.TxPayload, error) {
	if len(in.Batches) == 0 {
		return nil, nil, fmt.Errorf("batch cancel requires at least one (clob_pair_id, client_ids) tuple")
	}
	if in.GoodTilBlock == 0 {
		return nil, nil, fmt.Errorf("good_til_block is required (batch cancel is short-term only)")
	}

	orderBatches := make([]clobtypes.OrderBatch, 0, len(in.Batches))
	totalIDs := 0
	for i, b := range in.Batches {
		if len(b.ClientIDs) == 0 {
			return nil, nil, fmt.Errorf("batches[%d] (clob_pair_id=%d) has no client_ids", i, b.ClobPairID)
		}
		totalIDs += len(b.ClientIDs)
		orderBatches = append(orderBatches, clobtypes.OrderBatch{
			ClobPairId: b.ClobPairID,
			ClientIds:  append([]uint32(nil), b.ClientIDs...),
		})
	}

	msg := &clobtypes.MsgBatchCancel{
		SubaccountId:     satypes.SubaccountId{Owner: in.Owner, Number: in.SubaccountNum},
		ShortTermCancels: orderBatches,
		GoodTilBlock:     in.GoodTilBlock,
	}
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgBatchCancel.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:   "build_batch_cancel_orders",
		MsgTypeURL: "/dydxprotocol.clob.MsgBatchCancel",
		Subaccount: payload.SubaccountRef{Owner: in.Owner, Number: in.SubaccountNum},
		GoodTil:    fmt.Sprintf("block:%d", in.GoodTilBlock),
		// Repurpose OrderClientID's slot to communicate the batch size in
		// the summary; the agent never sets that field for batches.
		OrderClientID: uint32(totalIDs),
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

// validateCancelFlags ensures the cancel's order_flags and its good-til
// field are consistent. Short-term (flags=0) must use GoodTilBlock;
// stateful (flags=32|64) must use GoodTilBlockTime.
func validateCancelFlags(flags, goodTilBlock, goodTilTime uint32) error {
	switch flags {
	case clobtypes.OrderIdFlags_ShortTerm:
		if goodTilBlock == 0 {
			return fmt.Errorf("short-term cancel (order_flags=0) requires good_til_block")
		}
		if goodTilTime != 0 {
			return fmt.Errorf("short-term cancel must not set good_til_block_time")
		}
	case clobtypes.OrderIdFlags_Conditional, clobtypes.OrderIdFlags_LongTerm:
		if goodTilTime == 0 {
			return fmt.Errorf("stateful cancel (order_flags=%d) requires good_til_block_time", flags)
		}
		if goodTilBlock != 0 {
			return fmt.Errorf("stateful cancel must not set good_til_block")
		}
	default:
		return fmt.Errorf("invalid order_flags %d (want 0=ShortTerm, 32=Conditional, 64=LongTerm)", flags)
	}
	return nil
}

func describeGoodTil(block, blockTime uint32) string {
	if block > 0 {
		return fmt.Sprintf("block:%d", block)
	}
	return fmt.Sprintf("time:%d", blockTime)
}
