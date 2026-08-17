package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIPRateLimiter_UnderCapPasses(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	l := NewIPRateLimiter(3, time.Minute, func() time.Time { return clk })
	for range 3 {
		require.True(t, l.Allow("1.2.3.4"))
	}
}

func TestIPRateLimiter_OverCapRejects(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	l := NewIPRateLimiter(3, time.Minute, func() time.Time { return clk })
	for range 3 {
		require.True(t, l.Allow("1.2.3.4"))
	}
	require.False(t, l.Allow("1.2.3.4"))
	require.False(t, l.Allow("1.2.3.4"), "still denied within the same window")
}

func TestIPRateLimiter_PerIPIndependent(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	l := NewIPRateLimiter(2, time.Minute, func() time.Time { return clk })
	require.True(t, l.Allow("1.2.3.4"))
	require.True(t, l.Allow("1.2.3.4"))
	require.False(t, l.Allow("1.2.3.4"))
	// Different IP — independent counter.
	require.True(t, l.Allow("5.6.7.8"))
	require.True(t, l.Allow("5.6.7.8"))
}

func TestIPRateLimiter_WindowRollover(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	l := NewIPRateLimiter(2, time.Minute, func() time.Time { return clk })
	require.True(t, l.Allow("1.2.3.4"))
	require.True(t, l.Allow("1.2.3.4"))
	require.False(t, l.Allow("1.2.3.4"))
	clk = clk.Add(61 * time.Second)
	require.True(t, l.Allow("1.2.3.4"), "new window resets counter")
}

func TestIPRateLimiter_EmptyIPAllowed(t *testing.T) {
	// Empty IP means "no client identity available" — typical when tests
	// bypass the HTTP layer. Allow rather than fail.
	l := NewIPRateLimiter(1, time.Minute, nil)
	require.True(t, l.Allow(""))
	require.True(t, l.Allow(""), "empty IP is never rate-limited")
}

func TestIPRateLimiter_Sweep(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	l := NewIPRateLimiter(2, time.Minute, func() time.Time { return clk })
	l.Allow("1.2.3.4")
	l.Allow("5.6.7.8")
	require.Equal(t, 2, l.Len())
	clk = clk.Add(61 * time.Second)
	l.Sweep()
	require.Equal(t, 0, l.Len())
}
