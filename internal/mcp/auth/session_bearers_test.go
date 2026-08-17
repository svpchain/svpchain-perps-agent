package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionBearers_BindLookup(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewSessionBearers(time.Hour, func() time.Time { return clk })

	s.Bind("session-abc", "bearer-xyz")
	require.Equal(t, "bearer-xyz", s.Lookup("session-abc"))
	require.Equal(t, "", s.Lookup("unknown-session"))
}

func TestSessionBearers_RebindOverwrites(t *testing.T) {
	s := NewSessionBearers(time.Hour, nil)
	s.Bind("session-1", "bearer-old")
	s.Bind("session-1", "bearer-new")
	require.Equal(t, "bearer-new", s.Lookup("session-1"))
}

func TestSessionBearers_Expired(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewSessionBearers(time.Hour, func() time.Time { return clk })
	s.Bind("session-1", "bearer-1")
	clk = clk.Add(2 * time.Hour)
	require.Equal(t, "", s.Lookup("session-1"))
}

func TestSessionBearers_EmptyInputsIgnored(t *testing.T) {
	s := NewSessionBearers(time.Hour, nil)
	s.Bind("", "bearer-1")  // no-op
	s.Bind("session-1", "") // no-op
	require.Equal(t, 0, s.Len())
	require.Equal(t, "", s.Lookup(""))
}

func TestSessionBearers_Sweep(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewSessionBearers(time.Hour, func() time.Time { return clk })
	s.Bind("s1", "b1")
	s.Bind("s2", "b2")
	require.Equal(t, 2, s.Len())
	clk = clk.Add(2 * time.Hour)
	s.Sweep()
	require.Equal(t, 0, s.Len())
}
