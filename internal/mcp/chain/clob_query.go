package chain

import (
	"context"
	"fmt"

	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
	"google.golang.org/grpc"
)

// ClobQueryClient is the v0.1 surface over clob.Query that the MCP server
// uses to populate the market-metadata cache. Future capabilities
// (StatefulOrder / TwapOrderPlacement / ScaledOrderPlacement) land in v0.3.
type ClobQueryClient interface {
	// ClobPairAll returns every ClobPair on-chain — used by internal/mcp/markets
	// to build the ticker↔clobPairId table on boot and on periodic refresh.
	ClobPairAll(ctx context.Context) ([]clobtypes.ClobPair, error)
}

type clobQueryClient struct {
	inner clobtypes.QueryClient
}

// NewClobQueryClient wires up a ClobQueryClient against the given gRPC conn.
func NewClobQueryClient(conn *grpc.ClientConn) ClobQueryClient {
	return &clobQueryClient{inner: clobtypes.NewQueryClient(conn)}
}

func (c *clobQueryClient) ClobPairAll(ctx context.Context) ([]clobtypes.ClobPair, error) {
	resp, err := c.inner.ClobPairAll(ctx, &clobtypes.QueryAllClobPairRequest{})
	if err != nil {
		return nil, fmt.Errorf("clob.Query/ClobPairAll: %w", err)
	}
	return resp.ClobPair, nil
}
