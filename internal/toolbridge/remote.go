package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// remoteOps is the surface this agent advertises, and the skill each operation
// belongs to.
//
// ★ Static and authoritative, deliberately, even though the same list could be
// read from the MCP server at boot. The served Agent Card embeds these tool
// names, and the card's bytes are hashed into this agent's on-chain
// registration. A card built from a remote's reply would move whenever that
// remote's catalog moved, silently invalidating the registration with nothing
// to tell an operator to re-register. So the table is the contract; the
// server's catalog is checked against it (Registry.DiffCatalog) rather than
// used to build it.
//
// Tool descriptions and argument schemas are the opposite: they are NOT on the
// card, so they come from the server, where they are written next to the
// handlers.
var remoteOps = map[string][]string{
	SkillMarketData: {
		"list_markets", "get_market", "get_orderbook",
		"get_candles", "get_trades", "get_sparklines", "get_historical_funding",
		"get_height", "get_time",
	},
	SkillAccount: {
		"get_subaccount", "get_live_subaccount", "get_balance", "whoami",
		"get_orders", "get_order", "get_fills", "get_transfers", "get_pnl",
		"get_historical_pnl", "get_funding_payments",
	},
	SkillTrading: {
		"build_place_limit_order", "build_place_market_order",
		"build_place_conditional_order", "build_cancel_order",
		"build_batch_cancel_orders",
	},
	SkillFunds: {
		"build_deposit_to_subaccount", "build_withdraw_from_subaccount",
		"build_transfer_between_subaccounts", "build_bank_send",
		"get_transfer_out_cap", "set_transfer_out_cap",
	},
	SkillBroadcast: {"broadcast_signed_tx", "get_tx_status"},
	SkillAuth:      {"auth_challenge", "auth_verify"},
}

// idempotentTools may be retried through a pooled session that turned out to
// be dead.
//
// ★ Reads only. A builder consumes an account sequence number, so running it
// twice can hand back two payloads that cannot both land; broadcast_signed_tx
// retried after an ambiguous failure can land the transaction twice. Those
// surface the failure instead.
var idempotentTools = map[string]bool{
	"list_markets": true, "get_market": true, "get_orderbook": true,
	"get_candles": true, "get_trades": true, "get_sparklines": true,
	"get_historical_funding": true, "get_height": true, "get_time": true,
	"get_subaccount": true, "get_live_subaccount": true, "get_balance": true,
	"whoami": true, "get_orders": true, "get_order": true, "get_fills": true,
	"get_transfers": true, "get_pnl": true, "get_historical_pnl": true,
	"get_funding_payments": true, "get_transfer_out_cap": true,
	"get_tx_status": true,
}

// RemoteToolNames is every tool this agent proxies, sorted. The boot-time
// catalog check reads it.
func RemoteToolNames() []string {
	var out []string
	for _, tools := range remoteOps {
		out = append(out, tools...)
	}
	sort.Strings(out)
	return out
}

// NewRemote builds the operation registry over a remote MCP server.
//
// This is what replaced dispatching into a vendored copy of that server's
// handlers. The A2A surface is unchanged — same skills, same tool names, same
// envelope — but an operation is now one call to the server, made as the A2A
// caller rather than as this agent.
func NewRemote(mcp *mcpclient.Client) *Registry {
	r := newRegistry()
	for skill, tools := range remoteOps {
		for _, tool := range tools {
			r.add(skill, tool, remoteBound(mcp, tool))
		}
	}
	r.RegisterMeta()
	// A nil client builds the registry's shape without a connection, which is
	// what the card tests need: the card is made of tool names, and those are
	// this table's, not the server's.
	if mcp != nil {
		r.useRemoteToolInfo(mcp)
	}
	return r
}

// remoteBound is one proxied operation.
func remoteBound(mcp *mcpclient.Client, tool string) Bound {
	return Bound{Call: func(ctx context.Context, args json.RawMessage) (any, error) {
		if mcp == nil {
			return nil, fmt.Errorf("%s: this registry has no MCP server behind it", tool)
		}
		// The A2A caller's own credential, resolved by a2aserver's
		// AuthResolver. Absent, the call goes as nobody and the server
		// refuses on its own terms — the same answer the caller would get
		// asking the server directly.
		id, _ := mcpclient.CallerFrom(ctx)

		res, err := mcp.CallTool(ctx, mcpclient.Call{
			Tool:       tool,
			Args:       args,
			Identity:   id,
			Idempotent: idempotentTools[tool],
		})
		if err != nil {
			return nil, err
		}

		// ★ Mapping the server's two failure shapes onto one A2A answer.
		//
		// The server marks a hard failure IsError, and answers an
		// unauthenticated call to a gated tool with a NON-error result
		// carrying handshake instructions — soft on purpose, so an MCP agent
		// loop retries instead of aborting. Over A2A both are "the task ran
		// and the answer is no", which the envelope already expresses as
		// ok:false with a reason, and which is exactly what callers saw when
		// these handlers were in-process.
		//
		// The two are told apart by structured content: a tool that produced
		// a result has it, and a refusal carries only text.
		if res.IsError || len(res.Structured) == 0 {
			if res.Text != "" {
				return nil, fmt.Errorf("%s", res.Text)
			}
			return nil, fmt.Errorf("%s returned no result", tool)
		}

		var out any
		if err := json.Unmarshal(res.Structured, &out); err != nil {
			return nil, fmt.Errorf("decode %s result: %w", tool, err)
		}
		return out, nil
	}}
}
