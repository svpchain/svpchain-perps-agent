package chain

import (
	"context"
	"fmt"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

// AccountInfo bundles the auth-query fields the MCP server needs to build
// a tx payload: account_number is constant for the life of the account;
// sequence is the next nonce to use (or the current short-term-CLOB nonce
// per app/ante.go:331-342).
type AccountInfo struct {
	AccountNumber uint64
	Sequence      uint64
}

// AccountClient is the minimal x/auth surface the MCP server uses.
type AccountClient interface {
	Account(ctx context.Context, address string) (AccountInfo, error)
}

type accountClient struct {
	inner    authtypes.QueryClient
	registry codectypes.InterfaceRegistry
}

// NewAccountClient wires up an AccountClient. The InterfaceRegistry is
// required to UnpackAny the response into sdk.AccountI (BaseAccount /
// VestingAccount / etc.); obtain one from app.GetEncodingConfig() — see
// cmd/dex-bench/main.go:403 for prior art.
func NewAccountClient(conn *grpc.ClientConn, registry codectypes.InterfaceRegistry) AccountClient {
	return &accountClient{
		inner:    authtypes.NewQueryClient(conn),
		registry: registry,
	}
}

func (c *accountClient) Account(ctx context.Context, address string) (AccountInfo, error) {
	resp, err := c.inner.Account(ctx, &authtypes.QueryAccountRequest{Address: address})
	if err != nil {
		return AccountInfo{}, fmt.Errorf("auth.Query/Account %s: %w", address, err)
	}
	var acc sdk.AccountI
	if err := c.registry.UnpackAny(resp.Account, &acc); err != nil {
		return AccountInfo{}, fmt.Errorf("unpack account %s: %w", address, err)
	}
	return AccountInfo{
		AccountNumber: acc.GetAccountNumber(),
		Sequence:      acc.GetSequence(),
	}, nil
}
