package builder_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
)

func newPlaceOrderMsg(flags uint32) *clobtypes.MsgPlaceOrder {
	return clobtypes.NewMsgPlaceOrder(clobtypes.Order{
		OrderId: clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{Owner: "svp1abc", Number: 0},
			ClientId:     1,
			OrderFlags:   flags,
			ClobPairId:   0,
		},
		Side:         clobtypes.Order_SIDE_BUY,
		Quantums:     1,
		Subticks:     1,
		GoodTilOneof: &clobtypes.Order_GoodTilBlock{GoodTilBlock: 100},
	})
}

func TestIsShortTermClobMsgs(t *testing.T) {
	cases := []struct {
		name string
		msgs []sdk.Msg
		want bool
	}{
		{
			name: "single short-term place_order",
			msgs: []sdk.Msg{newPlaceOrderMsg(clobtypes.OrderIdFlags_ShortTerm)},
			want: true,
		},
		{
			name: "single long-term place_order",
			msgs: []sdk.Msg{newPlaceOrderMsg(clobtypes.OrderIdFlags_LongTerm)},
			want: false,
		},
		{
			name: "single conditional place_order",
			msgs: []sdk.Msg{newPlaceOrderMsg(clobtypes.OrderIdFlags_Conditional)},
			want: false,
		},
		{
			name: "short-term cancel",
			msgs: []sdk.Msg{
				&clobtypes.MsgCancelOrder{
					OrderId: clobtypes.OrderId{
						SubaccountId: satypes.SubaccountId{Owner: "svp1abc", Number: 0},
						OrderFlags:   clobtypes.OrderIdFlags_ShortTerm,
					},
					GoodTilOneof: &clobtypes.MsgCancelOrder_GoodTilBlock{GoodTilBlock: 100},
				},
			},
			want: true,
		},
		{
			name: "long-term cancel",
			msgs: []sdk.Msg{
				&clobtypes.MsgCancelOrder{
					OrderId: clobtypes.OrderId{
						SubaccountId: satypes.SubaccountId{Owner: "svp1abc", Number: 0},
						OrderFlags:   clobtypes.OrderIdFlags_LongTerm,
					},
				},
			},
			want: false,
		},
		{
			name: "batch cancel always short-term",
			msgs: []sdk.Msg{
				&clobtypes.MsgBatchCancel{
					SubaccountId: satypes.SubaccountId{Owner: "svp1abc", Number: 0},
				},
			},
			want: true,
		},
		{
			name: "two msgs never short-term",
			msgs: []sdk.Msg{
				newPlaceOrderMsg(clobtypes.OrderIdFlags_ShortTerm),
				newPlaceOrderMsg(clobtypes.OrderIdFlags_ShortTerm),
			},
			want: false,
		},
		{
			name: "empty never short-term",
			msgs: []sdk.Msg{},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, builder.IsShortTermClobMsgs(tc.msgs))
		})
	}
}
