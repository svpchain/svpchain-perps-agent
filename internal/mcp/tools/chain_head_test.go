package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/chain"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/policy"
)

// stubComet answers /status from a fixed head; TxStatus is not exercised.
type stubComet struct {
	status chain.ChainStatus
	err    error
}

func (s *stubComet) TxStatus(context.Context, string) (chain.TxStatus, error) {
	return chain.TxStatus{}, errors.New("not used")
}

func (s *stubComet) Status(context.Context) (chain.ChainStatus, error) {
	return s.status, s.err
}

func headFixture(t *testing.T, comet chain.CometBftClient) (*Handlers, context.Context) {
	t.Helper()
	const tenantID = "t1"
	h := &Handlers{Deps: Deps{
		Chain:     ChainDeps{CometBft: comet},
		Policy:    policy.NewEngine([]policy.TenantPolicy{{TenantID: tenantID, Owner: "svp1owner"}}),
		RateLimit: policy.NewRateLimiter(100, 100),
		// Indexer deliberately nil: these two tools must not touch it.
	}}
	return h, WithTenant(context.Background(), TenantContext{TenantID: tenantID, Owner: "svp1owner"})
}

// get_height and get_time come from the chain head, not the indexer — the
// Indexer dep is nil here and the handlers must not notice.
func TestHeightAndTimeReadTheChainHead(t *testing.T) {
	head := time.Date(2026, 8, 30, 10, 32, 54, 282_000_000, time.FixedZone("x", 8*3600))
	h, ctx := headFixture(t, &stubComet{status: chain.ChainStatus{LatestBlockHeight: 123456, LatestBlockTime: head}})

	_, height, err := h.GetHeight(ctx, nil, GetHeightInput{})
	require.NoError(t, err)
	require.Equal(t, "123456", height.Height.Height)
	require.Equal(t, "2026-08-30T02:32:54.282Z", height.Height.Time, "block time is reported in UTC, RFC3339")

	_, now, err := h.GetTime(ctx, nil, GetTimeInput{})
	require.NoError(t, err)
	require.Equal(t, "2026-08-30T02:32:54.282Z", now.Time.ISO)
	require.InDelta(t, float64(head.UnixMilli())/1000, now.Time.Epoch, 0.0005)
}

func TestHeightSurfacesTheRPCError(t *testing.T) {
	h, ctx := headFixture(t, &stubComet{err: errors.New("connection refused")})
	_, _, err := h.GetHeight(ctx, nil, GetHeightInput{})
	require.ErrorContains(t, err, "connection refused")
	_, _, err = h.GetTime(ctx, nil, GetTimeInput{})
	require.ErrorContains(t, err, "connection refused")
}
