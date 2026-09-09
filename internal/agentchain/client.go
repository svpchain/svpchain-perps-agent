// Package agentchain is the client side of this agent's x/agent registry
// lifecycle: query what the chain currently holds for an agent, and land a
// signed registration or update.
//
// It is a LOCAL client, run by the operator from their own machine, not
// something the deployed agent does. That is a change from the flow this
// replaces. Registration used to be self-registration — the agent held an
// operator key on the remote and signed its own MsgRegisterAgent, so the tool
// was an A2A client that authenticated to the running agent and asked it to
// register itself. A caller-signed agent has no key to do that with, so the
// transaction is built and signed here, against the owner key, and the agent's
// only part is serving the card that gets hashed into it.
//
// What still cannot move is the card hash. The value published on chain is the
// sha256 of the card as SERVED, so registering requires a running agent
// answering at its public URL — see cmd/agent-register, which fetches it.
package agentchain

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"
	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"
)

// Client bundles the three chain surfaces a registration needs: the x/agent
// registry to read current state, x/auth for the signer's account number and
// sequence, and the tx service to broadcast. DialREST is the only constructor:
// there was a gRPC one beside it, and the deploy stopped offering that route
// because a node's REST port is far more often exposed.
type Client struct {
	close     func() error
	agents    agentQuerier
	accounts  AccountClient
	broadcast BroadcastClient
}

// agentQuerier is the slice of agenttypes.QueryClient a registration reads.
// Both transports satisfy it; the gRPC one is the generated client itself.
type agentQuerier interface {
	Agent(ctx context.Context, in *agenttypes.QueryAgent, opts ...grpc.CallOption) (*agenttypes.QueryAgentResponse, error)
	Params(ctx context.Context, in *agenttypes.QueryParams, opts ...grpc.CallOption) (*agenttypes.QueryParamsResponse, error)

	// AgentsByOwner and NextAgentIndex are what an owner's identity now takes.
	// An agent id used to follow from the owner address alone; the chain since
	// grew per-owner allocation indexes, so which agent an owner means, and
	// which id a new one gets, are both questions only the chain can answer.
	AgentsByOwner(ctx context.Context, in *agenttypes.QueryAgentsByOwner, opts ...grpc.CallOption) (*agenttypes.QueryAgentsByOwnerResponse, error)
	NextAgentIndex(ctx context.Context, in *agenttypes.QueryNextAgentIndex, opts ...grpc.CallOption) (*agenttypes.QueryNextAgentIndexResponse, error)
}

func (c *Client) Close() error { return c.close() }

// Params returns the module's registration fee and minimum bond. The bond
// matters at registration time: MsgRegisterAgent carries an explicit
// InitialBond and the handler rejects anything under MinBond, so an operator
// who did not name one needs this to fill it in.
func (c *Client) Params(ctx context.Context) (agenttypes.Params, error) {
	resp, err := c.agents.Params(ctx, &agenttypes.QueryParams{})
	if err != nil {
		return agenttypes.Params{}, fmt.Errorf("agent.Query/Params: %w", err)
	}
	return resp.Params, nil
}

// AgentByID looks up a registration. A missing agent is (nil, false, nil)
// rather than an error: "not registered yet" is the ordinary starting state of
// the thing this package exists to fix, not a fault.
func (c *Client) AgentByID(ctx context.Context, agentID string) (*agenttypes.Agent, bool, error) {
	resp, err := c.agents.Agent(ctx, &agenttypes.QueryAgent{AgentId: agentID})
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("agent.Query/Agent %s: %w", agentID, err)
	}
	return &resp.Agent, true, nil
}

// ResolveAgent decides which DID a registration run targets, and returns the
// agent already registered under it when there is one.
//
// ★ This used to be a pure function of the owner address: AgentIdFromOwner.
// The chain since grew per-owner allocation indexes, so a new agent's id is
// did:svp:<owner>:<n> with n positive, and the old unsuffixed form is legacy —
// still addressable for agents registered before the change, but rejected for
// a new one. An id can therefore no longer be derived locally: which agent an
// owner means, and which id the next one gets, are both chain state.
//
// The repo's model is still one owner key per agent (scripts/deploy.sh
// --gen-owner-key refuses a second), so:
//
//   - no agents yet: take the id the chain says to allocate next, and register.
//   - exactly one: that is this deployment's, whatever its index and even if
//     its endpoint has since moved — which is the case an endpoint match would
//     get wrong.
//   - more than one: refuse. The owner has broken the one-key-one-agent model
//     and nothing here can tell which agent is meant, so guessing would update
//     the wrong registration.
func (c *Client) ResolveAgent(ctx context.Context, ownerAddr sdk.AccAddress) (string, *agenttypes.Agent, bool, error) {
	owned, err := c.agents.AgentsByOwner(ctx, &agenttypes.QueryAgentsByOwner{Owner: ownerAddr.String()})
	if err != nil {
		return "", nil, false, fmt.Errorf("agent.Query/AgentsByOwner %s: %w", ownerAddr, err)
	}

	switch len(owned.Agents) {
	case 0:
		next, err := c.agents.NextAgentIndex(ctx, &agenttypes.QueryNextAgentIndex{Owner: ownerAddr.String()})
		if err != nil {
			return "", nil, false, fmt.Errorf("agent.Query/NextAgentIndex %s: %w", ownerAddr, err)
		}
		if next.AgentId == "" {
			return "", nil, false, fmt.Errorf("chain returned an empty next agent id for %s", ownerAddr)
		}
		return next.AgentId, nil, false, nil
	case 1:
		a := owned.Agents[0]
		return a.AgentId, &a, true, nil
	default:
		ids := make([]string, 0, len(owned.Agents))
		for _, a := range owned.Agents {
			ids = append(ids, a.AgentId)
		}
		return "", nil, false, fmt.Errorf(
			"owner %s controls %d agents (%s); this tool registers one agent per owner key and cannot tell which is meant",
			ownerAddr, len(owned.Agents), strings.Join(ids, ", "))
	}
}

// Account returns the signer's account number and sequence.
func (c *Client) Account(ctx context.Context, address string) (AccountInfo, error) {
	return c.accounts.Account(ctx, address)
}

// BroadcastSync submits signed tx bytes and turns a non-zero CheckTx code into
// an error, so callers do not have to inspect the result to know it failed.
func (c *Client) BroadcastSync(ctx context.Context, txBytes []byte) (BroadcastResult, error) {
	res, err := c.broadcast.BroadcastSync(ctx, txBytes)
	if err != nil {
		return res, err
	}
	if err := ParseBroadcastError(res); err != nil {
		return res, err
	}
	// ParseBroadcastError only types the rejections it recognises and returns
	// nil for the rest, so a non-zero code still has to fail here — otherwise
	// an out-of-gas or a module error prints as "submitted" and the record
	// never changes.
	if res.Code != 0 {
		return res, fmt.Errorf("rejected by CheckTx (code %d): %s", res.Code, res.RawLog)
	}
	return res, nil
}

// isNotFound recognises the status the registry returns for an unknown agent
// id — a gRPC status either way, since the REST client re-raises the gateway's
// {"code":5,...} body as one. Matched on the code rather than the message:
// NotFound is the one outcome that means "nothing registered under this id
// yet", and treating a transport failure as that would silently re-register
// an agent that already exists.
func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}
