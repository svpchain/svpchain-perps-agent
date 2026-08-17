package indexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Comlink returns null for the position maps on a subaccount with none open.
// A nil map marshals back to null, which fails the MCP output schema's
// "type":"object" — so GetSubaccount must normalize them to empty maps.
func TestGetSubaccountNormalizesNullPositionMaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v4/addresses/svp1abc/subaccountNumber/0"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"address": "svp1abc",
			"subaccountNumber": 0,
			"equity": "10",
			"freeCollateral": "10",
			"openPerpetualPositions": null,
			"assetPositions": null,
			"marginEnabled": false,
			"updatedAtHeight": "100",
			"latestProcessedBlockHeight": "100"
		}`))
	}))
	defer srv.Close()

	sa, err := NewClient(srv.URL, Options{}).GetSubaccount(context.Background(), "svp1abc", 0)
	if err != nil {
		t.Fatalf("GetSubaccount: %v", err)
	}
	if sa.OpenPerpetualPositions == nil {
		t.Error("OpenPerpetualPositions is nil; want empty map")
	}
	if sa.AssetPositions == nil {
		t.Error("AssetPositions is nil; want empty map")
	}

	// The regression is only visible after marshaling: nil -> null fails
	// output validation, empty map -> {} passes.
	var round struct {
		OpenPerpetualPositions json.RawMessage `json:"openPerpetualPositions"`
		AssetPositions         json.RawMessage `json:"assetPositions"`
	}
	b, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := string(round.OpenPerpetualPositions); got != "{}" {
		t.Errorf("openPerpetualPositions marshaled to %s, want {}", got)
	}
	if got := string(round.AssetPositions); got != "{}" {
		t.Errorf("assetPositions marshaled to %s, want {}", got)
	}
}

// A populated response must pass through untouched.
func TestGetSubaccountPreservesPopulatedPositionMaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"address": "svp1abc",
			"subaccountNumber": 0,
			"openPerpetualPositions": {"BTC-USD": {"size": "1.5"}},
			"assetPositions": {"USDC": {"size": "100"}}
		}`))
	}))
	defer srv.Close()

	sa, err := NewClient(srv.URL, Options{}).GetSubaccount(context.Background(), "svp1abc", 0)
	if err != nil {
		t.Fatalf("GetSubaccount: %v", err)
	}
	if _, ok := sa.OpenPerpetualPositions["BTC-USD"]; !ok {
		t.Errorf("OpenPerpetualPositions = %v, want BTC-USD entry", sa.OpenPerpetualPositions)
	}
	if _, ok := sa.AssetPositions["USDC"]; !ok {
		t.Errorf("AssetPositions = %v, want USDC entry", sa.AssetPositions)
	}
}
