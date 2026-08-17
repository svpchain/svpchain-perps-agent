package payload

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
)

// DirectSignBytes returns the SIGN_MODE_DIRECT sign-bytes for a Cosmos tx:
// the proto-marshaled cosmos.tx.v1beta1.SignDoc carrying body bytes,
// auth-info bytes, chain id, and account number.
//
// This is the same byte sequence the chain's own signing path produces —
// see cmd/dex-bench/cosmos_signing.go:60-75 for the standard-SDK helper
// (tx.SignWithPrivKey) call — but here we stop short of producing the
// signature so the local signer can sign these bytes locally and round-trip
// the result back into broadcast_signed_tx.
func DirectSignBytes(bodyBytes, authInfoBytes []byte, chainID string, accountNumber uint64) ([]byte, error) {
	signDoc := &tx.SignDoc{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		ChainId:       chainID,
		AccountNumber: accountNumber,
	}
	out, err := proto.Marshal(signDoc)
	if err != nil {
		return nil, fmt.Errorf("marshal SignDoc: %w", err)
	}
	return out, nil
}
