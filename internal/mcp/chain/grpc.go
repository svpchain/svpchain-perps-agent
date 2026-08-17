package chain

import (
	"context"
	"fmt"

	daemontypes "github.com/dydxprotocol/v4-chain/protocol/daemons/types"
	"google.golang.org/grpc"
)

// Dial opens a gRPC connection to the chain's gRPC server at addr. Reuses
// daemons/types.GrpcClientImpl.NewTcpConnection so dial behavior matches
// the existing daemons exactly (HTTP/2 multiplex, insecure local creds).
func Dial(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	g := &daemontypes.GrpcClientImpl{}
	conn, err := g.NewTcpConnection(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("dial chain gRPC %s: %w", addr, err)
	}
	return conn, nil
}
