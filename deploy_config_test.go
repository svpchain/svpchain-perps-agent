package deps_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

	cases := map[string][]string{
		"defaults": {"--print-config", "--host", "www@agent.example.com"},
		"public-url": {
			"--print-config", "--host", "www@agent.example.com",
			"--public-url", "https://agents.example.com",
		},
		// Every optional block the script can render, on at once, so a typo in
		// one of those heredocs fails here rather than on a remote host.
		"all-optionals": {
			"--print-config", "--host", "www@agent.example.com",
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
			if name == "all-optionals" && cfg.Limits.DepositMaxUSDC != 1000 {
				t.Errorf("deposit_max_usdc = %d, want 1000", cfg.Limits.DepositMaxUSDC)
			}
		})
	}
}

// The route and the card must agree. An agent advertises public_url inside its
// Agent Card, callers dial that URL, and nginx is what makes it resolve. If
// the location block and public_url disagree on the path, the agent
// advertises a URL that 404s — with every process healthy and nothing in the
// logs.
//
// This agent hangs off the root of its own host: public_url is what was passed,
// unmodified, and the nginx block is a plain `location /` to AGENT_PORT. So the
// assertions are that no segment has crept back into the advertised URL and
// that the block routes the root to the same port the config listens on.
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

	if cfg.PublicURL != base {
		t.Fatalf("public_url = %q, want the URL passed in verbatim (%q)", cfg.PublicURL, base)
	}
	wantPort := cfg.ListenAddr[strings.LastIndex(cfg.ListenAddr, ":"):] // ":8082"

	nginx := run("--print-nginx")
	if want := "location / {"; !strings.Contains(nginx, want) {
		t.Errorf("nginx block does not route the advertised root.\nwant %q\ngot:\n%s", want, nginx)
	}
	if want := "proxy_pass http://127.0.0.1" + wantPort + ";"; !strings.Contains(nginx, want) {
		t.Errorf("nginx block does not proxy to the configured port.\nwant %q\ngot:\n%s", want, nginx)
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
		"SVPCHAIN_PERPS_AGENT_PUBLIC_URL=\"https://perps.example.org\"\n" +
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
		if cfg.PublicURL != "https://perps.example.org" {
			t.Errorf("public_url = %q, want the file's value verbatim", cfg.PublicURL)
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
	// deployer's shell.
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

// --help does not introspect anything: it re-prints the script's own header
// comment block. So the documented flags and the flags the argument loop
// actually accepts are two hand-maintained lists, and nothing but discipline
// keeps them equal. Discipline already failed once here — when the config file
// was added, seven of its variables never reached the header — so pin all
// three representations (arg loop, CONFIG_VARS, --print-env) to the docs.
//
// Same job as TestDeployScriptNginxRouteMatchesConfig, which pins the rendered
// config against the rendered nginx block for the same reason.
func TestDeployScriptDocumentsEveryFlagAndVariable(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}

	helpOut, err := exec.Command("bash", script, "--help").Output()
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	help := string(helpOut)

	t.Run("flags", func(t *testing.T) {
		// Case arms in the argument loop: whitespace, the flag, a paren.
		arms := regexp.MustCompile(`(?m)^\s+(--[a-z-]+)\)`).FindAllStringSubmatch(string(src), -1)
		if len(arms) == 0 {
			t.Fatal("found no flag arms; the parse is wrong, not the script")
		}
		for _, m := range arms {
			flag := m[1]
			// --help documenting itself is noise; every other flag must appear.
			if flag == "--help" {
				continue
			}
			if !strings.Contains(help, flag) {
				t.Errorf("%s is accepted but undocumented in --help", flag)
			}
		}
	})

	// CONFIG_VARS is the authoritative list of names the config file may set.
	// An entry missing from the example is a setting no operator can discover.
	t.Run("variables", func(t *testing.T) {
		block := regexp.MustCompile(`(?s)readonly CONFIG_VARS=\((.*?)\)`).FindStringSubmatch(string(src))
		if block == nil {
			t.Fatal("could not find the CONFIG_VARS array")
		}
		vars := regexp.MustCompile(`SVPCHAIN_[A-Z_]+`).FindAllString(block[1], -1)
		if len(vars) == 0 {
			t.Fatal("CONFIG_VARS parsed empty")
		}

		example, err := os.ReadFile(filepath.Join("scripts", "config.sh.example"))
		if err != nil {
			t.Fatal(err)
		}

		envOut, err := exec.Command("bash", script, "--no-config", "--print-env").Output()
		if err != nil {
			t.Fatalf("--print-env: %v", err)
		}

		for _, v := range vars {
			if !strings.Contains(help, v) {
				t.Errorf("%s is read by the script but undocumented in --help", v)
			}
			if !strings.Contains(string(example), v) {
				t.Errorf("%s is missing from scripts/config.sh.example", v)
			}
			if !strings.Contains(string(envOut), v) {
				t.Errorf("%s is missing from --print-env", v)
			}
		}
	})
}

// --print-env exists to make a computed config debuggable: every setting,
// its resolved value, and the layer it came from.
func TestPrintEnvReportsOrigins(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	out, err := exec.Command("bash", script, "--no-config", "--print-env",
		"--chain-id", "svp-from-flag-1").Output()
	if err != nil {
		t.Fatalf("--print-env: %v", err)
	}
	if !strings.Contains(string(out), "config file: ignored (--no-config)") {
		t.Errorf("--print-env should say the config file was ignored:\n%s", out)
	}
	line := regexp.MustCompile(`(?m)^SVPCHAIN_CHAIN_ID\s+(\S+)\s+(\S+)$`).FindStringSubmatch(string(out))
	if line == nil || line[1] != "flag" || line[2] != "svp-from-flag-1" {
		t.Errorf("--print-env should report the chain id as coming from the flag:\n%s", out)
	}
}
