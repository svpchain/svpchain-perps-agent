package limits

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// USDC quantums shorthand for test readability — n whole USDC × 10^6.
func usdc(n uint64) uint64 { return n * 1_000_000 }

func TestCheckPerTx(t *testing.T) {
	cfg := Config{DepositMaxUSDC: 1000, WithdrawMaxUSDC: 500, TransferMaxUSDC: 2000}

	t.Run("under limit passes", func(t *testing.T) {
		require.NoError(t, CheckPerTx(cfg, ToolDeposit, usdc(999)))
		require.NoError(t, CheckPerTx(cfg, ToolWithdraw, usdc(500))) // boundary OK
	})

	t.Run("over limit rejects with typed error", func(t *testing.T) {
		// 500.000001 USDC = 500_000_001 quantums — just barely over the 500 cap.
		err := CheckPerTx(cfg, ToolWithdraw, usdc(500)+1)
		require.Error(t, err)
		var ce *ErrCapExceeded
		require.True(t, errors.As(err, &ce))
		require.Equal(t, "per_tx", ce.Kind)
		require.Equal(t, ToolWithdraw, ce.Tool)
		require.Equal(t, "500.000000", ce.Limit)
		require.Equal(t, "500.000001", ce.Requested)
	})

	t.Run("zero limit disables check", func(t *testing.T) {
		require.NoError(t, CheckPerTx(Config{}, ToolWithdraw, 1<<40))
	})

	t.Run("unknown tool — no per-tool cap, passes", func(t *testing.T) {
		require.NoError(t, CheckPerTx(cfg, "unknown", 1<<40))
	})
}

func TestEnforce_DailyCap(t *testing.T) {
	cfg := Config{WithdrawMaxUSDC: 10_000, DailyWithdrawCapUSDC: 5_000}
	clk := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	ledger := NewMemoryLedger(cfg.DailyWithdrawCapUSDC, func() time.Time { return clk })

	// 3000 withdraw — OK; ledger still empty so daily check passes.
	require.NoError(t, Enforce(cfg, ledger, "t1", ToolWithdraw, usdc(3_000)))
	mustReserve(t, ledger, "t1", usdc(3_000))

	// 2000 more — exactly at cap, OK.
	require.NoError(t, Enforce(cfg, ledger, "t1", ToolWithdraw, usdc(2_000)))
	mustReserve(t, ledger, "t1", usdc(2_000))

	// 0.000001 USDC over — rejected with daily kind.
	err := Enforce(cfg, ledger, "t1", ToolWithdraw, 1)
	require.Error(t, err)
	var ce *ErrCapExceeded
	require.True(t, errors.As(err, &ce))
	require.Equal(t, "daily", ce.Kind)
	require.Equal(t, "5000.000000", ce.Limit)
	require.Equal(t, "0.000001", ce.Requested)
	require.Equal(t, "5000.000000", ce.Used)

	// Different tenant — independent counter.
	require.NoError(t, Enforce(cfg, ledger, "t2", ToolWithdraw, usdc(4_999)))

	// Deposit isn't capped daily even when ledger is present.
	require.NoError(t, Enforce(cfg, ledger, "t1", ToolDeposit, 1))
}

func TestMemoryLedger_UTCRollover(t *testing.T) {
	cfg := Config{WithdrawMaxUSDC: 10_000, DailyWithdrawCapUSDC: 5_000}
	clk := time.Date(2026, 5, 28, 23, 59, 0, 0, time.UTC)
	ledger := NewMemoryLedger(cfg.DailyWithdrawCapUSDC, func() time.Time { return clk })

	mustReserve(t, ledger, "t1", usdc(5_000))
	require.EqualValues(t, 0, ledger.Remaining("t1"))

	// Cross UTC midnight.
	clk = clk.Add(2 * time.Minute)
	require.Equal(t, usdc(5_000), ledger.Remaining("t1"), "rollover should reset used")
	require.NoError(t, Enforce(cfg, ledger, "t1", ToolWithdraw, usdc(5_000)))
}

// mustReserve takes headroom and fails the test if the ledger refuses it.
func mustReserve(t *testing.T, l *MemoryLedger, tenantID string, quantums uint64) {
	t.Helper()
	_, ok := l.Reserve(tenantID, quantums)
	require.True(t, ok, "expected reserve of %d quantums to be granted", quantums)
}

