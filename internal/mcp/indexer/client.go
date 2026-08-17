package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// ErrNotFound is returned by get() when the indexer responds 404. Callers
// for endpoints where "not found" is a legitimate empty result — e.g.
// /v4/pnl for a subaccount that has never traded — should check with
// errors.Is and translate to a zero-valued response. Endpoints where 404
// genuinely is an error (a typo'd ticker on /v4/orderbooks/...) should
// continue to propagate it.
var ErrNotFound = errors.New("indexer: not found")

// Client talks to the svpchain Indexer's Comlink REST API (/v4/*). It is
// intentionally small: only the endpoints used by MCP tools are exposed,
// returning DTOs limited to the fields the tools consume.
type Client struct {
	baseURL     string // e.g. "http://127.0.0.1:3002"
	httpClient  *http.Client
	bearerToken string // optional; empty == no auth header
	limiter     *rate.Limiter
}

// Options configures a new Client. Zero values mean sensible defaults.
type Options struct {
	// BearerToken, if set, is sent as `Authorization: Bearer <token>` —
	// useful when Comlink is gated behind a proxy in production.
	BearerToken string

	// Timeout is the overall per-request deadline (default 30s). The HTTP
	// client also enforces a 5s read header timeout via Transport.
	Timeout time.Duration

	// RateLimit caps outbound RPS to Comlink so the MCP server does not
	// hammer the indexer under bursty agent traffic. Default: 20 RPS, burst 5.
	RateLimitRPS   float64
	RateLimitBurst int
}

// NewClient constructs a Client. baseURL must include scheme + host (no
// trailing /v4 — Client adds the version prefix itself).
func NewClient(baseURL string, opts Options) *Client {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.RateLimitRPS == 0 {
		opts.RateLimitRPS = 20
	}
	if opts.RateLimitBurst == 0 {
		opts.RateLimitBurst = 5
	}
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		httpClient:  &http.Client{Timeout: opts.Timeout},
		bearerToken: opts.BearerToken,
		limiter:     rate.NewLimiter(rate.Limit(opts.RateLimitRPS), opts.RateLimitBurst),
	}
}

// get fetches GET ${baseURL}/v4${path} and JSON-decodes the body into out.
// It honors the rate limiter (blocking until a token is available or ctx
// fires) and adds the bearer token if configured.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	endpoint := c.baseURL + "/v4" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("new request %s: %w", endpoint, err)
	}
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Surface as a sentinel so callers can decide whether 404 is a
		// real error or an expected "empty" condition (see ErrNotFound
		// comment above).
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GET %s: status %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}
