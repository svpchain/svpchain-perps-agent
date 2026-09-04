package a2aserver

import (
	"strings"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// ★ The assistant is the one skill served conditionally, so the card it
// produces differs between two deployments of the same binary. That is
// deliberate — an operator who configures no model should not advertise one —
// but it means the on-chain capability hash differs too, and enabling the
// skill on a registered agent needs a --register run like any other card move.
// These two tests pin both halves of that.
func TestCardOmitsTheAssistantWhenItIsNotRegistered(t *testing.T) {
	card := BuildAgentCardFor(CardIdentity{Name: "a", Version: "1"}, "https://x.test",
		toolbridge.New(&tools.Handlers{}))

	for _, s := range card.Skills {
		if s.ID == toolbridge.SkillAssistant {
			t.Fatal("the card advertises the assistant on a binary that registered none")
		}
	}
}

func TestCardAdvertisesTheAssistantWhenRegistered(t *testing.T) {
	r := toolbridge.New(&tools.Handlers{})
	r.RegisterAssistant(nil) // the card reads names, never calls the operation

	card := BuildAgentCardFor(CardIdentity{Name: "a", Version: "1"}, "https://x.test", r)

	for _, s := range card.Skills {
		if s.ID != toolbridge.SkillAssistant {
			continue
		}
		if !strings.Contains(s.Description, "ask") {
			t.Errorf("assistant skill does not list its tool: %q", s.Description)
		}
		// The card is where a caller learns what the agent will not do. If
		// this drops out, a caller can reasonably expect the planner to
		// submit transactions for it.
		if !strings.Contains(s.Description, "cannot sign") {
			t.Errorf("assistant skill does not state the custody boundary: %q", s.Description)
		}
		return
	}
	t.Fatal("the card omits the assistant skill after it was registered")
}
