package a2aserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/marketdata"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
	"github.com/svpchain/svpchain-perps-agent/internal/wire"
)

// StartFullFor serves one binary's agent: every operation in its wired
// registry under its own card identity, with the auth resolver mapping A2A
// callers onto tool tenants. It runs the app's background caches alongside
// the HTTP server and stops both when ctx is cancelled or either fails.
func StartFullFor(ctx context.Context, cfg *config.Config, app *wire.App, ident CardIdentity) error {
	// The legacy {"skill":"svpchain-market-data","query":…} path answers from
	// this service before the registry is consulted, so a binary that does not
	// register the market-data family must not construct it — otherwise it
	// would serve queries its card never advertises.
	var market *marketdata.Service
	if len(app.Registry.BySkill()[toolbridge.SkillMarketData]) > 0 {
		market = marketdata.NewService(app.Indexer)
	}

	executor := NewFullExecutor(
		market,
		app.Registry,
		&AuthResolver{Tenants: app.Tenants, Sessions: app.Sessions},
		app.Delegated,
		app.ReadTenants,
	)

	card := BuildAgentCardFor(ident, cfg.PublicURL, app.Registry)

	// Hand the delegated service the exact bytes the card route serves
	// (NewStaticAgentCardHandler marshals the card the same way), so the
	// capability hash it registers on chain verifies against a fetch of
	// /.well-known/agent-card.json.
	if app.Delegated != nil {
		cardJSON, err := json.Marshal(card)
		if err != nil {
			return fmt.Errorf("marshal agent card: %w", err)
		}
		app.Delegated.SetCapabilityCard(cardJSON)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cacheErr := make(chan error, 1)
	go func() { cacheErr <- app.Run(ctx) }()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serve(ctx, cfg.ListenAddr, cfg.PublicURL, cfg.DEXChain.IndexerBaseURL, executor, card)
	}()

	// Either half failing takes the whole agent down: a dead markets cache
	// means build operations silently price against stale metadata, and a
	// dead HTTP server means nothing answers — neither is a state to limp in.
	select {
	case err := <-cacheErr:
		cancel()
		<-serveErr
		if err != nil {
			return fmt.Errorf("markets cache: %w", err)
		}
		return nil
	case err := <-serveErr:
		cancel()
		return err
	}
}

func serve(ctx context.Context, listenAddr, publicURL, indexerURL string, executor *Executor, card *a2a.AgentCard) error {
	handler := a2asrv.NewHandler(executor)
	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	// A plain liveness endpoint, so the agent can sit behind a load balancer
	// without the balancer needing to understand A2A.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	fmt.Fprintf(os.Stderr, "%s: listening on %s\n", card.Name, listenAddr)
	fmt.Fprintf(os.Stderr, "%s: agent card at %s%s\n", card.Name, publicURL, a2asrv.WellKnownAgentCardPath)
	fmt.Fprintf(os.Stderr, "%s: reading indexer at %s\n", card.Name, indexerURL)

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
