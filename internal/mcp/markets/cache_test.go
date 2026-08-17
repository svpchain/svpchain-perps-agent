package markets_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"

	clobtypes "github.com/dydxprotocol/v4-chain/protocol/x/clob/types"
	perptypes "github.com/dydxprotocol/v4-chain/protocol/x/perpetuals/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/markets"
)

// stubChain implements both chain.ClobQueryClient and
// chain.PerpetualsQueryClient off fixed in-memory slices (or errors), so
// the cache's chain join can be exercised without a live chain.
type stubChain struct {
	pairs    []clobtypes.ClobPair
	perps    []perptypes.Perpetual
	pairErr  error
	perpErr  error
	clobHits int
	perpHits int
}

func (s *stubChain) ClobPairAll(_ context.Context) ([]clobtypes.ClobPair, error) {
	s.clobHits++
	if s.pairErr != nil {
		return nil, s.pairErr
	}
	return s.pairs, nil
}

func (s *stubChain) AllPerpetuals(_ context.Context) ([]perptypes.Perpetual, error) {
	s.perpHits++
	if s.perpErr != nil {
		return nil, s.perpErr
	}
	return s.perps, nil
}

// perpPair builds a perpetual ClobPair + its backing Perpetual, sharing the
// perpetual id so the cache's join links them.
func perpPair(clobPairID, perpID uint32, ticker string) (clobtypes.ClobPair, perptypes.Perpetual) {
	pair := clobtypes.ClobPair{
		Id: clobPairID,
		Metadata: &clobtypes.ClobPair_PerpetualClobMetadata{
			PerpetualClobMetadata: &clobtypes.PerpetualClobMetadata{PerpetualId: perpID},
		},
		StepBaseQuantums:          1_000_000,
		SubticksPerTick:           1_000,
		QuantumConversionExponent: -9,
	}
	perp := perptypes.Perpetual{
		Params: perptypes.PerpetualParams{Id: perpID, Ticker: ticker, AtomicResolution: -10},
	}
	return pair, perp
}

func TestCache_RefreshAndResolve(t *testing.T) {
	pair, perp := perpPair(0, 0, "BTC-USD")
	stub := &stubChain{pairs: []clobtypes.ClobPair{pair}, perps: []perptypes.Perpetual{perp}}
	c := markets.NewCache(stub, stub, time.Hour, log.NewNopLogger())

	require.NoError(t, c.Refresh(context.Background()))
	require.Equal(t, 1, c.Size())
	require.Equal(t, 1, stub.clobHits, "Refresh must hit clob.Query exactly once")
	require.Equal(t, 1, stub.perpHits, "Refresh must hit perpetuals.Query exactly once")

	meta, ok := c.ResolveTicker("BTC-USD")
	require.True(t, ok)
	require.Equal(t, "BTC-USD", meta.Ticker)
	require.Equal(t, uint32(0), meta.ClobPairID)
	require.Equal(t, int32(-10), meta.AtomicResolution)
	require.Equal(t, int32(-9), meta.QuantumConversionExponent)
	require.Equal(t, uint64(1_000_000), meta.StepBaseQuantums)
	require.Equal(t, uint32(1_000), meta.SubticksPerTick)

	_, ok = c.ResolveTicker("ETH-USD")
	require.False(t, ok, "unknown ticker must miss")
}

func TestCache_RefreshAtomicSwap(t *testing.T) {
	// Two markets in round 1; one in round 2. The cache must atomically
	// replace its table (no half-populated state visible) and a previously-
	// resolvable ticker must vanish if it's no longer in the refresh.
	btcPair, btcPerp := perpPair(0, 0, "BTC-USD")
	ethPair, ethPerp := perpPair(1, 1, "ETH-USD")
	stub := &stubChain{
		pairs: []clobtypes.ClobPair{btcPair, ethPair},
		perps: []perptypes.Perpetual{btcPerp, ethPerp},
	}
	c := markets.NewCache(stub, stub, time.Hour, log.NewNopLogger())

	require.NoError(t, c.Refresh(context.Background()))
	require.Equal(t, 2, c.Size())
	_, ok := c.ResolveTicker("ETH-USD")
	require.True(t, ok)

	stub.pairs = []clobtypes.ClobPair{btcPair}
	stub.perps = []perptypes.Perpetual{btcPerp}
	require.NoError(t, c.Refresh(context.Background()))
	require.Equal(t, 1, c.Size())
	_, ok = c.ResolveTicker("ETH-USD")
	require.False(t, ok, "ticker dropped from upstream must disappear after refresh")
	_, ok = c.ResolveTicker("BTC-USD")
	require.True(t, ok)
}

// TestCache_DropsUnjoinableClobPairs covers the two join failures: a
// ClobPair whose perpetual_id has no matching Perpetual, and a
// non-perpetual (spot) ClobPair that carries no ticker. Both are dropped;
// the joinable entry is preserved.
func TestCache_DropsUnjoinableClobPairs(t *testing.T) {
	btcPair, btcPerp := perpPair(0, 0, "BTC-USD")
	orphan, _ := perpPair(1, 99, "ETH-USD") // perpetual 99 not supplied
	spot := clobtypes.ClobPair{Id: 2}       // no PerpetualClobMetadata

	stub := &stubChain{
		pairs: []clobtypes.ClobPair{btcPair, orphan, spot},
		perps: []perptypes.Perpetual{btcPerp},
	}
	c := markets.NewCache(stub, stub, time.Hour, log.NewNopLogger())

	require.NoError(t, c.Refresh(context.Background()))
	require.Equal(t, 1, c.Size(), "only the joinable BTC-USD pair should be cached")
	_, ok := c.ResolveTicker("BTC-USD")
	require.True(t, ok)
	_, ok = c.ResolveTicker("ETH-USD")
	require.False(t, ok, "clob pair with no matching perpetual must be dropped")
}

func TestCache_RefreshError(t *testing.T) {
	// clob.Query failure propagates and leaves the table empty.
	stub := &stubChain{pairErr: errors.New("boom")}
	c := markets.NewCache(stub, stub, time.Hour, log.NewNopLogger())
	require.Error(t, c.Refresh(context.Background()))
	require.Equal(t, 0, c.Size())

	// perpetuals.Query failure propagates too.
	pair, _ := perpPair(0, 0, "BTC-USD")
	stub2 := &stubChain{pairs: []clobtypes.ClobPair{pair}, perpErr: errors.New("boom")}
	c2 := markets.NewCache(stub2, stub2, time.Hour, log.NewNopLogger())
	require.Error(t, c2.Refresh(context.Background()))
	require.Equal(t, 0, c2.Size())
}
