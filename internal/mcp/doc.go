// Package mcp is what remains of this repo's copy of svpchain-mcp's lib/mcp:
// the pieces the registration tooling needs to build and sign one transaction.
//
// ★ It was the whole DEX service — tool handlers, chain clients, tx builders,
// an indexer client, a policy engine, limits ledgers and auth stores, some
// 7,000 lines absorbed from github.com/svpchain/svpchain-mcp at tag v0.1.0
// because this binary served every operation itself. It no longer does. The
// A2A surface is now a client of svpchain-dex-mcp, which is the same code
// running where it is maintained, so the fork's whole reason to exist went
// with the dispatch.
//
// Four packages survive, and only cmd/agent-register and internal/owner reach
// them:
//
//   - chain: dialling gRPC, reading an account's number and sequence, and
//     broadcasting. Pruned to that; the clob, perpetuals, subaccount and bank
//     query clients went with the handlers that used them, and the markets
//     cache with them.
//   - mcpcodec: the interface registry, so a registration tx encodes.
//   - signer, payload: signing that tx with the owner key.
//
// Registration is the last thing this repo signs. Everything an A2A caller
// asks for is built by the server and signed by the caller, so if the register
// command ever moves to its own module, this subtree goes with it and the
// agent keeps no chain-side code at all.
//
// # One hazard worth naming
//
// mcpcodec/codec.go and signer/signer.go each have an init() calling
// appconfig.SetAddressPrefixes(), which sets the svp bech32 prefix
// process-wide. Both are retained, so it fires. A future prune dropping both
// from a binary's import graph would silently change every sdk.AccAddress
// string in it.
package mcp
