package deps_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
)

// scripts/deploy.sh renders this agent's agent.toml itself. This pins the two
// together: whatever the script prints must parse and validate under core's
// config package, so a schema change that would brick a deploy fails here
// rather than on a remote host.
//
// It lives beside the script rather than in core, because after the split the
// script is this repo's and core cannot see it.
//
// Every invocation in this file passes --no-config: the script reads
// ~/.config/svpchain-perps-agent/config.sh, and a developer who has one would
// otherwise be testing their own host and chain rather than the defaults these
// cases assert.
func TestDeployScriptConfigParses(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	// A keyed variant needs a real key file: the script validates it is 32
	// bytes of hex before rendering an [operator] block for it.
	keyFile := filepath.Join(t.TempDir(), "operator.key")
	if err := os.WriteFile(keyFile, []byte(strings.Repeat("a1", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]string{
		"keyless": {"--print-config", "--host", "www@agent.example.com"},
		"keyed": {
			"--print-config", "--host", "www@agent.example.com",
			"--operator-key-file", keyFile,
			"--public-url", "https://agents.example.com",
		},
		// An optional family switched off must still yield a config that
		// loads — the faucet skills then refuse at call time with a reason.
		// The faucet is the only optional family this script renders: the
		// EVM/bridge/lendora surface went with the multi-agent scaffolding,
		// because wire.PerpsProfile builds no EVM clients and this binary
		// never read it.
		"faucet-off": {
			"--print-config", "--host", "www@agent.example.com",
			"--faucet-url", "",
		},
		// The other direction: every optional block the script can render, on
		// at once, so a typo in one of those heredocs fails here rather than
		// on a remote host. [agent_chain] is both-or-neither in core, which
		// this also pins.
		"all-optionals": {
			"--print-config", "--host", "www@agent.example.com",
			"--agent-chain-id", "svp-agent-1",
			"--agent-chain-rest", "http://127.0.0.1:1317",
			"--deposit-max-usdc", "1000", "--withdraw-max-usdc", "500",
			"--transfer-max-usdc", "250", "--daily-withdraw-cap-usdc", "2000",
			"--markets-refresh", "60s",
		},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := exec.Command("bash", append([]string{script, "--no-config"}, args...)...).Output()
			if err != nil {
				t.Fatalf("render config: %v", err)
			}
			path := filepath.Join(t.TempDir(), "agent.toml")
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("rendered config does not parse/validate:\n%s\nerror: %v", out, err)
			}
			if cfg.PublicURL == "" {
				t.Error("rendered config must carry a public_url")
			}
			if name == "keyed" && cfg.Operator.KeyFile == "" {
				t.Error("keyed variant must set key_file")
			}
			if name == "all-optionals" {
				if cfg.AgentChain.RestURL == "" {
					t.Error("all-optionals must render [agent_chain]")
				}
				if cfg.Limits.DepositMaxUSDC != 1000 {
					t.Errorf("deposit_max_usdc = %d, want 1000", cfg.Limits.DepositMaxUSDC)
				}
			}
		})
	}
}

