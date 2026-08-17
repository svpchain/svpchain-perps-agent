package builder

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
)

// IsShortTermClobMsgs reports whether msgs consists of a single CLOB
// short-term message: MsgPlaceOrder with a ShortTerm OrderId, MsgCancelOrder
// targeting a ShortTerm OrderId, or any MsgBatchCancel (the chain accepts
// MsgBatchCancel only for short-term orders today). Mirrors the predicate
// in x/clob/ante/clob.go:233 — IsShortTermClobMsgTx — but operates on
// []sdk.Msg directly so the build path can use it without wrapping the
// msgs into an sdk.Tx.
//
// The result drives payload.TxPayload.IsShortTermCLOB, which in turn tells
// the local signer to reuse the current account sequence rather than
// treat it as a nonce to increment at sign time. This is required because
// the chain's ante handler explicitly skips incrementSequence for short-term
// CLOB txs — see app/ante.go:331-342.
func IsShortTermClobMsgs(msgs []sdk.Msg) bool {
	if len(msgs) != 1 {
		return false
	}
	switch m := msgs[0].(type) {
	case *clobtypes.MsgPlaceOrder:
		return m.Order.OrderId.IsShortTermOrder()
	case *clobtypes.MsgCancelOrder:
		return m.OrderId.IsShortTermOrder()
	case *clobtypes.MsgBatchCancel:
		return true
	}
	return false
}
