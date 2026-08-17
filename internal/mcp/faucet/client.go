// Package faucet is a thin HTTP client for the svpchain faucet backend
// (https://pre-faucet.svpchain.org). The faucet is a standalone service: its
// operator account signs and submits the on-chain claim itself, so a caller
// never signs anything. The MCP faucet tools (faucet_claim, list_faucet_tokens)
// are thin wrappers over this client — no EVM JSON-RPC, no contract address,
// no client-side signing.
package faucet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// TokenInfo is one entry from GET /api/enabledTokens: a claimable token, the
// per-claim amount (base units, as a string to preserve the full uint range),
// and whether it is currently enabled.
type TokenInfo struct {
	Address       string `json:"address"`
	AmountAllowed string `json:"amount_allowed"`
	Enabled       bool   `json:"enabled"`
}

// ClaimResult is the body POST /api/claim returns on success: the on-chain tx
// hash of the operator-submitted claim, the amount dispensed, and the token.
type ClaimResult struct {
	TxHash string `json:"tx_hash"`
	Amount string `json:"amount"`
	Token  string `json:"token"`
}

// Client talks to the faucet backend's HTTP API (base path /api). It mirrors
// the indexer Client's shape: a shared http.Client, an outbound rate limiter,
// and centralized non-2xx handling. Only the two endpoints the MCP tools need
// are exposed.
type Client struct {
	baseURL    string // e.g. "https://pre-faucet.svpchain.org"
	httpClient *http.Client
	limiter    *rate.Limiter
}

// Options configures a new Client. Zero values mean sensible defaults.
type Options struct {
	// Timeout is the overall per-request deadline (default 30s).
	Timeout time.Duration

	// RateLimit caps outbound RPS to the faucet so the MCP server does not
	// hammer it under bursty agent traffic. Default: 20 RPS, burst 5.
	RateLimitRPS   float64
	RateLimitBurst int
}

// NewClient constructs a Client. baseURL must include scheme + host (no
// trailing /api — Client adds the path prefix itself).
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
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: opts.Timeout},
		limiter:    rate.NewLimiter(rate.Limit(opts.RateLimitRPS), opts.RateLimitBurst),
	}
}

// EnabledTokens fetches GET /api/enabledTokens — the tokens the faucet will
// currently dispense, with their per-claim amounts.
func (c *Client) EnabledTokens(ctx context.Context) ([]TokenInfo, error) {
	var out []TokenInfo
	if err := c.do(ctx, http.MethodGet, "/enabledTokens", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Claim POSTs /api/claim to dispense `token` to `address`. The faucet's
// operator account signs and submits the on-chain claim; the returned
// ClaimResult carries the resulting tx hash.
func (c *Client) Claim(ctx context.Context, token, address string) (ClaimResult, error) {
	body := map[string]string{"token": token, "address": address}
	var out ClaimResult
	if err := c.do(ctx, http.MethodPost, "/claim", body, &out); err != nil {
		return ClaimResult{}, err
	}
	return out, nil
}

// errorBody is the faucet's error envelope: {"error":"...","retry_after":N}.
// retry_after is only present on 429.
type errorBody struct {
	Error      string `json:"error"`
	RetryAfter int64  `json:"retry_after"`
}

// do issues an /api request, JSON-encoding reqBody (when non-nil) and decoding
// a 2xx response into out. It honors the rate limiter and turns non-2xx
// responses into a message that surfaces the faucet's own `error` field (and
// retry_after on 429), so the MCP client sees an actionable reason.
func (c *Client) do(ctx context.Context, method, path string, reqBody any, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	endpoint := c.baseURL + "/api" + path

	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encode %s body: %w", endpoint, err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return fmt.Errorf("new request %s: %w", endpoint, err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var eb errorBody
		_ = json.Unmarshal(raw, &eb)
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			msg := eb.Error
			if msg == "" {
				msg = "rate limited"
			}
			if eb.RetryAfter > 0 {
				return fmt.Errorf("faucet: %s (retry after %ds)", msg, eb.RetryAfter)
			}
			return fmt.Errorf("faucet: %s", msg)
		case eb.Error != "":
			return fmt.Errorf("faucet: %s", eb.Error)
		default:
			return fmt.Errorf("%s %s: status %d: %s", method, endpoint, resp.StatusCode, strings.TrimSpace(string(raw)))
		}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s: %w", endpoint, err)
		}
	}
	return nil
}
