package wire

import (
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"

	"github.com/svpchain/svpchain-perps-agent/internal/agentchain"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// ★ The one deployed profile must cover the whole bridged surface: every
// operation toolbridge can register has to be reachable from the binary that
// ships, or it is dead code.
//
// This used to compare the union of four profiles against the full registry,
// back when core was shared and no single agent served everything. With only
// perps left the assertion inverts into something sharper — a dead-code
// detector. Anything added to toolbridge that PerpsProfile does not register
// fails here, which is exactly the drift a freshly-pruned vendored tree invites.
func TestPerpsProfileCoversTheWholeSurface(t *testing.T) {
	h := &tools.Handlers{}
	agentSvc := agentchain.New(nil, nil, nil, nil, nil, nil, nil)

	expected := toolbridge.New(h)
	expected.RegisterAgentChain(agentSvc)
	expected.RegisterExecution(nil)
	want := map[string]bool{}
	for _, tools := range expected.BySkill() {
		for _, tool := range tools {
			want[tool] = true
		}
	}

	r := toolbridge.NewEmpty()
	PerpsProfile.Register(r, h, agentSvc, nil)
	got := map[string]bool{}
	for _, tools := range r.BySkill() {
		for _, tool := range tools {
			got[tool] = true
		}
	}

	for tool := range want {
		if !got[tool] {
			t.Errorf("the perps profile does not serve %q", tool)
		}
	}
	for tool := range got {
		if !want[tool] {
			t.Errorf("the perps profile serves %q, which the full registry does not build", tool)
		}
	}
	if len(want) != len(got) {
		t.Errorf("the perps profile serves %d tools, the full surface is %d", len(got), len(want))
	}
}

// The profile registers the shared delegation stack: auth to mint bearers, the
// agent-chain identity modules, and the execution core.
func TestPerpsProfileServesTheDelegationStack(t *testing.T) {
	h := &tools.Handlers{}
	agentSvc := agentchain.New(nil, nil, nil, nil, nil, nil, nil)

	r := toolbridge.NewEmpty()
	PerpsProfile.Register(r, h, agentSvc, nil)
	for _, tool := range []string{
		"auth_challenge", "auth_verify",
		"get_agent", "build_register_agent",
		"get_delegation", "build_create_delegation",
		"agent_identity", "agent_self_register", "execute_record_spend", "agent_claim",
	} {
		if _, ok := r.Lookup(tool); !ok {
			t.Errorf("profile %s missing delegation-stack tool %q", PerpsProfile.Name, tool)
		}
	}
}
