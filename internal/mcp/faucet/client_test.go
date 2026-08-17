package faucet_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/faucet"
)

func TestEnabledTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/enabledTokens", r.URL.Path)
		_, _ = w.Write([]byte(`[
			{"address":"0x0000000000000000000000000000000000000000","amount_allowed":"1000000000000000000","enabled":true},
			{"address":"0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951","amount_allowed":"1000000","enabled":true}
		]`))
	}))
	defer srv.Close()

	c := faucet.NewClient(srv.URL, faucet.Options{})
	tokens, err := c.EnabledTokens(context.Background())
	require.NoError(t, err)
	require.Len(t, tokens, 2)
	require.Equal(t, "0x0000000000000000000000000000000000000000", tokens[0].Address)
	require.Equal(t, "1000000000000000000", tokens[0].AmountAllowed)
	require.True(t, tokens[0].Enabled)
}

func TestClaim_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/claim", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, _ := io.ReadAll(r.Body)
		var got map[string]string
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "0x0000000000000000000000000000000000000000", got["token"])
		require.Equal(t, "0x0C48cD968bCeAb3bb81FA80A6114ED0d6B12d212", got["address"])

		_, _ = w.Write([]byte(`{"tx_hash":"0xabc","amount":"1000000000000000000","token":"0x0000000000000000000000000000000000000000"}`))
	}))
	defer srv.Close()

	c := faucet.NewClient(srv.URL, faucet.Options{})
	res, err := c.Claim(context.Background(),
		"0x0000000000000000000000000000000000000000",
		"0x0C48cD968bCeAb3bb81FA80A6114ED0d6B12d212")
	require.NoError(t, err)
	require.Equal(t, "0xabc", res.TxHash)
	require.Equal(t, "1000000000000000000", res.Amount)
}

func TestClaim_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited","retry_after":3600}`))
	}))
	defer srv.Close()

	c := faucet.NewClient(srv.URL, faucet.Options{})
	_, err := c.Claim(context.Background(), "0x0", "0x0")
	require.Error(t, err)
	// Surfaces the faucet's own message + retry window so the agent can act.
	require.Contains(t, err.Error(), "rate limited")
	require.Contains(t, err.Error(), "3600")
}

func TestClaim_TokenNotEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"token not enabled"}`))
	}))
	defer srv.Close()

	c := faucet.NewClient(srv.URL, faucet.Options{})
	_, err := c.Claim(context.Background(), "0xdead", "0x0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "token not enabled")
}
