// Command svpchain-perps-agent is the perpetuals-trading A2A agent for an
// SVP-Chain DEX: market data, accounts, unsigned order and funds tx building,
// the Cosmos broadcast rail, self-service auth, faucet, the chain's
// agent/agentwallet modules, and delegated perps execution when an operator
// key is configured.
//
// It is the perps-category slice of the full-surface svpchain-remote-agents; the
// EVM DeFi and Lendora families live in their own binaries.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/svpchain/svpchain-agent-core/a2aserver"
	"github.com/svpchain/svpchain-agent-core/config"
	"github.com/svpchain/svpchain-agent-core/wire"
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
