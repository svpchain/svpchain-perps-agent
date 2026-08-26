package agentchain_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"

	"github.com/svpchain/svpchain-perps-agent/internal/agentchain"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/signer"
	"github.com/svpchain/svpchain-perps-agent/internal/owner"
)

func want() agentchain.Desired {
	return agentchain.Desired{
		Endpoint:       "https://perps-agent.svpchain.org",
		CapabilityHash: make([]byte, 32),
		Capabilities:   []string{"perps.trading", "perps.market-data"},
	}
}

// The load-bearing test for the whole single-key model: a MsgRegisterAgent
// built with one account in BOTH roles must pass the chain's own stateless
// validation. That is what enforces AgentIdFromOperator and
// PublicKeyMatchesOperator, so if collapsing owner and operator were not
// allowed, this is where it would show.
func TestBuildRegisterPassesChainValidation(t *testing.T) {
	hexKey, addrStr, err := owner.Generate()
	require.NoError(t, err)
	priv, err := signer.ParsePrivKey(hexKey)
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)

	bond := sdk.Coin{Denom: "asvp", Amount: sdkmath.NewInt(5_000_000)}
	msg := agentchain.BuildRegister(priv, addr, want(), bond)

	require.NoError(t, msg.ValidateBasic())
	require.Equal(t, addrStr, msg.Owner)
	require.Equal(t, addrStr, msg.Operator, "owner and operator are the same account")
	require.Equal(t, agenttypes.DIDPrefix+addrStr, msg.AgentId)
	require.Len(t, msg.PublicKey, agenttypes.PubKeyLen)
	require.Equal(t, bond, msg.InitialBond)
}

// An update must never carry a public key: the chain refuses one outright
// rather than ignoring it, because the key is the identity.
func TestBuildUpdateOmitsPublicKey(t *testing.T) {
	existing := &agenttypes.Agent{
		AgentId:  "did:svp:svp1abc",
		Owner:    "svp1owner",
		Metadata: "kept",
	}
	msg := agentchain.BuildUpdate(existing, want())
	require.Empty(t, msg.PublicKey)
	require.Equal(t, existing.AgentId, msg.AgentId)
	require.Equal(t, existing.Owner, msg.Owner)
}

// MsgUpdateAgent overwrites every mutable field, so anything the deploy does
// not know about has to be carried forward or it is silently wiped.
func TestBuildUpdateCarriesForwardUnmanagedFields(t *testing.T) {
	pricing := &agenttypes.Pricing{Unit: "call"}
	existing := &agenttypes.Agent{
		AgentId:  "did:svp:svp1abc",
		Owner:    "svp1owner",
		Pricing:  pricing,
		Metadata: "set-elsewhere",
	}

	// No metadata supplied: the existing value survives.
	msg := agentchain.BuildUpdate(existing, want())
	require.Equal(t, pricing, msg.Pricing, "pricing must not be cleared by an update that never set it")
	require.Equal(t, "set-elsewhere", msg.Metadata)

	// Metadata supplied: it wins.
	w := want()
	w.Metadata = "explicit"
	require.Equal(t, "explicit", agentchain.BuildUpdate(existing, w).Metadata)
}

func TestDrift(t *testing.T) {
	base := want()
	current := &agenttypes.Agent{
		Endpoint:       base.Endpoint,
		CapabilityHash: base.CapabilityHash,
		Capabilities:   []string{"perps.trading", "perps.market-data"},
	}

	t.Run("current record reports nothing", func(t *testing.T) {
		require.Empty(t, agentchain.Drift(current, base))
	})

	t.Run("capability tag order is not drift", func(t *testing.T) {
		reordered := base
		reordered.Capabilities = []string{"perps.market-data", "perps.trading"}
		require.Empty(t, agentchain.Drift(current, reordered))
	})

	t.Run("moved endpoint", func(t *testing.T) {
		moved := base
		moved.Endpoint = "https://elsewhere.example"
		require.Len(t, agentchain.Drift(current, moved), 1)
	})

	t.Run("changed card hash", func(t *testing.T) {
		rehashed := base
		h := make([]byte, 32)
		h[0] = 0xff
		rehashed.CapabilityHash = h
		require.Len(t, agentchain.Drift(current, rehashed), 1)
	})

	t.Run("changed capabilities", func(t *testing.T) {
		retagged := base
		retagged.Capabilities = []string{"perps.trading"}
		require.Len(t, agentchain.Drift(current, retagged), 1)
	})

	t.Run("empty metadata leaves an existing value alone", func(t *testing.T) {
		withMeta := *current
		withMeta.Metadata = "something"
		require.Empty(t, agentchain.Drift(&withMeta, base))
	})
}

func TestAgentIDDerivesFromOwner(t *testing.T) {
	_, addrStr, err := owner.Generate()
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)
	require.Equal(t, agenttypes.DIDPrefix+addrStr, agentchain.AgentID(addr))
	require.NoError(t, agenttypes.ValidateAgentId(agentchain.AgentID(addr)))
}
