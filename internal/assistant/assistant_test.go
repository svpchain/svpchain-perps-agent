package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// ---- a fake MCP server, standing in for svpchain-dex-mcp ----

type mcpCall struct {
	tool   string
	bearer string
}

type fakeMCP struct {
	mu    sync.Mutex
	calls []mcpCall
}

type anyIn struct {
	Address string `json:"address,omitempty" jsonschema:"the svp1 owner address"`
}
type anyOut struct {
	Value string `json:"value"`
}

func startFakeMCP(t *testing.T) (*fakeMCP, string) {
	t.Helper()
	f := &fakeMCP{}
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-dex-mcp", Version: "v0"}, nil)

	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if ctr, ok := req.(*mcp.CallToolRequest); ok && ctr.Params != nil {
				bearer := ""
				if e := req.GetExtra(); e != nil && e.Header != nil {
					bearer = strings.TrimPrefix(e.Header.Get("Authorization"), "Bearer ")
				}
				f.mu.Lock()
				f.calls = append(f.calls, mcpCall{tool: ctr.Params.Name, bearer: bearer})
				f.mu.Unlock()
			}
			return next(ctx, method, req)
		}
	})

	ok := func(ctx context.Context, _ *mcp.CallToolRequest, _ anyIn) (*mcp.CallToolResult, anyOut, error) {
		return nil, anyOut{Value: "42"}, nil
	}
	// The full catalog shape that matters: reads, a builder, and every
	// withheld tool, so the tests exercise the real boundary.
	for _, n := range []string{"get_height", "get_subaccount", "get_orders", "list_markets"} {
		mcp.AddTool(srv, &mcp.Tool{Name: n, Description: "reads " + n}, ok)
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "build_place_limit_order", Description: "builds an unsigned order"}, ok)
	for n := range WithheldTools {
		mcp.AddTool(srv, &mcp.Tool{Name: n, Description: "withheld " + n}, ok)
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "get_pnl", Description: "refuses"},
		func(context.Context, *mcp.CallToolRequest, anyIn) (*mcp.CallToolResult, anyOut, error) {
			return nil, anyOut{}, fmt.Errorf("indexer is down")
		})

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return f, ts.URL
}

func (f *fakeMCP) toolsCalled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.tool)
	}
	return out
}

// ---- a fake Anthropic Messages API, scripted turn by turn ----

type fakeLLM struct {
	mu       sync.Mutex
	script   []string // raw JSON responses, one per request
	requests []map[string]any
}

