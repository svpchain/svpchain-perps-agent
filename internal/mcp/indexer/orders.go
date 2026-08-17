package indexer

import (
	"context"
	"fmt"
	"net/url"
)

// Order is one Comlink OrderResponseObject. v0.2.1 keeps it untyped — the order
// shape has many optional fields and the agent can read them as-is; v0.2.2 will
// type the subset the builder needs. It is map[string]any (not json.RawMessage
// or a bare any) so the MCP output schema is a valid object schema — see
// CandlesResponse.
type Order = map[string]any

// GetOrders fetches GET /v4/orders?address=&subaccountNumber=&... Returns
// a JSON array of orders (not a wrapped object — see orders-controller.ts:216,218).
func (c *Client) GetOrders(ctx context.Context, address string, subaccountNumber uint32, filters map[string]string) ([]Order, error) {
	q := url.Values{}
	q.Set("address", address)
	q.Set("subaccountNumber", fmt.Sprintf("%d", subaccountNumber))
	for k, v := range filters {
		if v != "" {
			q.Set(k, v)
		}
	}
	var out []Order
	if err := c.get(ctx, "/orders", q, &out); err != nil {
		return nil, fmt.Errorf("GetOrders %s/%d: %w", address, subaccountNumber, err)
	}
	return out, nil
}

// GetOrder fetches GET /v4/orders/:orderId.
func (c *Client) GetOrder(ctx context.Context, orderID string) (Order, error) {
	var out Order
	if err := c.get(ctx, "/orders/"+orderID, nil, &out); err != nil {
		return nil, fmt.Errorf("GetOrder %s: %w", orderID, err)
	}
	return out, nil
}
