// Package limits enforces per-tool size caps and per-tenant daily withdraw
// limits for the MCP server's funds tools.
//
// Two enforcement points share this package:
//
//   - build_* tool handlers, which call Enforce before assembling a tx so
//     the caller gets a structured rejection without burning a sign. This is
//     advisory: it reads the daily headroom without claiming any of it.
//   - broadcast_signed_tx, which decodes the tx, finds any funds messages,
//     and calls EnforceReserve — guards against a caller that hand-crafts an
//     unsigned tx to bypass the build_* checks, and is the point where the
//     daily headroom is actually claimed. Reserving rather than re-reading is
//     what stops concurrent broadcasts from a single tenant clearing the same
//     headroom and collectively blowing past the cap.
//
// Internally the package operates in USDC quantums (uint64); operator config
// is expressed in whole human USDC and converted to quantums at check time.
// That keeps the API precise (no ceiling-rounding bugs) while keeping the
// TOML readable.
//
// Cap state is in-memory (MemoryLedger). Replacing it with a durable backend
// (postgres) only requires implementing the WithdrawLedger interface — no
// handler-side changes.
package limits

import (
	"fmt"
	"sync"
	"time"
)

// Config holds the operator-configured caps in whole human USDC. A zero
// value disables that cap (treated as +∞) — useful for dev/test configs.
type Config struct {
	DepositMaxUSDC       uint64
	WithdrawMaxUSDC      uint64
	TransferMaxUSDC      uint64
	DailyWithdrawCapUSDC uint64
}

// Tool names recognised by CheckPerTx and Enforce. Kept as constants so
// typos surface at compile time and the per-tool cap lookup stays explicit.
const (
	ToolDeposit  = "deposit"
	ToolWithdraw = "withdraw"
	ToolTransfer = "transfer"
)

// ErrCapExceeded is the typed error returned when a per-tx or daily cap
// rejects an operation. Tool handlers surface this so the caller sees which
// cap fired and what the headroom was, not an opaque "rejected" string.
//
// All fields are reported in human USDC (with up-to-6 decimals) so the
// error message matches operator intuition.
type ErrCapExceeded struct {
	Kind      string // "per_tx" or "daily"
	Tool      string // ToolDeposit / ToolWithdraw / ToolTransfer
	Limit     string // configured cap, e.g. "5000.000000"
	Requested string // amount the caller asked for, e.g. "100.500000"
	Used      string // (daily only) amount already spent today
}

func (e *ErrCapExceeded) Error() string {
	if e.Kind == "daily" {
		return fmt.Sprintf(
			"daily_withdraw_cap exceeded: requested %s USDC + used %s USDC > limit %s USDC",
			e.Requested, e.Used, e.Limit,
		)
	}
	return fmt.Sprintf(
		"%s_max_usdc exceeded: requested %s USDC > limit %s USDC",
		e.Tool, e.Requested, e.Limit,
	)
}

// CheckPerTx rejects a single-operation amount against the per-tool cap.
// Inputs are USDC quantums (uint64 atomic units); use HumanToQuantums to
// convert. A zero limit disables the check.
func CheckPerTx(cfg Config, tool string, quantums uint64) error {
	capQ, ok := perToolCapQuantums(cfg, tool)
	if !ok {
		return nil
	}
	if quantums > capQ {
		return &ErrCapExceeded{
			Kind:      "per_tx",
			Tool:      tool,
			Limit:     QuantumsToHuman(capQ),
			Requested: QuantumsToHuman(quantums),
		}
	}
	return nil
}

// perToolCapQuantums returns the per-tool cap as quantums plus a flag
// indicating whether the check is enabled. Zero-config = disabled; an
// overflow on the multiply (cap so large it doesn't fit in uint64) is also
// treated as disabled — operators don't get to set "≈ +∞" by accident.
func perToolCapQuantums(cfg Config, tool string) (uint64, bool) {
	var capUSDC uint64
	switch tool {
	case ToolDeposit:
		capUSDC = cfg.DepositMaxUSDC
	case ToolWithdraw:
		capUSDC = cfg.WithdrawMaxUSDC
	case ToolTransfer:
		capUSDC = cfg.TransferMaxUSDC
	default:
		return 0, false
	}
	if capUSDC == 0 {
		return 0, false
	}
	if capUSDC > ^uint64(0)/quantumsPerUSDC {
		return 0, false // overflow guard
	}
	return capUSDC * quantumsPerUSDC, true
}

