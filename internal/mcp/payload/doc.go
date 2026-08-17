// Package payload defines the on-wire envelope exchanged between this agent
// (which builds unsigned txs and broadcasts signed ones) and the caller's
// local signer (which signs).
//
// The EVM envelope (EVMTxPayload / EVMSignedTx) was dropped in the absorption
// along with the tool families that produced it — see internal/mcp/doc.go.
// What remains is the Cosmos path.
//
// TxPayload is the build_*-tool return type; SignedTx is the broadcast_signed_tx
// input. sign_bytes.go produces SIGN_MODE_DIRECT sign-bytes from TxBody +
// AuthInfo + chain_id + account_number — the same path the chain itself uses
// (mirrors cmd/dex-bench/cosmos_signing.go:32-88), minus the actual signing
// step (which happens on the client side).
//
// This package is intentionally I/O-free so the future local-signer binary
// can import it too without pulling in chain or HTTP dependencies.
package payload
