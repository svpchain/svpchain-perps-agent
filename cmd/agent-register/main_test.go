package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitTags(t *testing.T) {
	require.Equal(t, []string{"perps.trading", "perps.market-data"}, splitTags("perps.trading,perps.market-data"))
	require.Equal(t, []string{"perps.trading", "perps.market-data"}, splitTags(" perps.trading , perps.market-data "))
	require.Equal(t, []string{"perps.trading"}, splitTags("perps.trading,,"))
	require.Nil(t, splitTags(""))
	require.Nil(t, splitTags(" , "))
}

// The card advertises the interface URL the agent built from its own
// public_url. If that disagrees with the endpoint being registered, the
// registration would publish a URL whose card sends callers elsewhere.
func TestCheckCardURL(t *testing.T) {
	card := func(url string) []byte {
		return []byte(`{"supportedInterfaces":[{"url":"` + url + `"}]}`)
	}

	t.Run("matching base URL", func(t *testing.T) {
		require.NoError(t, checkCardURL(card("https://a.example/invoke"), "https://a.example"))
	})

	t.Run("different host is refused", func(t *testing.T) {
		err := checkCardURL(card("https://other.example/invoke"), "https://a.example")
		require.ErrorContains(t, err, "public_url and -url disagree")
	})

	// A prefix match on the bare string would accept this; the separator is
	// what stops "https://a.example.evil.com" passing for "https://a.example".
	t.Run("host that merely starts the same is refused", func(t *testing.T) {
		err := checkCardURL(card("https://a.example.evil.com/invoke"), "https://a.example")
		require.Error(t, err)
	})

	t.Run("card with no interfaces is not judged", func(t *testing.T) {
		require.NoError(t, checkCardURL([]byte(`{}`), "https://a.example"))
	})

	t.Run("unparseable card is an error", func(t *testing.T) {
		require.ErrorContains(t, checkCardURL([]byte("not json"), "https://a.example"), "parse agent card")
	})
}
