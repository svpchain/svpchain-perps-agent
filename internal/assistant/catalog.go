package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// WithheldTools are the MCP tools the planner is not given. See the package
// doc for why each one is here; the short version is that the custody design
// makes an unsigned payload harmless, and these three do not produce one.
var WithheldTools = map[string]string{
	"broadcast_signed_tx":  "lands a transaction the caller already signed; submitting is the caller's decision, not the planner's",
	"set_transfer_out_cap": "moves a safety limit server-side with no signature; a planner that can raise a cap can weaken the control that bounds it",
	"auth_challenge":       "the handshake; a request reaches the planner with a bearer already resolved",
	"auth_verify":          "the handshake; minting a second identity mid-plan is a way to get confused, not a capability",
}

// readOnlyTools are the tools that only read. They are safe to retry through a
// dead pooled session, which the ones that build a payload are not: a builder
// consumes an account sequence number, so running it twice can hand back two
// payloads that cannot both land.
var readOnlyTools = map[string]bool{
	"get_balance": true, "get_candles": true, "get_fills": true,
	"get_funding_payments": true, "get_height": true, "get_historical_funding": true,
	"get_historical_pnl": true, "get_live_subaccount": true, "get_market": true,
	"get_order": true, "get_orderbook": true, "get_orders": true, "get_pnl": true,
	"get_sparklines": true, "get_subaccount": true, "get_time": true,
	"get_trades": true, "get_transfer_out_cap": true, "get_transfers": true,
	"get_tx_status": true, "list_markets": true, "whoami": true,
}

// Catalog is the tool surface the planner may use, built from what the MCP
// server actually serves.
//
// It is read from the server rather than declared here on purpose. The
// descriptions and argument schemas are the server's own, written next to the
// handlers, so the planner sees what the implementation says it does. A copy
// maintained here would be free to drift, and a planner reasoning from a stale
// description is worse than one reasoning from none.
type Catalog struct {
	tools []ToolDef
	names map[string]bool
}

// BuildCatalog reads the remote catalog and converts it into tool definitions.
func BuildCatalog(ctx context.Context, mcp *mcpclient.Client) (*Catalog, error) {
	remote, err := mcp.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("read tool catalog: %w", err)
	}

	c := &Catalog{names: map[string]bool{}}
	for _, t := range remote {
		if _, withheld := WithheldTools[t.Name]; withheld {
			continue
		}
		schema, err := toolSchema(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", t.Name, err)
		}
		c.tools = append(c.tools, ToolDef{Name: t.Name, Description: t.Description, Schema: schema})
		c.names[t.Name] = true
	}

	// Sorted, because the tool block is a cache prefix on providers that
	// cache: the same catalog must serialize the same way every request or
	// every call pays full price. Harmless on the providers that do not.
	sort.Slice(c.tools, func(i, j int) bool { return c.tools[i].Name < c.tools[j].Name })

	if len(c.tools) == 0 {
		return nil, fmt.Errorf("tool catalog is empty; the MCP server served nothing usable")
	}
	return c, nil
}

// Tools returns the definitions to offer the model, provider-neutral.
func (c *Catalog) Tools() []ToolDef { return c.tools }

// Len is how many tools the planner may call.
func (c *Catalog) Len() int { return len(c.tools) }

// Allows reports whether the planner may call this tool. The loop checks it
// before dispatching: a model can name a tool that is not in the catalog it
// was given, and that must come back as a tool error it can recover from
// rather than a call this agent actually makes.
func (c *Catalog) Allows(tool string) bool { return c.names[tool] }

// toolSchema converts a tool's JSON Schema into a plain map.
//
// The MCP schema arrives as a typed value from the Go SDK, and both providers
// want a map. Round-tripping through JSON is the honest conversion: it keeps
// every keyword the server published, including the per-field descriptions
// that are most of what the planner reasons from, and `required`, which is how
// a model learns an argument is not optional.
//
// ★ It keeps the WHOLE object rather than pulling out properties. An earlier
// version took properties alone, which silently dropped `required` on the way
// through — invisible in behaviour until a model omitted a mandatory argument
// and the tool refused for a reason the model had no way to anticipate.
func toolSchema(schema any) (map[string]any, error) {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode input schema: %w", err)
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	if _, ok := decoded["type"]; !ok {
		decoded["type"] = "object"
	}
	if _, ok := decoded["properties"]; !ok {
		decoded["properties"] = map[string]any{}
	}
	return decoded, nil
}