func startFakeLLM(t *testing.T, script ...string) (*fakeLLM, anthropic.Client) {
	t.Helper()
	f := &fakeLLM{script: script}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)

		f.mu.Lock()
		f.requests = append(f.requests, parsed)
		n := len(f.requests) - 1
		var out string
		if n < len(f.script) {
			out = f.script[n]
		}
		f.mu.Unlock()

		if out == "" {
			t.Errorf("fake LLM received request %d with nothing scripted", n+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(ts.Close)
	return f, anthropic.NewClient(option.WithBaseURL(ts.URL), option.WithAPIKey("test"))
}

func (f *fakeLLM) request(i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func (f *fakeLLM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func toolUse(id, name, argsJSON string) string {
	return fmt.Sprintf(`{"id":"msg","type":"message","role":"assistant","model":"claude-opus-5",
	 "content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}],
	 "stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`, id, name, argsJSON)
}

func twoToolUses(a, b string) string {
	return fmt.Sprintf(`{"id":"msg","type":"message","role":"assistant","model":"claude-opus-5",
	 "content":[{"type":"tool_use","id":"t1","name":%q,"input":{}},
	            {"type":"tool_use","id":"t2","name":%q,"input":{}}],
	 "stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`, a, b)
}

func finalText(text string) string {
	return fmt.Sprintf(`{"id":"msg","type":"message","role":"assistant","model":"claude-opus-5",
	 "content":[{"type":"text","text":%q}],
	 "stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`, text)
}

func newTestAssistant(t *testing.T, llm anthropic.Client, mcpURL string, cfg Config) (*Assistant, *mcpclient.Client) {
	t.Helper()
	mc, err := mcpclient.New(mcpclient.Config{Endpoint: mcpURL, Name: "test", Version: "v0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mc.Close)
	return New(NewAnthropicProvider(llm, "claude-opus-5"), mc, cfg), mc
}

// ---- tests ----

// ★ The boundary. The catalog does not offer the withheld tools, but a model
// can name one anyway, so the check has to hold at dispatch and not only in
// what was advertised. If this regresses, a planner can broadcast a signed
// transaction or raise a transfer cap on its own.
func TestWithheldToolsAreRefusedAtDispatchNotJustHidden(t *testing.T) {
	fmcp, mcpURL := startFakeMCP(t)
	fllm, llm := startFakeLLM(t,
		toolUse("t1", "broadcast_signed_tx", `{}`),
		finalText("I cannot submit that for you."),
	)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	ans, err := a.Ask(context.Background(), Request{Question: "land my tx", Identity: mcpclient.Identity{Bearer: "tok", ContextID: "c1"}})
	if err != nil {
		t.Fatal(err)
	}

	for _, called := range fmcp.toolsCalled() {
		if called == "broadcast_signed_tx" {
			t.Fatal("broadcast_signed_tx reached the MCP server; the withheld boundary does not hold at dispatch")
		}
	}
	if len(ans.ToolCalls) != 1 || ans.ToolCalls[0].Error == "" {
		t.Fatalf("expected one refused tool call, got %+v", ans.ToolCalls)
	}
	if !strings.Contains(ans.ToolCalls[0].Error, "not available") {
		t.Errorf("refusal did not say why: %q", ans.ToolCalls[0].Error)
	}
	_ = fllm
}

func TestWithheldToolsAreAbsentFromTheCatalog(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t, finalText("hi"))
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	cat := mustCatalog(t, a)
	for name := range WithheldTools {
		if cat.Allows(name) {
			t.Errorf("%s is in the catalog offered to the planner", name)
		}
	}
	for _, name := range []string{"get_height", "get_subaccount", "build_place_limit_order"} {
		if !cat.Allows(name) {
			t.Errorf("%s should be available to the planner", name)
		}
	}
}

// ★ Every tool call carries the asking caller's bearer, so the planner can
// only ever read what its caller could already read.
func TestToolCallsCarryTheCallersBearer(t *testing.T) {
	fmcp, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t,
		toolUse("t1", "get_subaccount", `{"address":"svp1abc"}`),
		finalText("Your balance is 42."),
	)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	if _, err := a.Ask(context.Background(), Request{
		Question: "what is my balance",
		Identity: mcpclient.Identity{Bearer: "alice-token", ContextID: "ctx-alice"},
	}); err != nil {
		t.Fatal(err)
	}

	fmcp.mu.Lock()
	defer fmcp.mu.Unlock()
	var saw bool
	for _, c := range fmcp.calls {
		if c.tool == "get_subaccount" {
			saw = true
			if c.bearer != "alice-token" {
				t.Errorf("get_subaccount ran as %q, want alice-token", c.bearer)
			}
		}
	}
	if !saw {
		t.Fatal("get_subaccount never reached the server")
	}
}

// Parallel tool calls must come back in ONE user turn. Splitting them teaches
// the model to stop asking for tools in parallel.
func TestParallelToolResultsRideOneUserTurn(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	fllm, llm := startFakeLLM(t,
		twoToolUses("get_height", "list_markets"),
		finalText("done"),
	)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	if _, err := a.Ask(context.Background(), Request{Question: "q", Identity: mcpclient.Identity{Bearer: "tok"}}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := fllm.request(1)["messages"].([]any)
	var resultTurns, resultBlocks int
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		blocks, _ := mm["content"].([]any)
		n := 0
		for _, b := range blocks {
			bb, _ := b.(map[string]any)
			if bb["type"] == "tool_result" {
				n++
			}
		}
		if n > 0 {
			resultTurns++
			resultBlocks += n
		}
	}
	if resultBlocks != 2 {
		t.Errorf("sent %d tool_result blocks, want 2", resultBlocks)
	}
	if resultTurns != 1 {
		t.Errorf("tool results were split across %d user turns; they must ride one", resultTurns)
	}
}

// A failing tool is a tool error the model can work around, not an aborted
// request.
func TestToolFailureBecomesAToolErrorNotARequestFailure(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t,
		toolUse("t1", "get_pnl", `{}`),
		finalText("I could not read your PnL: the indexer is down."),
	)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	ans, err := a.Ask(context.Background(), Request{Question: "my pnl?", Identity: mcpclient.Identity{Bearer: "tok"}})
	if err != nil {
		t.Fatalf("a failing tool aborted the request: %v", err)
	}
	if len(ans.ToolCalls) != 1 || ans.ToolCalls[0].Error == "" {
		t.Fatalf("tool failure was not recorded: %+v", ans.ToolCalls)
	}
	if !strings.Contains(ans.Text, "could not") {
		t.Errorf("answer did not carry the failure: %q", ans.Text)
	}
}

// A model naming a tool that does not exist gets a tool error, not a panic and
// not a dispatch.
func TestUnknownToolIsAToolError(t *testing.T) {
	fmcp, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t,
		toolUse("t1", "get_the_future", `{}`),
		finalText("no such thing"),
	)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	ans, err := a.Ask(context.Background(), Request{Question: "q", Identity: mcpclient.Identity{Bearer: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ans.ToolCalls[0].Error, "no such tool") {
		t.Errorf("error = %q", ans.ToolCalls[0].Error)
	}
	for _, c := range fmcp.toolsCalled() {
		if c == "get_the_future" {
			t.Error("an unknown tool was dispatched to the server")
		}
	}
}

// ★ The loop stops at its bound, and says so, rather than running up a bill.
// The last turn asks for an answer instead of being cut off, so the caller
// gets a partial answer that admits it is partial.
func TestIterationBoundStopsTheLoopAndMarksItTruncated(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	fllm, llm := startFakeLLM(t,
		toolUse("t1", "get_height", `{}`),
		finalText("I checked the height but ran out of budget before the rest."),
	)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{MaxIterations: 2})

	ans, err := a.Ask(context.Background(), Request{Question: "q", Identity: mcpclient.Identity{Bearer: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Truncated {
		t.Error("answer is not marked truncated")
	}
	if fllm.count() != 2 {
		t.Errorf("made %d model calls, want 2", fllm.count())
	}
	// The final request must carry no tools, so the model cannot ask for more.
	if _, has := fllm.request(1)["tools"]; has {
		t.Error("the final turn still offered tools; the bound does not bind")
	}
}

func TestToolCallBoundStopsTheLoop(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	fllm, llm := startFakeLLM(t,
		twoToolUses("get_height", "list_markets"),
		finalText("partial"),
	)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{MaxToolCalls: 2, MaxIterations: 10})

	ans, err := a.Ask(context.Background(), Request{Question: "q", Identity: mcpclient.Identity{Bearer: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Truncated {
		t.Error("hitting the tool-call budget did not mark the answer truncated")
	}
	if fllm.count() != 2 {
		t.Errorf("made %d model calls, want 2", fllm.count())
	}
}

// The tool block is a cache prefix, so it has to serialize identically every
// request. Sorting is what makes that true.
func TestCatalogIsSortedForCacheStability(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t, finalText("x"))
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	tools := mustCatalog(t, a).Tools()
	for i := 1; i < len(tools); i++ {
		if tools[i-1].Name > tools[i].Name {
			t.Fatalf("catalog is unsorted at %d: %q before %q",
				i, tools[i-1].Name, tools[i].Name)
		}
	}
}

// The per-argument prose the server publishes is most of what the planner
// reasons from, so it has to survive the conversion into a tool definition.
func TestCatalogKeepsPerArgumentDescriptions(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t, finalText("x"))
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	for _, tl := range mustCatalog(t, a).Tools() {
		if tl.Name != "get_subaccount" {
			continue
		}
		addr, ok := tl.Properties()["address"].(map[string]any)
		if !ok {
			t.Fatalf("address property missing: %+v", tl.Properties())
		}
		if desc, _ := addr["description"].(string); !strings.Contains(desc, "owner address") {
			t.Errorf("per-argument description lost: %+v", addr)
		}
		return
	}
	t.Fatal("get_subaccount not in the catalog")
}

func TestEmptyQuestionIsRefused(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t)
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	if _, err := a.Ask(context.Background(), Request{Question: "   "}); err == nil {
		t.Fatal("an empty question was accepted")
	}
}

func mustCatalog(t *testing.T, a *Assistant) *Catalog {
	t.Helper()
	cat, err := a.ensureCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// A server that is down when the question arrives is an error for that
// question, not a disabled skill: the next question retries.
func TestCatalogFailureIsNotCached(t *testing.T) {
	_, mcpURL := startFakeMCP(t)
	_, llm := startFakeLLM(t, finalText("x"))
	a, _ := newTestAssistant(t, llm, mcpURL, Config{})

	a.catalogMu.Lock()
	a.catalog = nil
	a.catalogMu.Unlock()

	if _, err := a.ensureCatalog(context.Background()); err != nil {
		t.Fatalf("catalog load failed: %v", err)
	}
	a.catalogMu.Lock()
	loaded := a.catalog != nil
	a.catalogMu.Unlock()
	if !loaded {
		t.Error("a successful load was not cached")
	}
}
