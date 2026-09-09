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

const minimal = `
listen_addr  = ":8081"
mcp.endpoint = "https://mcp.example.test/"
`

func TestLoadMinimalAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "http://localhost:8081" {
		t.Errorf("public_url = %q, want the listen addr", cfg.PublicURL)
	}
}

func TestLoadRejectsMissingRequiredFields(t *testing.T) {
	for name, body := range map[string]string{
		"no listen_addr":  `mcp.endpoint = "https://mcp.example.test/"`,
		"no mcp.endpoint": `listen_addr = ":8081"`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// ★ Every operation is one call to the MCP server, so a config without one
// describes an agent with nothing to serve. It was optional while a vendored
// copy of that server ran in-process.
func TestMCPEndpointIsRequired(t *testing.T) {
	_, err := Load(writeConfig(t, `listen_addr = ":8081"`))
	if err == nil {
		t.Fatal("a config with no mcp.endpoint was accepted")
	}
	if !strings.Contains(err.Error(), "mcp.endpoint") {
		t.Errorf("the error does not name the missing field: %v", err)
	}
}

// ★ A deployed agent.toml still carrying the blocks that configured the
// in-process copy — chain endpoints, fees, limits, the markets cache — must
// still load, so upgrading the binary does not require replacing the config
// first.
func TestRetiredBlocksAreIgnoredNotRejected(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimal+`
broadcast_mode        = "server"
transfer_out_cap_path = "caps.json"

[dex_chain]
id               = "svp-2517-1"
grpc_addr        = "127.0.0.1:9090"
comet_rpc_url    = "http://127.0.0.1:26657"
indexer_base_url = "http://127.0.0.1:3002"

[evm]
rpc_url = "http://127.0.0.1:8545"

[cache]
markets_refresh = "60s"

[limits]
deposit_max_usdc = 1000

[fee]
denom = "asvp"
`))
	if err != nil {
		t.Fatalf("a config carrying retired blocks must still load: %v", err)
	}
	if cfg.MCP.Endpoint != "https://mcp.example.test/" {
		t.Errorf("endpoint = %q", cfg.MCP.Endpoint)
	}
}

func TestMCPSectionLoads(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
listen_addr = ":8081"

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

// A provider name that is not one of the two must fail at load, or a typo runs
// the wrong API against the wrong key variable and surfaces at the first
// question instead of at boot.
func TestAssistantProviderMustBeKnown(t *testing.T) {
	for _, name := range []string{"anthropic", "openai"} {
		if _, err := Load(writeConfig(t, minimal+"\n[assistant]\nprovider = \""+name+"\"\n")); err != nil {
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
