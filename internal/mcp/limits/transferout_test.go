package limits

import (
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// bi parses a base-10 integer into a *big.Int for test readability.
func bi(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad big.Int literal: " + s)
	}
	return n
}

func usdcCap(base string) SymbolCap {
	return SymbolCap{Symbol: "usdc", Decimals: 6, CapBase: bi(base)}
}

func TestBaseToHuman(t *testing.T) {
	require.Equal(t, "1.5", baseToHuman(bi("1500000"), 6))
	require.Equal(t, "500", baseToHuman(bi("500000000"), 6))
	require.Equal(t, "0.000001", baseToHuman(bi("1"), 6))
	require.Equal(t, "10", baseToHuman(bi("10000000000000000000"), 18))
	require.Equal(t, "0", baseToHuman(big.NewInt(0), 6))
	require.Equal(t, "42", baseToHuman(big.NewInt(42), 0))
}

func TestTransferOutStore_CapLifecycle(t *testing.T) {
	s := NewMemoryTransferOutStore(time.Now)

	t.Run("uncapped until set", func(t *testing.T) {
		_, ok := s.Cap("t1", "usdc")
		require.False(t, ok)
		require.NoError(t, s.Check("t1", "usdc", bi("999999999999")))
	})

	t.Run("set then enforced", func(t *testing.T) {
		s.SetCap("t1", usdcCap("500000000")) // 500
		require.NoError(t, s.Check("t1", "usdc", bi("500000000")))
		err := s.Check("t1", "usdc", bi("500000001"))
		require.Error(t, err)
		var ce *ErrSymbolCapExceeded
		require.True(t, errors.As(err, &ce))
		require.Equal(t, "usdc", ce.Symbol)
		require.Equal(t, "500", ce.Limit)
		require.Equal(t, "500.000001", ce.Requested)
	})

	t.Run("raise cap — no ceiling", func(t *testing.T) {
		s.SetCap("t1", usdcCap("1000000000")) // 1000
		require.NoError(t, s.Check("t1", "usdc", bi("900000000")))
	})

	t.Run("set unlimited clears the cap", func(t *testing.T) {
		s.SetUnlimited("t1", "usdc")
		_, ok := s.Cap("t1", "usdc")
		require.False(t, ok)
		require.NoError(t, s.Check("t1", "usdc", bi("999999999999")))
	})

	t.Run("non-positive amount and nil store pass", func(t *testing.T) {
		s.SetCap("t1", usdcCap("1"))
		require.NoError(t, s.Check("t1", "usdc", big.NewInt(0)))
		var nilStore *MemoryTransferOutStore
		require.NoError(t, nilStore.Check("t1", "usdc", bi("999")))
	})
}

func TestTransferOutStore_UsageAndIsolation(t *testing.T) {
	s := NewMemoryTransferOutStore(time.Now)
	s.SetCap("t1", usdcCap("500000000"))

	// Spend to the cap, cumulative.
	require.NoError(t, s.Check("t1", "usdc", bi("300000000")))
	s.Record("t1", "usdc", bi("300000000"))
	require.NoError(t, s.Check("t1", "usdc", bi("200000000")))
	s.Record("t1", "usdc", bi("200000000"))

	err := s.Check("t1", "usdc", bi("1"))
	require.Error(t, err)
	var ce *ErrSymbolCapExceeded
	require.True(t, errors.As(err, &ce))
	require.Equal(t, "500", ce.Used)

	// A different tenant's usage + cap are independent.
	require.NoError(t, s.Check("t2", "usdc", bi("999999999")))
	require.EqualValues(t, 0, s.Used("t2", "usdc").Int64())

	// svp is a separate symbol, unaffected by usdc usage.
	require.EqualValues(t, 0, s.Used("t1", "svp").Int64())
}

func TestTransferOutStore_UTCRollover(t *testing.T) {
	clk := time.Date(2026, 5, 28, 23, 59, 0, 0, time.UTC)
	s := NewMemoryTransferOutStore(func() time.Time { return clk })
	s.SetCap("t1", usdcCap("500000000"))

	s.Record("t1", "usdc", bi("500000000"))
	require.Error(t, s.Check("t1", "usdc", bi("1")))

	// Cross UTC midnight — usage resets, the cap persists.
	clk = clk.Add(2 * time.Minute)
	require.EqualValues(t, 0, s.Used("t1", "usdc").Int64())
	require.NoError(t, s.Check("t1", "usdc", bi("500000000")))
	_, ok := s.Cap("t1", "usdc")
	require.True(t, ok, "cap should survive a day rollover")
}

func TestTransferOutStore_UsedReturnsCopy(t *testing.T) {
	s := NewMemoryTransferOutStore(time.Now)
	s.Record("t1", "usdc", bi("100"))
	got := s.Used("t1", "usdc")
	got.Add(got, bi("999"))
	require.Equal(t, bi("100"), s.Used("t1", "usdc"))
}

func TestTransferOutStore_PersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caps.json")
	clk := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clk }

	s1, err := LoadMemoryTransferOutStore(path, now, nil)
	require.NoError(t, err)
	s1.SetCap("t1", usdcCap("500000000")) // 500
	require.NoError(t, s1.Check("t1", "usdc", bi("300000000")))
	s1.Record("t1", "usdc", bi("300000000"))

	// A second store built from the same file sees the cap and today's usage,
	// so enforcement continues across a restart rather than refilling.
	s2, err := LoadMemoryTransferOutStore(path, now, nil)
	require.NoError(t, err)

	c, ok := s2.Cap("t1", "usdc")
	require.True(t, ok, "cap should survive a reload")
	require.Equal(t, bi("500000000"), c.CapBase)
	require.EqualValues(t, 6, c.Decimals)
	require.Equal(t, "usdc", c.Symbol)

	require.Equal(t, bi("300000000"), s2.Used("t1", "usdc"))
	require.NoError(t, s2.Check("t1", "usdc", bi("200000000")))
	require.Error(t, s2.Check("t1", "usdc", bi("200000001")), "remaining 200 already spent down from the cap")
}

