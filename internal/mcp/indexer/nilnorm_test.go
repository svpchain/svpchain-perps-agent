package indexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// clientReturning spins an httptest server that always responds with body, and
// returns a Client pointed at it — so each case can feed a null/empty collection
// and assert the Get* method normalizes it to a non-nil empty value.
func clientReturning(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, Options{})
}

// TestGet_NilCollectionsNormalizedToEmpty guards the output-schema bug across the
// indexer passthrough responses: a null/omitted collection from Comlink must be
// decoded into a non-nil empty slice/map so the tool result marshals to []/{}
// (a nil value marshals to null and fails the go-sdk's reflected array/object
// output schema).
func TestGet_NilCollectionsNormalizedToEmpty(t *testing.T) {
	ctx := context.Background()

	t.Run("fills", func(t *testing.T) {
		r, err := clientReturning(t, `{"fills":null}`).GetFills(ctx, "svp1", 0, "")
		mustNonNilRows(t, err, r.Fills)
	})
	t.Run("transfers", func(t *testing.T) {
		r, err := clientReturning(t, `{"transfers":null}`).GetTransfers(ctx, "svp1", 0)
		mustNonNilRows(t, err, r.Transfers)
	})
	t.Run("candles", func(t *testing.T) {
		r, err := clientReturning(t, `{}`).GetCandles(ctx, "BTC-USD", GetCandlesArgs{})
		mustNonNilRows(t, err, r.Candles)
	})
	t.Run("trades", func(t *testing.T) {
		r, err := clientReturning(t, `{"trades":null}`).GetTrades(ctx, "BTC-USD", 0)
		mustNonNilRows(t, err, r.Trades)
	})
	t.Run("historicalFunding", func(t *testing.T) {
		r, err := clientReturning(t, `{"historicalFunding":null}`).GetHistoricalFunding(ctx, "BTC-USD")
		mustNonNilRows(t, err, r.HistoricalFunding)
	})
	t.Run("fundingPayments", func(t *testing.T) {
		r, err := clientReturning(t, `{"fundingPayments":null}`).GetFundingPayments(ctx, "svp1", 0)
		mustNonNilRows(t, err, r.FundingPayments)
	})
	t.Run("pnl", func(t *testing.T) {
		r, err := clientReturning(t, `{"historicalPnl":null}`).GetPnl(ctx, "svp1", 0)
		mustNonNilRows(t, err, r.HistoricalPnl)
	})
	t.Run("historicalPnl", func(t *testing.T) {
		r, err := clientReturning(t, `{"historicalPnl":null}`).GetHistoricalPnl(ctx, "svp1", 0)
		mustNonNilRows(t, err, r.HistoricalPnl)
	})
	t.Run("orderbook", func(t *testing.T) {
		r, err := clientReturning(t, `{"bids":null,"asks":null}`).GetOrderbook(ctx, "BTC-USD")
		if err != nil {
			t.Fatalf("GetOrderbook: %v", err)
		}
		if r.Bids == nil || r.Asks == nil {
			t.Errorf("bids/asks must be non-nil: bids=%v asks=%v", r.Bids, r.Asks)
		}
	})
	t.Run("markets", func(t *testing.T) {
		r, err := clientReturning(t, `{"markets":null}`).ListPerpetualMarkets(ctx)
		if err != nil {
			t.Fatalf("ListPerpetualMarkets: %v", err)
		}
		if r.Markets == nil {
			t.Error("markets map must be non-nil")
		}
	})
	t.Run("sparklines", func(t *testing.T) {
		r, err := clientReturning(t, `null`).GetSparklines(ctx, "ONE_DAY")
		if err != nil {
			t.Fatalf("GetSparklines: %v", err)
		}
		if r == nil {
			t.Error("sparklines map must be non-nil")
		}
	})
	t.Run("sparklines_null_value", func(t *testing.T) {
		r, err := clientReturning(t, `{"BTC-USD":null}`).GetSparklines(ctx, "ONE_DAY")
		if err != nil {
			t.Fatalf("GetSparklines: %v", err)
		}
		if r["BTC-USD"] == nil {
			t.Error("per-ticker slice must be non-nil")
		}
	})
}

func mustNonNilRows(t *testing.T, err error, rows []map[string]any) {
	t.Helper()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rows == nil {
		t.Error("collection must be non-nil (marshals to [] not null)")
	}
}
