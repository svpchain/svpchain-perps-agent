package auth

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/crypto"

	appconfig "github.com/dydxprotocol/v4-chain/protocol/app/config"
)

func init() {
	// Auth recovery returns sdk.AccAddress strings; without the svp bech32
	// prefix in place we'd hand back "cosmos1…" addresses and silently
	// mismatch every verify. Same idiom as internal/mcp/signer/signer.go init().
	appconfig.SetAddressPrefixes()
}

// RecoverOwner extracts the bech32 svp address that signed message. The
// signature is the 65-byte eth_secp256k1 form (R||S||V) the signer
// returns from sign_challenge — go-ethereum's crypto.SigToPub handles
// the keccak256 hashing + secp256k1 recovery; we then derive the
// ethereum-style address from the recovered pubkey and bech32-encode it
// with the svp prefix (matches signer.DeriveAddress on the signer side
// so the two ends compare equal).
func RecoverOwner(message string, signature []byte) (string, error) {
	if len(signature) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes (got %d)", len(signature))
	}
	hash := crypto.Keccak256([]byte(message))
	pub, err := crypto.SigToPub(hash, signature)
	if err != nil {
		return "", fmt.Errorf("recover pubkey: %w", err)
	}
	ethAddr := crypto.PubkeyToAddress(*pub)
	return sdk.AccAddress(ethAddr.Bytes()).String(), nil
}
