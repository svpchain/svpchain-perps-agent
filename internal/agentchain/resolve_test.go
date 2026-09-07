package agentchain

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"
	"google.golang.org/grpc"
)

// fakeAgents is the chain's answers, scripted.
type fakeAgents struct {
	owned     []agenttypes.Agent
	nextID    string
	nextCalls int
}

func (f *fakeAgents) Agent(context.Context, *agenttypes.QueryAgent, ...grpc.CallOption) (*agenttypes.QueryAgentResponse, error) {
	return nil, nil
}
func (f *fakeAgents) Params(context.Context, *agenttypes.QueryParams, ...grpc.CallOption) (*agenttypes.QueryParamsResponse, error) {
	return nil, nil
}
func (f *fakeAgents) AgentsByOwner(_ context.Context, _ *agenttypes.QueryAgentsByOwner, _ ...grpc.CallOption) (*agenttypes.QueryAgentsByOwnerResponse, error) {
	return &agenttypes.QueryAgentsByOwnerResponse{Agents: f.owned}, nil
}
func (f *fakeAgents) NextAgentIndex(_ context.Context, _ *agenttypes.QueryNextAgentIndex, _ ...grpc.CallOption) (*agenttypes.QueryNextAgentIndexResponse, error) {
	f.nextCalls++
	return &agenttypes.QueryNextAgentIndexResponse{AgentId: f.nextID, NextIndex: 1}, nil
}

func testOwner(t *testing.T) sdk.AccAddress {
	t.Helper()
	addr, err := sdk.AccAddressFromBech32("svp129kfvda5kfh37ch42v294x5x7q0gjrmq2lhefw")
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

// ★ A first registration must take the id the CHAIN allocates, not one derived
// from the owner address. Deriving it locally produced the unsuffixed legacy
// form, which a live chain rejected with "must include a positive index".
func TestResolveAgentAsksTheChainForANewID(t *testing.T) {
	owner := testOwner(t)
	want := agenttypes.AgentIdFromOwnerIndex(owner, 1)
	f := &fakeAgents{nextID: want}
	c := &Client{agents: f}

	id, existing, found, err := c.ResolveAgent(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if found || existing != nil {
		t.Error("reported an existing agent where the owner has none")
	}
	if id != want {
		t.Errorf("id = %q, want the chain's %q", id, want)
	}
	if id == LegacyAgentID(owner) {
		t.Error("resolved to the legacy unsuffixed id, which the chain rejects for a new agent")
	}
	if f.nextCalls != 1 {
		t.Errorf("queried the next index %d times, want 1", f.nextCalls)
	}
}

// One agent is this deployment's, whatever its index — including the legacy
// form, so an agent registered before indexes existed keeps updating.
func TestResolveAgentReusesTheOwnersOnlyAgent(t *testing.T) {
	owner := testOwner(t)
	for _, existing := range []string{
		agenttypes.AgentIdFromOwnerIndex(owner, 3),
		LegacyAgentID(owner),
	} {
		f := &fakeAgents{owned: []agenttypes.Agent{{AgentId: existing, Endpoint: "https://old.example"}}}
		c := &Client{agents: f}

		id, agent, found, err := c.ResolveAgent(context.Background(), owner)
		if err != nil {
			t.Fatal(err)
		}
		if !found || agent == nil {
			t.Fatalf("%s: existing agent not reported", existing)
		}
		if id != existing {
			t.Errorf("id = %q, want %q", id, existing)
		}
		if f.nextCalls != 0 {
			t.Error("allocated a new index despite an existing agent")
		}
	}
}

// An endpoint that has moved is exactly the drift --register exists to fix, so
// matching on it would fail the case that matters most.
func TestResolveAgentIgnoresAMovedEndpoint(t *testing.T) {
	owner := testOwner(t)
	existing := agenttypes.AgentIdFromOwnerIndex(owner, 1)
	f := &fakeAgents{owned: []agenttypes.Agent{{AgentId: existing, Endpoint: "https://old.example"}}}
	c := &Client{agents: f}

	id, _, found, err := c.ResolveAgent(context.Background(), owner)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if id != existing {
		t.Errorf("id = %q; a moved endpoint must not orphan the registration", id)
	}
}

// More than one is ambiguous, and guessing would update the wrong record.
func TestResolveAgentRefusesWhenTheOwnerHasSeveral(t *testing.T) {
	owner := testOwner(t)
	f := &fakeAgents{owned: []agenttypes.Agent{
		{AgentId: agenttypes.AgentIdFromOwnerIndex(owner, 1)},
		{AgentId: agenttypes.AgentIdFromOwnerIndex(owner, 2)},
	}}
	c := &Client{agents: f}

	_, _, _, err := c.ResolveAgent(context.Background(), owner)
	if err == nil {
		t.Fatal("picked one of two agents instead of refusing")
	}
	for _, want := range []string{":1", ":2", "cannot tell which"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}
