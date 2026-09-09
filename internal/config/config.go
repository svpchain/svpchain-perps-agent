// Package config is this agent's configuration, loaded from a TOML file.
//
// It is short, because the agent is. Where it once described a whole DEX
// service — chain endpoints, a markets cache refresh, gas fees, funds limits,
// a transfer-cap state file — those all configured a vendored copy of the MCP
// server running in-process. That copy is gone, so those settings belong to
// the server now, and what is left is this agent's own identity plus where to
// find that server.
//
// Unknown keys are collected rather than rejected, so a deployed agent.toml
// still carrying the retired blocks loads unchanged and an upgrade does not
// need the config replaced first.
package config

import (
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the agent's configuration.
type Config struct {
	ListenAddr string `toml:"listen_addr"`

	// PublicURL is how callers reach this agent, advertised in the Agent Card.
	// Defaults to "http://localhost"+ListenAddr when empty.
	PublicURL string `toml:"public_url"`

	MCP MCPConfig `toml:"mcp"`

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
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// ApplyDefaults fills the fields a config may omit.
func (c *Config) ApplyDefaults() {
	if c.PublicURL == "" && c.ListenAddr != "" {
		c.PublicURL = "http://localhost" + c.ListenAddr
	}
}

// Validate enforces the required network-level fields and the optional
// families' invariants.
func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if c.MCP.Endpoint == "" {
		return fmt.Errorf("mcp.endpoint is required: every operation this agent serves is a call to that server")
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
