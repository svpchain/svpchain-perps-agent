// chainapi.go holds the small chain-facing surface a registration needs: what
// an account query returns, what a broadcast returns, and how to read a
// CheckTx rejection.
//
// ★ These came from internal/mcp/chain, the vendored copy of the MCP server's
// chain clients. That package also carried gRPC implementations of both
// interfaces; removing the gRPC registration route left them with no caller,
// and the four types below were all that was still reachable. Keeping a
// package for four types and a regex was worse than moving them here, so the
// last chain-side package in internal/mcp is gone.
package agentchain

import (
	"context"
	"fmt"
	"regexp"
)

// AccountInfo is what signing a transaction needs from an auth query:
// account_number is constant for the life of the account, sequence is the
// next nonce.
type AccountInfo struct {
	AccountNumber uint64
	Sequence      uint64
}

// AccountClient reads an account's number and sequence.
type AccountClient interface {
	Account(ctx context.Context, address string) (AccountInfo, error)
}

// BroadcastResult is the chain's response to a synchronous broadcast. Code 0
// means accepted into the mempool; non-zero is a CheckTx rejection, with
// RawLog explaining why.
type BroadcastResult struct {
	TxHash string
	Code   uint32
	RawLog string
}

// BroadcastClient submits pre-signed transaction bytes.
type BroadcastClient interface {
	BroadcastSync(ctx context.Context, txBytes []byte) (BroadcastResult, error)
}

// ErrInsufficientFee carries the structured fee shortfall extracted from a
// cosmos-sdk MempoolFeeDecorator rejection. Tool handlers surface this so
// callers see the chain's suggested min-gas-price instead of an opaque
// Code != 0 — the MCP server itself never retries; the caller decides.
type ErrInsufficientFee struct {
	// Coin strings as the chain reports them, e.g. "1000stake".
	// Kept as strings rather than sdk.Coin so the parser stays independent of
	// the SDK denom registry — callers that need numeric comparison can
	// re-parse with sdk.ParseCoinNormalized.
	Required string
	Got      string
}

func (e *ErrInsufficientFee) Error() string {
	return fmt.Sprintf("insufficient fee: got %s, required %s", e.Got, e.Required)
}

// cosmos-sdk x/auth/ante/fee.go (MempoolFeeDecorator) wraps the sentinel
// ErrInsufficientFee with the format:
//
//	insufficient fees; got: <coin> required: <coin>: insufficient fee
//
// The trailing `: insufficient fee` is the wrapped sentinel's own string; we
// stop the `required` capture at it (or at end-of-string) so we don't grab
// the sentinel by accident.
var insufficientFeeRE = regexp.MustCompile(
	`insufficient fees?;\s*got:\s*(\S+)\s+required:\s*(\S+?)(?:\s*:\s*insufficient fee|$)`,
)

// ParseBroadcastError inspects a non-zero-Code BroadcastResult and returns a
// typed error when the RawLog matches a known chain rejection pattern.
// Returns nil on Code == 0 (success) and on unrecognised RawLog (caller
// should fall back to surfacing Code + RawLog verbatim).
func ParseBroadcastError(r BroadcastResult) error {
	if r.Code == 0 {
		return nil
	}
	if m := insufficientFeeRE.FindStringSubmatch(r.RawLog); m != nil {
		return &ErrInsufficientFee{Got: m[1], Required: m[2]}
	}
	return nil
}
