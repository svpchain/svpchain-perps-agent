// Package config is this agent's configuration, loaded from a TOML file.
//
// The schema was adapted from svpchain-mcp's cmd/mcp-server config — a
// historical origin, not a live dependency, now that internal/mcp is a fork
// rather than a module require (see internal/mcp/doc.go): the same
// network endpoints, the same all-or-nothing rules for optional families, and
// the same graceful degradation — an unset optional family means those
// operations refuse at call time with a reason, and the agent still boots. On
// top of that the agent adds its A2A identity (public_url).
//
// The EVM / swap / bridge / oracle / lendora sections, and later the
// [operator] / [agent_chain] sections of the delegated-execution stack, are
// gone along with the surfaces that read them. Unknown keys are collected
// rather than rejected, so a deployed agent.toml still carrying them loads
// unchanged.
package config

import (
	"fmt"
	"path/filepath"
	"time"

	"cosmossdk.io/math"
	"github.com/BurntSushi/toml"
)

// Config is the agent's configuration.
type Config struct {
	DEXChain   DEXChainConfig `toml:"dex_chain"`
	ListenAddr string         `toml:"listen_addr"`

	// PublicURL is how callers reach this agent, advertised in the Agent Card.
	// Defaults to "http://localhost"+ListenAddr when empty.
	PublicURL string `toml:"public_url"`

	// TransferOutCapPath persists per-symbol daily transfer-out caps and the
	// running tally to a JSON file. Optional: when empty the state is
	// in-memory only and resets on restart. Relative paths resolve against
	// the config file's directory.
	TransferOutCapPath string `toml:"transfer_out_cap_path"`

	// BroadcastMode is informational for whoami; the agent always broadcasts
	// the signed tx a caller lands via broadcast_signed_tx.
	BroadcastMode string `toml:"broadcast_mode"`

	Cache  CacheConfig  `toml:"cache"`
	Limits LimitsConfig `toml:"limits"`
	Fee    FeeConfig    `toml:"fee"`
	MCP    MCPConfig    `toml:"mcp"`

	Assistant AssistantConfig `toml:"assistant"`
}

// AssistantConfig tunes the model-driven skill, which answers a plain-English
// question by planning calls over the MCP catalog.
//
// The skill is served only when an Anthropic API key is present in the
// environment AND mcp.endpoint is set — it is the one family that reaches
// outside this deployment for something other than chain data, and it costs
// money per call, so it is opt-in by configuration rather than on by default.
// The card follows: an agent without both simply does not advertise it.
//
// ★ There is deliberately no api_key field. The deploy script renders this
// file and rsyncs it to the remote host, so a key here would be written to
// disk in two places and read back by --print-config. It comes from
// ANTHROPIC_API_KEY in the process environment, which is also where the SDK
// looks by default.
type AssistantConfig struct {
	// Provider selects the model API format: "anthropic" for the native
	// Messages API, "openai" for the chat-completions format that OpenAI,
	// DeepSeek and most local runtimes serve. Empty means anthropic.
	//
	// The key comes from ANTHROPIC_API_KEY or OPENAI_API_KEY to match, so
	// switching provider switches which variable is read.
	Provider string `toml:"provider"`

	// BaseURL overrides the provider's endpoint. Ordinary configuration, not
	// a secret — it is a public address, and seeing it in a config dump is a
	// diagnostic rather than a leak.
	//
	// It is how one provider serves several vendors: point the openai
	// provider at https://api.deepseek.com for DeepSeek, or at a local
	// runtime. Empty means each provider's own default.
	BaseURL string `toml:"base_url"`

	// Model is the model id. Empty means the package default on the
	// anthropic provider; required on openai, which spans vendors that share
	// no model names.
	Model string `toml:"model"`

	// MaxIterations bounds trips through the planning loop; MaxToolCalls
	// bounds tool calls across one question. One model turn can request
	// several tools, so neither bounds the other. Zero means the defaults.
	MaxIterations int `toml:"max_iterations"`
	MaxToolCalls  int `toml:"max_tool_calls"`

	// Timeout is the wall clock for one question, planning included.
	Timeout Duration `toml:"timeout"`
}

// MCPConfig points the agent at the remote MCP server that implements its
// operations — svpchain-dex-mcp, which owns the chain clients, the tx
// builders, the policy engine and the tenant stores this agent is moving off.
//
// ★ Required. Every operation this agent advertises is one call to that
// server, so without it the agent has nothing to serve. It was optional while
// a vendored copy of the server ran in-process; dispatch has since moved, and
// the endpoint went from a diagnostic to the whole dependency.
type MCPConfig struct {
	// Endpoint is the server's Streamable HTTP URL. svpchain-dex-mcp serves
	// MCP at the root of its listener, so this is an origin and not a path.
	Endpoint string `toml:"endpoint"`

	// CallTimeout bounds one tool call, as a Go duration string ("30s").
	// Zero means the mcpclient package default.
	CallTimeout Duration `toml:"call_timeout"`
}

