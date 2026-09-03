package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeServer mirrors the parts of svpchain-dex-mcp this client has to be
// correct against: it resolves a caller from the Authorization header, falls
// back to the bearer bound to the request's Mcp-Session-Id, and lets
// "auth_verify" create that binding. Getting the fallback wrong on the real
// server means one caller acting as another, so the fake reproduces it rather
// than assuming it away.
type fakeServer struct {
	mu sync.Mutex
	// bindings maps an MCP session id to the bearer auth_verify minted on it.
	bindings map[string]string
	// calls records what the server resolved for each call, in order.
	calls []callRecord
	// failNext makes the next tool call fail at the transport level.
	failNext bool
	// httpHits counts POSTs, so a test can tell a reconnect from a retry.
	httpHits int
}

type callRecord struct {
	tool      string
	sessionID string
	bearer    string
	// resolved is the identity the server decided the call was made as.
	resolved string
	xff      string
}

type echoIn struct {
	Ping string `json:"ping,omitempty"`
}

type echoOut struct {
	// ResolvedAs is who the server thinks called. The whole point of the
	// leak test is that this must never be another caller's identity.
	ResolvedAs string `json:"resolved_as"`
}

func newFakeServer(t *testing.T) (*fakeServer, string) {
	t.Helper()
	f := &fakeServer{bindings: map[string]string{}}

	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-dex-mcp", Version: "v0"}, nil)

	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			ctr, _ := req.(*mcp.CallToolRequest)
			var sessionID, bearer, xff string
			if e := req.GetExtra(); e != nil && e.Header != nil {
				sessionID = e.Header.Get("Mcp-Session-Id")
				bearer = strings.TrimPrefix(e.Header.Get("Authorization"), "Bearer ")
				xff = e.Header.Get("X-Forwarded-For")
			}

			f.mu.Lock()
			// Path 1: explicit bearer. Path 2: the session's bound bearer.
			resolved := bearer
			if resolved == "" {
				resolved = f.bindings[sessionID]
			}
			if ctr != nil && ctr.Params != nil && ctr.Params.Name == "auth_verify" {
				// Mint a bearer named for the conversation and bind it, the
				// way the real auth_verify does.
				minted := "tok-" + sessionID
				f.bindings[sessionID] = minted
				resolved = minted
			}
			name := ""
			if ctr != nil && ctr.Params != nil {
				name = ctr.Params.Name
			}
			f.calls = append(f.calls, callRecord{
				tool: name, sessionID: sessionID, bearer: bearer, resolved: resolved, xff: xff,
			})
			f.mu.Unlock()

			return next(context.WithValue(ctx, resolvedKey{}, resolved), method, req)
		}
	})

	echo := func(ctx context.Context, _ *mcp.CallToolRequest, _ echoIn) (*mcp.CallToolResult, echoOut, error) {
		r, _ := ctx.Value(resolvedKey{}).(string)
		return nil, echoOut{ResolvedAs: r}, nil
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "whoami", Description: "who am i"}, echo)
	mcp.AddTool(srv, &mcp.Tool{Name: "auth_verify", Description: "mint"}, echo)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_orderbook", Description: "book"}, echo)
	mcp.AddTool(srv, &mcp.Tool{Name: "broadcast_signed_tx", Description: "land"}, echo)
	// A tool that refuses without erroring, the way the real auth gate does.
	mcp.AddTool(srv, &mcp.Tool{Name: "gated", Description: "gated"},
		func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, echoOut, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "auth_required: call auth_challenge first"}},
			}, echoOut{}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.httpHits++
		fail := f.failNext && r.Method == http.MethodPost
		if fail {
			f.failNext = false
		}
		f.mu.Unlock()
		if fail {
			// A dead session on the far side answers 404, which is what a
			// restarted stateful server does to a stale Mcp-Session-Id.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return f, ts.URL
}

type resolvedKey struct{}

func (f *fakeServer) record(i int) callRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

func (f *fakeServer) numCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: endpoint, Name: "test", Version: "v0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func mustCall(t *testing.T, c *Client, call Call) *Result {
	t.Helper()
	res, err := c.CallTool(context.Background(), call)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", call.Tool, err)
	}
	return res
}

func resolvedAs(t *testing.T, res *Result) string {
	t.Helper()
	var out echoOut
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		t.Fatalf("decode result %q: %v", res.Structured, err)
	}
	return out.ResolvedAs
}

