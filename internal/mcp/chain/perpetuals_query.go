package chain

import (
	"context"
	"fmt"

	perptypes "github.com/dydxprotocol/v4-chain/protocol/x/perpetuals/types"
	"google.golang.org/grpc"
)

// PerpetualsQueryClient is the surface over perpetuals.Query that
// internal/mcp/markets needs to source the human ticker string + atomic
// resolution for each market directly from the chain — the two fields the
// clob.Query/ClobPairAll response does not carry. Joining these on-chain is
// what lets the markets cache (and therefore every build_* tool) resolve a
// ticker without any dependency on the off-chain indexer.
type PerpetualsQueryClient interface {
	// AllPerpetuals returns every Perpetual on-chain, keyed downstream by
	// Params.Id (which a ClobPair references via PerpetualClobMetadata).
	AllPerpetuals(ctx context.Context) ([]perptypes.Perpetual, error)
}

type perpetualsQueryClient struct {
	inner perptypes.QueryClient
}

// NewPerpetualsQueryClient wires up a PerpetualsQueryClient against the
// given gRPC conn.
func NewPerpetualsQueryClient(conn *grpc.ClientConn) PerpetualsQueryClient {
	return &perpetualsQueryClient{inner: perptypes.NewQueryClient(conn)}
}

func (c *perpetualsQueryClient) AllPerpetuals(ctx context.Context) ([]perptypes.Perpetual, error) {
	resp, err := c.inner.AllPerpetuals(ctx, &perptypes.QueryAllPerpetualsRequest{})
	if err != nil {
		return nil, fmt.Errorf("perpetuals.Query/AllPerpetuals: %w", err)
	}
	return resp.Perpetual, nil
}
