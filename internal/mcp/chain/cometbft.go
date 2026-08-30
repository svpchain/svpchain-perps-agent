package chain

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
)

// ChainStatus is the node's view of the chain head, from CometBFT's /status:
// the latest committed block and the time stamped in its header. It is what
// get_height and get_time serve — read from the chain directly, so they
// answer even when the indexer is down or lagging, and cannot disagree with
// what the chain has actually committed.
type ChainStatus struct {
	LatestBlockHeight int64
	LatestBlockTime   time.Time
	// CatchingUp is the node's own admission that it is still syncing, in
	// which case the height above is behind the network's.
	CatchingUp bool
}

// TxStatus is the subset of CometBFT's ResultTx that the MCP server exposes
// to clients through get_tx_status. Height == 0 means "not yet included".
type TxStatus struct {
	TxHash string
	Height int64
	Code   uint32
	RawLog string
}

// CometBftClient is the v0.1 surface over CometBFT's RPC for tx-status
// polling (broadcast_signed_tx returns immediately on CheckTx accept; the
// agent polls get_tx_status to confirm inclusion).
type CometBftClient interface {
	TxStatus(ctx context.Context, txHash string) (TxStatus, error)
	Status(ctx context.Context) (ChainStatus, error)
}

type cometBftClient struct {
	inner *rpchttp.HTTP
}

// NewCometBftClient dials the CometBFT RPC at rpcURL (e.g.
// "http://127.0.0.1:26657") using the standard CometBFT HTTP client.
func NewCometBftClient(rpcURL string) (CometBftClient, error) {
	c, err := rpchttp.New(rpcURL, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("cometbft.New %s: %w", rpcURL, err)
	}
	return &cometBftClient{inner: c}, nil
}

// TxStatus looks up a tx by hex hash via CometBFT's /tx endpoint.
// `prove=false` skips merkle proof generation — we just want the code,
// height, and raw log.
func (c *cometBftClient) TxStatus(ctx context.Context, txHash string) (TxStatus, error) {
	hashBytes, err := hex.DecodeString(txHash)
	if err != nil {
		return TxStatus{}, fmt.Errorf("invalid tx hash %q: %w", txHash, err)
	}
	resp, err := c.inner.Tx(ctx, hashBytes, false)
	if err != nil {
		return TxStatus{}, fmt.Errorf("cometbft Tx %s: %w", txHash, err)
	}
	return TxStatus{
		TxHash: txHash,
		Height: resp.Height,
		Code:   resp.TxResult.Code,
		RawLog: resp.TxResult.Log,
	}, nil
}

// Status reads the chain head via CometBFT's /status endpoint.
func (c *cometBftClient) Status(ctx context.Context) (ChainStatus, error) {
	resp, err := c.inner.Status(ctx)
	if err != nil {
		return ChainStatus{}, fmt.Errorf("cometbft Status: %w", err)
	}
	return ChainStatus{
		LatestBlockHeight: resp.SyncInfo.LatestBlockHeight,
		LatestBlockTime:   resp.SyncInfo.LatestBlockTime,
		CatchingUp:        resp.SyncInfo.CatchingUp,
	}, nil
}
