package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Dotted dex_chain.* keys (not a [dex_chain] header) so tests can keep
// appending top-level keys to this fixture without them landing inside the
// table.
const minimal = `
dex_chain.id               = "svp-test-1"
dex_chain.grpc_addr        = "127.0.0.1:9090"
dex_chain.comet_rpc_url    = "http://127.0.0.1:26657"
dex_chain.indexer_base_url = "http://127.0.0.1:3002"
listen_addr                = ":8081"
mcp.endpoint               = "https://mcp.example.test/"
`

// withoutMCP is the minimal fixture minus its endpoint, for the cases that
// supply their own [mcp] table or assert the field is required.
func withoutMCP() string {
	return strings.ReplaceAll(minimal, `mcp.endpoint               = "https://mcp.example.test/"`+"\n", "")
}

func TestLoadMinimalAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Fee.Denom != DefaultFeeDenom || cfg.Fee.Amount != DefaultFeeAmount || cfg.Fee.GasLimit != DefaultFeeGasLimit {
		t.Errorf("fee defaults not applied: %+v", cfg.Fee)
	}
	if cfg.BroadcastMode != "server" {
		t.Errorf("broadcast_mode default not applied: %q", cfg.BroadcastMode)
	}
	if cfg.PublicURL != "http://localhost:8081" {
		t.Errorf("public_url default not derived from listen_addr: %q", cfg.PublicURL)
	}
}

func TestLoadRejectsMissingRequiredFields(t *testing.T) {
	for _, missing := range []string{"dex_chain.id", "dex_chain.grpc_addr", "dex_chain.comet_rpc_url", "dex_chain.indexer_base_url", "listen_addr"} {
		t.Run(missing, func(t *testing.T) {
			var body strings.Builder
			for _, line := range strings.Split(strings.TrimSpace(minimal), "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), missing) {
					body.WriteString(line + "\n")
				}
			}
			if _, err := Load(writeConfig(t, body.String())); err == nil || !strings.Contains(err.Error(), missing) {
				t.Errorf("expected error naming %s, got %v", missing, err)
			}
		})
	}
}

// ★ The [evm] schema was removed with the EVM and Lendora surfaces, and the
// [operator] / [agent_chain] schema with delegated execution, but agents
// already deployed have an agent.toml on disk that still carries those blocks.
// TOML decoding must ignore them rather than reject the file — otherwise
// shrinking the schema silently turns every running deployment into a boot
// failure on its next restart, with no config change on the operator's side.
func TestRetiredEVMKeysAreIgnoredNotRejected(t *testing.T) {
	body := minimal + `
dex_chain.evm_rpc_url              = "http://127.0.0.1:8545"
evm.swap.uniswap_router_addr       = "0x0000000000000000000000000000000000000001"
evm.swap.wsvp_addr                 = "0x0000000000000000000000000000000000000002"
evm.lendora.comptroller_addr       = "0x0000000000000000000000000000000000000003"
evm.bridge.addr                    = "0x0000000000000000000000000000000000000004"
evm.bridge.routes_path             = "routes.json"
evm.bridge.source_chain_id         = 1
agent_chain.id                     = "svpagent-1"
agent_chain.rest_url               = "http://127.0.0.1:1317"
operator.key_file                  = "operator.key"

[[evm.bridge.foreign_chain]]
chain_id    = 421614
rpc_url     = "http://foreign:8545"
bridge_addr = "0x0000000000000000000000000000000000000005"
`
	if _, err := Load(writeConfig(t, body)); err != nil {
		t.Errorf("a config carrying retired blocks must still load, got %v", err)
	}
}

func TestFeeAmountMustBeANonNegativeInteger(t *testing.T) {
	body := minimal + `
[fee]
denom  = "asvp"
amount = "not-a-number"
`
	if _, err := Load(writeConfig(t, body)); err == nil || !strings.Contains(err.Error(), "fee.amount") {
		t.Errorf("bad fee amount must fail, got %v", err)
	}
}

// The MCP endpoint is how a deploy names the remote server this agent's
// operations run on. It is optional while the agent still answers from its own
// handlers, so both states have to load.
func TestMCPSectionLoads(t *testing.T) {
	cfg, err := Load(writeConfig(t, withoutMCP()+`
[mcp]
endpoint     = "https://dex-mcp-testnet.svpchain.org/"
call_timeout = "45s"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCP.Endpoint; got != "https://dex-mcp-testnet.svpchain.org/" {
		t.Errorf("endpoint = %q", got)
	}
	if got := time.Duration(cfg.MCP.CallTimeout); got != 45*time.Second {
		t.Errorf("call_timeout = %v, want 45s", got)
	}
}

// ★ Every operation is a call to the MCP server, so a config without one
// describes an agent with nothing to serve. This was optional while a vendored
// copy of that server ran in-process.
func TestMCPEndpointIsRequired(t *testing.T) {
	_, err := Load(writeConfig(t, withoutMCP()))
	if err == nil {
		t.Fatal("a config with no mcp.endpoint was accepted")
	}
	if !strings.Contains(err.Error(), "mcp.endpoint") {
		t.Errorf("the error does not name the missing field: %v", err)
	}
}

// A provider name that is not one of the two must fail at load. Defaulting a
// typo to anthropic would run the wrong API against the wrong key variable and
// surface at the first question instead of at boot.
func TestAssistantProviderMustBeKnown(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", ""} {
		body := minimal
		if name != "" {
			body += "\n[assistant]\nprovider = \"" + name + "\"\n"
		}
		if _, err := Load(writeConfig(t, body)); err != nil {
			t.Errorf("provider %q was rejected: %v", name, err)
		}
	}
	_, err := Load(writeConfig(t, minimal+`
[assistant]
provider = "gemini"
`))
	if err == nil {
		t.Fatal("an unknown provider name was accepted")
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("the error does not name the valid options: %v", err)
	}
}

func TestAssistantProviderSettingsLoad(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimal+`
[assistant]
provider = "openai"
base_url = "https://api.deepseek.com"
model    = "deepseek-v4-pro"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Assistant.Provider != "openai" || cfg.Assistant.BaseURL != "https://api.deepseek.com" ||
		cfg.Assistant.Model != "deepseek-v4-pro" {
		t.Errorf("assistant config = %+v", cfg.Assistant)
	}
}
