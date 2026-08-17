package builder_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	// testOwner, newTestAsm, and the app/config blank-import live in
	// testutil_test.go (same package).
	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
)

func TestBuildCancelOrder_ShortTerm(t *testing.T) {
	msg, p, err := builder.BuildCancelOrder(builder.CancelOrderInput{
		Owner:           testOwner,
		SubaccountNum:   0,
		ClobPairID:      0,
		OrderClientID:   42,
		OrderFlags:      clobtypes.OrderIdFlags_ShortTerm,
		GoodTilBlock:    100,
		PayloadClientID: "uuid-1",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, clobtypes.OrderIdFlags_ShortTerm, msg.OrderId.OrderFlags)
	require.NotNil(t, msg.GoodTilOneof)
	require.True(t, p.IsShortTermCLOB, "short-term cancel must mark payload IsShortTermCLOB")
}

func TestBuildCancelOrder_Stateful(t *testing.T) {
	msg, p, err := builder.BuildCancelOrder(builder.CancelOrderInput{
		Owner:            testOwner,
		SubaccountNum:    0,
		ClobPairID:       0,
		OrderClientID:    42,
		OrderFlags:       clobtypes.OrderIdFlags_LongTerm,
		GoodTilBlockTime: 1780000000,
		PayloadClientID:  "uuid-2",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, clobtypes.OrderIdFlags_LongTerm, msg.OrderId.OrderFlags)
	require.False(t, p.IsShortTermCLOB, "long-term cancel must not be marked IsShortTermCLOB")
}

func TestBuildCancelOrder_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		in      builder.CancelOrderInput
		wantErr string
	}{
		{
			name: "short-term needs GoodTilBlock",
			in: builder.CancelOrderInput{
				Owner: "x", OrderFlags: 0, PayloadClientID: "u",
			},
			wantErr: "good_til_block",
		},
		{
			name: "short-term can't set GoodTilBlockTime",
			in: builder.CancelOrderInput{
				Owner: "x", OrderFlags: 0, GoodTilBlock: 1, GoodTilBlockTime: 1, PayloadClientID: "u",
			},
			wantErr: "must not set good_til_block_time",
		},
		{
			name: "stateful needs GoodTilBlockTime",
			in: builder.CancelOrderInput{
				Owner: "x", OrderFlags: clobtypes.OrderIdFlags_LongTerm, PayloadClientID: "u",
			},
			wantErr: "good_til_block_time",
		},
		{
			name: "stateful can't set GoodTilBlock",
			in: builder.CancelOrderInput{
				Owner: "x", OrderFlags: clobtypes.OrderIdFlags_LongTerm,
				GoodTilBlock: 1, GoodTilBlockTime: 1, PayloadClientID: "u",
			},
			wantErr: "must not set good_til_block",
		},
		{
			name: "invalid flags",
			in: builder.CancelOrderInput{
				Owner: "x", OrderFlags: 999, PayloadClientID: "u",
			},
			wantErr: "invalid order_flags",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := builder.BuildCancelOrder(tc.in, newTestAsm(t), 1, 1)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildBatchCancelOrders_HappyPath(t *testing.T) {
	msg, p, err := builder.BuildBatchCancelOrders(builder.BatchCancelOrdersInput{
		Owner:         testOwner,
		SubaccountNum: 0,
		Batches: []builder.OrderBatchInput{
			{ClobPairID: 0, ClientIDs: []uint32{1, 2, 3}},
			{ClobPairID: 1, ClientIDs: []uint32{42}},
		},
		GoodTilBlock:    100,
		PayloadClientID: "uuid-batch-1",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Len(t, msg.ShortTermCancels, 2)
	require.Equal(t, []uint32{1, 2, 3}, msg.ShortTermCancels[0].ClientIds)
	require.Equal(t, uint32(100), msg.GoodTilBlock)
	require.True(t, p.IsShortTermCLOB)
	require.Equal(t, uint32(4), p.Summary.OrderClientID, "batch summary records total ids cancelled")
}

func TestBuildBatchCancelOrders_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		in      builder.BatchCancelOrdersInput
		wantErr string
	}{
		{
			name:    "no batches",
			in:      builder.BatchCancelOrdersInput{Owner: "x", GoodTilBlock: 1, PayloadClientID: "u"},
			wantErr: "at least one",
		},
		{
			name: "no good_til_block",
			in: builder.BatchCancelOrdersInput{
				Owner: "x", PayloadClientID: "u",
				Batches: []builder.OrderBatchInput{{ClobPairID: 0, ClientIDs: []uint32{1}}},
			},
			wantErr: "good_til_block is required",
		},
		{
			name: "batch with no client_ids",
			in: builder.BatchCancelOrdersInput{
				Owner: "x", GoodTilBlock: 1, PayloadClientID: "u",
				Batches: []builder.OrderBatchInput{{ClobPairID: 0, ClientIDs: nil}},
			},
			wantErr: "no client_ids",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := builder.BuildBatchCancelOrders(tc.in, newTestAsm(t), 1, 1)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
