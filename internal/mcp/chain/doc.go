// Package chain wraps the svpchain gRPC and CometBFT RPC clients the tool
// handlers query through.
//
// Each service gets its own file and a small interface (so mockery can
// generate test doubles): AccountClient (auth.Query), BroadcastClient
// (sdktx.Service, BROADCAST_MODE_SYNC), ClobQueryClient, SubaccountQueryClient,
// BankQueryClient, and CometBftClient (tx-status via
// cometbft/rpc/client/http). Upstream's EVMClient (EVM JSON-RPC) was dropped in
// the absorption along with the tool families that dialed it — see
// internal/mcp/doc.go.
//
// internal/agentrest implements AccountClient and BroadcastClient against a
// Cosmos REST API, for deployments whose agent-identity modules live on a
// different chain than the DEX.
//
// gRPC dialing reuses daemons/types.GrpcClientImpl.NewTcpConnection so the
// dial pattern matches existing daemons exactly.
package chain
