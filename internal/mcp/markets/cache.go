package markets

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"cosmossdk.io/log"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/chain"
)

// MarketMeta is the per-market constant set that every build_* tool path
// needs: clobPairId for the on-chain Order field, plus the conversion
// constants for the units package to translate human size/price into
// quantums/subticks. Assembled by joining the chain's ClobPair and
// Perpetual records.
type MarketMeta struct {
	Ticker                    string
	ClobPairID                uint32
	AtomicResolution          int32
	QuantumConversionExponent int32
	StepBaseQuantums          uint64
	SubticksPerTick           uint32
}

// Cache is a periodically-refreshed in-memory snapshot of market metadata
// keyed by ticker. Reads (ResolveTicker) are lock-free in the common
// case via sync.RWMutex.
//
// Refresh sources everything from the chain — no off-chain indexer
// dependency — by joining two authoritative queries:
//   - chain.ClobQueryClient.ClobPairAll — the set of tradable ClobPairs,
//     carrying clob_pair_id, step_base_quantums, subticks_per_tick,
//     quantum_conversion_exponent, and the perpetual_id link.
//   - chain.PerpetualsQueryClient.AllPerpetuals — the human ticker string
//     and atomic_resolution, keyed by perpetual id.
//
// Every build_* tool resolves a ticker through this cache, so keeping it
// chain-only means the write path works with no indexer running.
type Cache struct {
	clobQuery    chain.ClobQueryClient
	perpQuery    chain.PerpetualsQueryClient
	refreshEvery time.Duration
	logger       log.Logger

	mu       sync.RWMutex
	byTicker map[string]MarketMeta
}

// NewCache returns a Cache that refreshes every `refresh` (defaults to 60s
// when zero). clobQuery and perpQuery must both be non-nil; production
// callers (cmd/mcp-server/wire.go) pass real chain clients and tests pass
// inline stubs.
func NewCache(clobQuery chain.ClobQueryClient, perpQuery chain.PerpetualsQueryClient, refresh time.Duration, logger log.Logger) *Cache {
	if refresh == 0 {
		refresh = 60 * time.Second
	}
	return &Cache{
		clobQuery:    clobQuery,
		perpQuery:    perpQuery,
		refreshEvery: refresh,
		logger:       logger,
		byTicker:     make(map[string]MarketMeta),
	}
}

// Run blocks until ctx is done. It does one synchronous refresh on entry
// (so the server is not started with an empty cache) and then ticks.
// Errors from periodic refreshes are logged and the ticker continues —
// only the initial refresh propagates an error to the caller.
func (c *Cache) Run(ctx context.Context) error {
	if err := c.Refresh(ctx); err != nil {
		return fmt.Errorf("initial market cache refresh: %w", err)
	}
	t := time.NewTicker(c.refreshEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := c.Refresh(ctx); err != nil {
				c.logger.Error("market cache refresh failed", "error", err)
			}
		}
	}
}

// Refresh joins the chain's ClobPair set with its Perpetual records to
// build the ticker→MarketMeta table, then atomically swaps it in.
//
// ClobPairs whose perpetual_id has no matching Perpetual (and non-perpetual
// ClobPairs, which carry no ticker) are dropped, with the dropped
// clob_pair_ids logged for diagnostics.
func (c *Cache) Refresh(ctx context.Context) error {
	clobPairs, err := c.clobQuery.ClobPairAll(ctx)
	if err != nil {
		return fmt.Errorf("clob.Query/ClobPairAll: %w", err)
	}
	perps, err := c.perpQuery.AllPerpetuals(ctx)
	if err != nil {
		return fmt.Errorf("perpetuals.Query/AllPerpetuals: %w", err)
	}

	// Index perpetuals by id so each ClobPair can pull its ticker + atomic
	// resolution via PerpetualClobMetadata.PerpetualId.
	perpByID := make(map[uint32]perpMeta, len(perps))
	for _, p := range perps {
		perpByID[p.Params.Id] = perpMeta{
			ticker:           p.Params.Ticker,
			atomicResolution: p.Params.AtomicResolution,
		}
	}

	next := make(map[string]MarketMeta, len(clobPairs))
	var dropped []uint32
	for _, cp := range clobPairs {
		meta := cp.GetPerpetualClobMetadata()
		if meta == nil {
			// Non-perpetual (e.g. spot) ClobPair — no ticker to resolve.
			dropped = append(dropped, cp.Id)
			continue
		}
		perp, ok := perpByID[meta.PerpetualId]
		if !ok {
			dropped = append(dropped, cp.Id)
			continue
		}
		next[perp.ticker] = MarketMeta{
			Ticker:                    perp.ticker,
			ClobPairID:                cp.Id,
			AtomicResolution:          perp.atomicResolution,
			QuantumConversionExponent: cp.QuantumConversionExponent,
			StepBaseQuantums:          cp.StepBaseQuantums,
			SubticksPerTick:           cp.SubticksPerTick,
		}
	}
	if len(dropped) > 0 {
		slices.Sort(dropped) // stable log output
		c.logger.Info("markets cache: dropped clob pairs with no resolvable perpetual",
			"clob_pair_ids", dropped)
	}

	c.mu.Lock()
	c.byTicker = next
	c.mu.Unlock()
	return nil
}

// perpMeta is the slice of a Perpetual the cache joins onto its ClobPair.
type perpMeta struct {
	ticker           string
	atomicResolution int32
}

// ResolveTicker returns the cached MarketMeta for ticker, if known. It is
// the single entry point used by build_* paths so the ticker→clobPairId
// + conversion constants lookup happens in exactly one place.
func (c *Cache) ResolveTicker(ticker string) (MarketMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byTicker[ticker]
	return m, ok
}

// Size reports how many markets are currently cached (for diagnostics).
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byTicker)
}
