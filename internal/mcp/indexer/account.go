package indexer

import (
	"context"
	"fmt"
	"strconv"
)

// Subaccount mirrors Comlink's SubaccountResponseObject. The position maps are
// passed through as untyped objects (map[string]any) — MCP tools hand them to
// the agent as-is; v0.2 may type them when the policy engine needs to inspect
// positions for risk caps. map[string]any (not json.RawMessage or a bare any)
// is required so the MCP output schema is a valid object schema — see
// CandlesResponse for the full rationale.
type Subaccount struct {
	Address                    string         `json:"address"`
	SubaccountNumber           int32          `json:"subaccountNumber"`
	Equity                     string         `json:"equity"`
	FreeCollateral             string         `json:"freeCollateral"`
	OpenPerpetualPositions     map[string]any `json:"openPerpetualPositions"`
	AssetPositions             map[string]any `json:"assetPositions"`
	MarginEnabled              bool           `json:"marginEnabled"`
	UpdatedAtHeight            string         `json:"updatedAtHeight"`
	LatestProcessedBlockHeight string         `json:"latestProcessedBlockHeight"`
}

// GetSubaccount fetches GET /v4/addresses/:address/subaccountNumber/:n.
// The endpoint returns SubaccountResponseObject directly (not wrapped).
func (c *Client) GetSubaccount(ctx context.Context, address string, number uint32) (*Subaccount, error) {
	path := "/addresses/" + address + "/subaccountNumber/" + strconv.FormatUint(uint64(number), 10)
	var sa Subaccount
	if err := c.get(ctx, path, nil, &sa); err != nil {
		return nil, fmt.Errorf("GetSubaccount %s/%d: %w", address, number, err)
	}
	// Comlink sends null, not {}, for a position map that is empty. A nil map
	// marshals back to null and fails the reflected output schema's
	// "type":"object", so the tool call errors on any subaccount holding no
	// positions. Normalize to an empty map.
	if sa.OpenPerpetualPositions == nil {
		sa.OpenPerpetualPositions = map[string]any{}
	}
	if sa.AssetPositions == nil {
		sa.AssetPositions = map[string]any{}
	}
	return &sa, nil
}
