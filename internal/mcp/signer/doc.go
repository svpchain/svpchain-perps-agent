// Package signer turns a TxPayload produced by the build_* tools into a
// SignedTx ready for broadcast_signed_tx. It owns the eth_secp256k1 +
// SIGN_MODE_DIRECT signing path and the cross-checks (signer address matches
// the loaded key, payload version matches the supported one).
//
// Callers here are internal/operator, which uses ParsePrivKey and
// DeriveAddress to load the operator key, and the tests. Sign itself has no
// non-test caller in this binary — remote callers sign their own payloads via
// svpchain-signer-mcp — but it is retained as the counterpart of
// broadcast_signed_tx's verification path, and signer_test.go is the only
// executable spec of the sign-byte layout the two sides agree on.
//
// SignEVM and the EVM payload types were dropped in the absorption; this agent
// never speaks that half of the wire contract. See internal/mcp/doc.go.
//
// The package's init() sets the svp bech32 prefix so every sdk.AccAddress
// stringification (notably in DeriveAddress and the signer-address
// cross-check) matches the chain. Importing this package is sufficient —
// no caller needs its own blank import of app/config.
package signer
