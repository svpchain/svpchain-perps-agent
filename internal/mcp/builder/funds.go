package builder

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	assettypes "github.com/dydxprotocol/v4-chain/protocol/x/assets/types"
	sendingtypes "github.com/dydxprotocol/v4-chain/protocol/x/sending/types"
	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/limits"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
)

// -- build_deposit_to_subaccount ---------------------------------------

// DepositToSubaccountInput drives build_deposit_to_subaccount. svpchain's
// sending module only accepts USDC (see ErrNonUsdcAssetTransferNotImplemented
// in x/sending/types), so the asset is implicit — the input carries only the
// human USDC amount.
type DepositToSubaccountInput struct {
	// Owner is the bech32 bank-account sender. The recipient subaccount
	// belongs to the same owner; per-tenant subaccount allowlist enforcement
	// happens in the tool handler, not here.
	Owner         string
	SubaccountNum uint32

	HumanUSDC string // e.g. "100", "1.5"

	PayloadClientID string
}

// BuildDepositToSubaccount turns a DepositToSubaccountInput into a
// MsgDepositToSubaccount and an envelope TxPayload. The human USDC string is
// converted to quantums via limits.HumanToQuantums (10^6 = 1 USDC).
//
// Cap enforcement is the tool handler's responsibility — keeping the builder
// cap-agnostic mirrors how policy / subaccount checks live in handlers
// (see tools/trade.go).
func BuildDepositToSubaccount(
	in DepositToSubaccountInput,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*sendingtypes.MsgDepositToSubaccount, *payload.TxPayload, error) {
	quantums, err := limits.HumanToQuantums(in.HumanUSDC)
	if err != nil {
		return nil, nil, fmt.Errorf("human_usdc: %w", err)
	}
	if quantums == 0 {
		return nil, nil, fmt.Errorf("human_usdc must be > 0")
	}

	msg := sendingtypes.NewMsgDepositToSubaccount(
		in.Owner,
		satypes.SubaccountId{Owner: in.Owner, Number: in.SubaccountNum},
		assettypes.AssetUsdc.Id,
		quantums,
	)
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgDepositToSubaccount.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:    "build_deposit_to_subaccount",
		MsgTypeURL:  "/dydxprotocol.sending.MsgDepositToSubaccount",
		Subaccount:  payload.SubaccountRef{Owner: in.Owner, Number: in.SubaccountNum},
		AssetID:     assettypes.AssetUsdc.Id,
		AmountHuman: in.HumanUSDC,
	}
	txPayload, err := asm.Assemble(Args{
		Msgs:          []sdk.Msg{msg},
		SignerAddress: in.Owner,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		ClientID:      in.PayloadClientID,
		Summary:       summary,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assemble: %w", err)
	}
	return msg, txPayload, nil
}

// -- build_transfer_between_subaccounts (same-owner only) --------------

// TransferBetweenSubaccountsInput drives build_transfer_between_subaccounts.
// v0.2.3 deliberately restricts this to same-owner transfers: the
// recipient's owner field is implicit (equal to in.Owner), so the API
// can't be used to move funds to a third party until a future version
// adds the cap surface (allowlist + per-recipient ledger).
type TransferBetweenSubaccountsInput struct {
	Owner string

	// Sender + Recipient subaccount numbers under the same Owner. Must be
	// distinct or the chain rejects with ErrSenderSameAsRecipient.
	SenderSubaccountNum    uint32
	RecipientSubaccountNum uint32

	HumanUSDC string

	PayloadClientID string
}

// BuildTransferBetweenSubaccounts constructs a MsgCreateTransfer moving
// USDC from one subaccount to another under the same owner. Cap
// enforcement (per-tx only — no daily cap since funds stay in the
// tenant's books) is the handler's responsibility.
func BuildTransferBetweenSubaccounts(
	in TransferBetweenSubaccountsInput,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*sendingtypes.MsgCreateTransfer, *payload.TxPayload, error) {
	if in.SenderSubaccountNum == in.RecipientSubaccountNum {
		return nil, nil, fmt.Errorf("sender_subaccount_number must differ from recipient_subaccount_number")
	}
	quantums, err := limits.HumanToQuantums(in.HumanUSDC)
	if err != nil {
		return nil, nil, fmt.Errorf("human_usdc: %w", err)
	}
	if quantums == 0 {
		return nil, nil, fmt.Errorf("human_usdc must be > 0")
	}

	transfer := &sendingtypes.Transfer{
		Sender:    satypes.SubaccountId{Owner: in.Owner, Number: in.SenderSubaccountNum},
		Recipient: satypes.SubaccountId{Owner: in.Owner, Number: in.RecipientSubaccountNum},
		AssetId:   assettypes.AssetUsdc.Id,
		Amount:    quantums,
	}
	msg := sendingtypes.NewMsgCreateTransfer(transfer)
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgCreateTransfer.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:       "build_transfer_between_subaccounts",
		MsgTypeURL:     "/dydxprotocol.sending.MsgCreateTransfer",
		Subaccount:     payload.SubaccountRef{Owner: in.Owner, Number: in.SenderSubaccountNum},
		AssetID:        assettypes.AssetUsdc.Id,
		AmountHuman:    in.HumanUSDC,
		RecipientOwner: in.Owner,
		RecipientNum:   in.RecipientSubaccountNum,
	}
	txPayload, err := asm.Assemble(Args{
		Msgs:          []sdk.Msg{msg},
		SignerAddress: in.Owner,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		ClientID:      in.PayloadClientID,
		Summary:       summary,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assemble: %w", err)
	}
	return msg, txPayload, nil
}

