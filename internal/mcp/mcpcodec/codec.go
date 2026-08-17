// Package mcpcodec provides the interface registry / codec the MCP server
// needs to unpack accounts and encode transaction payloads, WITHOUT importing
// the top-level protocol `app` package.
//
// The chain's app.GetEncodingConfig() would serve the same purpose, but
// importing `app` transitively drags in the entire application (every module
// keeper, the module manager, ante handlers) — thousands of packages the
// stateless MCP server never uses. This package reproduces the same registry
// from the two keeper-free building blocks the app itself uses:
//   - app/module.NewInterfaceRegistry — the svpchain interface registry with
//     the chain's custom message signers.
//   - app/basic_manager.ModuleBasics  — the per-module interface registration.
//
// It mirrors app/encoding.go's makeEncodingConfig + initEncodingConfig, with
// one deliberate hardening: the registry is built with the `svp` bech32
// prefixes passed explicitly, rather than read from the global sdk config at
// package-init time. app/module and app/basic_manager do not import app/config,
// so the global prefix is not guaranteed to be set when their init() runs;
// building the registry here (called at server-wiring time, after all package
// inits) with explicit prefixes removes that ordering fragility.
package mcpcodec

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	evmcryptocodec "github.com/cosmos/evm/crypto/codec"
	"github.com/cosmos/evm/crypto/ethsecp256k1"

	basicmanager "github.com/dydxprotocol/v4-chain/protocol/app/basic_manager"
	appconfig "github.com/dydxprotocol/v4-chain/protocol/app/config"
	custommodule "github.com/dydxprotocol/v4-chain/protocol/app/module"
)

// EncodingConfig mirrors app.EncodingConfig — the concrete encoding types used
// across protobuf and amino.
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

var encodingConfig = initEncodingConfig()

// GetEncodingConfig returns the MCP server's EncodingConfig — the drop-in
// replacement for app.GetEncodingConfig().
func GetEncodingConfig() EncodingConfig {
	return encodingConfig
}

// makeEncodingConfig builds the base EncodingConfig. Unlike app/encoding.go,
// it constructs a fresh interface registry with the svpchain bech32 prefixes
// passed explicitly (see package doc) instead of using the module package-level
// var built at init time.
func makeEncodingConfig() EncodingConfig {
	amino := codec.NewLegacyAmino()

	// Ensure the global sdk bech32 prefixes are `svp` before building the
	// registry's address codec. Idempotent; the MCP server never seals the
	// sdk config, so this cannot panic (matches internal/mcp/signer + auth/recover,
	// which likewise call SetAddressPrefixes()).
	appconfig.SetAddressPrefixes()

	interfaceRegistry, err := custommodule.NewInterfaceRegistry(
		appconfig.Bech32PrefixAccAddr,
		appconfig.Bech32PrefixValAddr,
	)
	if err != nil {
		panic(err)
	}

	cdc := codec.NewProtoCodec(interfaceRegistry)
	txCfg := authtx.NewTxConfig(cdc, authtx.DefaultSignModes)

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             cdc,
		TxConfig:          txCfg,
		Amino:             amino,
	}
}

// initEncodingConfig registers every interface the MCP server may encounter —
// std crypto/tx types, all svpchain module types, and the eth_secp256k1 key
// types used by the chain. Mirrors app/encoding.go's initEncodingConfig.
func initEncodingConfig() EncodingConfig {
	encConfig := makeEncodingConfig()

	std.RegisterLegacyAminoCodec(encConfig.Amino)
	std.RegisterInterfaces(encConfig.InterfaceRegistry)

	basicmanager.ModuleBasics.RegisterInterfaces(encConfig.InterfaceRegistry)

	// Register eth_secp256k1 in both the protobuf InterfaceRegistry and the
	// Amino codec. std.RegisterLegacyAminoCodec already registered the crypto
	// interfaces via cryptocodec.RegisterCrypto, and Amino panics on duplicate
	// RegisterInterface calls, so register the concrete types only and replace
	// legacy.Cdc so UnarmorDecryptPrivKey can handle eth keys.
	evmcryptocodec.RegisterInterfaces(encConfig.InterfaceRegistry)
	encConfig.Amino.RegisterConcrete(&ethsecp256k1.PubKey{}, ethsecp256k1.PubKeyName, nil)
	encConfig.Amino.RegisterConcrete(&ethsecp256k1.PrivKey{}, ethsecp256k1.PrivKeyName, nil)
	legacy.Cdc = encConfig.Amino

	return encConfig
}
