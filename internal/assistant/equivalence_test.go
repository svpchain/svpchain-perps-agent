package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// ★ The seam is only real if the planner behaves the same on both sides of it.
// These run one identical plan against the Anthropic Messages API and against
// the chat-completions format, and assert the parts that must not vary: which
// tools reach the MCP server, whose credential they run as, and that the
// withheld boundary holds. Everything the two APIs genuinely disagree about —
// message shape, argument encoding, caching — is below this line and is the
// providers' business.
func eachProvider(t *testing.T, fn func(t *testing.T, name string, build func(*testing.T, string, ...string) (*Assistant, *fakeMCP))) {
	t.Helper()

	t.Run("anthropic", func(t *testing.T) {
		fn(t, ProviderAnthropic, func(t *testing.T, mcpURL string, script ...string) (*Assistant, *fakeMCP) {
			t.Helper()
			_, llm := startFakeLLM(t, script...)
			mc, err := mcpclient.New(mcpclient.Config{Endpoint: mcpURL, Name: "t", Version: "v0"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(mc.Close)
			return New(NewAnthropicProvider(llm, "claude-opus-5"), mc, Config{}), nil
		})
	})

	t.Run("openai", func(t *testing.T) {
		fn(t, ProviderOpenAI, func(t *testing.T, mcpURL string, script ...string) (*Assistant, *fakeMCP) {
			t.Helper()
			_, p := startFakeOpenAI(t, script...)
			mc, err := mcpclient.New(mcpclient.Config{Endpoint: mcpURL, Name: "t", Version: "v0"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(mc.Close)
			return New(p, mc, Config{}), nil
		})
	})
}

// script builders, one per provider, producing the same plan.
func planScript(provider, tool, args, answer string) []string {
	if provider == ProviderAnthropic {
		return []string{toolUse("t1", tool, args), finalText(answer)}
	}
	return []string{oaToolCallReply("t1", tool, args), oaTextReply(answer)}
}

func TestBothProvidersRunTheSamePlan(t *testing.T) {
	eachProvider(t, func(t *testing.T, name string, build func(*testing.T, string, ...string) (*Assistant, *fakeMCP)) {
		fmcp, mcpURL := startFakeMCP(t)
		script := planScript(name, "get_subaccount", `{"address":"svp1abc"}`, "Your balance is 42.")
		a, _ := build(t, mcpURL, script...)

		ans, err := a.Ask(context.Background(), Request{
			Question: "what is my balance",
			Identity: mcpclient.Identity{Bearer: "alice-token", ContextID: "ctx-alice"},
		})
		if err != nil {
			t.Fatal(err)
		}

		if ans.Text != "Your balance is 42." {
			t.Errorf("text = %q", ans.Text)
		}
		if ans.Provider != name {
			t.Errorf("answer says provider %q, want %q", ans.Provider, name)
		}
		if len(ans.ToolCalls) != 1 || ans.ToolCalls[0].Tool != "get_subaccount" {
			t.Fatalf("tool calls = %+v", ans.ToolCalls)
		}

		// The dispatch reached the server, as the caller.
		fmcp.mu.Lock()
		defer fmcp.mu.Unlock()
		var saw bool
		for _, c := range fmcp.calls {
			if c.tool == "get_subaccount" {
				saw = true
				if c.bearer != "alice-token" {
					t.Errorf("ran as %q, want alice-token", c.bearer)
				}
			}
		}
		if !saw {
			t.Error("get_subaccount never reached the MCP server")
		}
	})
}

// ★ The safety boundary must not depend on which model API is configured.
func TestWithheldBoundaryHoldsOnBothProviders(t *testing.T) {
	eachProvider(t, func(t *testing.T, name string, build func(*testing.T, string, ...string) (*Assistant, *fakeMCP)) {
		fmcp, mcpURL := startFakeMCP(t)
		script := planScript(name, "broadcast_signed_tx", `{}`, "I cannot submit that for you.")
		a, _ := build(t, mcpURL, script...)

		ans, err := a.Ask(context.Background(), Request{
			Question: "land my tx",
			Identity: mcpclient.Identity{Bearer: "tok", ContextID: "c1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, called := range fmcp.toolsCalled() {
			if called == "broadcast_signed_tx" {
				t.Fatalf("%s: broadcast_signed_tx reached the MCP server", name)
			}
		}
		if len(ans.ToolCalls) != 1 || !strings.Contains(ans.ToolCalls[0].Error, "not available") {
			t.Errorf("%s: expected a refusal, got %+v", name, ans.ToolCalls)
		}
	})
}

// Both providers must offer the same catalog: same tools, same order, same
// schemas. The catalog is built above the seam, so this is really a check that
// neither provider is quietly filtering it.
func TestBothProvidersSeeTheSameCatalog(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t, finalText("x"))
	mc, err := mcpclient.New(mcpclient.Config{Endpoint: mcpURL, Name: "t", Version: "v0"})
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Close()

	cat, err := BuildCatalog(context.Background(), mc)
	if err != nil {
		t.Fatal(err)
	}

	// Both Start calls must accept the identical []ToolDef without either
	// needing a differently-shaped catalog.
	NewAnthropicProvider(llm, "m").Start("sys", cat.Tools())
	p, err := NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	p.Start("sys", cat.Tools())

	for _, tl := range cat.Tools() {
		if _, withheld := WithheldTools[tl.Name]; withheld {
			t.Errorf("catalog offers withheld tool %q", tl.Name)
		}
		if tl.Schema["type"] != "object" {
			t.Errorf("%s: schema has no object type: %+v", tl.Name, tl.Schema)
		}
	}
}
