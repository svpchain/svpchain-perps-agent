package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Defaults for the knobs an operator rarely sets.
const (
	DefaultIdleTimeout = 10 * time.Minute
	DefaultSweepEvery  = time.Minute
	DefaultCallTimeout = 30 * time.Second
)

// Config points the client at one remote MCP server.
type Config struct {
	// Endpoint is the server's Streamable HTTP URL. svpchain-dex-mcp serves
	// MCP at the root of its listener, so this is an origin, not a path.
	Endpoint string

	// Name and Version identify this agent to the server in the MCP
	// initialize handshake.
	Name    string
	Version string

	// IdleTimeout is how long a pooled session may go unused before it is
	// closed. Zero means DefaultIdleTimeout.
	IdleTimeout time.Duration

	// CallTimeout bounds a single tool call. Zero means DefaultCallTimeout.
	CallTimeout time.Duration

	// HTTPClient is the transport to dial with. Zero value means a default
	// client. Its Transport is wrapped so per-call credentials still apply.
	HTTPClient *http.Client
}

func (c *Config) applyDefaults() {
	if c.IdleTimeout == 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.CallTimeout == 0 {
		c.CallTimeout = DefaultCallTimeout
	}
	if c.Name == "" {
		c.Name = "svpchain-perps-agent"
	}
	if c.Version == "" {
		c.Version = "dev"
	}
}

func (c *Config) validate() error {
	if c.Endpoint == "" {
		return fmt.Errorf("mcp endpoint is required")
	}
	return nil
}

// Client calls tools on a remote MCP server on behalf of many A2A callers.
//
// It is safe for concurrent use. See the package doc for why sessions are
// pooled per identity rather than shared.
type Client struct {
	cfg  Config
	impl *mcp.Implementation
	http *http.Client

	mu      sync.Mutex
	pool    map[string]*entry
	closed  bool
	sweepOn bool
}

// entry is one pooled session. Its own mutex serialises connect attempts for
// that key, so two concurrent calls from one tenant open one session rather
// than racing to open two.
type entry struct {
	mu       sync.Mutex
	session  *mcp.ClientSession
	lastUsed time.Time
}

// New returns a client for cfg. It opens no connection: sessions are dialled
// on first use, so a server that is down at boot costs the agent nothing until
// a caller actually needs it.
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	// Copy rather than mutate the caller's client, and wrap whatever
	// Transport it already had so per-call credentials survive a custom one.
	wrapped := *httpClient
	base := wrapped.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	wrapped.Transport = &bearerTransport{base: base}

	return &Client{
		cfg:  cfg,
		http: &wrapped,
		impl: &mcp.Implementation{Name: cfg.Name, Version: cfg.Version},
		pool: map[string]*entry{},
	}, nil
}

// Endpoint is the remote this client calls, for logs and diagnostics.
func (c *Client) Endpoint() string { return c.cfg.Endpoint }

// Call is one tool invocation.
type Call struct {
	// Tool is the MCP tool name, e.g. "get_orderbook".
	Tool string

	// Args is the tool's arguments object, already JSON. Nil is sent as no
	// arguments.
	Args json.RawMessage

	// Identity is who the call is made as.
	Identity Identity

	// Idempotent permits one transparent reconnect-and-retry when a pooled
	// session turns out to be dead.
	//
	// ★ Default false, and deliberately so. A retry is only safe for a call
	// that can run twice; broadcast_signed_tx is the counter-example, where a
	// retry after an ambiguous failure risks landing the transaction twice.
	// Reads set it, builders and broadcast do not.
	Idempotent bool
}

// Result is a tool's reply.
//
// IsError is carried rather than converted into a Go error because the server
// uses a non-error result to say "authenticate first": its auth gate answers
// an unauthenticated call to a gated tool with a normal result containing the
// handshake instructions, precisely so an agent loop can act on it instead of
// aborting. Collapsing that into an error would defeat it.
type Result struct {
	// Structured is the tool's typed output, as JSON. Empty when the tool
	// returned only unstructured content.
	Structured json.RawMessage

	// Text is the concatenated text content, which is what a tool that
	// refuses puts its reason in.
	Text string

	// IsError reports a tool-level failure, as opposed to a transport error.
	IsError bool
}

// CallTool invokes one tool on the remote server as the call's identity.
func (c *Client) CallTool(ctx context.Context, call Call) (*Result, error) {
	if call.Tool == "" {
		return nil, fmt.Errorf("no tool named")
	}

	res, err := c.callOnce(ctx, call)
	if err == nil {
		return res, nil
	}
	// A pooled session outlives the server that issued it: a restart on the
	// other side leaves a handle that fails on first use. Reconnecting and
	// trying again turns that into a hiccup rather than an error the A2A
	// caller sees — but only where running the call twice is harmless.
	if !call.Idempotent {
		return nil, err
	}
	c.evict(sessionKey(call.Identity))
	return c.callOnce(ctx, call)
}