// -- build_bank_send (generic x/bank MsgSend) --------------------------

// BankSendInput drives build_bank_send. Unlike the subaccount funds tools
// (USDC-only, same-owner), this is a generic cosmos x/bank send: any denom, to
// an arbitrary recipient. The handler resolves the human/base amount into the
// Amount coin; the builder just constructs the MsgSend + envelope.
type BankSendInput struct {
	Owner     string   // bech32 sender (the tenant owner)
	Recipient string   // bech32 recipient (may be a third party)
	Amount    sdk.Coin // resolved denom + base-unit amount

	// AmountHuman is the display string for the summary (e.g. "0.01 SVP");
	// informational only.
	AmountHuman string

	PayloadClientID string
}

// BuildBankSend constructs a bank MsgSend from Owner to Recipient and assembles
// it into a TxPayload. The fee is stamped by the assembler (a send is not a CLOB
// msg). No cap is enforced — the signer==owner guard at broadcast already
// ensures funds only leave the tenant's own wallet.
func BuildBankSend(
	in BankSendInput,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*banktypes.MsgSend, *payload.TxPayload, error) {
	if !in.Amount.IsValid() || !in.Amount.IsPositive() {
		return nil, nil, fmt.Errorf("amount must be a positive coin")
	}
	if _, err := sdk.AccAddressFromBech32(in.Owner); err != nil {
		return nil, nil, fmt.Errorf("owner: %w", err)
	}
	if _, err := sdk.AccAddressFromBech32(in.Recipient); err != nil {
		return nil, nil, fmt.Errorf("recipient: %w", err)
	}

	msg := &banktypes.MsgSend{
		FromAddress: in.Owner,
		ToAddress:   in.Recipient,
		Amount:      sdk.NewCoins(in.Amount),
	}

	summary := payload.Summary{
		ToolName:       "build_bank_send",
		MsgTypeURL:     "/cosmos.bank.v1beta1.MsgSend",
		AmountHuman:    in.AmountHuman,
		RecipientOwner: in.Recipient,
		Denom:          in.Amount.Denom,
	}
	txPayload, err := asm.Assemble(Args{
		Msgs:          []sdk.Msg{msg},
		SignerAddress: in.Owner,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		ClientID:      in.PayloadClientID,
		Summary:       summary,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assemble: %w", err)
	}
	return msg, txPayload, nil
}

// -- build_withdraw_from_subaccount ------------------------------------

// WithdrawFromSubaccountInput drives build_withdraw_from_subaccount.
// Mirror of DepositToSubaccountInput with the bank/subaccount roles
// reversed. USDC-only, same rationale.
type WithdrawFromSubaccountInput struct {
	// Owner is both the subaccount sender (Owner+SubaccountNum) and the
	// bech32 bank-account recipient — withdraw moves funds out of the
	// owner's subaccount back into the owner's bank balance.
	Owner         string
	SubaccountNum uint32

	HumanUSDC string

	PayloadClientID string
}

// BuildWithdrawFromSubaccount constructs a MsgWithdrawFromSubaccount +
// TxPayload. As with the deposit builder, cap enforcement happens in the
// tool handler, not here.
func BuildWithdrawFromSubaccount(
	in WithdrawFromSubaccountInput,
	asm *Assembler,
	accountNumber, sequence uint64,
) (*sendingtypes.MsgWithdrawFromSubaccount, *payload.TxPayload, error) {
	quantums, err := limits.HumanToQuantums(in.HumanUSDC)
	if err != nil {
		return nil, nil, fmt.Errorf("human_usdc: %w", err)
	}
	if quantums == 0 {
		return nil, nil, fmt.Errorf("human_usdc must be > 0")
	}

	msg := sendingtypes.NewMsgWithdrawFromSubaccount(
		satypes.SubaccountId{Owner: in.Owner, Number: in.SubaccountNum},
		in.Owner,
		assettypes.AssetUsdc.Id,
		quantums,
	)
	if err := msg.ValidateBasic(); err != nil {
		return nil, nil, fmt.Errorf("MsgWithdrawFromSubaccount.ValidateBasic: %w", err)
	}

	summary := payload.Summary{
		ToolName:    "build_withdraw_from_subaccount",
		MsgTypeURL:  "/dydxprotocol.sending.MsgWithdrawFromSubaccount",
		Subaccount:  payload.SubaccountRef{Owner: in.Owner, Number: in.SubaccountNum},
		AssetID:     assettypes.AssetUsdc.Id,
		AmountHuman: in.HumanUSDC,
	}
	txPayload, err := asm.Assemble(Args{
		Msgs:          []sdk.Msg{msg},
		SignerAddress: in.Owner,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		ClientID:      in.PayloadClientID,
		Summary:       summary,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assemble: %w", err)
	}
	return msg, txPayload, nil
}
