package tools

import (
	"cosmossdk.io/log"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/auth"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/chain"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/faucet"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/indexer"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/limits"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/policy"
)

// ChainDeps groups the gRPC clients used by tool handlers.
type ChainDeps struct {
	Account         chain.AccountClient
	Broadcast       chain.BroadcastClient
	ClobQuery       chain.ClobQueryClient
	PerpetualsQuery chain.PerpetualsQueryClient
	SubaccountQuery chain.SubaccountQueryClient
	BankQuery       chain.BankQueryClient
	CometBft        chain.CometBftClient
}

// Deps is the full dependency bundle every tool handler receives. v0.1
// keeps it flat; v0.2 may split into smaller per-capability bundles when
// the handler count grows.
type Deps struct {
	Chain   ChainDeps
	Indexer *indexer.Client
	Markets *markets.Cache
	Builder *builder.Assembler

	// Faucet is the HTTP client for the faucet backend (faucet_base_url).
	// Nil when the server runs without faucet_base_url; the faucet tools
	// check Faucet != nil and refuse otherwise.
	Faucet *faucet.Client

	Policy      *policy.Engine
	Auditor     *policy.Auditor
	Idempotency *policy.Idempotency
	RateLimit   *policy.RateLimiter

	// Limits + WithdrawLedger drive the v0.2.3 funds-tool safety rails.
	// Limits is a pure config; WithdrawLedger holds per-tenant daily spend
	// state (MemoryLedger by default, swappable for a durable backend
	// without touching handler code).
	Limits         limits.Config
	WithdrawLedger limits.WithdrawLedger

	// TransferOut holds each owner wallet's per-symbol daily "transfer out" caps
	// and usage (svp / usdc / usdv), keyed by owner address so all of a wallet's
	// concurrent agents / re-auths share one cap and daily total. Upstream
	// accumulated a symbol's outflow across two rails — x/bank sends
	// (build_bank_send) and EVM transfers (broadcast_evm_tx); only the x/bank
	// rail survives the absorption, and it is enforced at broadcast. Caps are
	// set at runtime via set_transfer_out_cap; there is no operator config.
	// In-memory; resets on restart / UTC midnight.
	TransferOut *limits.MemoryTransferOutStore

	// Self-service auth backend (v0.3). NonceStore + DynamicTenants are
	// populated by auth_challenge / auth_verify; IPChallengeLimit caps
	// auth_challenge per-IP since the tool runs before any tenant
	// context is established.
	NonceStore       *auth.NonceStore
	DynamicTenants   *auth.DynamicTenantStore
	IPChallengeLimit *auth.IPRateLimiter
	SessionBearers   *auth.SessionBearers

	Logger log.Logger

	// InterfaceRegistry is used by broadcast_signed_tx to decode the
	// signer pubkey (eth_secp256k1) carried inside the TxRaw's AuthInfo,
	// and to verify the resulting bech32 address matches the tenant's
	// configured owner.
	InterfaceRegistry codectypes.InterfaceRegistry

	// BroadcastMode reports which broadcast variant is configured (for
	// whoami). v0.1 always "server" — server broadcasts the signed tx
	// the client returns.
	BroadcastMode string
}
