package agentchain

import (
	"bytes"
	"fmt"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"

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
	// Pricing is what the agent advertises per unit of work. Required on a
	// first registration; nil on an update leaves the registered value alone.
	Pricing *agenttypes.Pricing
}

// LegacyAgentID returns the pre-index DID form for an owner: did:svp:<owner>,
// which the chain treats as allocation index zero.
//
// ★ Not what a new registration uses. The chain rejects an unsuffixed id for a
// new agent ("must include a positive index") and only keeps the form
// addressable so agents registered before indexes existed still resolve. Use
// Client.ResolveAgent, which asks the chain. This is retained for reading such
// an agent back by hand.
func LegacyAgentID(ownerAddr sdk.AccAddress) string {
	return agenttypes.AgentIdFromOwner(ownerAddr)
}

// BuildRegister assembles the first registration. The owner account is the
// agent's whole identity: the chain no longer carries a separate operator or
// public key, so nothing beyond the owner address goes into the message.
// agentID is passed in rather than derived: since the chain grew per-owner
// allocation indexes, only the chain knows which id a new agent gets. See
// Client.ResolveAgent.
func BuildRegister(
	ownerAddr sdk.AccAddress,
	agentID string,
	want Desired,
	bond sdk.Coin,
) *agenttypes.MsgRegisterAgent {
	return &agenttypes.MsgRegisterAgent{
		Owner:          ownerAddr.String(),
		AgentId:        agentID,
		Endpoint:       want.Endpoint,
		CapabilityHash: want.CapabilityHash,
		Capabilities:   want.Capabilities,
		Pricing:        want.Pricing,
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
// Metadata and Pricing are treated the same: only overridden when the caller
// supplied one.
func BuildUpdate(existing *agenttypes.Agent, want Desired) *agenttypes.MsgUpdateAgent {
	metadata := existing.Metadata
	if want.Metadata != "" {
		metadata = want.Metadata
	}
	pricing := existing.Pricing
	if want.Pricing != nil {
		pricing = want.Pricing
	}
	return &agenttypes.MsgUpdateAgent{
		Owner:          existing.Owner,
		AgentId:        existing.AgentId,
		Endpoint:       want.Endpoint,
		CapabilityHash: want.CapabilityHash,
		Capabilities:   want.Capabilities,
		Pricing:        pricing,
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
	if want.Pricing != nil && !samePricing(existing.Pricing, want.Pricing) {
		reasons = append(reasons, fmt.Sprintf(
			"pricing changed: registered %s, configured %s",
			describePricing(existing.Pricing), describePricing(want.Pricing)))
	}
	return reasons
}

func samePricing(a, b *agenttypes.Pricing) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Amount == b.Amount && a.Unit == b.Unit
}

func describePricing(p *agenttypes.Pricing) string {
	if p == nil {
		return "none"
	}
	return p.Amount + "/" + p.Unit
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

// Gas defaults for the registration transaction this tool signs.
//
// ★ These lived in internal/config while the agent itself built and signed
// transactions. It no longer does — every payload is built by the MCP server
// and signed by the caller — so the only transaction this repo still signs is
// a registration, and the defaults belong beside it.
//
// They match a chain whose minimum-gas-prices is 25000000000asvp at a
// 1,000,000 gas limit, about 0.025 SVP.
const (
	DefaultFeeDenom    = "asvp"
	DefaultFeeAmount   = "25000000000000000"
	DefaultFeeGasLimit = uint64(1_000_000)
)
