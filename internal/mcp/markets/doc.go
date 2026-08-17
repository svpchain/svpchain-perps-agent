// Package markets owns the market-metadata cache that every build_* tool
// path depends on for ticker→clobPairId resolution and unit-conversion
// constants (atomicResolution, stepBaseQuantums, subticksPerTick).
//
// Cache is populated at boot entirely from the chain — joining
// clob.Query/ClobPairAll with perpetuals.Query/AllPerpetuals (the latter
// carries the human ticker strings + atomic resolution) — and is refreshed
// periodically (default 60s). It has no dependency on the off-chain
// indexer, so the build_* write path works with no indexer running.
//
// ResolveTicker is the single entry point exposed to builders so the
// metadata lookup happens in exactly one place.
package markets
