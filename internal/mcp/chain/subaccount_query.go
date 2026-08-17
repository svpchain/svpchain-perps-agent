package chain

import (
	"context"
	"fmt"

	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"
	"google.golang.org/grpc"
)

// SubaccountQueryClient is the v0.1 surface over subaccounts.Query that
// the MCP server uses for "live" subaccount reads (state not yet ingested
// by the indexer).
type SubaccountQueryClient interface {
	Subaccount(ctx context.Context, owner string, number uint32) (satypes.Subaccount, error)
}

type subaccountQueryClient struct {
	inner satypes.QueryClient
}

func NewSubaccountQueryClient(conn *grpc.ClientConn) SubaccountQueryClient {
	return &subaccountQueryClient{inner: satypes.NewQueryClient(conn)}
}

func (c *subaccountQueryClient) Subaccount(ctx context.Context, owner string, number uint32) (satypes.Subaccount, error) {
	resp, err := c.inner.Subaccount(ctx, &satypes.QueryGetSubaccountRequest{
		Owner:  owner,
		Number: number,
	})
	if err != nil {
		return satypes.Subaccount{}, fmt.Errorf("subaccounts.Query/Subaccount %s/%d: %w", owner, number, err)
	}
	return resp.Subaccount, nil
}
