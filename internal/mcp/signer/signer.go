package signer

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/cosmos/gogoproto/proto"

	appconfig "github.com/dydxprotocol/v4-chain/protocol/app/config"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/payload"
)

func init() {
	// Set the svp bech32 prefix so any sdk.AccAddress stringification
	// matches the chain. Importing this package is sufficient — no caller
	// needs its own blank import of app/config.
	appconfig.SetAddressPrefixes()
}

// ParsePrivKey decodes a 32-byte eth_secp256k1 private key from a hex
// string. A leading "0x" is tolerated; surrounding whitespace is trimmed.
func ParsePrivKey(s string) (*ethsecp256k1.PrivKey, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	bz, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	if len(bz) != 32 {
		return nil, fmt.Errorf("private key must be 32 bytes (got %d)", len(bz))
	}
	return &ethsecp256k1.PrivKey{Key: bz}, nil
}

// DeriveAddress returns the bech32 string address derived from priv's
// public key, with the svp prefix in place.
func DeriveAddress(priv *ethsecp256k1.PrivKey) string {
	return sdk.AccAddress(priv.PubKey().Address()).String()
}

// Sign turns p into a SignedTx by building an AuthInfo with the signer's
// pubkey, computing the SIGN_MODE_DIRECT sign-bytes via
// payload.DirectSignBytes (shared with the remote MCP server so both
// sides agree on the byte layout), signing with priv, and proto-marshaling
// a TxRaw.
//
// Cross-checks:
//   - p.Version must equal payload.CurrentVersion.
//   - If p.SignerAddress is non-empty, it must equal the key-derived
//     address; an empty p.SignerAddress is tolerated for ad-hoc demos
//     (the caller is responsible for the address mismatch downstream).
func Sign(priv *ethsecp256k1.PrivKey, p *payload.TxPayload) (*payload.SignedTx, error) {
	if p.Version != payload.CurrentVersion {
		return nil, fmt.Errorf("unsupported TxPayload version %d (want %d)", p.Version, payload.CurrentVersion)
	}

	pub := priv.PubKey()
	signerAddr := sdk.AccAddress(pub.Address()).String()
	if p.SignerAddress != "" && signerAddr != p.SignerAddress {
		return nil, fmt.Errorf("key-derived signer address %s does not match payload.signer_address %s",
			signerAddr, p.SignerAddress)
	}

	accNum, err := strconv.ParseUint(p.AccountNumber, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse account_number %q: %w", p.AccountNumber, err)
	}
	seq, err := strconv.ParseUint(p.Sequence, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse sequence %q: %w", p.Sequence, err)
	}
	gasLimit, err := strconv.ParseUint(p.Fee.GasLimit, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse fee.gas_limit %q: %w", p.Fee.GasLimit, err)
	}

	pubAny, err := codectypes.NewAnyWithValue(pub)
	if err != nil {
		return nil, fmt.Errorf("wrap pubkey in Any: %w", err)
	}

	authInfo := &txtypes.AuthInfo{
		SignerInfos: []*txtypes.SignerInfo{{
			PublicKey: pubAny,
			ModeInfo: &txtypes.ModeInfo{
				Sum: &txtypes.ModeInfo_Single_{
					Single: &txtypes.ModeInfo_Single{
						Mode: signing.SignMode_SIGN_MODE_DIRECT,
					},
				},
			},
			Sequence: seq,
		}},
		Fee: &txtypes.Fee{
			Amount:   nil, // CLOB: zero fee
			GasLimit: gasLimit,
		},
	}
	authInfoBytes, err := proto.Marshal(authInfo)
	if err != nil {
		return nil, fmt.Errorf("marshal AuthInfo: %w", err)
	}

	// TxPayload's TxBodyBytesB64 is a base64-encoded string on the wire
	// (see the comment on payload.TxPayload). Decode it before signing.
	bodyBytes, err := base64.StdEncoding.DecodeString(p.TxBodyBytesB64)
	if err != nil {
		return nil, fmt.Errorf("decode tx_body_bytes_b64: %w", err)
	}
	signBytes, err := payload.DirectSignBytes(bodyBytes, authInfoBytes, p.ChainID, accNum)
	if err != nil {
		return nil, fmt.Errorf("compute sign-bytes: %w", err)
	}
	sig, err := priv.Sign(signBytes)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	txRaw := &txtypes.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		Signatures:    [][]byte{sig},
	}
	txRawBytes, err := proto.Marshal(txRaw)
	if err != nil {
		return nil, fmt.Errorf("marshal TxRaw: %w", err)
	}

	return &payload.SignedTx{
		TxRawBytesB64: base64.StdEncoding.EncodeToString(txRawBytes),
		SignatureB64:  base64.StdEncoding.EncodeToString(sig),
		PubKeyB64:     base64.StdEncoding.EncodeToString(pub.Bytes()),
	}, nil
}
