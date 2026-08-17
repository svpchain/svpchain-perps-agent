package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"sync"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	assettypes "github.com/dydxprotocol/v4-chain/protocol/x/assets/types"
	sendingtypes "github.com/dydxprotocol/v4-chain/protocol/x/sending/types"
	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/chain"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/limits"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/mcpcodec"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/policy"
)

// These tests exercise broadcast_signed_tx's daily-cap enforcement at the
// handler level: that concurrent broadcasts cannot collectively overshoot a
// cap, and that a tx the chain refuses gives its reservation back.

// stubBroadcast is a chain.BroadcastClient returning a fixed result. code 0 is
// an accepted tx; a non-zero code is a CheckTx rejection.
// A delay stands in for the round-trip to the node: it is the window between
// the cap check and the spend being recorded, and it is what lets concurrent
// requests actually overlap inside the handler.
type stubBroadcast struct {
	code  uint32
	delay time.Duration
}

func (s *stubBroadcast) BroadcastSync(context.Context, []byte) (chain.BroadcastResult, error) {
	time.Sleep(s.delay)
	return chain.BroadcastResult{TxHash: "DEADBEEF", Code: s.code, RawLog: ""}, nil
}

// capsFixture wires a Handlers whose broadcast path enforces the withdraw and
// transfer-out caps, plus the tenant ctx and the owner address the signed txs
// must carry. The owner is derived from a generated key so the handler's
// signer-matches-tenant check passes.
type capsFixture struct {
	h      *Handlers
	ctx    context.Context
	owner  string
	pubKey *codectypes.Any
	ledger *limits.MemoryLedger
	xfer   *limits.MemoryTransferOutStore
}

func newCapsFixture(t *testing.T, cfg limits.Config, code uint32, delay time.Duration) *capsFixture {
	t.Helper()
	const tenantID = "t1"

	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey()
	owner := sdk.AccAddress(pub.Address()).String()
	pubAny, err := codectypes.NewAnyWithValue(pub)
	require.NoError(t, err)

	ledger := limits.NewMemoryLedger(cfg.DailyWithdrawCapUSDC, time.Now)
	xfer := limits.NewMemoryTransferOutStore(time.Now)

	h := &Handlers{Deps: Deps{
		Chain:             ChainDeps{Broadcast: &stubBroadcast{code: code, delay: delay}},
		InterfaceRegistry: mcpcodec.GetEncodingConfig().InterfaceRegistry,
		Policy:            policy.NewEngine([]policy.TenantPolicy{{TenantID: tenantID, Owner: owner}}),
		// Generous limits: these tests fire many requests at once and must not
		// trip the rate limiter instead of the cap under test.
		RateLimit:      policy.NewRateLimiter(10_000, 10_000),
		Idempotency:    policy.NewIdempotency(time.Minute),
		Auditor:        policy.NewAuditor(io.Discard),
		Limits:         cfg,
		WithdrawLedger: ledger,
		TransferOut:    xfer,
	}}
	return &capsFixture{
		h:      h,
		ctx:    WithTenant(context.Background(), TenantContext{TenantID: tenantID, Owner: owner}),
		owner:  owner,
		pubKey: pubAny,
		ledger: ledger,
		xfer:   xfer,
	}
}

// signedTxRaw wraps msgs in a TxRaw whose AuthInfo names the fixture's key as
// the sole signer — enough for the handler, which checks the signer address
// but does not verify the signature.
func (f *capsFixture) signedTxRaw(t *testing.T, msgs ...proto.Message) payload.SignedTx {
	t.Helper()
	anyMsgs := make([]*codectypes.Any, 0, len(msgs))
	for _, m := range msgs {
		a, err := codectypes.NewAnyWithValue(m)
		require.NoError(t, err)
		anyMsgs = append(anyMsgs, a)
	}
	bodyBytes, err := proto.Marshal(&txtypes.TxBody{Messages: anyMsgs})
	require.NoError(t, err)
	authInfoBytes, err := proto.Marshal(&txtypes.AuthInfo{
		SignerInfos: []*txtypes.SignerInfo{{PublicKey: f.pubKey}},
	})
	require.NoError(t, err)
	rawBytes, err := proto.Marshal(&txtypes.TxRaw{BodyBytes: bodyBytes, AuthInfoBytes: authInfoBytes})
	require.NoError(t, err)
	return payload.SignedTx{TxRawBytesB64: base64.StdEncoding.EncodeToString(rawBytes)}
}

func (f *capsFixture) withdraw(quantums uint64) *sendingtypes.MsgWithdrawFromSubaccount {
	return sendingtypes.NewMsgWithdrawFromSubaccount(
		satypes.SubaccountId{Owner: f.owner, Number: 0},
		f.owner, assettypes.AssetUsdc.Id, quantums,
	)
}

