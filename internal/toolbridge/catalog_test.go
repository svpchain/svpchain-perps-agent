package toolbridge

import "testing"

// The bridged surface is exactly the DEX server's catalog, so a diff against
// the real tool list must come back clean. This is the check the agent runs at
// boot, run here against the names svpchain-dex-mcp actually serves.
// dexMCPToolNames is the catalog svpchain-dex-mcp actually serves, checked
// against the live deployment.
func dexMCPToolNames() []string {
	return []string{
		"auth_challenge", "auth_verify", "broadcast_signed_tx", "build_bank_send",
		"build_batch_cancel_orders", "build_cancel_order", "build_deposit_to_subaccount",
		"build_place_conditional_order", "build_place_limit_order", "build_place_market_order",
		"build_transfer_between_subaccounts", "build_withdraw_from_subaccount", "get_balance",
		"get_candles", "get_fills", "get_funding_payments", "get_height", "get_historical_funding",
		"get_historical_pnl", "get_live_subaccount", "get_market", "get_order", "get_orderbook",
		"get_orders", "get_pnl", "get_sparklines", "get_subaccount", "get_time", "get_trades",
		"get_transfer_out_cap", "get_transfers", "get_tx_status", "list_markets",
		"set_transfer_out_cap", "whoami",
	}
}

func TestDiffCatalogAgreesWithTheDexServer(t *testing.T) {
	d := NewRemote(nil).DiffCatalog(dexMCPToolNames())
	if !d.OK() {
		t.Errorf("surfaces disagree: missing=%v extra=%v", d.Missing, d.Extra)
	}
}

// list_tools is this agent's own, so a server that does not serve it is not
// drifting.
func TestDiffCatalogIgnoresAgentOwnedTools(t *testing.T) {
	r := NewEmpty()
	r.RegisterMeta()
	if d := r.DiffCatalog(nil); !d.OK() {
		t.Errorf("list_tools counted against an empty remote: %+v", d)
	}
}

func TestDiffCatalogReportsBothDirections(t *testing.T) {
	r := NewEmpty()
	r.add(SkillBroadcast, "broadcast_signed_tx", Bound{})
	r.add(SkillBroadcast, "get_tx_status", Bound{})

	d := r.DiffCatalog([]string{"broadcast_signed_tx", "a_tool_we_do_not_bridge"})
	if len(d.Missing) != 1 || d.Missing[0] != "get_tx_status" {
		t.Errorf("missing = %v, want [get_tx_status]", d.Missing)
	}
	if len(d.Extra) != 1 || d.Extra[0] != "a_tool_we_do_not_bridge" {
		t.Errorf("extra = %v, want [a_tool_we_do_not_bridge]", d.Extra)
	}
	if d.OK() {
		t.Error("OK() true despite a diff in both directions")
	}
}

// ★ The catalog check must be clean with EVERY skill registered, including the
// conditional ones. It was not: "ask" is the assistant's own tool, and the
// hand-kept list of agent-owned tools never gained it, so an agent with a
// model configured refused to start — the boot check read the assistant's own
// tool as an operation the card promised and the server could not serve.
//
// It crash-looped, and only where the assistant was configured, so every test
// and every local run here was clean. This registers the whole surface, which
// is the state a real deployment with a model key is in.
func TestDiffCatalogIsCleanWithEverySkillRegistered(t *testing.T) {
	r := NewRemote(nil)
	r.RegisterAssistant(nil)

	d := r.DiffCatalog(dexMCPToolNames())
	if !d.OK() {
		t.Errorf("a fully-registered agent does not match the server it proxies: "+
			"advertised-but-not-served=%v served-but-not-bridged=%v", d.Missing, d.Extra)
	}
}

// Every tool this agent implements itself must be excluded from the
// comparison, and every proxied one included.
func TestOnlyProxiedToolsAreComparedAgainstTheServer(t *testing.T) {
	for _, own := range []string{"list_tools", EstimateClearingPrice, "ask"} {
		if isProxied(own) {
			t.Errorf("%s is this agent's own operation but is compared against the server", own)
		}
	}
	for _, proxied := range []string{"list_markets", "get_subaccount", "broadcast_signed_tx"} {
		if !isProxied(proxied) {
			t.Errorf("%s is proxied but excluded from the comparison", proxied)
		}
	}
}
