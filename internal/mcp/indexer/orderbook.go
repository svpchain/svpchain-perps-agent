package indexer

import (
	"context"
	"fmt"
)

// OrderbookPriceLevel is one resting level on the book.
type OrderbookPriceLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// Orderbook mirrors Comlink's OrderbookResponseObject.
type Orderbook struct {
	Bids []OrderbookPriceLevel `json:"bids"`
	Asks []OrderbookPriceLevel `json:"asks"`
}

// GetOrderbook fetches GET /v4/orderbooks/perpetualMarket/:ticker.
func (c *Client) GetOrderbook(ctx context.Context, ticker string) (*Orderbook, error) {
	var ob Orderbook
	if err := c.get(ctx, "/orderbooks/perpetualMarket/"+ticker, nil, &ob); err != nil {
		return nil, fmt.Errorf("GetOrderbook %q: %w", ticker, err)
	}
	// Non-nil so an empty side marshals to [] not null (fails "type":"array").
	if ob.Bids == nil {
		ob.Bids = []OrderbookPriceLevel{}
	}
	if ob.Asks == nil {
		ob.Asks = []OrderbookPriceLevel{}
	}
	return &ob, nil
}
