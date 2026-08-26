// Package wire assembles the agent's dependency graph from its config: the
// chain gRPC + CometBFT clients, the indexer client, the markets cache, the
// self-service auth stores, the policy engine, and the MCP tool handlers the
// A2A tool bridge dispatches into.
//
// The body deliberately mirrors the wiring in svpchain-mcp's cmd/mcp-server:
// same optional families, same all-or-nothing rules, same graceful
// degradation. Drift between the two is a bug in whichever copied last — and
// now that internal/mcp is a fork of that repo's lib/mcp rather than a
// dependency on it (see internal/mcp/doc.go), nothing makes the drift fail
// loudly. Check both when changing either.
package wire

import (
	"context"
	"fmt"
	"os"
	"time"

	"cosmossdk.io/log"
	"google.golang.org/grpc"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/auth"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/chain"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/indexer"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/limits"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/mcpcodec"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/policy"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// App is the wired agent: everything the A2A server needs to serve requests,
// plus the background caches it must run.
type App struct {
	Handlers *tools.Handlers      // the MCP tool handlers
	Registry *toolbridge.Registry // A2A operation registry over them
	Markets  *markets.Cache       // must Run(ctx); initial refresh failure is fatal
	Tenants  *auth.DynamicTenantStore
	Sessions *auth.SessionBearers
	Indexer  *indexer.Client
	GrpcConn *grpc.ClientConn
	Logger   log.Logger
}

// Close releases the app's long-lived connections.
func (a *App) Close() {
	if a.GrpcConn != nil {
		_ = a.GrpcConn.Close()
	}
}

// Run starts the markets cache refresher and blocks until ctx is cancelled.
// The cache must complete its initial refresh before build operations work,
// and its Run fails fast when the chain is unreachable — surfacing a dead gRPC
// endpoint at boot instead of on the first trade.
func (a *App) Run(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- a.Markets.Run(ctx) }()
	select {
	case err := <-errc:
		if err != nil && ctx.Err() == nil {
			return err
		}
	case <-ctx.Done():
	}
	return nil
}

// dynamicTenantAdapter converts auth.TenantRecord into policy.TenantPolicy so
// the policy engine can resolve auto-issued tenants; kept here so auth never
// imports policy (mirrors the mcp-server adapter).
type dynamicTenantAdapter struct{ store *auth.DynamicTenantStore }

func (a dynamicTenantAdapter) LookupTenantPolicy(tenantID string) (policy.TenantPolicy, bool) {
	rec, err := a.store.LookupByTenantID(tenantID)
	if err != nil {
		return policy.TenantPolicy{}, false
	}
	return policy.TenantPolicy{
		TenantID:           rec.TenantID,
		Owner:              rec.Owner,
		AllowedSubaccounts: rec.AllowedSubaccounts,
		KillSwitch:         rec.KillSwitch,
	}, true
}

// BuildProfile wires the configuration into a ready-to-run App registering
// only the profile's operation families.
func BuildProfile(ctx context.Context, cfg *config.Config, p Profile) (*App, error) {
	logger := log.NewLogger(os.Stderr).With("module", "remote-agent", "profile", p.Name)

	grpcConn, err := chain.Dial(ctx, cfg.DEXChain.GrpcAddr)
	if err != nil {
		return nil, fmt.Errorf("dial chain gRPC: %w", err)
	}
	encCfg := mcpcodec.GetEncodingConfig()

	chainDeps := tools.ChainDeps{
		Account:         chain.NewAccountClient(grpcConn, encCfg.InterfaceRegistry),
		Broadcast:       chain.NewBroadcastClient(grpcConn),
		ClobQuery:       chain.NewClobQueryClient(grpcConn),
		PerpetualsQuery: chain.NewPerpetualsQueryClient(grpcConn),
		SubaccountQuery: chain.NewSubaccountQueryClient(grpcConn),
		BankQuery:       chain.NewBankQueryClient(grpcConn),
	}
	cometClient, err := chain.NewCometBftClient(cfg.DEXChain.CometRPCURL)
	if err != nil {
		grpcConn.Close()
		return nil, fmt.Errorf("cometbft client: %w", err)
	}
	chainDeps.CometBft = cometClient

	idx := indexer.NewClient(cfg.DEXChain.IndexerBaseURL, indexer.Options{})
	mkts := markets.NewCache(chainDeps.ClobQuery, chainDeps.PerpetualsQuery, time.Duration(cfg.Cache.MarketsRefresh), logger)

	limitsCfg := limits.Config{
		DepositMaxUSDC:       cfg.Limits.DepositMaxUSDC,
		WithdrawMaxUSDC:      cfg.Limits.WithdrawMaxUSDC,
		TransferMaxUSDC:      cfg.Limits.TransferMaxUSDC,
		DailyWithdrawCapUSDC: cfg.Limits.DailyWithdrawCapUSDC,
	}
	withdrawLedger := limits.NewMemoryLedger(limitsCfg.DailyWithdrawCapUSDC, nil)
	transferOut, err := limits.LoadMemoryTransferOutStore(cfg.TransferOutCapPath, nil, func(err error) {
		logger.Error("transfer-out cap persistence failed", "error", err)
	})
	if err != nil {
		grpcConn.Close()
		return nil, fmt.Errorf("load transfer-out cap state: %w", err)
	}

	// Self-service auth state: in-memory + TTL-bounded, same defaults as the
	// MCP server (auto-issued tenants get subaccounts 0..9).
	nonceStore := auth.NewNonceStore(auth.DefaultChallengeTTL, nil)
	dynamicTenants := auth.NewDynamicTenantStore(auth.DynamicTenantStoreConfig{
		BearerTTL:                 auth.DefaultBearerTTL,
		DefaultAllowedSubaccounts: []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	}, nil)
	ipLimit := auth.NewIPRateLimiter(auth.DefaultIPChallengeRate, auth.DefaultIPChallengeWindow, nil)
	sessionBearers := auth.NewSessionBearers(auth.DefaultBearerTTL, nil)

	// Bearer-minted tenants ("auto-…") resolve through the engine's dynamic
	// fallback slot.
	policyEngine := policy.NewEngine(nil)
	policyEngine.SetDynamicSource(dynamicTenantAdapter{store: dynamicTenants})

	deps := tools.Deps{
		Chain:             chainDeps,
		Indexer:           idx,
		Markets:           mkts,
		Builder:           builder.NewAssembler(cfg.DEXChain.ID, cfg.Fee.Denom, cfg.Fee.Amount, cfg.Fee.GasLimit),
		Policy:            policyEngine,
		Auditor:           policy.NewStdoutAuditor(),
		Idempotency:       policy.NewIdempotency(0),
		RateLimit:         policy.NewRateLimiter(0, 0),
		Limits:            limitsCfg,
		WithdrawLedger:    withdrawLedger,
		TransferOut:       transferOut,
		NonceStore:        nonceStore,
		DynamicTenants:    dynamicTenants,
		IPChallengeLimit:  ipLimit,
		SessionBearers:    sessionBearers,
		Logger:            logger,
		InterfaceRegistry: encCfg.InterfaceRegistry,
		BroadcastMode:     cfg.BroadcastMode,
	}
	handlers := tools.New(cfg.DEXChain.ID, deps)

	registry := toolbridge.NewEmpty()

	p.Register(registry, handlers)

	return &App{
		Handlers: handlers,
		Registry: registry,
		Markets:  mkts,
		Tenants:  dynamicTenants,
		Sessions: sessionBearers,
		Indexer:  idx,
		GrpcConn: grpcConn,
		Logger:   logger,
	}, nil
}
