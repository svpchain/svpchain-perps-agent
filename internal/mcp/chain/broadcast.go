package chain

import (
	"context"
	"fmt"

	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// BroadcastResult is the chain-side response to a BroadcastSync — Code 0
// means accepted into mempool; non-zero is a CheckTx reject with RawLog
// explaining why.
type BroadcastResult struct {
	TxHash string
	Code   uint32
	RawLog string
}

// BroadcastClient is the minimal cosmos.tx.v1beta1.Service surface the MCP
// server uses. The server only ever broadcasts pre-signed bytes received
// from the local signer — it does not assemble the signature itself.
type BroadcastClient interface {
	BroadcastSync(ctx context.Context, txBytes []byte) (BroadcastResult, error)
}

type broadcastClient struct {
	inner sdktx.ServiceClient
}

// NewBroadcastClient returns a BroadcastClient backed by the standard SDK
// Tx service over the supplied gRPC connection.
func NewBroadcastClient(conn *grpc.ClientConn) BroadcastClient {
	return &broadcastClient{inner: sdktx.NewServiceClient(conn)}
}

// BroadcastSync submits txBytes with BROADCAST_MODE_SYNC (i.e. wait for
// CheckTx but not for inclusion in a block). v0.2 will wrap this with an
// AIMD inflight window — see cmd/dex-bench/cosmos_client.go:37-140 for the
// reference implementation.
func (c *broadcastClient) BroadcastSync(ctx context.Context, txBytes []byte) (BroadcastResult, error) {
	resp, err := c.inner.BroadcastTx(ctx, &sdktx.BroadcastTxRequest{
		TxBytes: txBytes,
		Mode:    sdktx.BroadcastMode_BROADCAST_MODE_SYNC,
	})
	if err != nil {
		return BroadcastResult{}, fmt.Errorf("Tx.Service/BroadcastTx: %w", err)
	}
	return BroadcastResult{
		TxHash: resp.TxResponse.TxHash,
		Code:   resp.TxResponse.Code,
		RawLog: resp.TxResponse.RawLog,
	}, nil
}
