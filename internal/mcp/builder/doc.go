// Package builder contains pure functions that turn human-friendly tool
// arguments into canonical sdk.Msgs and TxPayload envelopes. NO I/O happens
// here except market-metadata and account-state lookups passed in by the
// caller — keeping builders unit-testable.
//
// orders.go / cancels.go / funds.go implement the BuildPlace*/BuildCancel*/
// BuildDeposit-Withdraw-Transfer builders. tx.go is the single Assemble
// entry point that sets GasLimit, stamps the configured Fee.Amount on
// non-CLOB txs (short-term CLOB orders stay gas-free / empty), serializes
// TxBody+AuthInfo round-1, precomputes SIGN_MODE_DIRECT sign-bytes, and
// returns a *payload.TxPayload.
//
// seq.go mirrors app/ante.go:331-342: IsShortTermClobMsgTx(msgs) decides
// whether the signer should reuse the current account sequence (short-term
// CLOB skip incrementSequence) or treat it as the starting nonce to increment
// at signing time.
package builder
