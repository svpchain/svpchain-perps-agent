package owner

import (
	"fmt"

	sdkmath "cosmossdk.io/math"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/cosmos/gogoproto/proto"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/chain"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
)

// FeeSpec is the fee stamped onto the registration transaction, mirroring the
// [fee] table the payload assembler uses.
type FeeSpec struct {
	Denom    string
	Amount   string
	GasLimit uint64
}

// SignTx builds and signs a transaction in one step: Any-packs msgs into a
// TxBody, builds a SIGN_MODE_DIRECT AuthInfo, signs with priv, and returns the
// marshaled TxRaw ready for BroadcastSync.
//
// It exists because signer.Sign — the client-side payload signer — always
// stamps an EMPTY fee. That is right for its caller: short-term CLOB orders
// are gas-free on svpchain. Registry lifecycle messages are not, and a tx
// whose declared fee is missing from AuthInfo is rejected outright, so this
// path always stamps one. There is deliberately no gas-free branch here — no
// message this package signs qualifies for it.
func SignTx(
	priv *ethsecp256k1.PrivKey,
	chainID string,
	acct chain.AccountInfo,
	msgs []sdk.Msg,
	fee FeeSpec,
) ([]byte, error) {
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no messages to sign")
	}

	anys := make([]*codectypes.Any, 0, len(msgs))
	for i, m := range msgs {
		a, err := codectypes.NewAnyWithValue(m)
		if err != nil {
			return nil, fmt.Errorf("pack msg[%d]: %w", i, err)
		}
		anys = append(anys, a)
	}
	bodyBytes, err := proto.Marshal(&txtypes.TxBody{Messages: anys})
	if err != nil {
		return nil, fmt.Errorf("marshal TxBody: %w", err)
	}

	amt, ok := sdkmath.NewIntFromString(fee.Amount)
	if !ok {
		return nil, fmt.Errorf("fee amount %q is not a valid integer", fee.Amount)
	}
	var feeCoins []sdk.Coin
	if !amt.IsZero() {
		feeCoins = []sdk.Coin{{Denom: fee.Denom, Amount: amt}}
	}

	pubAny, err := codectypes.NewAnyWithValue(priv.PubKey())
	if err != nil {
		return nil, fmt.Errorf("pack pubkey: %w", err)
	}
	authInfo := &txtypes.AuthInfo{
		SignerInfos: []*txtypes.SignerInfo{{
			PublicKey: pubAny,
			ModeInfo: &txtypes.ModeInfo{
				Sum: &txtypes.ModeInfo_Single_{
					Single: &txtypes.ModeInfo_Single{Mode: signing.SignMode_SIGN_MODE_DIRECT},
				},
			},
			Sequence: acct.Sequence,
		}},
		Fee: &txtypes.Fee{Amount: feeCoins, GasLimit: fee.GasLimit},
	}
	authInfoBytes, err := proto.Marshal(authInfo)
	if err != nil {
		return nil, fmt.Errorf("marshal AuthInfo: %w", err)
	}

	// Shared with the remote MCP server so both sides agree on the byte layout.
	signBytes, err := payload.DirectSignBytes(bodyBytes, authInfoBytes, chainID, acct.AccountNumber)
	if err != nil {
		return nil, fmt.Errorf("compute sign-bytes: %w", err)
	}
	sig, err := priv.Sign(signBytes)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	txRaw, err := proto.Marshal(&txtypes.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		Signatures:    [][]byte{sig},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal TxRaw: %w", err)
	}
	return txRaw, nil
}