// DEXChainConfig points the agent at the DEX chain (an EVM-compatible
// cosmos-sdk chain): its chain id and the endpoints the chain-facing families
// share — queries and broadcast over gRPC, tx status over CometBFT RPC, reads
// over the Comlink indexer, and the chain's EVM JSON-RPC. All but the EVM
// endpoint are required.
type DEXChainConfig struct {
	ID             string `toml:"id"`
	GrpcAddr       string `toml:"grpc_addr"`
	CometRPCURL    string `toml:"comet_rpc_url"`
	IndexerBaseURL string `toml:"indexer_base_url"`
}

// FeeConfig sets the gas fee stamped onto non-CLOB txs. Short-term CLOB
// orders are gas-free on svpchain and always ship with an empty fee.
type FeeConfig struct {
	Denom    string `toml:"denom"`
	Amount   string `toml:"amount"`
	GasLimit uint64 `toml:"gas_limit"`
}

// LimitsConfig caps the size of funds movements, in human USDC. Zero
// disables the corresponding check.
type LimitsConfig struct {
	DepositMaxUSDC       uint64 `toml:"deposit_max_usdc"`
	WithdrawMaxUSDC      uint64 `toml:"withdraw_max_usdc"`
	TransferMaxUSDC      uint64 `toml:"transfer_max_usdc"`
	DailyWithdrawCapUSDC uint64 `toml:"daily_withdraw_cap_usdc"`
}

type CacheConfig struct {
	// MarketsRefresh is parsed as a Go duration string ("60s", "2m"…).
	// Zero means the package default in markets.NewCache.
	MarketsRefresh Duration `toml:"markets_refresh"`
}

// Duration parses TOML strings like "60s" into a time.Duration.
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", string(b), err)
	}
	*d = Duration(v)
	return nil
}

// Load reads and validates a TOML config file.
func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("decode TOML %s: %w", path, err)
	}
	c.ApplyDefaults()
	// Relative paths resolve against the config file's own directory, so a
	// state file kept next to agent.toml works regardless of where the agent
	// is launched from.
	if c.TransferOutCapPath != "" && !filepath.IsAbs(c.TransferOutCapPath) {
		c.TransferOutCapPath = filepath.Join(filepath.Dir(path), c.TransferOutCapPath)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// ApplyDefaults fills the fields a config may omit.
func (c *Config) ApplyDefaults() {
	if c.BroadcastMode == "" {
		c.BroadcastMode = "server"
	}
	if c.PublicURL == "" && c.ListenAddr != "" {
		c.PublicURL = "http://localhost" + c.ListenAddr
	}
	c.Fee.applyDefaults()
}

// Validate enforces the required network-level fields and the optional
// families' invariants.
func (c *Config) Validate() error {
	if c.DEXChain.ID == "" {
		return fmt.Errorf("dex_chain.id is required")
	}
	if c.DEXChain.GrpcAddr == "" {
		return fmt.Errorf("dex_chain.grpc_addr is required")
	}
	if c.DEXChain.CometRPCURL == "" {
		return fmt.Errorf("dex_chain.comet_rpc_url is required")
	}
	if c.DEXChain.IndexerBaseURL == "" {
		return fmt.Errorf("dex_chain.indexer_base_url is required")
	}
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if c.MCP.Endpoint == "" {
		return fmt.Errorf("mcp.endpoint is required: every operation this agent serves is a call to that server")
	}
	if err := c.Fee.validate(); err != nil {
		return err
	}
	return c.Assistant.validate()
}

// validate rejects a provider name that is not one of the two. Left to default
// silently, a typo would run the wrong API against the wrong key variable and
// fail at the first question rather than at boot.
func (a *AssistantConfig) validate() error {
	switch a.Provider {
	case "", "anthropic", "openai":
		return nil
	default:
		return fmt.Errorf("assistant.provider %q is not one of anthropic, openai", a.Provider)
	}
}

// Default fee applied to non-CLOB txs when the [fee] section is absent.
// Matches a chain whose minimum-gas-prices is 25000000000asvp at a
// 1,000,000 gas limit (≈0.025 SVP total).
const (
	DefaultFeeDenom    = "asvp"
	DefaultFeeAmount   = "25000000000000000"
	DefaultFeeGasLimit = uint64(1_000_000)
)

func (f *FeeConfig) applyDefaults() {
	if f.Denom == "" {
		f.Denom = DefaultFeeDenom
	}
	if f.Amount == "" {
		f.Amount = DefaultFeeAmount
	}
	if f.GasLimit == 0 {
		f.GasLimit = DefaultFeeGasLimit
	}
}

func (f *FeeConfig) validate() error {
	if f.Denom == "" {
		return fmt.Errorf("fee.denom is required")
	}
	amt, ok := math.NewIntFromString(f.Amount)
	if !ok {
		return fmt.Errorf("fee.amount %q is not a valid integer", f.Amount)
	}
	if amt.IsNegative() {
		return fmt.Errorf("fee.amount %q must be non-negative", f.Amount)
	}
	return nil
}