// dailyCapQuantums is perToolCapQuantums's twin for the daily withdraw cap.
func dailyCapQuantums(cfg Config) (uint64, bool) {
	if cfg.DailyWithdrawCapUSDC == 0 {
		return 0, false
	}
	if cfg.DailyWithdrawCapUSDC > ^uint64(0)/quantumsPerUSDC {
		return 0, false
	}
	return cfg.DailyWithdrawCapUSDC * quantumsPerUSDC, true
}

// WithdrawLedger tracks how much a tenant has withdrawn in the current UTC
// day. Implementations must be safe for concurrent use. All amounts are in
// USDC quantums.
//
// The gate is Reserve, not Remaining: reading the headroom and then spending
// it in two steps lets N concurrent withdraws from one tenant all observe the
// same headroom and each pass, overshooting the cap by up to N×. Reserve
// checks and commits under one lock, so only the requests that actually fit
// succeed. A caller that does not land its tx calls Release to hand the
// headroom back, which preserves the property that a rejected broadcast does
// not eat the tenant's cap.
//
// Remaining survives as an advisory read for the build_* path, where a
// stale answer only costs the caller a wasted sign. It must never be used
// as the enforcement check.
type WithdrawLedger interface {
	// Remaining returns headroom under DailyWithdrawCapUSDC for the tenant,
	// in quantums. Advisory only — see the note above.
	Remaining(tenantID string) uint64
	// Reserve atomically commits quantums against the tenant's headroom. It
	// reports the headroom it observed under the lock, and whether the
	// reservation was granted; on ok == false nothing is committed.
	Reserve(tenantID string, quantums uint64) (remaining uint64, ok bool)
	// Release returns a previously reserved amount to the tenant's headroom.
	// Called when the reserved tx does not reach the chain.
	Release(tenantID string, quantums uint64)
}

// Enforce runs the per-tx cap plus an advisory read of the daily withdraw
// headroom. It is the build_* entry point: a pass here is not a claim on the
// cap, and the amount can be gone by the time the tx is broadcast. The
// broadcast path must call EnforceReserve instead — see the WithdrawLedger
// doc for why a read-then-spend check cannot hold the cap.
//
// The daily check only fires for ToolWithdraw and only when the ledger and
// cap are non-nil/non-zero. quantums is the requested amount in USDC atomic
// units.
func Enforce(cfg Config, ledger WithdrawLedger, tenantID, tool string, quantums uint64) error {
	if err := CheckPerTx(cfg, tool, quantums); err != nil {
		return err
	}
	dailyCapQ, ok := dailyWithdrawGate(cfg, ledger, tool)
	if !ok {
		return nil
	}
	remaining := ledger.Remaining(tenantID)
	if quantums > remaining {
		return dailyExceeded(tool, dailyCapQ, remaining, quantums)
	}
	return nil
}

// EnforceReserve is Enforce for the broadcast path: it runs the per-tx cap and
// then atomically reserves the daily headroom, so concurrent withdraws from
// one tenant cannot all pass the same check and collectively overshoot the
// cap.
//
// It always returns a non-nil release func. Call it exactly once if the tx
// does not reach the chain — that hands the headroom back, keeping a rejected
// broadcast from eating the tenant's cap. Do not call it once the chain has
// accepted the tx. When no reservation was taken (non-withdraw tool, nil
// ledger, cap disabled, or an error) release is a no-op, so callers need no
// special-casing.
func EnforceReserve(cfg Config, ledger WithdrawLedger, tenantID, tool string, quantums uint64) (release func(), err error) {
	noop := func() {}
	if err := CheckPerTx(cfg, tool, quantums); err != nil {
		return noop, err
	}
	dailyCapQ, ok := dailyWithdrawGate(cfg, ledger, tool)
	if !ok {
		return noop, nil
	}
	remaining, ok := ledger.Reserve(tenantID, quantums)
	if !ok {
		return noop, dailyExceeded(tool, dailyCapQ, remaining, quantums)
	}
	return func() { ledger.Release(tenantID, quantums) }, nil
}

