package chain

import (
	"fmt"
	"regexp"
)

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
