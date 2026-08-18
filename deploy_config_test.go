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
	"github.com/svpchain/svpchain-perps-agent/internal/operator"
)

// The operator key reaches the deploy through the environment — there is no
// flag, because a hex key in argv lands in `ps` and in shell history. Taken
// from the operator package rather than spelled again here, so a rename there
// cannot leave these tests silently exercising a variable nothing reads.
const envKey = operator.KeyEnvVar

// secretMount is where docker compose mounts the shipped secret, and therefore
// what the rendered agent.toml must point key_file at.
const secretMount = "/run/secrets/operator_key"

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
		"keyless": {"--print-config", "--host", "www@agent.example.com"},
		"keyed": {
			"--print-config", "--host", "www@agent.example.com",
			"--public-url", "https://agents.example.com",
		},
		// Every optional block the script can render, on at once, so a typo in
		// one of those heredocs fails here rather than on a remote host.
		// [agent_chain] is both-or-neither in core, which this also pins.
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
			cmd := exec.Command("bash", append([]string{script, "--no-config"}, args...)...)
			// The key is supplied through the environment now, so only the
			// "keyed" case sets it. It is the material itself, not a path —
			// the script validates it is 32 bytes of hex. Every other case
			// scrubs it, so a developer's exported key cannot turn a keyless
			// assertion into a keyed one.
			cmd.Env = append(os.Environ(), envKey+"=")
			if name == "keyed" {
				cmd.Env = append(cmd.Env, envKey+"="+strings.Repeat("a1", 32))
			}
			out, err := cmd.Output()
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
			// The key must be reachable at the compose secret mount, and the
			// path must survive config.Load unchanged: a RELATIVE key_file is
			// joined against the agent.toml directory, which would silently
			// rewrite this to /etc/svpchain-perps-agent/run/secrets/… and
			// leave the agent keyless with nothing in the logs.
			if name == "keyed" && cfg.Operator.KeyFile != secretMount {
				t.Errorf("key_file = %q, want %q", cfg.Operator.KeyFile, secretMount)
			}
			// Keyless is a supported mode for this agent, not a failure: the
			// execution skills refuse at call time with a reason.
			if name != "keyed" && cfg.Operator.KeyFile != "" {
				t.Errorf("keyless variant must not set key_file, got %q", cfg.Operator.KeyFile)
			}
			if strings.Contains(string(out), strings.Repeat("a1", 32)) {
				t.Error("rendered agent.toml contains the operator key material")
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
// disagree on the path, the agent advertises a URL that 404s and reads as
// unverified — with every process healthy and nothing in the logs.
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

// The key must reach the container as a compose secret, never as a bind mount
// or a container environment variable: `docker inspect` prints both the volume
// list and Config.Env in full, so either would put the operator key wherever
// that output gets pasted.
func TestDeployComposeShipsKeyAsSecret(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	render := func(t *testing.T, key string) string {
		t.Helper()
		cmd := exec.Command("bash", script, "--no-config", "--print-compose",
			"--host", "www@agent.example.com")
		cmd.Env = append(os.Environ(), envKey+"="+key)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("render compose: %v", err)
		}
		return string(out)
	}

	key := strings.Repeat("a1", 32)
	got := render(t, key)
	if strings.Contains(got, key) {
		t.Error("docker-compose.yml contains the operator key material")
	}
	if strings.Contains(got, "operator.key:/etc/") {
		t.Error("operator key is still bind-mounted; it must ship as a compose secret")
	}
	if strings.Contains(got, envKey) {
		t.Errorf("operator key must not be passed as a container env var:\n%s", got)
	}
	if !strings.Contains(got, "secrets:") || !strings.Contains(got, "operator_key:") {
		t.Errorf("compose is missing the operator_key secret:\n%s", got)
	}

	// Keyless renders no secret at all, rather than one pointing at a file the
	// deploy never stages — compose fails to start on a missing secret source.
	if out := render(t, ""); strings.Contains(out, "secrets:") {
		t.Errorf("keyless compose must not declare a secret:\n%s", out)
	}
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

// --print-env exists to make a computed config debuggable, which is only safe
// if it never prints the one value that must not be echoed.
func TestPrintEnvRedactsTheOperatorKey(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	key := strings.Repeat("a1", 32)
	cmd := exec.Command("bash", script, "--no-config", "--print-env")
	cmd.Env = append(os.Environ(), envKey+"="+key)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("--print-env: %v", err)
	}
	if strings.Contains(string(out), key) {
		t.Errorf("--print-env printed the operator key:\n%s", out)
	}
	// Still has to confirm a key resolved, or it cannot do its job.
	if !strings.Contains(string(out), "set (64 chars)") {
		t.Errorf("--print-env should report the key as set:\n%s", out)
	}
	// And keyless must read as keyless, not as an empty string.
	cmd = exec.Command("bash", script, "--no-config", "--print-env")
	cmd.Env = append(os.Environ(), envKey+"=")
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("--print-env keyless: %v", err)
	}
	if !strings.Contains(string(out), "unset") {
		t.Errorf("--print-env should report a missing key as unset:\n%s", out)
	}
}

