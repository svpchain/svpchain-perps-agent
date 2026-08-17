package auth

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNonceStore_IssueConsume_Happy(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewNonceStore(DefaultChallengeTTL, func() time.Time { return clk })

	nonce, exp, err := s.Issue("svp1alice")
	require.NoError(t, err)
	require.Len(t, nonce, nonceLen*2, "expected hex-encoded nonce")
	require.True(t, exp.After(clk))

	owner, _, err := s.Consume(nonce)
	require.NoError(t, err)
	require.Equal(t, "svp1alice", owner)
}

func TestNonceStore_SingleUse(t *testing.T) {
	// Replay protection: even within the TTL window the nonce only works
	// once.
	s := NewNonceStore(DefaultChallengeTTL, nil)
	nonce, _, err := s.Issue("svp1bob")
	require.NoError(t, err)

	_, _, err = s.Consume(nonce)
	require.NoError(t, err)

	_, _, err = s.Consume(nonce)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNonceNotFound))
}

func TestNonceStore_Expired(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewNonceStore(5*time.Minute, func() time.Time { return clk })

	nonce, _, err := s.Issue("svp1charlie")
	require.NoError(t, err)

	clk = clk.Add(6 * time.Minute) // past expiry
	_, _, err = s.Consume(nonce)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNonceExpired))
	// Expired consumes still remove the entry — single use even on failure.
	_, _, err = s.Consume(nonce)
	require.True(t, errors.Is(err, ErrNonceNotFound))
}

func TestNonceStore_Sweep(t *testing.T) {
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := NewNonceStore(5*time.Minute, func() time.Time { return clk })

	for range 3 {
		_, _, err := s.Issue("svp1")
		require.NoError(t, err)
	}
	require.Equal(t, 3, s.Len())

	clk = clk.Add(6 * time.Minute)
	s.Sweep()
	require.Equal(t, 0, s.Len(), "all expired entries should be swept")
}

func TestNonceStore_Concurrent(t *testing.T) {
	// Issue + Consume from many goroutines; the mutex must serialise so
	// neither map grows unbounded nor concurrent Consume succeeds twice.
	const goroutines = 100
	s := NewNonceStore(time.Hour, time.Now)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			nonce, _, err := s.Issue("svp1concurrent")
			require.NoError(t, err)
			_, _, err = s.Consume(nonce)
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	require.Equal(t, 0, s.Len())
}

func TestNonceStore_EmptyOwnerRejected(t *testing.T) {
	s := NewNonceStore(time.Hour, nil)
	_, _, err := s.Issue("")
	require.Error(t, err)
}
