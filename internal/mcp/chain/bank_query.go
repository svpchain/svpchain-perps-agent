package chain

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
)

// BankQueryClient is the minimal x/bank surface the MCP server uses to read an
// address's wallet balances across every denom it holds. This is distinct from
// subaccount trading collateral (see SubaccountQueryClient): it's the cosmos
// bank balance that build_deposit_to_subaccount / build_withdraw_from_subaccount
// move USDC in and out of.
type BankQueryClient interface {
	AllBalances(ctx context.Context, address string) (sdk.Coins, error)
}

type bankQueryClient struct {
	inner banktypes.QueryClient
}

func NewBankQueryClient(conn *grpc.ClientConn) BankQueryClient {
	return &bankQueryClient{inner: banktypes.NewQueryClient(conn)}
}

func (c *bankQueryClient) AllBalances(ctx context.Context, address string) (sdk.Coins, error) {
	resp, err := c.inner.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{Address: address})
	if err != nil {
		return nil, fmt.Errorf("bank.Query/AllBalances %s: %w", address, err)
	}
	return resp.Balances, nil
}