func (c *Client) callOnce(ctx context.Context, call Call) (*Result, error) {
	key := sessionKey(call.Identity)

	// A caller with neither a bearer nor a conversation has no key that could
	// be reused safely, so it gets a session of its own that dies with the
	// call. Rare in practice: A2A requests carry a context id.
	ephemeral := key == ""

	var (
		sess *mcp.ClientSession
		err  error
	)
	if ephemeral {
		sess, err = c.connect(ctx)
		if err != nil {
			return nil, err
		}
		defer sess.Close()
	} else {
		sess, err = c.sessionFor(ctx, key)
		if err != nil {
			return nil, err
		}
	}

	callCtx, cancel := context.WithTimeout(withIdentity(ctx, call.Identity), c.cfg.CallTimeout)
	defer cancel()

	params := &mcp.CallToolParams{Name: call.Tool}
	if len(call.Args) > 0 {
		params.Arguments = call.Args
	}

	out, err := sess.CallTool(callCtx, params)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", call.Tool, err)
	}
	return toResult(out)
}

// ListTools returns the remote's catalog. The agent uses it at boot to check
// that the tools it advertises on its card are the tools the server actually
// serves.
func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	sess, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	var tools []*mcp.Tool
	var cursor string
	for {
		res, err := sess.ListTools(withIdentity(ctx, Identity{}), &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" {
			return tools, nil
		}
		cursor = res.NextCursor
	}
}

// sessionKey picks the pool slot an identity may safely share. Empty means no
// slot is safe; see the package doc.
func sessionKey(id Identity) string {
	switch {
	case id.Bearer != "":
		return "b:" + id.Bearer
	case id.ContextID != "":
		return "c:" + id.ContextID
	default:
		return ""
	}
}

func (c *Client) sessionFor(ctx context.Context, key string) (*mcp.ClientSession, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp client is closed")
	}
	e, ok := c.pool[key]
	if !ok {
		e = &entry{}
		c.pool[key] = e
	}
	c.mu.Unlock()

	// Held across the dial so concurrent calls on one key open one session.
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastUsed = time.Now()
	if e.session != nil {
		return e.session, nil
	}
	sess, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	e.session = sess
	return sess, nil
}

func (c *Client) connect(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(c.impl, nil)
	tr := &mcp.StreamableClientTransport{Endpoint: c.cfg.Endpoint, HTTPClient: c.http}
	sess, err := client.Connect(ctx, tr, nil)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", c.cfg.Endpoint, err)
	}
	return sess, nil
}

func (c *Client) evict(key string) {
	if key == "" {
		return
	}
	c.mu.Lock()
	e, ok := c.pool[key]
	if ok {
		delete(c.pool, key)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		_ = e.session.Close()
		e.session = nil
	}
}

// Run sweeps idle sessions until ctx is cancelled, then closes the pool. It
// never returns an error; it is a background chore, not a health signal.
func (c *Client) Run(ctx context.Context) error {
	t := time.NewTicker(DefaultSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Close()
			return nil
		case <-t.C:
			c.sweep(time.Now())
		}
	}
}

func (c *Client) sweep(now time.Time) {
	c.mu.Lock()
	var stale []*entry
	for key, e := range c.pool {
		e.mu.Lock()
		idle := e.session != nil && now.Sub(e.lastUsed) > c.cfg.IdleTimeout
		// An entry that never got a session (a dial that failed) is also
		// garbage once it goes cold, or the map grows one slot per failure.
		empty := e.session == nil && now.Sub(e.lastUsed) > c.cfg.IdleTimeout
		e.mu.Unlock()
		if idle || empty {
			stale = append(stale, e)
			delete(c.pool, key)
		}
	}
	c.mu.Unlock()

	for _, e := range stale {
		e.mu.Lock()
		if e.session != nil {
			_ = e.session.Close()
			e.session = nil
		}
		e.mu.Unlock()
	}
}

// Close shuts every pooled session. Further calls fail rather than silently
// reopening connections after shutdown.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	pool := c.pool
	c.pool = map[string]*entry{}
	c.mu.Unlock()

	for _, e := range pool {
		e.mu.Lock()
		if e.session != nil {
			_ = e.session.Close()
			e.session = nil
		}
		e.mu.Unlock()
	}
}

func toResult(out *mcp.CallToolResult) (*Result, error) {
	res := &Result{IsError: out.IsError}
	for _, ct := range out.Content {
		if t, ok := ct.(*mcp.TextContent); ok && t.Text != "" {
			if res.Text != "" {
				res.Text += "\n"
			}
			res.Text += t.Text
		}
	}
	if out.StructuredContent != nil {
		b, err := json.Marshal(out.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("encode structured result: %w", err)
		}
		res.Structured = b
	}
	return res, nil
}