func TestMemoryLedger_ConcurrentReserveCannotOvershootCap(t *testing.T) {
	// The race this closes: every goroutine asks for the whole daily cap at
	// once. Under a read-then-spend check they all observe full headroom and
	// all pass; Reserve settles it under one lock, so exactly one wins and the
	// tenant's total never exceeds the cap.
	const goroutines = 64
	const capUSDC = 5_000
	ledger := NewMemoryLedger(capUSDC, time.Now)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if _, ok := ledger.Reserve("t1", usdc(capUSDC)); ok {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 1, granted, "only one full-cap withdraw may be granted")
	require.EqualValues(t, 0, ledger.Remaining("t1"))
}

func TestMemoryLedger_Release(t *testing.T) {
	ledger := NewMemoryLedger(5_000, time.Now)

	mustReserve(t, ledger, "t1", usdc(4_000))
	require.Equal(t, usdc(1_000), ledger.Remaining("t1"))

	// A tx that never landed hands its headroom back.
	ledger.Release("t1", usdc(4_000))
	require.Equal(t, usdc(5_000), ledger.Remaining("t1"))

	// Releasing more than is held clamps at the full cap rather than
	// underflowing the unsigned counter.
	ledger.Release("t1", usdc(9_000))
	require.Equal(t, usdc(5_000), ledger.Remaining("t1"))
}

func TestEnforceReserve_HoldsAndReleases(t *testing.T) {
	cfg := Config{WithdrawMaxUSDC: 10_000, DailyWithdrawCapUSDC: 5_000}
	ledger := NewMemoryLedger(cfg.DailyWithdrawCapUSDC, time.Now)

	// A granted reservation is spent immediately — a second request for the
	// same headroom is rejected while the first is still in flight.
	release, err := EnforceReserve(cfg, ledger, "t1", ToolWithdraw, usdc(5_000))
	require.NoError(t, err)
	require.EqualValues(t, 0, ledger.Remaining("t1"))

	_, err = EnforceReserve(cfg, ledger, "t1", ToolWithdraw, usdc(1))
	var ce *ErrCapExceeded
	require.True(t, errors.As(err, &ce))
	require.Equal(t, "daily", ce.Kind)
	require.Equal(t, "5000.000000", ce.Used)

	// Releasing an unlanded tx restores the headroom.
	release()
	require.Equal(t, usdc(5_000), ledger.Remaining("t1"))

	// A rejected reservation returns a no-op release, safe to call.
	release, err = EnforceReserve(cfg, ledger, "t1", ToolWithdraw, usdc(20_000))
	require.Error(t, err) // per-tx cap
	release()
	require.Equal(t, usdc(5_000), ledger.Remaining("t1"))
}

func TestEnforceReserve_NilLedgerOrZeroCap_NoOp(t *testing.T) {
	cfg := Config{WithdrawMaxUSDC: 10_000} // DailyWithdrawCapUSDC = 0

	release, err := EnforceReserve(cfg, nil, "t1", ToolWithdraw, usdc(10_000))
	require.NoError(t, err)
	release() // must not panic on the nil ledger

	ledger := NewMemoryLedger(0, time.Now)
	release, err = EnforceReserve(cfg, ledger, "t1", ToolWithdraw, usdc(10_000))
	require.NoError(t, err)
	release()
	require.EqualValues(t, 0, ledger.Remaining("t1"))
}

func TestEnforce_NilLedgerOrZeroCap_NoOp(t *testing.T) {
	cfg := Config{WithdrawMaxUSDC: 10_000} // DailyWithdrawCapUSDC = 0

	// No ledger configured.
	require.NoError(t, Enforce(cfg, nil, "t1", ToolWithdraw, usdc(10_000)))

	// Ledger present but daily cap is zero.
	ledger := NewMemoryLedger(0, time.Now)
	require.NoError(t, Enforce(cfg, ledger, "t1", ToolWithdraw, usdc(10_000)))
}

func TestPerToolCapQuantums_OverflowGuard(t *testing.T) {
	// A cap so absurd it overflows uint64 when × 10^6 — treat as disabled
	// rather than wrap around to a tiny number.
	cfg := Config{WithdrawMaxUSDC: ^uint64(0)}
	require.NoError(t, CheckPerTx(cfg, ToolWithdraw, ^uint64(0)))
}