func TestTransferOutStore_PersistSetUnlimited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caps.json")
	s1, err := LoadMemoryTransferOutStore(path, time.Now, nil)
	require.NoError(t, err)
	s1.SetCap("t1", usdcCap("500000000"))
	s1.SetUnlimited("t1", "usdc")

	s2, err := LoadMemoryTransferOutStore(path, time.Now, nil)
	require.NoError(t, err)
	_, ok := s2.Cap("t1", "usdc")
	require.False(t, ok, "uncapping should survive a reload")
	require.NoError(t, s2.Check("t1", "usdc", bi("999999999999")))
}

func TestTransferOutStore_PersistRolloverDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caps.json")
	clk := time.Date(2026, 5, 28, 23, 59, 0, 0, time.UTC)
	now := func() time.Time { return clk }

	s1, err := LoadMemoryTransferOutStore(path, now, nil)
	require.NoError(t, err)
	s1.SetCap("t1", usdcCap("500000000"))
	s1.Record("t1", "usdc", bi("500000000"))

	// Cross UTC midnight, then read — the rollover clears usage and the empty
	// state must be written through, not just held in memory.
	clk = clk.Add(2 * time.Minute)
	require.EqualValues(t, 0, s1.Used("t1", "usdc").Int64())

	// A reload after the rollover (now firmly in the new day) must not resurrect
	// yesterday's usage from the file.
	s2, err := LoadMemoryTransferOutStore(path, now, nil)
	require.NoError(t, err)
	require.EqualValues(t, 0, s2.Used("t1", "usdc").Int64(), "rolled-over usage must not come back from disk")
	require.NoError(t, s2.Check("t1", "usdc", bi("500000000")))
}

func TestTransferOutStore_LoadMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	s, err := LoadMemoryTransferOutStore(path, time.Now, nil)
	require.NoError(t, err)
	require.NotNil(t, s)
	_, ok := s.Cap("t1", "usdc")
	require.False(t, ok)
	// First mutation creates the file.
	s.SetCap("t1", usdcCap("1"))
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "file should be created on first write")
}

func TestTransferOutStore_LoadCorruptFileErrors(t *testing.T) {
	t.Run("not json", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "caps.json")
		require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
		_, err := LoadMemoryTransferOutStore(path, time.Now, nil)
		require.Error(t, err)
	})

	t.Run("non-numeric cap_base", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "caps.json")
		body := `{"day":"2026-05-28","caps":{"t1":{"usdc":{"symbol":"usdc","decimals":6,"cap_base":"abc"}}}}`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		_, err := LoadMemoryTransferOutStore(path, time.Now, nil)
		require.Error(t, err)
	})
}

func TestTransferOutStore_NoPathWritesNothing(t *testing.T) {
	dir := t.TempDir()
	s := NewMemoryTransferOutStore(time.Now)
	s.SetCap("t1", usdcCap("500000000"))
	s.Record("t1", "usdc", bi("100"))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "a non-persistent store must not touch disk")
}

func TestTransferOutStore_ConcurrentRecord(t *testing.T) {
	const goroutines = 100
	const perGoroutine = 10
	s := NewMemoryTransferOutStore(time.Now)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				s.Record("t1", "usdc", big.NewInt(1))
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, goroutines*perGoroutine, s.Used("t1", "usdc").Int64())
}

func TestTransferOutStore_Reserve(t *testing.T) {
	s := NewMemoryTransferOutStore(time.Now)
	s.SetCap("t1", usdcCap("500000000")) // 500 usdc

	require.NoError(t, s.Reserve("t1", "usdc", bi("300000000")))
	require.Equal(t, "300000000", s.Used("t1", "usdc").String(), "a granted reserve counts immediately")

	// The remaining 200 doesn't cover a second 300.
	err := s.Reserve("t1", "usdc", bi("300000000"))
	var ce *ErrSymbolCapExceeded
	require.True(t, errors.As(err, &ce))
	require.Equal(t, "300", ce.Used)
	require.Equal(t, "300000000", s.Used("t1", "usdc").String(), "a refused reserve records nothing")

	// Releasing an unlanded tx hands the amount back.
	s.Release("t1", "usdc", bi("300000000"))
	require.Equal(t, "0", s.Used("t1", "usdc").String())

	// Over-releasing clamps at zero rather than going negative.
	s.Release("t1", "usdc", bi("999000000"))
	require.Equal(t, "0", s.Used("t1", "usdc").String())

	// An uncapped symbol still meters usage, so a cap set later in the day
	// sees the real total.
	require.NoError(t, s.Reserve("t1", "svp", bi("42")))
	require.Equal(t, "42", s.Used("t1", "svp").String())
}

func TestTransferOutStore_ConcurrentReserveCannotOvershootCap(t *testing.T) {
	// The race this closes: N concurrent transfers each asking for the whole
	// cap. A read-then-record check lets them all pass; Reserve settles it
	// under one lock, so exactly one is granted.
	const goroutines = 64
	s := NewMemoryTransferOutStore(time.Now)
	s.SetCap("t1", usdcCap("500000000"))

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if err := s.Reserve("t1", "usdc", bi("500000000")); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 1, granted, "only one full-cap transfer may be granted")
	require.Equal(t, "500000000", s.Used("t1", "usdc").String())
}
