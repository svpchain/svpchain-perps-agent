package payload

import (
	"time"
)

// CurrentVersion is the only supported wire version of TxPayload / SignedTx
// in v0.1. Bumped if the on-wire shape changes incompatibly.
const CurrentVersion = 1

// TxPayload is the on-wire envelope returned by every build_* MCP tool and
// later supplied (alongside SignedTx) to broadcast_signed_tx.
//
// The remote MCP server fills every field except the signature; the local
// signer signs the precomputed SignBytes (SIGN_MODE_DIRECT) and round-trips
// the whole TxPayload back inside a BroadcastSignedTxArgs so the server can
// re-verify the bytes match before broadcasting.
//
// Cosmos uint64 fields are JSON-encoded as strings (matching standard
// Cosmos SDK JSON conventions) so JS-based MCP clients do not lose
// precision.
type TxPayload struct {
	Version int `json:"version"`

	// ClientID is the broadcast-idempotency key (uuid). Distinct from the
	// per-order Order.ClientId (uint32) carried inside Summary, which goes
	// on-chain — see protocol/x/clob/types/order_id.go:24.
	ClientID string `json:"client_id"`

	ChainID         string `json:"chain_id"`
	SignerAddress   string `json:"signer_address"`
	AccountNumber   string `json:"account_number"`
	Sequence        string `json:"sequence"`
	IsShortTermCLOB bool   `json:"is_short_term_clob"`

	// Encoded transaction parts. Standard-base64 strings — the field-name
	// suffix _b64 makes the wire shape explicit. We use `string` rather
	// than `[]byte` because the MCP SDK's reflection-based JSON Schema
	// generator turns []byte into {type:"array", items:{type:"integer"}},
	// which doesn't match what encoding/json actually emits for []byte
	// (a base64 string) — the SDK's output validator then rejects it.
	// Callers encode/decode with base64.StdEncoding at the boundary.
	//
	// TxBodyBytesB64 is always set. AuthInfoBytesB64 and SignBytesB64 are
	// pre-computed by the server only when the tenant config carries the
	// signer's public key (v0.2+); when absent (v0.1), the local signer
	// builds AuthInfo and computes sign-bytes itself using the chain id,
	// account number, sequence, and fee carried in this payload.
	TxBodyBytesB64   string `json:"tx_body_bytes_b64"`
	AuthInfoBytesB64 string `json:"auth_info_bytes_b64,omitempty"`
	SignBytesB64     string `json:"sign_bytes_b64,omitempty"`

	Fee     Fee     `json:"fee"`
	Summary Summary `json:"summary"`

	// ExpiresAt is the server-side TTL on payload validity; broadcast_signed_tx
	// rejects payloads past this point so a stale long-pending sign-then-broadcast
	// cycle does not produce surprise on-chain actions.
	ExpiresAt time.Time `json:"expires_at"`
}

// Fee mirrors cosmos.tx.v1beta1.Fee on the wire. Short-term CLOB txs in
// svpchain are gas-free, so their Amount stays empty; all other txs carry the
// configured fee (see builder.Assemble / cmd/mcp-server FeeConfig). GasLimit
// is a comfortable constant — see cmd/dex-bench/cosmos_signing.go:42-43.
type Fee struct {
	GasLimit string `json:"gas_limit"`
	Amount   []Coin `json:"amount"`
}

// Coin is the JSON form of sdk.Coin (amount kept as string to preserve
// uint128/uint64 precision).
type Coin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// SubaccountRef is the JSON form of subaccounts.SubaccountId
// (proto/dydxprotocol/subaccounts/subaccount.proto:11).
type SubaccountRef struct {
	Owner  string `json:"owner"`
	Number uint32 `json:"number"`
}

// Summary is the human-readable description of a build_* result. The
// remote server fills it from tool args; clients display it for user
// approval. The server is the authority on what was actually signed (via
// TxBodyBytesB64) — Summary is informational only and never re-validated
// at broadcast time except for sanity comparisons.
type Summary struct {
	ToolName   string        `json:"tool_name"`
	MsgTypeURL string        `json:"msg_type_url"`
	Subaccount SubaccountRef `json:"subaccount"`

	// Trading-specific fields (omitted for non-trading tools).
	Ticker        string `json:"ticker,omitempty"`
	Side          string `json:"side,omitempty"`
	SizeHuman     string `json:"size_human,omitempty"`
	PriceHuman    string `json:"price_human,omitempty"`
	NotionalUSD   string `json:"notional_usd,omitempty"`
	GoodTil       string `json:"good_til,omitempty"`
	ReduceOnly    bool   `json:"reduce_only,omitempty"`
	OrderClientID uint32 `json:"order_client_id,omitempty"`

	// Fund-movement fields (v0.2; left here so the shape is stable).
	AssetID        uint32 `json:"asset_id,omitempty"`
	AmountHuman    string `json:"amount_human,omitempty"`
	RecipientOwner string `json:"recipient_owner,omitempty"`
	RecipientNum   uint32 `json:"recipient_subaccount,omitempty"`

	// Denom is the bank denom for a generic bank send (build_bank_send); the
	// subaccount funds tools use AssetID instead.
	Denom string `json:"denom,omitempty"`
}

// SignedTx is what broadcast_signed_tx receives from the MCP client. The
// remote server base64-decodes TxRawBytesB64, decodes the TxRaw proto, and
// verifies the signer address matches the tenant owner before broadcasting.
//
// All three fields are standard-base64 strings. See the comment on
// TxPayload's TxBodyBytesB64 for why we use string rather than []byte.
//
// The jsonschema descriptions instruct the MCP client (an LLM) to relay every
// field byte-for-byte from sign_transaction. These are opaque encoded blobs;
// the long TxRawBytesB64 in particular breaks the broadcast if a single
// character is dropped, re-wrapped, or "normalized" in transit.
type SignedTx struct {
	TxRawBytesB64 string `json:"tx_raw_bytes_b64" jsonschema:"The signed transaction, standard base64. Copy this VERBATIM from sign_transaction's output. Do NOT modify, truncate, re-encode, pretty-print, line-wrap, add or strip whitespace, or otherwise normalize it — pass the exact string through."`
	SignatureB64  string `json:"signature_b64" jsonschema:"The signature, standard base64. Copy this VERBATIM from sign_transaction's output — do not modify, re-encode, or normalize it."`
	PubKeyB64     string `json:"pub_key_b64" jsonschema:"The signer public key, standard base64. Copy this VERBATIM from sign_transaction's output — do not modify, re-encode, or normalize it."`
}

// BroadcastResult is the return shape of broadcast_signed_tx after a
// successful BroadcastSync. Code 0 means accepted into mempool;
// non-zero is a CheckTx reject with RawLog explaining why.
type BroadcastResult struct {
	TxHash string `json:"tx_hash"` // hex
	Code   uint32 `json:"code"`
	RawLog string `json:"raw_log,omitempty"`
}