// dailyWithdrawGate reports the daily cap in quantums and whether the daily
// check applies at all for this tool/ledger/config combination.
func dailyWithdrawGate(cfg Config, ledger WithdrawLedger, tool string) (uint64, bool) {
	if tool != ToolWithdraw || ledger == nil {
		return 0, false
	}
	return dailyCapQuantums(cfg)
}

// dailyExceeded builds the typed daily-cap rejection from the headroom the
// ledger reported.
func dailyExceeded(tool string, dailyCapQ, remaining, quantums uint64) error {
	usedQ := uint64(0)
	if dailyCapQ > remaining {
		usedQ = dailyCapQ - remaining
	}
	return &ErrCapExceeded{
		Kind:      "daily",
		Tool:      tool,
		Limit:     QuantumsToHuman(dailyCapQ),
		Requested: QuantumsToHuman(quantums),
		Used:      QuantumsToHuman(usedQ),
	}
}

// MemoryLedger is the default in-process WithdrawLedger. State resets on
// restart — acceptable for the current single-instance deployment, and the
// known limitation is documented in the package doc. Internal accounting
// is in quantums.
type MemoryLedger struct {
	capQuantums uint64 // DailyWithdrawCapUSDC × 10^6, captured at construction
	now         func() time.Time

	mu   sync.Mutex
	day  string            // UTC date "2006-01-02" of `used`'s validity
	used map[string]uint64 // tenant_id → quantums spent today
}

// NewMemoryLedger constructs a ledger pinned to the supplied daily cap
// (whole human USDC). Pass time.Now to use wall-clock time; tests inject a
// fake clock. A zero dailyCapUSDC creates a ledger whose Remaining always
// returns 0 (effectively unusable) — production callers should gate on
// `cfg.DailyWithdrawCapUSDC > 0` before constructing.
func NewMemoryLedger(dailyCapUSDC uint64, now func() time.Time) *MemoryLedger {
	if now == nil {
		now = time.Now
	}
	var capQ uint64
	if dailyCapUSDC > 0 && dailyCapUSDC <= ^uint64(0)/quantumsPerUSDC {
		capQ = dailyCapUSDC * quantumsPerUSDC
	}
	return &MemoryLedger{
		capQuantums: capQ,
		now:         now,
		used:        map[string]uint64{},
	}
}

// Remaining returns the tenant's headroom for the current UTC day (quantums),
// rolling the ledger over if the day has changed since the last call.
func (l *MemoryLedger) Remaining(tenantID string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rolloverLocked()
	used := l.used[tenantID]
	if used >= l.capQuantums {
		return 0
	}
	return l.capQuantums - used
}

// Reserve checks the tenant's headroom and commits the spend under a single
// hold of the mutex, so two concurrent callers cannot both be granted the
// same quantums. Overflow on the per-tenant counter is clamped to MaxUint64 —
// unreachable given the headroom check, but keeps the arithmetic
// well-defined.
func (l *MemoryLedger) Reserve(tenantID string, quantums uint64) (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rolloverLocked()
	cur := l.used[tenantID]
	remaining := uint64(0)
	if cur < l.capQuantums {
		remaining = l.capQuantums - cur
	}
	if quantums > remaining {
		return remaining, false
	}
	sum := cur + quantums
	if sum < cur { // overflow
		sum = ^uint64(0)
	}
	l.used[tenantID] = sum
	return remaining, true
}

// Release returns quantums to the tenant's headroom, clamping at zero so a
// release that outlives a day rollover cannot underflow the counter.
func (l *MemoryLedger) Release(tenantID string, quantums uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rolloverLocked()
	cur := l.used[tenantID]
	if quantums >= cur {
		delete(l.used, tenantID)
		return
	}
	l.used[tenantID] = cur - quantums
}

func (l *MemoryLedger) rolloverLocked() {
	today := l.now().UTC().Format("2006-01-02")
	if l.day == today {
		return
	}
	l.day = today
	// Reset all tenants — preserving the map allocation is cheap and avoids
	// reallocating on every rollover.
	for k := range l.used {
		delete(l.used, k)
	}
}
