// Package wire assembles the agent from its config: the connection to the
// remote MCP server that implements its operations, the A2A operation registry
// over that connection, and the read-layer indexer client.
//
// It used to assemble a great deal more — chain gRPC and CometBFT clients, a
// markets cache, tx builders, a policy engine, limits ledgers and the
// self-service auth stores — because this agent ran a vendored copy of the MCP
// server's handlers in-process. Those all live on the server now. What remains
// here is a client and a registry.
package wire

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"cosmossdk.io/log"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/svpchain/svpchain-perps-agent/internal/assistant"
	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// App is the wired agent: everything the A2A server needs to serve requests.
type App struct {
	Registry *toolbridge.Registry
	MCP      *mcpclient.Client
	Logger   log.Logger
}

// Close releases the app's long-lived connections.
func (a *App) Close() {
	if a.MCP != nil {
		a.MCP.Close()
	}
}

// Run sweeps idle MCP sessions until ctx is cancelled.
//
// It used to run the markets cache and treat its failure as fatal, because a
// stale cache meant build operations priced against stale metadata. The cache
// is the server's problem now, and there is nothing here whose failure should
// take the agent down.
func (a *App) Run(ctx context.Context) error {
	if a.MCP != nil {
		return a.MCP.Run(ctx)
	}
	<-ctx.Done()
	return nil
}

// CheckCatalog verifies the MCP server serves the surface this agent's card
// advertises.
//
// ★ Fatal, and on the boot path, which it was not while dispatch ran locally.
// Every advertised operation is now one call to that server, so a tool the
// server does not serve is an operation the card promises and nothing can
// answer. Better to refuse to start than to serve a card that lies.
func (a *App) CheckCatalog(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	remote, err := a.MCP.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("read the MCP server's catalog at %s: %w", a.MCP.Endpoint(), err)
	}
	names := make([]string, 0, len(remote))
	for _, t := range remote {
		names = append(names, t.Name)
	}
	if diff := a.Registry.DiffCatalog(names); !diff.OK() {
		var parts []string
		if len(diff.Missing) > 0 {
			parts = append(parts, fmt.Sprintf("it does not serve %v, which this agent advertises", diff.Missing))
		}
		if len(diff.Extra) > 0 {
			parts = append(parts, fmt.Sprintf("it serves %v, which this agent does not bridge", diff.Extra))
		}
		return fmt.Errorf("the MCP server at %s does not match the advertised surface: %s",
			a.MCP.Endpoint(), strings.Join(parts, "; "))
	}
	a.Logger.Info("mcp catalog matches the advertised surface",
		"endpoint", a.MCP.Endpoint(), "tools", len(names))
	return nil
}

// Build wires the configuration into a ready-to-run App.
func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	logger := log.NewLogger(os.Stderr).With("module", "perps-agent")

	mcpConn, err := mcpclient.New(mcpclient.Config{
		Endpoint:    cfg.MCP.Endpoint,
		Name:        "svpchain-perps-agent",
		Version:     "perps",
		CallTimeout: time.Duration(cfg.MCP.CallTimeout),
	})
	if err != nil {
		return nil, fmt.Errorf("mcp client: %w", err)
	}

	registry := toolbridge.NewRemote(mcpConn)

	// The model-driven skill, where the operator configured one.
	provider, why := buildProvider(cfg)
	if provider != nil {
		registry.RegisterAssistant(assistant.New(provider, mcpConn, assistant.Config{
			MaxIterations: cfg.Assistant.MaxIterations,
			MaxToolCalls:  cfg.Assistant.MaxToolCalls,
			Timeout:       time.Duration(cfg.Assistant.Timeout),
		}))
		logger.Info("assistant skill enabled", "provider", provider.Name(), "model", provider.Model())
	} else {
		logger.Info("assistant skill not served", "reason", why)
	}

	app := &App{Registry: registry, MCP: mcpConn, Logger: logger}
	if err := app.CheckCatalog(ctx); err != nil {
		app.Close()
		return nil, err
	}
	return app, nil
}

// buildProvider constructs the planner's model API from config, or explains
// why it cannot. A missing key is the ordinary case — the skill is opt-in — so
// it returns a reason rather than an error.
func buildProvider(cfg *config.Config) (assistant.Provider, string) {
	switch cfg.Assistant.Provider {
	case "", assistant.ProviderAnthropic:
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return nil, "ANTHROPIC_API_KEY is unset"
		}
		var opts []option.RequestOption
		if cfg.Assistant.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(cfg.Assistant.BaseURL))
		}
		model := cfg.Assistant.Model
		if model == "" {
			model = assistant.DefaultModel
		}
		return assistant.NewAnthropicProvider(anthropic.NewClient(opts...), model), ""

	case assistant.ProviderOpenAI:
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, "OPENAI_API_KEY is unset"
		}
		p, err := assistant.NewOpenAIProvider(assistant.OpenAIConfig{
			BaseURL: cfg.Assistant.BaseURL,
			APIKey:  key,
			Model:   cfg.Assistant.Model,
		})
		if err != nil {
			return nil, err.Error()
		}
		return p, ""
	}
	// Unreachable: config.Validate rejects any other name at load.
	return nil, "unknown provider " + cfg.Assistant.Provider
}
