package agentchain_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"

	"github.com/svpchain/svpchain-perps-agent/internal/agentchain"
	"github.com/svpchain/svpchain-perps-agent/internal/owner"
)

func want() agentchain.Desired {
	return agentchain.Desired{
		Endpoint:       "https://perps-agent.svpchain.org",
		CapabilityHash: make([]byte, 32),
		Capabilities:   []string{"perps.trading", "perps.market-data"},
	}
}

// wantRegister is want() plus the pricing a first registration must carry.
func wantRegister() agentchain.Desired {
	w := want()
	w.Pricing = &agenttypes.Pricing{Amount: "1000000", Unit: "call"}
	return w
}

// A MsgRegisterAgent built from the owner key alone must pass the chain's own
// stateless validation, which is what enforces that the id derives from the
// owner address (AgentIdFromOwner).
func TestBuildRegisterPassesChainValidation(t *testing.T) {
	_, addrStr, err := owner.Generate()
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)

	bond := sdk.Coin{Denom: "asvp", Amount: sdkmath.NewInt(5_000_000)}
	// The id is resolved against the chain and passed in; a new agent's is
	// index-suffixed, which is exactly what the unsuffixed form is not.
	agentID := agenttypes.AgentIdFromOwnerIndex(addr, 1)
	msg := agentchain.BuildRegister(addr, agentID, wantRegister(), bond)

	require.NoError(t, msg.ValidateBasic())
	require.Equal(t, addrStr, msg.Owner)
	require.Equal(t, agentID, msg.AgentId)
	require.Equal(t, bond, msg.InitialBond)
	require.Equal(t, wantRegister().Pricing, msg.Pricing)
}

// The chain refuses a registration without pricing, so a Desired missing it
// must fail the same stateless check rather than reach the chain.
func TestBuildRegisterWithoutPricingFailsValidation(t *testing.T) {
	_, addrStr, err := owner.Generate()
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)

	bond := sdk.Coin{Denom: "asvp", Amount: sdkmath.NewInt(5_000_000)}
	require.Error(t, agentchain.BuildRegister(addr, agenttypes.AgentIdFromOwnerIndex(addr, 1), want(), bond).ValidateBasic())
}

// An update keeps the identity fields of the existing record.
func TestBuildUpdateKeepsIdentity(t *testing.T) {
	existing := &agenttypes.Agent{
		AgentId:  "did:svp:svp1abc",
		Owner:    "svp1owner",
		Metadata: "kept",
	}
	msg := agentchain.BuildUpdate(existing, want())
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

	t.Run("pricing is drift only when configured and different", func(t *testing.T) {
		require.Empty(t, agentchain.Drift(current, base), "unset pricing is not drift")
		priced := *current
		priced.Pricing = &agenttypes.Pricing{Amount: "1000000", Unit: "call"}
		require.Empty(t, agentchain.Drift(&priced, wantRegister()))
		repriced := wantRegister()
		repriced.Pricing.Amount = "2000000"
		require.Len(t, agentchain.Drift(&priced, repriced), 1)
	})

	t.Run("empty metadata leaves an existing value alone", func(t *testing.T) {
		withMeta := *current
		withMeta.Metadata = "something"
		require.Empty(t, agentchain.Drift(&withMeta, base))
	})
}

// ★ The chain rejects an unsuffixed id for a NEW agent ("must include a
// positive index") and keeps the form addressable only for agents registered
// before indexes existed. This pins both halves, because deriving the id
// locally from the owner address is exactly the bug that made a first
// registration fail on a live chain.
func TestLegacyAgentIDIsIndexZeroAndNotForNewAgents(t *testing.T) {
	_, addrStr, err := owner.Generate()
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)

	legacy := agentchain.LegacyAgentID(addr)
	require.Equal(t, agenttypes.DIDPrefix+addrStr, legacy)
	require.NoError(t, agenttypes.ValidateAgentId(legacy), "the legacy form must stay addressable")

	_, index, err := agenttypes.ParseAgentId(legacy)
	require.NoError(t, err)
	require.Zero(t, index, "the unsuffixed form is allocation index zero")

	allocated := agenttypes.AgentIdFromOwnerIndex(addr, 1)
	require.NotEqual(t, legacy, allocated, "a newly allocated id must not be the legacy form")
	_, index, err = agenttypes.ParseAgentId(allocated)
	require.NoError(t, err)
	require.EqualValues(t, 1, index)
}
