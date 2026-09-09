package deps_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/owner"
)

// The owner key reaches the deploy through the environment — there is no
// flag, because a hex key in argv lands in `ps` and in shell history. Taken
// from the owner package rather than spelled again here, so a rename there
// cannot leave these tests silently exercising a variable nothing reads.
const envKey = owner.KeyEnvVar

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
		// The assistant block, which is the only optional one left to render:
		// the chain endpoints, fee, cache and limits blocks configured the
		// in-process copy of the MCP server and are that server's settings now.
		"assistant": {
			"--print-config", "--host", "www@agent.example.com",
			"--assistant-provider", "openai",
			"--assistant-base-url", "https://api.deepseek.com",
			"--assistant-model", "deepseek-v4-pro",
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
			if cfg.MCP.Endpoint == "" {
				t.Error("rendered config must name the MCP server every operation runs on")
			}
			if name == "assistant" && cfg.Assistant.Model != "deepseek-v4-pro" {
				t.Errorf("assistant model = %q, want deepseek-v4-pro", cfg.Assistant.Model)
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
		if cfg.PublicURL != "https://perps.example.org" {
			t.Errorf("public_url = %q, want the file's value verbatim", cfg.PublicURL)
		}
		if cfg.MCP.Endpoint == "" {
			t.Error("the rendered config names no MCP server")
		}
	})

	t.Run("a flag overrides the file", func(t *testing.T) {
		cfg := load(t, "--mcp-endpoint", "https://mcp-from-flag.example")
		if cfg.MCP.Endpoint != "https://mcp-from-flag.example" {
			t.Errorf("mcp endpoint = %q, want the flag's", cfg.MCP.Endpoint)
		}
	})

	t.Run("--no-config ignores the file", func(t *testing.T) {
		cfg := load(t, "--no-config")
		if cfg.PublicURL == "https://perps.example.org" {
			t.Errorf("public_url = %q, want the built-in default", cfg.PublicURL)
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

// --print-env exists to make a computed config debuggable, which is only safe
// if it never prints the one value that must not be echoed.
func TestPrintEnvRedactsTheOwnerKey(t *testing.T) {
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
		t.Errorf("--print-env printed the owner key:\n%s", out)
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

// --gen-owner-key mints the identity this agent registers under and wires the
// config file to it in one step. The halves have to stay together: a key
// nothing references leaves --register saying it has none, while a config
// line naming a key that was never created fails the *source* and takes every
// other mode down with it.
//
// Slower than its neighbours — it shells out to `go run ./cmd/owner-keygen` —
// but the build cache is warm by the time this runs under `go test ./...`.
func TestDeployScriptGeneratesAnOwnerKey(t *testing.T) {
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
	out := run(t, "--gen-owner-key")

	keyPath := filepath.Join(dir, "owner.key")
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("no key file written: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %04o, want 0600", perm)
	}

	// cmd/agent-register must be able to load what the deploy wrote, and the
	// address the script printed must be the one that key derives — it is
	// what gets funded with the fee and the bond before MsgRegisterAgent.
	priv, addr, err := owner.Load(keyPath)
	if err != nil || priv == nil {
		t.Fatalf("cannot load the generated key: %v", err)
	}
	if !strings.Contains(out, addr) {
		t.Errorf("--gen-owner-key did not print the address to fund (%s):\n%s", addr, out)
	}

	// The key is written, never printed: stdout here becomes scrollback and CI
	// logs.
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(raw))
	if strings.Contains(out, key) {
		t.Error("--gen-owner-key printed the key material")
	}
	if envOut := run(t, "--print-env"); strings.Contains(envOut, key) {
		t.Error("--print-env printed the generated key")
	} else if !strings.Contains(envOut, "set (64 chars)") {
		t.Errorf("the config file does not resolve to the generated key:\n%s", envOut)
	}

	// The key stays here. Nothing the deploy renders or ships may carry it, or
	// even mention a key: the remote has no use for one.
	for _, mode := range []string{"--print-config", "--print-compose"} {
		rendered := run(t, mode, "--host", "www@agent.example.com")
		if strings.Contains(rendered, key) || strings.Contains(strings.ToLower(rendered), "key") {
			t.Errorf("%s references the owner key; it must never reach the host:\n%s", mode, rendered)
		}
	}

	// A second run must refuse. The key is an on-chain identity with a bond
	// posted against it; replacing one silently would strand both.
	cmd := exec.Command("bash", script, "--config-dir", dir, "--gen-owner-key")
	cmd.Env = append(os.Environ(), envKey+"=")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("a second --gen-owner-key succeeded:\n%s", out)
	}
	again, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(raw) {
		t.Error("the refused second run still modified the key file")
	}
}

// --register is the owner proving it holds the key the agent is registered
// under, so a keyless invocation has nothing to prove with. It must say that
// rather than shelling out and failing somewhere less legible.
func TestRegisterRefusesWithoutAnOwnerKey(t *testing.T) {
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
		t.Fatalf("--register succeeded with no owner key:\n%s", out)
	}
	if !strings.Contains(string(out), "no owner key") {
		t.Errorf("the refusal does not name the missing key:\n%s", out)
	}
}

