// Command svpchain-perps-agent is the perpetuals-trading A2A agent for an
// SVP-Chain DEX: market data, accounts, unsigned order and funds tx building,
// the Cosmos broadcast rail, self-service auth, the chain's agent/agentwallet
// modules, and delegated perps execution when an operator key is configured.
//
// Everything it serves is implemented under internal/, which was the shared
// svpchain-agent-core library until that repo was retired. This binary is the
// only consumer, so the library was pruned to the surface it actually serves —
// there is no EVM DeFi or Lendora code, and no profile but perps.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/svpchain/svpchain-perps-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/wire"
)

func main() {
	configPath := flag.String("config", "", "TOML config (see internal/config)")
	flag.Parse()

	// Stop cleanly on SIGINT/SIGTERM so a container orchestrator gets a prompt
	// exit rather than a killed process.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "svpchain-perps-agent: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string) error {
	if configPath == "" {
		return fmt.Errorf("-config is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	app, err := wire.BuildProfile(ctx, cfg, wire.PerpsProfile)
	if err != nil {
		return err
	}
	defer app.Close()
	return a2aserver.StartFullFor(ctx, cfg, app, identity)
}
