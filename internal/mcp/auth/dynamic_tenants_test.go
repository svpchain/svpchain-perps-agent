package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func defaultCfg() DynamicTenantStoreConfig {
	return DynamicTenantStoreConfig{
		BearerTTL:                 DefaultBearerTTL,
		DefaultAllowedSubaccounts: []uint32{0, 1, 2},
	}
}

func TestDynamicTenants_MintAndLookup(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewDynamicTenantStore(defaultCfg(), func() time.Time { return clk })

	bearer, tenantID, exp, err := s.Mint("svp1alice")
	require.NoError(t, err)
	require.Len(t, bearer, bearerLen*2)
	require.True(t, exp.After(clk))
	require.Contains(t, tenantID, "auto-")

	rec, err := s.LookupByBearer(bearer)
	require.NoError(t, err)
	require.Equal(t, tenantID, rec.TenantID)
	require.Equal(t, "svp1alice", rec.Owner)
	require.Len(t, rec.AllowedSubaccounts, 3)
	_, ok := rec.AllowedSubaccounts[1]
	require.True(t, ok)
	require.False(t, rec.KillSwitch)

	// Lookup by tenant id (used post-middleware by handler resolver) must
	// return the same record.
	recByID, err := s.LookupByTenantID(tenantID)
	require.NoError(t, err)
	require.Equal(t, rec.Owner, recByID.Owner)
}

func TestDynamicTenants_Expired(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewDynamicTenantStore(DynamicTenantStoreConfig{
		BearerTTL: time.Hour, DefaultAllowedSubaccounts: []uint32{0},
	}, func() time.Time { return clk })

	bearer, tenantID, _, err := s.Mint("svp1bob")
	require.NoError(t, err)

	clk = clk.Add(61 * time.Minute) // past TTL
	_, err = s.LookupByBearer(bearer)
	require.True(t, errors.Is(err, ErrBearerExpired))
	_, err = s.LookupByTenantID(tenantID)
	require.True(t, errors.Is(err, ErrBearerExpired))
}

func TestDynamicTenants_Sweep(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewDynamicTenantStore(DynamicTenantStoreConfig{
		BearerTTL: time.Hour, DefaultAllowedSubaccounts: []uint32{0},
	}, func() time.Time { return clk })

	for range 5 {
		_, _, _, err := s.Mint("svp1")
		require.NoError(t, err)
	}
	require.Equal(t, 5, s.Len())

	clk = clk.Add(61 * time.Minute)
	s.Sweep()
	require.Equal(t, 0, s.Len())
}

func TestDynamicTenants_MintMultipleSameOwner(t *testing.T) {
	// Re-minting for the same owner produces a distinct bearer + tenant_id;
	// the old one stays valid (independent TTLs).
	s := NewDynamicTenantStore(defaultCfg(), nil)
	b1, t1, _, err := s.Mint("svp1alice")
	require.NoError(t, err)
	b2, t2, _, err := s.Mint("svp1alice")
	require.NoError(t, err)
	require.NotEqual(t, b1, b2)
	require.NotEqual(t, t1, t2)

	// Both still resolve.
	_, err = s.LookupByBearer(b1)
	require.NoError(t, err)
	_, err = s.LookupByBearer(b2)
	require.NoError(t, err)
}

func TestDynamicTenants_BadBearer(t *testing.T) {
	s := NewDynamicTenantStore(defaultCfg(), nil)
	_, err := s.LookupByBearer("nonexistent-bearer")
	require.True(t, errors.Is(err, ErrBearerNotFound))
}
