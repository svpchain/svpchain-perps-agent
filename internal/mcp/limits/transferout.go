package limits

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
)

// This file implements the per-symbol daily "transfer out" cap (svp / usdc /
// usdv). Funds leave a wallet through two rails — x/bank sends and EVM
// transfers — and one symbol (usdc is both an x/bank denom and an ERC-20) can
// leave through either, so caps and usage are keyed by symbol and sum across
// both rails. Caps and usage are keyed by the owner wallet address (not the
// per-auth tenant id), so all of a wallet's concurrent agents / re-auths share
// one cap and one daily total. Caps are set at runtime (set_transfer_out_cap);
// there is no operator config. Usage is metered per UTC day and resets at
// midnight; caps persist until changed.
//
// The package stays a dependency-free leaf: amounts are math/big base units,
// no SDK / go-ethereum imports. The symbol<->rail registry lives in the tools
// layer, which converts denoms / token addresses to a symbol before calling in.

// SymbolCap is one token's daily cap: the limit in base units plus the symbol
// and decimals needed to render human error / display strings.
type SymbolCap struct {
	Symbol   string
	Decimals int64
	CapBase  *big.Int
}

// ErrSymbolCapExceeded is the typed error returned when a transfer-out would
// exceed the cap. Amounts are rendered human (decimal-adjusted, trailing zeros
// trimmed) so the message matches the operator/agent's intuition.
type ErrSymbolCapExceeded struct {
	Symbol    string
	Limit     string
	Requested string
	Used      string
}

func (e *ErrSymbolCapExceeded) Error() string {
	return fmt.Sprintf(
		"daily_transfer_out_cap exceeded for %s: requested %s + used %s > limit %s %s",
		e.Symbol, e.Requested, e.Used, e.Limit, strings.ToUpper(e.Symbol),
	)
}

// MemoryTransferOutStore holds each owner wallet's per-symbol daily caps and
// usage in one place. Caps persist until the owner changes them; the usage
// tally resets at UTC midnight. Safe for concurrent use. A nil
// *MemoryTransferOutStore is usable and treats everything as uncapped — the
// methods short-circuit.
//
// When constructed with a non-empty path (LoadMemoryTransferOutStore), the
// full state — caps, today's usage, and the day key — is written through to a
// JSON file after every mutation and reloaded on the next boot, so neither the
// configured caps nor the running daily total reset on restart. Without a path
// (NewMemoryTransferOutStore) the store is in-memory only and resets on
// restart, exactly as before.
type MemoryTransferOutStore struct {
	now   func() time.Time
	path  string      // JSON write-through file; "" disables persistence
	onErr func(error) // optional sink for write/marshal errors; nil swallows

	mu   sync.Mutex
	day  string                          // UTC date the `used` map is valid for
	caps map[string]map[string]SymbolCap // owner -> symbol -> finite cap
	used map[string]map[string]*big.Int  // owner -> symbol -> base units today
}

// NewMemoryTransferOutStore returns an empty, non-persistent store (every
// symbol uncapped until an owner sets a cap; state resets on restart). Pass
// time.Now for wall-clock; tests inject a fake clock.
func NewMemoryTransferOutStore(now func() time.Time) *MemoryTransferOutStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryTransferOutStore{
		now:  now,
		caps: map[string]map[string]SymbolCap{},
		used: map[string]map[string]*big.Int{},
	}
}

// persistedSymbolCap is the on-disk form of SymbolCap: the base-unit cap is a
// decimal string because JSON has no big-integer type.
type persistedSymbolCap struct {
	Symbol   string `json:"symbol"`
	Decimals int64  `json:"decimals"`
	CapBase  string `json:"cap_base"`
}

// persistedTransferOut is the full on-disk snapshot of the store. Usage amounts
// are decimal strings for the same reason as CapBase.
type persistedTransferOut struct {
	Day  string                                   `json:"day"`
	Caps map[string]map[string]persistedSymbolCap `json:"caps"`
	Used map[string]map[string]string             `json:"used"`
}

