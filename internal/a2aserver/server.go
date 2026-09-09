package a2aserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/wire"
)

// StartFullFor serves one binary's agent: every operation in its wired
// registry under its own card identity, with the auth resolver mapping A2A
// callers onto tool tenants. It runs the app's background caches alongside
// the HTTP server and stops both when ctx is cancelled or either fails.
func StartFullFor(ctx context.Context, cfg *config.Config, app *wire.App, ident CardIdentity) error {
	executor := NewFullExecutor(app.Registry, &AuthResolver{})

	card := BuildAgentCardFor(ident, cfg.PublicURL, app.Registry)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cacheErr := make(chan error, 1)
	go func() { cacheErr <- app.Run(ctx) }()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serve(ctx, cfg.ListenAddr, cfg.PublicURL, app.MCP.Endpoint(), executor, card)
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

func serve(ctx context.Context, listenAddr, publicURL, mcpEndpoint string, executor *Executor, card *a2a.AgentCard) error {
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
	fmt.Fprintf(os.Stderr, "%s: operations served by %s\n", card.Name, mcpEndpoint)

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
