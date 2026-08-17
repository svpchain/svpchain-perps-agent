package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// SparklinesResponse is Comlink's SparklineResponseObject — a flat
// ticker → string-array map (each string is a price tick).
type SparklinesResponse map[string][]string

// GetSparklines fetches GET /v4/sparklines?timePeriod=...
// (timePeriod = "ONE_DAY" | "SEVEN_DAYS"; empty for the indexer default).
func (c *Client) GetSparklines(ctx context.Context, timePeriod string) (SparklinesResponse, error) {
	q := url.Values{}
	if timePeriod != "" {
		q.Set("timePeriod", timePeriod)
	}
	var resp SparklinesResponse
	if err := c.get(ctx, "/sparklines", q, &resp); err != nil {
		return nil, fmt.Errorf("GetSparklines: %w", err)
	}
	// Non-nil map so an empty result marshals to {} not null; and non-nil value
	// slices so a per-ticker null marshals to [] — both fail the reflected schema.
	if resp == nil {
		resp = SparklinesResponse{}
	}
	for k, v := range resp {
		if v == nil {
			resp[k] = []string{}
		}
	}
	return resp, nil
}