// ★ The linchpin. The Streamable HTTP transport sets headers per connection,
// so this client depends on the ctx passed to CallTool reaching the outgoing
// request, where a RoundTripper can stamp that one call's bearer. An SDK
// upgrade that decoupled the two would otherwise send every call
// unauthenticated and silently, which this catches.
func TestPerCallCredentialsReachTheWire(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	res := mustCall(t, c, Call{
		Tool:     "whoami",
		Identity: Identity{Bearer: "alice-token", ContextID: "ctx-a", ClientIP: "203.0.113.7"},
	})

	if got := resolvedAs(t, res); got != "alice-token" {
		t.Fatalf("server resolved %q, want alice-token: per-call bearer did not reach the request", got)
	}
	if got := f.record(0).xff; got != "203.0.113.7" {
		t.Errorf("X-Forwarded-For = %q, want 203.0.113.7", got)
	}
}

// ★ The reason sessions are pooled by identity instead of shared. On a single
// shared session the server's session→bearer binding would make Bob's
// unauthenticated call resolve to Alice, and Bob would act as Alice.
func TestOneCallerNeverInheritsAnothersIdentity(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	// Alice completes the handshake in her own conversation. On the real
	// server this binds her minted bearer to whatever MCP session carried it.
	aliceVerify := mustCall(t, c, Call{Tool: "auth_verify", Identity: Identity{ContextID: "ctx-alice"}})
	aliceIdent := resolvedAs(t, aliceVerify)
	if aliceIdent == "" {
		t.Fatal("auth_verify resolved to nothing")
	}

	// Bob calls a gated tool with no bearer at all, from his own conversation.
	bobRes := mustCall(t, c, Call{Tool: "whoami", Identity: Identity{ContextID: "ctx-bob"}})

	if got := resolvedAs(t, bobRes); got == aliceIdent {
		t.Fatalf("Bob resolved as Alice (%q): sessions are being shared across identities", got)
	}

	// And the sessions really were distinct, which is the mechanism.
	var aliceSess, bobSess string
	for i := 0; i < f.numCalls(); i++ {
		r := f.record(i)
		switch r.tool {
		case "auth_verify":
			aliceSess = r.sessionID
		case "whoami":
			bobSess = r.sessionID
		}
	}
	if aliceSess == "" || bobSess == "" {
		t.Fatalf("missing session ids: alice=%q bob=%q", aliceSess, bobSess)
	}
	if aliceSess == bobSess {
		t.Fatalf("both conversations used session %q", aliceSess)
	}
}

// A caller that has authenticated keeps using one session, so the pool tracks
// tenants rather than opening a connection per call.
func TestSameBearerSharesOneSession(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	for i := 0; i < 3; i++ {
		mustCall(t, c, Call{Tool: "whoami", Identity: Identity{Bearer: "same-token", ContextID: "ctx-1"}})
	}

	seen := map[string]bool{}
	for i := 0; i < f.numCalls(); i++ {
		seen[f.record(i).sessionID] = true
	}
	if len(seen) != 1 {
		t.Fatalf("one bearer opened %d sessions, want 1", len(seen))
	}
}

// The same tenant reaching the agent through two different conversations still
// shares one session, because the bearer is the key.
func TestSameBearerAcrossContextsSharesOneSession(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	mustCall(t, c, Call{Tool: "whoami", Identity: Identity{Bearer: "tok", ContextID: "ctx-1"}})
	mustCall(t, c, Call{Tool: "whoami", Identity: Identity{Bearer: "tok", ContextID: "ctx-2"}})

	if a, b := f.record(0).sessionID, f.record(1).sessionID; a != b {
		t.Fatalf("same bearer used sessions %q and %q", a, b)
	}
}

// A refusal that the server reports as a normal result must survive as one.
// The server answers an unauthenticated gated call this way on purpose, so an
// agent can authenticate and retry instead of aborting.
func TestSoftRefusalIsNotAnError(t *testing.T) {
	_, url := newFakeServer(t)
	c := newTestClient(t, url)

	res, err := c.CallTool(context.Background(), Call{
		Tool: "gated", Identity: Identity{ContextID: "ctx-1"},
	})
	if err != nil {
		t.Fatalf("soft refusal surfaced as a transport error: %v", err)
	}
	if res.IsError {
		t.Error("soft refusal marked IsError")
	}
	if !strings.Contains(res.Text, "auth_required") {
		t.Errorf("refusal text = %q, want the handshake instructions", res.Text)
	}
}

