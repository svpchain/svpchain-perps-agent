package mcpclient_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// TestLiveDexMCP exercises the client against a real svpchain-dex-mcp
// deployment. Skipped unless SVPCHAIN_MCP_ENDPOINT is set, so the default
// `go test ./...` stays hermetic and offline.
//
//	SVPCHAIN_MCP_ENDPOINT=https://dex-mcp-testnet.svpchain.org/ go test ./internal/mcpclient/ -run Live -v
//
// It calls only unauthenticated reads, so it needs no key and no bearer.
func TestLiveDexMCP(t *testing.T) {
	endpoint := os.Getenv("SVPCHAIN_MCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("set SVPCHAIN_MCP_ENDPOINT to run this against a live server")
	}

	c, err := mcpclient.New(mcpclient.Config{
		Endpoint:    endpoint,
		Name:        "svpchain-perps-agent",
		Version:     "test",
		CallTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	t.Logf("catalog: %d tools", len(tools))

	withSchema := 0
	for _, tl := range tools {
		if tl.InputSchema != nil {
			withSchema++
		}
	}
	if withSchema != len(tools) {
		t.Errorf("%d of %d tools carry no input schema", len(tools)-withSchema, len(tools))
	}

	// A read that needs no credential, through the normal call path.
	res, err := c.CallTool(ctx, mcpclient.Call{
		Tool:       "get_height",
		Identity:   mcpclient.Identity{ContextID: "live-test"},
		Idempotent: true,
	})
	if err != nil {
		t.Fatalf("get_height: %v", err)
	}
	t.Logf("get_height -> isError=%v structured=%s text=%q", res.IsError, res.Structured, res.Text)

	// A gated tool with no bearer must come back as a soft refusal, not a
	// transport error: that is what lets an A2A caller authenticate and retry.
	gated, err := c.CallTool(ctx, mcpclient.Call{
		Tool:       "get_subaccount",
		Args:       json.RawMessage(`{"address":"svp1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq","subaccount_number":0}`),
		Identity:   mcpclient.Identity{ContextID: "live-test"},
		Idempotent: true,
	})
	if err != nil {
		t.Fatalf("gated call surfaced as a transport error: %v", err)
	}
	t.Logf("unauthenticated get_subaccount -> isError=%v text=%q structured=%s", gated.IsError, gated.Text, gated.Structured)
}
