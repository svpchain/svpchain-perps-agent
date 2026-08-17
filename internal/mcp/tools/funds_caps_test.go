package tools

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/limits"
)

// dtoFor returns the DTO for a symbol from a view slice.
func dtoFor(view []TransferOutCapDTO, symbol string) (TransferOutCapDTO, bool) {
	for _, d := range view {
		if d.Symbol == symbol {
			return d, true
		}
	}
	return TransferOutCapDTO{}, false
}

func TestTransferOutCapView(t *testing.T) {
	usdc, _ := assetForSymbol("usdc")
	usdcCap := func() limits.SymbolCap {
		return limits.SymbolCap{Symbol: "usdc", Decimals: 6, CapBase: big.NewInt(500_000_000)}
	}
	store := limits.NewMemoryTransferOutStore(time.Now)
	store.SetCap("t1", usdcCap())

	t.Run("cap set, nothing used", func(t *testing.T) {
		view := transferOutCapView(store, "t1", []assetSymbol{usdc})
		d, ok := dtoFor(view, "usdc")
		require.True(t, ok)
		require.Equal(t, "500", d.Cap)
		require.Equal(t, "0", d.UsedToday)
		require.Equal(t, "500", d.Remaining)
	})

	t.Run("remaining reflects used", func(t *testing.T) {
		store.Record("t1", "usdc", big.NewInt(200_000_000)) // 200 used
		view := transferOutCapView(store, "t1", []assetSymbol{usdc})
		d, _ := dtoFor(view, "usdc")
		require.Equal(t, "200", d.UsedToday)
		require.Equal(t, "300", d.Remaining)
	})

	t.Run("unset symbol shows unlimited", func(t *testing.T) {
		svp, _ := assetForSymbol("svp")
		view := transferOutCapView(store, "t1", []assetSymbol{svp})
		d, _ := dtoFor(view, "svp")
		require.Equal(t, "unlimited", d.Cap)
		require.Equal(t, "unlimited", d.Remaining)
	})

	t.Run("set unlimited clears the cap", func(t *testing.T) {
		store.SetUnlimited("t1", "usdc")
		view := transferOutCapView(store, "t1", []assetSymbol{usdc})
		d, _ := dtoFor(view, "usdc")
		require.Equal(t, "unlimited", d.Cap)
	})

	t.Run("a tenant that set nothing is unlimited (no defaults)", func(t *testing.T) {
		view := transferOutCapView(store, "t2", []assetSymbol{usdc})
		d, _ := dtoFor(view, "usdc")
		require.Equal(t, "unlimited", d.Cap)
	})

	t.Run("over-spent cap clamps remaining to 0", func(t *testing.T) {
		store.SetCap("t3", usdcCap())
		store.Record("t3", "usdc", big.NewInt(600_000_000)) // 600 used vs 500 cap
		view := transferOutCapView(store, "t3", []assetSymbol{usdc})
		d, _ := dtoFor(view, "usdc")
		require.Equal(t, "0", d.Remaining)
	})

	t.Run("nil store -> all unlimited", func(t *testing.T) {
		var nilStore *limits.MemoryTransferOutStore
		view := transferOutCapView(nilStore, "t1", []assetSymbol{usdc})
		d, _ := dtoFor(view, "usdc")
		require.Equal(t, "unlimited", d.Cap)
		require.Equal(t, "0", d.UsedToday)
	})
}
