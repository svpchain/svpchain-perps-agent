package agentchain

import (
	"bytes"
	"fmt"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"
)

// Desired is the registration state derived from what the agent actually
// serves plus what the deploy configured: the endpoint callers reach it at,
// the sha256 of the card served there, and the capability tags it is
// discoverable by.
type Desired struct {
	Endpoint       string
	CapabilityHash []byte
	Capabilities   []string
	Metadata       string
}

// AgentID returns the DID this owner key registers under.
//
// There is no choice involved: the id embeds the operator address, and this
// agent registers the owner key as its own operator (see internal/owner), so
// the id follows from the key. A verifier off this chain derives the signing
// key's address straight back out of the DID, which is what makes it
// verifiable with nothing but the library.
func AgentID(ownerAddr sdk.AccAddress) string {
	return agenttypes.AgentIdFromOperator(ownerAddr)
}

// BuildRegister assembles the first registration. Owner and Operator are the
// same account by design — the agent signs nothing, so there is no separate
// operator identity to hold — and PublicKey is that account's own compressed
// secp256k1 key, which is what MsgRegisterAgent's PublicKeyMatchesOperator
// check requires.
func BuildRegister(
	priv *ethsecp256k1.PrivKey,
	ownerAddr sdk.AccAddress,
	want Desired,
	bond sdk.Coin,
) *agenttypes.MsgRegisterAgent {
	addr := ownerAddr.String()
	return &agenttypes.MsgRegisterAgent{
		Owner:          addr,
		AgentId:        AgentID(ownerAddr),
		Operator:       addr,
		PublicKey:      priv.PubKey().Bytes(),
		Endpoint:       want.Endpoint,
		CapabilityHash: want.CapabilityHash,
		Capabilities:   want.Capabilities,
		InitialBond:    bond,
		Metadata:       want.Metadata,
	}
}

// BuildUpdate assembles an update from the CURRENT record, overriding only the
// fields this tool is authoritative for.
//
// Starting from `existing` rather than from zero is the whole point.
// MsgUpdateAgent replaces every mutable field with the value sent — an omitted
// field is cleared, not left alone — so an update built from the deploy's
// knowledge alone would silently wipe pricing that was set some other way.
// Metadata is treated the same: only overridden when the caller supplied one.
//
// PublicKey is left empty deliberately. The chain refuses an update that
// carries one, because the key is the identity and cannot be rotated.
func BuildUpdate(existing *agenttypes.Agent, want Desired) *agenttypes.MsgUpdateAgent {
	metadata := existing.Metadata
	if want.Metadata != "" {
		metadata = want.Metadata
	}
	return &agenttypes.MsgUpdateAgent{
		Owner:          existing.Owner,
		AgentId:        existing.AgentId,
		Endpoint:       want.Endpoint,
		CapabilityHash: want.CapabilityHash,
		Capabilities:   want.Capabilities,
		Pricing:        existing.Pricing,
		Metadata:       metadata,
	}
}

// Drift reports what an existing registration disagrees with the served agent
// about, as human-readable reasons. Empty means the record is current and
// there is nothing to submit.
//
// Both drifts it looks for are silent failures otherwise. A stale capability
// hash makes verifiers read the agent as unverified while every process is
// healthy, because they recompute the hash from a live fetch. A stale endpoint
// points them at a URL that may no longer answer at all.
func Drift(existing *agenttypes.Agent, want Desired) []string {
	var reasons []string
	if existing.Endpoint != want.Endpoint {
		reasons = append(reasons, fmt.Sprintf(
			"endpoint moved: registered %q, serving %q", existing.Endpoint, want.Endpoint))
	}
	if !bytes.Equal(existing.CapabilityHash, want.CapabilityHash) {
		reasons = append(reasons, fmt.Sprintf(
			"card changed: registered sha256 %x, serving %x",
			existing.CapabilityHash, want.CapabilityHash))
	}
	// Order is not significant to the chain — the tags land in an index — so
	// compare as sets rather than reporting a reordering as drift.
	if !sameSet(existing.Capabilities, want.Capabilities) {
		reasons = append(reasons, fmt.Sprintf(
			"capabilities changed: registered %v, configured %v",
			existing.Capabilities, want.Capabilities))
	}
	if want.Metadata != "" && existing.Metadata != want.Metadata {
		reasons = append(reasons, "metadata changed")
	}
	return reasons
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := slices.Clone(a)
	bs := slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}
