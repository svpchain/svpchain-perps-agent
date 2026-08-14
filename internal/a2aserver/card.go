package a2aserver

import (
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// Skill-ID aliases, so callers of this package need not import toolbridge for
// the two skills its own doc comments and tests name most.
const (
	SkillMarketData = toolbridge.SkillMarketData
	SkillExecution  = toolbridge.SkillExecution
)

// skillMeta is the static, human-facing half of a skill; the tool list comes
// from the registry so the card can never advertise an operation the executor
// would not dispatch (a test asserts the two agree).
type skillMeta struct {
	id       string
	name     string
	desc     string
	tags     []string
	examples []string
}

var skillMetas = []skillMeta{
	{
		id:   toolbridge.SkillMarketData,
		name: "SVP-Chain Market Data",
		desc: "Read-only market intelligence: perpetual markets, live orderbooks, candles, " +
			"trades, funding, oracle price, and a batch-auction clearing-price estimate " +
			"for a given order size. Needs no credential and no account.",
		tags: []string{"market-data", "orderbook", "funding", "perpetuals", "read-only"},
		examples: []string{
			`{"skill":"svpchain-market-data","query":"estimate","ticker":"BTC-USD","side":"buy","size":"2.5"}`,
			`{"skill":"svpchain-market-data","tool":"list_markets"}`,
			`{"skill":"svpchain-market-data","tool":"get_orderbook","args":{"ticker":"BTC-USD"}}`,
		},
	},
	{
		id:   toolbridge.SkillAccount,
		name: "SVP-Chain Account & Positions",
		desc: "Owner-scoped reads: subaccounts (indexer, or live from chain gRPC via " +
			"get_live_subaccount, which answers even when the indexer is down), wallet " +
			"balances, orders, fills, transfers, PnL, and funding payments. Requires a bearer from " +
			"the svpchain-auth skill — or an SVP-DT credential granting the " +
			`"query.account" action, attached as message.metadata "svp.delegation/v1", ` +
			"which covers every owner-scoped read except the id-only get_order and the " +
			"session tool whoami; the owner then defaults to the credential's principal, " +
			"and the credential stays reusable for polling until it expires.",
		tags: []string{"account", "positions", "pnl", "orders"},
		examples: []string{
			`{"skill":"svpchain-account","tool":"get_subaccount","args":{"address":"svp1…","subaccount_number":0},"bearer":"…"}`,
			`message.metadata: {"svp.delegation/v1":{"tokens":["<base64 token>", "…"]}} · ` +
				`text: {"skill":"svpchain-account","tool":"get_balance","args":{}}`,
		},
	},
	{
		id:   toolbridge.SkillTrading,
		name: "SVP-Chain Trading (build)",
		desc: "Build unsigned order transactions — limit, market, conditional, cancel, batch " +
			"cancel — as payloads the caller signs with its own key and lands via " +
			"broadcast_signed_tx. The agent never holds the caller's key.",
		tags: []string{"trading", "orders", "unsigned-tx"},
		examples: []string{
			`{"skill":"svpchain-trading","tool":"build_place_limit_order","args":{"owner":"svp1…","subaccount_number":0,"ticker":"BTC-USD","side":"buy","size":"0.1","price":"60000"},"bearer":"…"}`,
		},
	},
	{
		id:   toolbridge.SkillFunds,
		name: "SVP-Chain Funds (build)",
		desc: "Build unsigned funds movements — deposit/withdraw/transfer between subaccounts, " +
			"bank send — plus per-symbol daily transfer-out caps. Movements are size-capped " +
			"by the operator's limits config.",
		tags: []string{"funds", "deposit", "withdraw", "unsigned-tx"},
	},
	{
		id:   toolbridge.SkillBroadcast,
		name: "SVP-Chain Broadcast",
		desc: "Land a signed transaction (the signer must be the authenticated owner) and " +
			"query transaction status by hash.",
		tags: []string{"broadcast", "tx-status"},
	},
	{
		id:   toolbridge.SkillAuth,
		name: "SVP-Chain Self-Service Auth",
		desc: "Wallet-signature authentication: auth_challenge issues a challenge bound to " +
			"an owner address, auth_verify checks the wallet's signature and mints a " +
			"bearer token that authenticates subsequent calls (Authorization header, " +
			"envelope field, or bound to this A2A context).",
		tags: []string{"auth", "bearer", "wallet-signature"},
		examples: []string{
			`{"skill":"svpchain-auth","tool":"auth_challenge","args":{"owner":"svp1…"}}`,
			`{"skill":"svpchain-auth","tool":"auth_verify","args":{"nonce":"…","signature":"…"}}`,
		},
	},
	{
		id:   toolbridge.SkillFaucet,
		name: "SVP-Chain Faucet",
		desc: "Testnet faucet: list claimable tokens and claim them to an address.",
		tags: []string{"faucet", "testnet"},
	},
	{
		id:   toolbridge.SkillAgentRegistry,
		name: "SVP-Chain Agent Registry",
		desc: "The chain's x/agent module: query registered agents (by id, operator, owner, " +
			"capability) and build unsigned registration-lifecycle txs — register, update, " +
			"bond deposit/withdraw, deregister — for the owner to sign.",
		tags: []string{"agent-registry", "x/agent", "registration", "bond"},
	},
	{
		id:   toolbridge.SkillDelegation,
		name: "SVP-Chain Delegations",
		desc: "The chain's x/agentwallet module: query root delegations, epochs, and spend " +
			"ledgers, and build unsigned delegation-lifecycle txs — create, update, pause, " +
			"resume, revoke, revoke-token — for the delegator to sign.",
		tags: []string{"delegation", "x/agentwallet", "svp-dt"},
	},
	{
		id:   toolbridge.SkillExecution,
		name: "SVP-Chain Delegated Execution",
		desc: "Place and cancel orders — and deposit the user's own wallet USDC into " +
			"their subaccount — on behalf of a user under an SVP-DT delegation " +
			"credential: the agent verifies the credential chain, wraps the message in " +
			"MsgAgentExecDelegated, signs as the registered operator, and broadcasts. The " +
			"position lands on the user's subaccount, never the agent's; a deposit can " +
			"only move funds from the delegator's wallet into the delegator's subaccount " +
			"and debits the delegation budget. Paid work settles on chain: " +
			"execute_record_spend records this agent's spend against the settlement " +
			"escrow order the credential's settlement caveat names, accruing a " +
			"claimable the operator later withdraws with agent_claim; get_settlement " +
			"reads any order's books without a credential. Requires a delegation " +
			"proof whose leaf token is addressed to this agent, carried in " +
			"message.metadata under " +
			`"svp.delegation/v1" (or, deprecated, as the "proof" args field). ` +
			"Each execute tool nests its parameters under a wrapper key — " +
			`"order" (execute_place_order), "cancel" (execute_cancel_order, ` +
			`execute_batch_cancel), "deposit" (execute_deposit_to_subaccount), ` +
			`"record" (execute_record_spend) — ` +
			"never flat in args; an unknown top-level args key is refused.",
		tags: []string{"execution", "trading", "deposit", "settlement", "delegation", "svp-dt"},
		examples: []string{
			`message.metadata: {"svp.delegation/v1":{"tokens":["<base64 token>", "…"]}} · ` +
				`text: {"skill":"svpchain-execution","tool":"execute_place_order","args":{"order":{"subaccount_number":0,"ticker":"BTC-USD","side":"BUY","size":"0.001","price":"60000","good_til_block":123,"order_client_id":7}}}`,
			`message.metadata: {"svp.delegation/v1":{"tokens":["<base64 token>", "…"]}} · ` +
				`text: {"skill":"svpchain-execution","tool":"execute_deposit_to_subaccount","args":{"deposit":{"subaccount_number":1,"human_usdc":"10"}}}`,
			`message.metadata: {"svp.delegation/v1":{"tokens":["<base64 token>", "…"]}} · ` +
				`text: {"skill":"svpchain-execution","tool":"execute_record_spend","args":{"record":{"amount":{"denom":"uusdc","amount":"500000"}}}}`,
		},
	},
}

// CardIdentity is the per-binary half of the Agent Card: who this agent says
// it is. The skill list still derives from the registry, so a card can only
// advertise operations the binary actually registered.
//
// It carried a SkillDescOverrides map when this was a shared library, so a
// binary serving a deliberate subset of a family could restate that family's
// description. Nothing sets it now — the one identity lives in
// cmd/svpchain-perps-agent/card.go — so it is gone.
type CardIdentity struct {
	Name        string
	Version     string
	Description string
}

// ★ The identity above and skillMetas below both feed bytes that are hashed
// into this agent's on-chain registration. Editing either moves the served
// card, and the deployment must then run agent_self_update or the agent reads
// as unverified. cmd/svpchain-perps-agent/testdata/card.json is the golden that
// makes such a change deliberate.

// BuildAgentCardFor returns the public Agent Card for one binary's identity
// over its registry. The registry supplies each skill's tool list, so a card
// can never advertise an operation the executor would not dispatch.
func BuildAgentCardFor(id CardIdentity, publicURL string, reg *toolbridge.Registry) *a2a.AgentCard {
	bySkill := map[string][]string{}
	if reg != nil {
		bySkill = reg.BySkill()
	}

	var skills []a2a.AgentSkill
	for _, m := range skillMetas {
		tools := bySkill[m.id]
		// A skill this agent registers nothing under is left off its card.
		if len(tools) == 0 {
			continue
		}
		desc := fmt.Sprintf("%s Tools: %s.", m.desc, strings.Join(tools, ", "))
		skills = append(skills, a2a.AgentSkill{
			ID:          m.id,
			Name:        m.name,
			Description: desc,
			Tags:        m.tags,
			Examples:    m.examples,
		})
	}

	return &a2a.AgentCard{
		Name:        id.Name,
		Description: id.Description,
		Version:     id.Version,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json"},
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
			// Required is false because the read layer (market data, and the
			// whole build-and-sign-yourself surface) serves callers with no
			// credential at all; only delegated execution needs one.
			Extensions: []a2a.AgentExtension{{
				URI: DelegationExtensionURI,
				Description: "SVP-DT delegated execution and read-only account queries: attach " +
					"the credential chain as message.metadata[\"" + DelegationMetadataKey + "\"] = " +
					"{\"tokens\": [<base64 canonical-CBOR>, …]}, root-issued token first, leaf " +
					"addressed to this agent. Execution needs the matching execute action — " +
					"execute_place_order: \"clob.place_order\" (budget required), " +
					"execute_cancel_order: \"clob.cancel_order\", " +
					"execute_batch_cancel: \"clob.batch_cancel\", " +
					"execute_deposit_to_subaccount: \"sending.deposit_to_subaccount\" " +
					"(budget required); " +
					"owner-scoped account reads (all but the id-only get_order and whoami) " +
					"need the \"query.account\" action (no budget).",
				Params: map[string]any{"metadataKey": DelegationMetadataKey},
			}},
		},
		Provider: &a2a.AgentProvider{
			Org: "svpchain",
			URL: "https://www.svpchain.org",
		},
		Skills: skills,
	}
}