func (f *capsFixture) bankSendUSDC(base int64) *banktypes.MsgSend {
	return &banktypes.MsgSend{
		FromAddress: f.owner,
		ToAddress:   sdk.AccAddress(make([]byte, 20)).String(),
		Amount:      sdk.NewCoins(sdk.NewCoin(assettypes.UusdcDenom, sdkmath.NewInt(base))),
	}
}

func TestBroadcastSignedTx_ConcurrentWithdrawsCannotOvershootDailyCap(t *testing.T) {
	// The race: N withdraws for the whole daily cap arrive at once. A
	// read-then-record check lets every one of them observe full headroom and
	// pass, overshooting the cap N×. The reservation must let exactly one
	// through.
	const goroutines = 16
	const capUSDC = 5_000
	cfg := limits.Config{WithdrawMaxUSDC: 10_000, DailyWithdrawCapUSDC: capUSDC}
	f := newCapsFixture(t, cfg, 0, 25*time.Millisecond)

	full := uint64(capUSDC) * 1_000_000
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			_, _, err := f.h.BroadcastSignedTx(f.ctx, nil, BroadcastSignedTxInput{
				ClientID: fmt.Sprintf("cid-%d", i), // distinct: idempotency must not mask the race
				SignedTx: f.signedTxRaw(t, f.withdraw(full)),
			})
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 1, accepted, "only one full-cap withdraw may reach the chain")
	require.EqualValues(t, 0, f.ledger.Remaining("t1"))
}

func TestBroadcastSignedTx_RejectedTxReleasesReservations(t *testing.T) {
	// A tx the chain refuses must not consume cap: both the withdraw ledger
	// and the transfer-out total are handed back, so a retry still fits.
	cfg := limits.Config{WithdrawMaxUSDC: 10_000, DailyWithdrawCapUSDC: 5_000}
	f := newCapsFixture(t, cfg, 11, 0) // non-zero CheckTx code = rejected
	f.xfer.SetCap(f.owner, limits.SymbolCap{Symbol: "usdc", Decimals: 6, CapBase: big.NewInt(500_000_000)})

	_, out, err := f.h.BroadcastSignedTx(f.ctx, nil, BroadcastSignedTxInput{
		ClientID: "cid-rejected",
		SignedTx: f.signedTxRaw(t, f.withdraw(5_000_000_000), f.bankSendUSDC(500_000_000)),
	})
	require.NoError(t, err) // a CheckTx rejection surfaces as a result code, not an error
	require.EqualValues(t, 11, out.Result.Code)

	require.EqualValues(t, uint64(5_000)*1_000_000, f.ledger.Remaining("t1"), "rejected tx must not eat withdraw cap")
	require.Equal(t, "0", f.xfer.Used(f.owner, "usdc").String(), "rejected tx must not eat transfer-out cap")
}

func TestBroadcastSignedTx_AcceptedTxKeepsReservations(t *testing.T) {
	// The mirror of the release test: once the chain takes the tx, both caps
	// stay spent.
	cfg := limits.Config{WithdrawMaxUSDC: 10_000, DailyWithdrawCapUSDC: 5_000}
	f := newCapsFixture(t, cfg, 0, 0)
	f.xfer.SetCap(f.owner, limits.SymbolCap{Symbol: "usdc", Decimals: 6, CapBase: big.NewInt(500_000_000)})

	_, out, err := f.h.BroadcastSignedTx(f.ctx, nil, BroadcastSignedTxInput{
		ClientID: "cid-accepted",
		SignedTx: f.signedTxRaw(t, f.withdraw(2_000_000_000), f.bankSendUSDC(100_000_000)),
	})
	require.NoError(t, err)
	require.EqualValues(t, 0, out.Result.Code)

	require.EqualValues(t, uint64(3_000)*1_000_000, f.ledger.Remaining("t1"))
	require.Equal(t, "100000000", f.xfer.Used(f.owner, "usdc").String())
}

func TestBroadcastSignedTx_BankSendOverCapRejectedWithoutSpending(t *testing.T) {
	// The transfer-out cap rejects before the wire, and the refused amount is
	// not counted against the day.
	cfg := limits.Config{}
	f := newCapsFixture(t, cfg, 0, 0)
	f.xfer.SetCap(f.owner, limits.SymbolCap{Symbol: "usdc", Decimals: 6, CapBase: big.NewInt(100_000_000)})

	_, _, err := f.h.BroadcastSignedTx(f.ctx, nil, BroadcastSignedTxInput{
		ClientID: "cid-over-cap",
		SignedTx: f.signedTxRaw(t, f.bankSendUSDC(150_000_000)),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "daily_transfer_out_cap exceeded")
	require.Equal(t, "0", f.xfer.Used(f.owner, "usdc").String())
}
