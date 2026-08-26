package wire

import (
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"

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

	expected := toolbridge.New(h)
	want := map[string]bool{}
	for _, tools := range expected.BySkill() {
		for _, tool := range tools {
			want[tool] = true
		}
	}

	r := toolbridge.NewEmpty()
	PerpsProfile.Register(r, h)
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

// The profile registers self-service auth: it mints the bearer every
// owner-scoped family gates on.
func TestPerpsProfileServesAuth(t *testing.T) {
	r := toolbridge.NewEmpty()
	PerpsProfile.Register(r, &tools.Handlers{})
	for _, tool := range []string{"auth_challenge", "auth_verify"} {
		if _, ok := r.Lookup(tool); !ok {
			t.Errorf("profile %s missing auth tool %q", PerpsProfile.Name, tool)
		}
	}
}