// --gen-operator-key mints this agent's identity and wires the config file to
// it in one step. The halves have to stay together: a key nothing references
// deploys keyless and says nothing, while a config line naming a key that was
// never created fails the *source* and takes every other mode down with it.
//
// Slower than its neighbours — it shells out to `go run ./cmd/operator-keygen`
// — but the build cache is warm by the time this runs under `go test ./...`.
func TestDeployScriptGeneratesAnOperatorKey(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}
	// A developer's own exported key would otherwise look like "already
	// configured" and turn every assertion below into a refusal.
	t.Setenv(envKey, "")

	dir := filepath.Join(t.TempDir(), "cfg")
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command("bash", append([]string{script, "--config-dir", dir}, args...)...)
		cmd.Env = append(os.Environ(), envKey+"=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("deploy.sh %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	// Bootstrapped through --init-config rather than a hand-written stub, so
	// this exercises the template that actually ships — including the
	// commented key line the rewrite is supposed to land on.
	run(t, "--init-config")
	out := run(t, "--gen-operator-key")

	keyPath := filepath.Join(dir, "operator.key")
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("no key file written: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %04o, want 0600", perm)
	}

	// The agent must be able to boot on what the deploy wrote, and the address
	// the script printed must be the one that key derives — it is what the
	// operator funds the bond to before agent_self_register.
	priv, addr, err := operator.Load(config.Operator{KeyFile: keyPath})
	if err != nil || priv == nil {
		t.Fatalf("the agent cannot load the generated key: %v", err)
	}
	if !strings.Contains(out, addr) {
		t.Errorf("--gen-operator-key did not print the address to fund (%s):\n%s", addr, out)
	}

	// The key is written, never printed: stdout here becomes scrollback and CI
	// logs.
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(raw))
	if strings.Contains(out, key) {
		t.Error("--gen-operator-key printed the key material")
	}
	if envOut := run(t, "--print-env"); strings.Contains(envOut, key) {
		t.Error("--print-env printed the generated key")
	} else if !strings.Contains(envOut, "set (64 chars)") {
		t.Errorf("the config file does not resolve to the generated key:\n%s", envOut)
	}

	// And the deploy now renders an [operator] block, which is the whole point
	// of having generated one.
	if cfgOut := run(t, "--print-config", "--host", "www@agent.example.com"); !strings.Contains(cfgOut, "[operator]") {
		t.Errorf("rendered config has no [operator] block after key generation:\n%s", cfgOut)
	}

	// A second run must refuse. The key is an on-chain identity with a bond
	// posted against it; replacing one silently would strand both.
	cmd := exec.Command("bash", script, "--config-dir", dir, "--gen-operator-key")
	cmd.Env = append(os.Environ(), envKey+"=")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("a second --gen-operator-key succeeded:\n%s", out)
	}
	again, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(raw) {
		t.Error("the refused second run still modified the key file")
	}
}

// --register is the operator proving it holds the key the agent runs as, so a
// keyless invocation has nothing to prove with. It must say that rather than
// shelling out and failing somewhere less legible.
func TestRegisterRefusesWithoutAnOperatorKey(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	cmd := exec.Command("bash", script, "--no-config", "--register")
	cmd.Env = append(os.Environ(), envKey+"=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("--register succeeded with no operator key:\n%s", out)
	}
	if !strings.Contains(string(out), "no operator key") {
		t.Errorf("the refusal does not name the missing key:\n%s", out)
	}
}