// LoadMemoryTransferOutStore returns a store that writes its full state through
// to the JSON file at path after every mutation and rehydrates from it here.
// An empty path yields a non-persistent store (identical to
// NewMemoryTransferOutStore). A missing file is not an error — the store starts
// empty and the file is created on the first write. A present-but-unparseable
// file is an error, so a corrupt or hand-edited file fails startup loudly
// rather than silently dropping every cap. onErr (may be nil) receives any
// write/marshal error encountered after startup; the in-memory state stays
// authoritative and the next successful write reconciles the file.
func LoadMemoryTransferOutStore(path string, now func() time.Time, onErr func(error)) (*MemoryTransferOutStore, error) {
	s := NewMemoryTransferOutStore(now)
	s.path = path
	s.onErr = onErr
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read transfer-out cap file %s: %w", path, err)
	}
	var snap persistedTransferOut
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("parse transfer-out cap file %s: %w", path, err)
	}
	s.day = snap.Day
	for owner, bySym := range snap.Caps {
		for sym, pc := range bySym {
			capBase, ok := new(big.Int).SetString(pc.CapBase, 10)
			if !ok {
				return nil, fmt.Errorf("transfer-out cap file %s: owner %s symbol %s: bad cap_base %q", path, owner, sym, pc.CapBase)
			}
			if s.caps[owner] == nil {
				s.caps[owner] = map[string]SymbolCap{}
			}
			s.caps[owner][sym] = SymbolCap{Symbol: pc.Symbol, Decimals: pc.Decimals, CapBase: capBase}
		}
	}
	for owner, bySym := range snap.Used {
		for sym, amt := range bySym {
			v, ok := new(big.Int).SetString(amt, 10)
			if !ok {
				return nil, fmt.Errorf("transfer-out cap file %s: owner %s symbol %s: bad used amount %q", path, owner, sym, amt)
			}
			if s.used[owner] == nil {
				s.used[owner] = map[string]*big.Int{}
			}
			s.used[owner][sym] = v
		}
	}
	return s, nil
}

// persistLocked writes the full store state through to s.path atomically (temp
// file + rename). A no-op when persistence is disabled. Errors are routed to
// s.onErr rather than returned, so a write failure never aborts the transfer
// whose Record triggered it — the broadcast already happened. Must be called
// with s.mu held.
func (s *MemoryTransferOutStore) persistLocked() {
	if s.path == "" {
		return
	}
	snap := persistedTransferOut{
		Day:  s.day,
		Caps: map[string]map[string]persistedSymbolCap{},
		Used: map[string]map[string]string{},
	}
	for owner, bySym := range s.caps {
		if len(bySym) == 0 {
			continue
		}
		snap.Caps[owner] = map[string]persistedSymbolCap{}
		for sym, c := range bySym {
			capStr := "0"
			if c.CapBase != nil {
				capStr = c.CapBase.String()
			}
			snap.Caps[owner][sym] = persistedSymbolCap{Symbol: c.Symbol, Decimals: c.Decimals, CapBase: capStr}
		}
	}
	for owner, bySym := range s.used {
		if len(bySym) == 0 {
			continue
		}
		snap.Used[owner] = map[string]string{}
		for sym, v := range bySym {
			if v == nil {
				continue
			}
			snap.Used[owner][sym] = v.String()
		}
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		s.reportErr(fmt.Errorf("marshal transfer-out cap state: %w", err))
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		s.reportErr(fmt.Errorf("write transfer-out cap file %s: %w", tmp, err))
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		s.reportErr(fmt.Errorf("rename transfer-out cap file into place %s: %w", s.path, err))
	}
}

func (s *MemoryTransferOutStore) reportErr(err error) {
	if s.onErr != nil {
		s.onErr(err)
	}
}

// SetCap sets a finite daily cap for the owner's symbol (keyed by c.Symbol).
func (s *MemoryTransferOutStore) SetCap(owner string, c SymbolCap) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bySym := s.caps[owner]
	if bySym == nil {
		bySym = map[string]SymbolCap{}
		s.caps[owner] = bySym
	}
	bySym[c.Symbol] = c
	s.persistLocked()
}

// SetUnlimited removes any cap for the owner's symbol (uncapped).
func (s *MemoryTransferOutStore) SetUnlimited(owner, symbol string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.caps[owner], symbol)
	s.persistLocked()
}

