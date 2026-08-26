// Package owner holds the key that registers this agent on chain: the account
// that pays the registration fee and the bond, and controls the record
// thereafter.
//
// This key is NOT the agent's. Since the switch to caller-signed MCP the agent
// signs nothing — every transaction it builds is signed by the caller — so the
// deployed binary never loads a key and the deploy ships none to the remote.
// This one lives on the operator's own machine, in the agent's config
// directory, and is read by cmd/agent-register alone.
//
// # One key, both chain roles
//
// x/agent separates two accounts. The OWNER signs the lifecycle messages
// (register, update, deposit/withdraw bond, deregister) and is where a
// withdrawn bond returns. The OPERATOR is the address the DID embeds —
// did:svp:<bech32> — and whose public key verifies SVP-DT credentials the
// agent issues.
//
// A caller-signed agent issues no credentials and holds no signing key, so
// there is no second identity to mint. This key is registered as both, and the
// agent id derives from it. MsgRegisterAgent accepts that: it requires the
// public key to be the operator's own (PublicKeyMatchesOperator) and the id to
// derive from the operator address (AgentIdFromOperator), and both hold when
// the two roles are one account.
//
// # One key registers exactly one agent
//
// This is the cost of collapsing the roles, and it is not recoverable after
// the fact. x/agent binds an operator address to at most one agent — see
// keeper/registry.go, "operator %q is already bound to agent %q" — and the
// registered public key is explicitly not updatable. So a second agent needs a
// second owner key, and a fleet keeps one per agent. The deploy already gives
// each agent its own config directory, which is where that separation lands.
package owner

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/cosmos/evm/crypto/ethsecp256k1"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/signer"
)

// KeyEnvVar carries the owner key material itself, as 64 hex characters.
//
// Named for this agent specifically rather than the fleet, because the key is
// this agent's on-chain identity as well as its owner: two agents reading one
// shared variable would be a single id claiming two cards, and the chain would
// refuse the second registration. Keeping the name per-agent makes that a
// configuration mistake you cannot make by exporting one variable.
//
// Nothing on the deployed host reads this. It is set on the operator's machine
// for the length of a registration, normally by the config file sourcing the
// key file the deploy wrote.
const KeyEnvVar = "SVPCHAIN_PERPS_AGENT_OWNER_KEY"

// Load resolves the owner key from KeyEnvVar, falling back to keyFile when the
// variable is unset and keyFile is non-empty.
//
// Absent from both is not an error here — it is the ordinary state of a
// machine that is not registering anything. The caller decides whether that is
// fatal, and cmd/agent-register does say so, because registration is precisely
// the operation this key exists for.
func Load(keyFile string) (*ethsecp256k1.PrivKey, string, error) {
	hexKey := strings.TrimSpace(os.Getenv(KeyEnvVar))
	if hexKey == "" && keyFile != "" {
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, "", fmt.Errorf("read owner key file: %w", err)
		}
		hexKey = strings.TrimSpace(string(raw))
	}
	if hexKey == "" {
		return nil, "", nil
	}
	priv, err := signer.ParsePrivKey(hexKey)
	if err != nil {
		return nil, "", fmt.Errorf("parse owner key: %w", err)
	}
	return priv, signer.DeriveAddress(priv), nil
}

// Generate mints a fresh owner key, returning it in exactly the form Load
// reads — 64 lowercase hex characters, no 0x — together with the svp1… address
// it derives.
//
// The address is why this returns two values. It is where the registration fee
// and the bond have to land before MsgRegisterAgent will succeed, and it
// cannot be read off the key: the derivation is keccak over the public key,
// then bech32 with the chain's prefix. Minting a key without being told its
// address leaves the operator holding a secret they have no way to fund.
//
// Every call yields a different key, and a key IS an identity here — the agent
// id derives from it and the bond is posted against it. A caller that persists
// the result must refuse to overwrite rather than treat this as a regenerable
// artifact; cmd/owner-keygen does that with O_EXCL.
func Generate() (hexKey, addr string, err error) {
	priv, err := ethsecp256k1.GenerateKey()
	if err != nil {
		return "", "", fmt.Errorf("generate owner key: %w", err)
	}
	return hex.EncodeToString(priv.Key), signer.DeriveAddress(priv), nil
}