// The route and the card must agree. An agent advertises public_url inside its
// Agent Card, a verifier fetches that URL to recompute the capability hash, and
// nginx is what makes the URL resolve. If the location block and public_url
// disagree on the path segment, the agent advertises a URL that 404s and reads
// as unverified — with every process healthy and nothing in the logs.
//
// Both come from the same two constants, AGENT_PORT and AGENT_SEGMENT, so this
// asserts they stay wired to them rather than to two hand-maintained copies of
// the same fact.
func TestDeployScriptNginxRouteMatchesConfig(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	const base = "https://agents.example.com"
	run := func(mode string) string {
		out, err := exec.Command("bash", script, "--no-config", mode, "--host", "www@agent.example.com",
			"--public-url", base).Output()
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		return string(out)
	}

	path := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(path, []byte(run("--print-config")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	wantSeg := strings.TrimPrefix(cfg.PublicURL, base) // "/perps"
	if wantSeg == "" || wantSeg == cfg.PublicURL {
		t.Fatalf("public_url %q does not extend the base %q with a path segment", cfg.PublicURL, base)
	}
	wantPort := cfg.ListenAddr[strings.LastIndex(cfg.ListenAddr, ":"):] // ":8082"

	nginx := run("--print-nginx")
	if want := "location " + wantSeg + "/ {"; !strings.Contains(nginx, want) {
		t.Errorf("nginx block does not route the advertised path.\nwant %q\ngot:\n%s", want, nginx)
	}
	if want := "proxy_pass http://127.0.0.1" + wantPort + "/;"; !strings.Contains(nginx, want) {
		t.Errorf("nginx block does not proxy to the configured port.\nwant %q\ngot:\n%s", want, nginx)
	}
	// Without the trailing slash on proxy_pass the prefix is forwarded, and the
	// agent — which binds at root — 404s every request through the proxy.
	if strings.Contains(nginx, "proxy_pass http://127.0.0.1"+wantPort+";") {
		t.Error("proxy_pass must end in / so the path segment is stripped")
	}
}

// The config file is the normal way to drive a deploy: sourced from
// ~/.config/svpchain-perps-agent/config.sh so a routine install takes no flags.
// It is sourced rather than parsed so it can compute values, which is also why
// the script refuses one other users can write.
func TestDeployScriptReadsConfigFile(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	dir := t.TempDir()
	settings := "SVPCHAIN_DEPLOY_HOST=\"www@host.example.com\"\n" +
		"SVPCHAIN_CHAIN_ID=\"svp-from-file-1\"\n" +
		"SVPCHAIN_AGENT_PUBLIC_URL=\"https://agents.example.org\"\n" +
		"SVPCHAIN_MARKETS_REFRESH=\"90s\"\n" +
		"SVPCHAIN_WITHDRAW_MAX_USDC=\"777\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.sh"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	load := func(t *testing.T, extra ...string) *config.Config {
		t.Helper()
		args := append([]string{script, "--config-dir", dir, "--print-config"}, extra...)
		out, err := exec.Command("bash", args...).Output()
		if err != nil {
			t.Fatalf("render config: %v", err)
		}
		path := filepath.Join(t.TempDir(), "agent.toml")
		if err := os.WriteFile(path, out, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("rendered config does not parse/validate:\n%s\nerror: %v", out, err)
		}
		return cfg
	}

	t.Run("scalars come from the file", func(t *testing.T) {
		cfg := load(t)
		if cfg.DEXChain.ID != "svp-from-file-1" {
			t.Errorf("chain id = %q, want the file's", cfg.DEXChain.ID)
		}
		if cfg.PublicURL != "https://agents.example.org/perps" {
			t.Errorf("public_url = %q, want the file's base + this agent's segment", cfg.PublicURL)
		}
	})

	// These two had no env var at all before the config file existed — they
	// were flag-only, so the file is the first thing that can set them.
	t.Run("tuning and caps come from the file", func(t *testing.T) {
		cfg := load(t)
		if cfg.Limits.WithdrawMaxUSDC != 777 {
			t.Errorf("withdraw cap = %d, want the file's 777", cfg.Limits.WithdrawMaxUSDC)
		}
		if got := time.Duration(cfg.Cache.MarketsRefresh); got != 90*time.Second {
			t.Errorf("markets refresh = %s, want the file's 90s", got)
		}
	})

	t.Run("a flag overrides the file", func(t *testing.T) {
		cfg := load(t, "--chain-id", "svp-from-flag-1")
		if cfg.DEXChain.ID != "svp-from-flag-1" {
			t.Errorf("chain id = %q, want the flag's", cfg.DEXChain.ID)
		}
	})

	t.Run("--no-config ignores the file", func(t *testing.T) {
		cfg := load(t, "--no-config")
		if cfg.DEXChain.ID == "svp-from-file-1" {
			t.Errorf("chain id = %q, want the built-in default", cfg.DEXChain.ID)
		}
	})

	// Sourcing executes the file, so one anybody can write is a way into the
	// operator's shell and the key paths it names.
	t.Run("a world-writable config file is refused", func(t *testing.T) {
		loose := t.TempDir()
		loosePath := filepath.Join(loose, "config.sh")
		if err := os.WriteFile(loosePath, []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
		// Explicitly, because WriteFile's mode is masked by the umask — 0o666
		// there lands as 0644 under the usual 022 and would not trip the check.
		if err := os.Chmod(loosePath, 0o666); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("bash", script, "--config-dir", loose, "--print-config").CombinedOutput()
		if err == nil {
			t.Fatalf("world-writable config was sourced; it must refuse:\n%s", out)
		}
		if !strings.Contains(string(out), "world-writable") {
			t.Errorf("refusal should say why:\n%s", out)
		}
	})
}