// Cap returns the owner's finite cap for the symbol, if one is set.
func (s *MemoryTransferOutStore) Cap(owner, symbol string) (SymbolCap, bool) {
	if s == nil {
		return SymbolCap{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.caps[owner][symbol]
	return c, ok
}

// Used returns a copy of the base units the owner has transferred out of the
// symbol so far in the current UTC day.
func (s *MemoryTransferOutStore) Used(owner, symbol string) *big.Int {
	if s == nil {
		return big.NewInt(0)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked()
	if v := s.used[owner][symbol]; v != nil {
		return new(big.Int).Set(v)
	}
	return big.NewInt(0)
}

// Record adds to today's usage without checking the cap. Broadcast paths use
// Reserve (which checks and records atomically) — Record remains for callers
// that are seeding or reconciling a known-good total. Non-positive amounts
// are ignored.
func (s *MemoryTransferOutStore) Record(owner, symbol string, amt *big.Int) {
	if s == nil || amt == nil || amt.Sign() <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked()
	s.recordLocked(owner, symbol, amt)
}

// recordLocked is Record's body; the caller holds the mutex and has rolled over.
func (s *MemoryTransferOutStore) recordLocked(owner, symbol string, amt *big.Int) {
	bySym := s.used[owner]
	if bySym == nil {
		bySym = map[string]*big.Int{}
		s.used[owner] = bySym
	}
	cur := bySym[symbol]
	if cur == nil {
		cur = big.NewInt(0)
	}
	bySym[symbol] = new(big.Int).Add(cur, amt)
	s.persistLocked()
}

// Check rejects a transfer-out of amt base units of symbol when it would push
// the owner's running daily total past the cap. Uncapped symbols and
// non-positive amounts pass; a nil store passes (feature inert).
//
// Check is advisory: it reads the total without claiming any of it, so a pass
// here does not hold the cap against a concurrent transfer. Broadcast paths
// must gate on Reserve instead.
func (s *MemoryTransferOutStore) Check(owner, symbol string, amt *big.Int) error {
	if s == nil || amt == nil || amt.Sign() <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked()
	return s.checkLocked(owner, symbol, amt)
}

// Reserve is Check plus the matching Record, applied under a single hold of
// the mutex: on success the amount is already counted against the owner's
// daily total, so concurrent transfers of the same symbol cannot both clear a
// cap that only fits one of them. Call Release if the tx does not reach the
// chain. Uncapped symbols still record usage (as Record does), so a cap set
// later in the day sees the day's real total.
func (s *MemoryTransferOutStore) Reserve(owner, symbol string, amt *big.Int) error {
	if s == nil || amt == nil || amt.Sign() <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked()
	if err := s.checkLocked(owner, symbol, amt); err != nil {
		return err
	}
	s.recordLocked(owner, symbol, amt)
	return nil
}

// Release subtracts a previously reserved amount from today's usage, clamping
// at zero so a release that lands after a day rollover cannot drive the total
// negative.
func (s *MemoryTransferOutStore) Release(owner, symbol string, amt *big.Int) {
	if s == nil || amt == nil || amt.Sign() <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked()
	cur := s.used[owner][symbol]
	if cur == nil {
		return
	}
	next := new(big.Int).Sub(cur, amt)
	if next.Sign() < 0 {
		next = big.NewInt(0)
	}
	s.used[owner][symbol] = next
	s.persistLocked()
}

// checkLocked is Check's body; the caller holds the mutex and has rolled over.
func (s *MemoryTransferOutStore) checkLocked(owner, symbol string, amt *big.Int) error {
	c, ok := s.caps[owner][symbol]
	if !ok || c.CapBase == nil {
		return nil
	}
	used := big.NewInt(0)
	if v := s.used[owner][symbol]; v != nil {
		used = v
	}
	if new(big.Int).Add(used, amt).Cmp(c.CapBase) > 0 {
		return &ErrSymbolCapExceeded{
			Symbol:    c.Symbol,
			Limit:     baseToHuman(c.CapBase, c.Decimals),
			Requested: baseToHuman(amt, c.Decimals),
			Used:      baseToHuman(used, c.Decimals),
		}
	}
	return nil
}

func (s *MemoryTransferOutStore) rolloverLocked() {
	today := s.now().UTC().Format("2006-01-02")
	if s.day == today {
		return
	}
	s.day = today
	for k := range s.used {
		delete(s.used, k)
	}
	// Make the midnight reset durable: a restart later in the new day must not
	// resurrect yesterday's usage from the file. A read path (Used / Check) can
	// trigger this, so the write lives here rather than only in the mutators.
	s.persistLocked()
}

// baseToHuman renders a base-unit integer as a decimal string with `decimals`
// fractional places, trailing zeros trimmed: baseToHuman(1_500_000, 6) -> "1.5".
func baseToHuman(base *big.Int, decimals int64) string {
	if base == nil {
		return "0"
	}
	if decimals <= 0 {
		return base.String()
	}
	neg := base.Sign() < 0
	abs := new(big.Int).Abs(base)
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(decimals), nil)
	whole := new(big.Int)
	frac := new(big.Int)
	whole.QuoRem(abs, divisor, frac)

	out := whole.String()
	if frac.Sign() > 0 {
		fracStr := frac.String()
		for int64(len(fracStr)) < decimals {
			fracStr = "0" + fracStr
		}
		fracStr = strings.TrimRight(fracStr, "0")
		out = out + "." + fracStr
	}
	if neg {
		out = "-" + out
	}
	return out
}