// A read may be retried through a dead pooled session.
func TestIdempotentCallRetriesThroughADeadSession(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	id := Identity{Bearer: "tok", ContextID: "ctx-1"}
	mustCall(t, c, Call{Tool: "get_orderbook", Identity: id, Idempotent: true})

	f.mu.Lock()
	f.failNext = true
	f.mu.Unlock()

	res, err := c.CallTool(context.Background(), Call{Tool: "get_orderbook", Identity: id, Idempotent: true})
	if err != nil {
		t.Fatalf("idempotent call did not recover from a dead session: %v", err)
	}
	if resolvedAs(t, res) != "tok" {
		t.Errorf("identity lost across the reconnect: %q", resolvedAs(t, res))
	}
}

// ★ And a broadcast may not. Retrying an ambiguous broadcast risks landing the
// transaction twice, which is worse than reporting the failure.
func TestNonIdempotentCallDoesNotRetry(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	id := Identity{Bearer: "tok", ContextID: "ctx-1"}
	mustCall(t, c, Call{Tool: "whoami", Identity: id, Idempotent: true})

	before := f.numCalls()
	f.mu.Lock()
	f.failNext = true
	f.mu.Unlock()

	if _, err := c.CallTool(context.Background(), Call{Tool: "broadcast_signed_tx", Identity: id}); err == nil {
		t.Fatal("broadcast_signed_tx succeeded through a dead session; it must surface the failure")
	}
	if got := f.numCalls(); got != before {
		t.Errorf("broadcast reached the server %d times after the failure, want 0", got-before)
	}
}

// A caller with no bearer and no conversation shares nothing, because there is
// no key that could be reused safely.
func TestAnonymousCallerGetsItsOwnSession(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	mustCall(t, c, Call{Tool: "whoami", Identity: Identity{}})
	mustCall(t, c, Call{Tool: "whoami", Identity: Identity{}})

	if a, b := f.record(0).sessionID, f.record(1).sessionID; a == b {
		t.Fatalf("two anonymous callers shared session %q", a)
	}
}

func TestListToolsReadsTheRemoteCatalog(t *testing.T) {
	_, url := newFakeServer(t)
	c := newTestClient(t, url)

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"whoami", "get_orderbook", "broadcast_signed_tx"} {
		if !names[want] {
			t.Errorf("catalog missing %q", want)
		}
	}
}

func TestSweepClosesIdleSessions(t *testing.T) {
	_, url := newFakeServer(t)
	c, err := New(Config{Endpoint: url, IdleTimeout: time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	mustCall(t, c, Call{Tool: "whoami", Identity: Identity{Bearer: "tok"}})

	c.mu.Lock()
	n := len(c.pool)
	c.mu.Unlock()
	if n != 1 {
		t.Fatalf("pool holds %d sessions after one call, want 1", n)
	}

	c.sweep(time.Now().Add(time.Hour))

	c.mu.Lock()
	n = len(c.pool)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("pool still holds %d sessions after the sweep", n)
	}
}

func TestNewRequiresAnEndpoint(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New accepted a config with no endpoint")
	}
}

// The pool is shared mutable state on the hot path, so hammer it: many callers
// at once must still get one session each and never see another's identity.
func TestConcurrentCallersKeepTheirOwnSessions(t *testing.T) {
	f, url := newFakeServer(t)
	c := newTestClient(t, url)

	const callers, each = 8, 5
	var wg sync.WaitGroup
	errs := make(chan error, callers*each)

	for i := 0; i < callers; i++ {
		bearer := "tok-" + string(rune('a'+i))
		for j := 0; j < each; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := c.CallTool(context.Background(), Call{
					Tool:     "whoami",
					Identity: Identity{Bearer: bearer, ContextID: "ctx-" + bearer},
				})
				if err != nil {
					errs <- err
					return
				}
				var out echoOut
				if err := json.Unmarshal(res.Structured, &out); err != nil {
					errs <- err
					return
				}
				if out.ResolvedAs != bearer {
					errs <- fmtErrorf("resolved as %q, want %q", out.ResolvedAs, bearer)
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// One session per bearer, not one per call.
	sessions := map[string]map[string]bool{}
	for i := 0; i < f.numCalls(); i++ {
		r := f.record(i)
		if sessions[r.bearer] == nil {
			sessions[r.bearer] = map[string]bool{}
		}
		sessions[r.bearer][r.sessionID] = true
	}
	for bearer, ss := range sessions {
		if len(ss) != 1 {
			t.Errorf("bearer %q opened %d sessions, want 1", bearer, len(ss))
		}
	}
	if len(sessions) != callers {
		t.Errorf("saw %d distinct bearers, want %d", len(sessions), callers)
	}
}

func fmtErrorf(format string, a ...any) error { return fmt.Errorf(format, a...) }
