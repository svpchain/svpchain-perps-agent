package builder_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	// testOwner, newTestAsm, and the app/config blank-import live in
	// testutil_test.go (same package).
	assettypes "github.com/dydxprotocol/v4-chain/protocol/x/assets/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
)

// testRecipient is a second valid svp bech32 address (distinct from testOwner)
// for bank-send tests.
const testRecipient = "svp1n7rdhntv4w4j30t4g76aavg3k20mdv3zjsn3q5"

func TestBuildBankSend_Happy(t *testing.T) {
	msg, p, err := builder.BuildBankSend(builder.BankSendInput{
		Owner:           testOwner,
		Recipient:       testRecipient,
		Amount:          sdk.NewCoin("asvp", math.NewIntWithDecimal(1, 16)), // 0.01 SVP
		AmountHuman:     "0.01 SVP",
		PayloadClientID: "uuid-send-1",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, testOwner, msg.FromAddress)
	require.Equal(t, testRecipient, msg.ToAddress)
	require.Equal(t, "10000000000000000asvp", msg.Amount.String())
	require.Equal(t, "/cosmos.bank.v1beta1.MsgSend", p.Summary.MsgTypeURL)
	require.Equal(t, "0.01 SVP", p.Summary.AmountHuman)
	require.Equal(t, "asvp", p.Summary.Denom)
	require.Equal(t, testRecipient, p.Summary.RecipientOwner)
	require.False(t, p.IsShortTermCLOB, "a bank send is not a short-term CLOB msg")
	// Non-CLOB txs carry the configured fee.
	require.Len(t, p.Fee.Amount, 1)
	require.Equal(t, testFeeAmount, p.Fee.Amount[0].Amount)
}

func TestBuildBankSend_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		in      builder.BankSendInput
		wantErr string
	}{
		{
			name:    "bad recipient bech32",
			in:      builder.BankSendInput{Owner: testOwner, Recipient: "not-a-bech32", Amount: sdk.NewCoin("asvp", math.NewInt(1)), PayloadClientID: "u"},
			wantErr: "recipient",
		},
		{
			name:    "zero amount",
			in:      builder.BankSendInput{Owner: testOwner, Recipient: testRecipient, Amount: sdk.NewCoin("asvp", math.NewInt(0)), PayloadClientID: "u"},
			wantErr: "positive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := builder.BuildBankSend(tc.in, newTestAsm(t), 1, 1)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildDepositToSubaccount_Happy(t *testing.T) {
	msg, p, err := builder.BuildDepositToSubaccount(builder.DepositToSubaccountInput{
		Owner:           testOwner,
		SubaccountNum:   0,
		HumanUSDC:       "100",
		PayloadClientID: "uuid-dep-1",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, testOwner, msg.Sender)
	require.Equal(t, testOwner, msg.Recipient.Owner)
	require.Equal(t, uint32(0), msg.Recipient.Number)
	require.Equal(t, assettypes.AssetUsdc.Id, msg.AssetId)
	require.EqualValues(t, 100_000_000, msg.Quantums) // 100 USDC * 10^6
	require.NotNil(t, p)
	require.Equal(t, "/dydxprotocol.sending.MsgDepositToSubaccount", p.Summary.MsgTypeURL)
	require.Equal(t, "100", p.Summary.AmountHuman)
	require.False(t, p.IsShortTermCLOB, "funds movements are not short-term CLOB")
}

func TestBuildDepositToSubaccount_FractionalUSDC(t *testing.T) {
	msg, _, err := builder.BuildDepositToSubaccount(builder.DepositToSubaccountInput{
		Owner:           testOwner,
		SubaccountNum:   0,
		HumanUSDC:       "1.500001",
		PayloadClientID: "uuid-dep-2",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.EqualValues(t, 1_500_001, msg.Quantums)
}

func TestBuildWithdrawFromSubaccount_Happy(t *testing.T) {
	msg, p, err := builder.BuildWithdrawFromSubaccount(builder.WithdrawFromSubaccountInput{
		Owner:           testOwner,
		SubaccountNum:   0,
		HumanUSDC:       "50",
		PayloadClientID: "uuid-wd-1",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, testOwner, msg.Sender.Owner)
	require.Equal(t, uint32(0), msg.Sender.Number)
	require.Equal(t, testOwner, msg.Recipient)
	require.Equal(t, assettypes.AssetUsdc.Id, msg.AssetId)
	require.EqualValues(t, 50_000_000, msg.Quantums)
	require.Equal(t, "/dydxprotocol.sending.MsgWithdrawFromSubaccount", p.Summary.MsgTypeURL)
}

func TestBuildWithdrawFromSubaccount_Rejects(t *testing.T) {
	_, _, err := builder.BuildWithdrawFromSubaccount(builder.WithdrawFromSubaccountInput{
		Owner:           testOwner,
		SubaccountNum:   0,
		HumanUSDC:       "0",
		PayloadClientID: "u",
	}, newTestAsm(t), 1, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "> 0")
}

func TestBuildTransferBetweenSubaccounts_Happy(t *testing.T) {
	msg, p, err := builder.BuildTransferBetweenSubaccounts(builder.TransferBetweenSubaccountsInput{
		Owner:                  testOwner,
		SenderSubaccountNum:    0,
		RecipientSubaccountNum: 1,
		HumanUSDC:              "10",
		PayloadClientID:        "uuid-xfer-1",
	}, newTestAsm(t), 7, 17)
	require.NoError(t, err)
	require.Equal(t, testOwner, msg.Transfer.Sender.Owner)
	require.Equal(t, uint32(0), msg.Transfer.Sender.Number)
	require.Equal(t, testOwner, msg.Transfer.Recipient.Owner)
	require.Equal(t, uint32(1), msg.Transfer.Recipient.Number)
	require.EqualValues(t, 10_000_000, msg.Transfer.Amount)
	require.Equal(t, "/dydxprotocol.sending.MsgCreateTransfer", p.Summary.MsgTypeURL)
	require.Equal(t, testOwner, p.Summary.RecipientOwner)
	require.Equal(t, uint32(1), p.Summary.RecipientNum)
}

func TestBuildTransferBetweenSubaccounts_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		in      builder.TransferBetweenSubaccountsInput
		wantErr string
	}{
		{
			name: "sender == recipient (same subaccount num)",
			in: builder.TransferBetweenSubaccountsInput{
				Owner: testOwner, SenderSubaccountNum: 0, RecipientSubaccountNum: 0,
				HumanUSDC: "1", PayloadClientID: "u",
			},
			wantErr: "must differ",
		},
		{
			name: "zero amount",
			in: builder.TransferBetweenSubaccountsInput{
				Owner: testOwner, SenderSubaccountNum: 0, RecipientSubaccountNum: 1,
				HumanUSDC: "0", PayloadClientID: "u",
			},
			wantErr: "> 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := builder.BuildTransferBetweenSubaccounts(tc.in, newTestAsm(t), 1, 1)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildDepositToSubaccount_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		in      builder.DepositToSubaccountInput
		wantErr string
	}{
		{
			name:    "zero amount",
			in:      builder.DepositToSubaccountInput{Owner: testOwner, HumanUSDC: "0", PayloadClientID: "u"},
			wantErr: "> 0",
		},
		{
			name:    "negative amount",
			in:      builder.DepositToSubaccountInput{Owner: testOwner, HumanUSDC: "-1", PayloadClientID: "u"},
			wantErr: "non-negative",
		},
		{
			name:    "too many decimals",
			in:      builder.DepositToSubaccountInput{Owner: testOwner, HumanUSDC: "1.1234567", PayloadClientID: "u"},
			wantErr: "more than 6",
		},
		{
			name:    "invalid owner bech32",
			in:      builder.DepositToSubaccountInput{Owner: "not-a-bech32", HumanUSDC: "1", PayloadClientID: "u"},
			wantErr: "ValidateBasic",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := builder.BuildDepositToSubaccount(tc.in, newTestAsm(t), 1, 1)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
