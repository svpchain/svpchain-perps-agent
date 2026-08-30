package toolbridge

import (
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"
)

// expectedOps pins the full skill → tool table. A tool added to (or removed
// from) the bridge without updating this table fails the test — the table is
// the completeness contract for "the A2A surface covers every MCP tool".
var expectedOps = map[string][]string{
	SkillMarketData: {
		"list_markets", "get_market", "get_orderbook", "get_oracle_price",
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
	SkillMeta:      {"list_tools"},
}

func TestRegistryCoversEveryExpectedTool(t *testing.T) {
	r := New(&tools.Handlers{})

	total := 0
	for skill, toolNames := range expectedOps {
		for _, tool := range toolNames {
			total++
			op, ok := r.Lookup(tool)
			if !ok {
				t.Errorf("tool %q missing from registry", tool)
				continue
			}
			if op.Skill != skill {
				t.Errorf("tool %q registered under skill %q, expected %q", tool, op.Skill, skill)
			}
		}
	}
	// 37 = 36 bridged MCP tools (the 64-tool MCP surface minus the 14 EVM and
	// 12 Lendora tools this binary does not bridge, minus the 2 faucet tools
	// it no longer serves) + list_tools, which is this agent's own and has no
	// MCP twin.
	if total != 37 {
		t.Fatalf("expected table lists %d tools; the bridged surface is 37 — fix the table", total)
	}

	// The reverse direction: nothing extra is registered under these skills.
	for skill, got := range r.BySkill() {
		want, ok := expectedOps[skill]
		if !ok {
			t.Errorf("skill %q is registered but not in the expected table: %v", skill, got)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("skill %q has %d tools registered, expected %d: %v", skill, len(got), len(want), got)
		}
	}
}
