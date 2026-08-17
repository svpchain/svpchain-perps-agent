// Package mcp is the root of this repo's copy of svpchain-mcp's lib/mcp: the
// MCP tool handlers the A2A tool bridge dispatches into, plus the chain
// clients, tx builders, indexer client, policy engine and auth stores they
// stand on.
//
// ★ This subtree was absorbed from github.com/svpchain/svpchain-mcp at tag
// v0.1.0 (commit a9ef41f), which used to be a module dependency of this repo.
// It follows svpchain-agent-core, absorbed into internal/ by df98513 for the
// same reason: this binary is meant to build from its own source plus tagged
// third-party modules, not from a sibling checkout's moving library.
//
// Unlike agent-core, svpchain-mcp is NOT retired — it still ships
// cmd/mcp-server, and svpchain-evm-agent, svpchain-lending-agent,
// svpchain-research-agent and svpchain-agent-core all still require it. So
// this is a fork, and upstream fixes no longer arrive on their own. That is
// the cost of the absorption; the mitigation is that everything here is kept
// diffable against the tag (see "Re-syncing" below).
//
// # Pruned to the perps surface
//
// Only what internal/wire's PerpsProfile registers came across. Dropped
// wholesale: the lendora/ and bridge/ packages; the lendora, swap, ERC-20/721,
// EVM and bridge tool families and their builders; chain's EVM JSON-RPC client;
// builder's oracle/uniswap ABI layers; and tools/registry.go, which was
// Register(srv *mcp.Server, …) — the MCP-server registration path this binary
// does not use, since internal/toolbridge bridges these same handlers onto A2A
// operations instead.
//
// The faucet/ package and tools/faucet.go came across and were dropped later,
// once removed from the surface entirely: what it dispenses is EVM-side and
// testnet-only, and a credential can never reach it — a claim moves nothing of
// the delegator's, so there is no action to narrow and no budget to debit.
// Funding lives in svpchain-evm-agent. That removal changed the served card and
// therefore the on-chain capability hash, which is a cost the next paragraph is
// specifically about avoiding; it was paid deliberately, alongside a public_url
// change that already required agent_self_update everywhere.
//
// Two behaviours changed shape but not observable result, because both were
// already gated on an EVM client internal/config has no way to configure (the
// [evm] section went away with df98513):
//
//   - get_balance no longer merges contract-read ERC-20 balances. That path
//     short-circuited on a nil EVM client, so it only ever returned nil.
//   - get_oracle_price is now a literal refusal rather than a computed one. It
//     stays registered and advertised: the served agent card's sha256 is
//     published on chain by agent_self_register, so dropping a tool would force
//     an agent_self_update on every deployment.
//
// # Files that are not verbatim
//
// Everything here is byte-identical to the tag except its import paths, apart
// from: tools/handlers.go (symbols rescued from deleted files, marked at their
// definition), tools/tokens.go (the same, minus the three the faucet was the
// last caller of — knownTokenSymbol, parseSwapToken, ownerEthAddress — pruned
// when it went), tools/{account,market,deps}.go (the prunes above),
// payload/payload.go and signer/signer.go (the EVM half of the signer wire
// contract, which only the dropped tool families spoke), and the comments —
// package docs that described a server this binary is not, and
// cross-references that named the old lib/mcp path.
//
// The tree is also gofmt-clean, which upstream's is not (18 files differ there,
// mostly import ordering and struct-tag alignment). That is deliberate: this
// repo's `make fmt` runs gofmt -w over everything, so leaving them unformatted
// would mean the next fmt run silently rewrote a third of the subtree.
//
// # Re-syncing
//
// To compare a file against its upstream original:
//
//	git -C ../svpchain-mcp show v0.1.0:lib/mcp/<pkg>/<file>.go | gofmt | diff - internal/mcp/<pkg>/<file>.go
//
// The only expected hunks are the import block and the exceptions listed above.
// internal/wire mirrors upstream's cmd/mcp-server wiring by hand; drift between
// the two is a bug in whichever copied last.
//
// # One hazard worth naming
//
// auth/recover.go, mcpcodec/codec.go and signer/signer.go each have an init()
// that calls appconfig.SetAddressPrefixes(), setting the svp bech32 prefix
// process-wide. All three are retained, so it fires. A future prune that drops
// every one of them from a binary's import graph would silently change every
// sdk.AccAddress string in that binary.
package mcp
