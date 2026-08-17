// Package operator holds the agent's own signing identity: the eth_secp256k1
// operator key delegated executions (and the agent's self-registration) are
// signed with, and the fee-aware direct-sign path those transactions need.
//
// This is the one place the agent stops being keyless. Everything else the
// agent builds is signed by the *caller*; only MsgAgentExecDelegated — where
// the chain requires the registered operator as the tx signer — and the
// operator's own registry lifecycle sign in-process.
package operator

import (
	"fmt"
	"os"
	"strings"

	"github.com/cosmos/evm/crypto/ethsecp256k1"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/signer"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
)

// KeyEnvVar overrides the configured key file, so a local run can supply the
// operator key without writing it to disk.
//
// Named for this agent specifically, not the fleet: an agent's on-chain id
// derives from its operator key, so two agents reading one shared variable
// would be a single id claiming two cards. The deploy keeps their config
// directories apart for the same reason; a fleet-wide name would undo it.
//
// Note the deploy does NOT use this. It ships the key as a docker compose
// secret and points key_file at the mount, because a container environment
// variable is visible in `docker inspect` and /proc/<pid>/environ.
const KeyEnvVar = "SVPCHAIN_PERPS_AGENT_OPERATOR_KEY"

// Load resolves the operator key: the environment variable first, then the
// configured key file. Both absent means the agent runs keyless — (nil, "",
// nil) — and the execution skill refuses at call time.
func Load(cfg config.Operator) (*ethsecp256k1.PrivKey, string, error) {
	hexKey := strings.TrimSpace(os.Getenv(KeyEnvVar))
	if hexKey == "" && cfg.KeyFile != "" {
		raw, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, "", fmt.Errorf("read operator key file: %w", err)
		}
		hexKey = strings.TrimSpace(string(raw))
	}
	if hexKey == "" {
		return nil, "", nil
	}
	priv, err := signer.ParsePrivKey(hexKey)
	if err != nil {
		return nil, "", fmt.Errorf("parse operator key: %w", err)
	}
	return priv, signer.DeriveAddress(priv), nil
}