// An install needs no key. The deployed agent signs nothing, so the deploy
// must neither require one nor ship one — a keyless deploy has to get past
// the argument checks, and a keyed one must render exactly what a keyless one
// does.
func TestDeployNeedsNoOwnerKey(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	render := func(key string) string {
		t.Helper()
		cmd := exec.Command("bash", script, "--no-config", "--print-compose", "--host", "www@agent.example.com")
		cmd.Env = append(os.Environ(), envKey+"="+key)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("render compose: %v", err)
		}
		return string(out)
	}
	if keyed, keyless := render(strings.Repeat("a1", 32)), render(""); keyed != keyless {
		t.Errorf("the owner key changed what the deploy ships:\nkeyed:\n%s\nkeyless:\n%s", keyed, keyless)
	}

	// --dry-run so this reaches the argument checks without touching docker,
	// ssh, or the network; it must get PAST them, which the old operator-key
	// gate did not allow.
	cmd := exec.Command("bash", script, "--no-config", "--dry-run", "--host", "www@agent.example.com")
	cmd.Env = append(os.Environ(), envKey+"=")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "Preflight") {
		t.Errorf("keyless deploy did not reach preflight:\n%s", out)
	}
}

// --jump-box has to reach every remote call, or a deploy through a bastion
// fails on whichever step still dials the host directly — after the earlier
// steps have already shipped files. --dry-run prints each remote command, so
// the assertion is that all of them carry -J and none carry a bare ssh.
func TestDeployJumpBoxReachesEveryRemoteCall(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	const bastion = "ops@bastion.example.com"
	// No --skip-build: under --dry-run the build and save are only printed, so
	// the run needs neither docker nor a local image and reaches every remote
	// phase.
	out, err := exec.Command("bash", script, "--no-config", "--dry-run",
		"--host", "www@agent.example.com", "--jump-box", bastion).CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}

	var remote []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "[dry-run] ssh") || strings.Contains(line, "[dry-run] rsync") {
			remote = append(remote, line)
		}
	}
	if len(remote) == 0 {
		t.Fatalf("dry-run printed no remote commands:\n%s", out)
	}
	for _, line := range remote {
		if !strings.Contains(line, "-J "+bastion) {
			t.Errorf("remote command bypasses the jump box: %s", line)
		}
	}
	if !strings.Contains(string(out), "via jump-box="+bastion) {
		t.Errorf("preflight does not report the jump box:\n%s", out)
	}
}

// ★ The model API key is the one secret this deploy ships to the host, so the
// places it must NOT appear are worth pinning rather than trusting to review.
//
// agent.toml is rendered, rsynced, and printable with --print-config;
// docker-compose.yml is generated and printed the same way. The key belongs in
// neither. It reaches the container through a 0600 env file referenced by
// compose, which names the path but never the value.
func TestModelAPIKeyNeverReachesRenderedFiles(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	const secret = "sk-this-value-must-never-be-printed"

	// Both provider keys, since either can be the one configured.
	for _, mode := range []string{"--print-config", "--print-compose", "--print-env"} {
		t.Run(strings.TrimPrefix(mode, "--"), func(t *testing.T) {
			cmd := exec.Command(script, "--no-config", mode, "--host", "www@agent.example.com")
			cmd.Env = append(os.Environ(),
				"SVPCHAIN_ANTHROPIC_API_KEY="+secret+"-anthropic",
				"SVPCHAIN_OPENAI_API_KEY="+secret+"-openai")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
			if strings.Contains(string(out), secret) {
				t.Errorf("%s printed the model API key", mode)
			}
		})
	}
}

// The other half: with a key configured, compose must actually load the env
// file, or the skill is silently off on the deployed host.
func TestComposeLoadsTheAssistantEnvFileOnlyWhenAKeyIsSet(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("scripts", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("deploy script not found: %v", err)
	}

	run := func(t *testing.T, key string) string {
		t.Helper()
		cmd := exec.Command(script, "--no-config", "--print-compose", "--host", "www@agent.example.com")
		cmd.Env = append(os.Environ(), "SVPCHAIN_ANTHROPIC_API_KEY="+key)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("print-compose: %v\n%s", err, out)
		}
		return string(out)
	}

	if got := run(t, "sk-ant-configured"); !strings.Contains(got, "env_file:") ||
		!strings.Contains(got, "assistant.env") {
		t.Errorf("a configured key produced no env_file entry:\n%s", got)
	}
	if got := run(t, ""); strings.Contains(got, "assistant.env") {
		t.Errorf("compose references assistant.env with no key configured:\n%s", got)
	}
}
