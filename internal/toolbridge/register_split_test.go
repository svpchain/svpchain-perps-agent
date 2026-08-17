package toolbridge

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"
)

// The per-family registration methods exist so per-category binaries can
// compose subsets of the bridged surface. These tests pin each family's tool
// set against the full table in register_test.go, so a tool added to New()
// without landing in exactly one family method fails here.

func sorted(ss []string) []string {
	out := append([]string{}, ss...)
	sort.Strings(out)
	return out
}

func TestFamilyMethodsMatchTheFullTable(t *testing.T) {
	h := &tools.Handlers{}
	families := map[string]func(*Registry){
		SkillMarketData: func(r *Registry) { r.RegisterMarketData(h) },
		SkillAccount:    func(r *Registry) { r.RegisterAccount(h) },
		SkillTrading:    func(r *Registry) { r.RegisterTrading(h) },
		SkillFunds:      func(r *Registry) { r.RegisterFunds(h) },
		SkillBroadcast:  func(r *Registry) { r.RegisterBroadcast(h) },
		SkillAuth:       func(r *Registry) { r.RegisterAuth(h) },
		SkillFaucet:     func(r *Registry) { r.RegisterFaucet(h) },
	}
	for skill, register := range families {
		r := NewEmpty()
		register(r)
		got := r.BySkill()
		if len(got) != 1 {
			t.Errorf("family %q registered tools under %d skills, expected 1: %v", skill, len(got), got)
			continue
		}
		if !reflect.DeepEqual(got[skill], sorted(expectedOps[skill])) {
			t.Errorf("family %q tools = %v, expected table = %v", skill, got[skill], sorted(expectedOps[skill]))
		}
	}
}

// The core/perps execution split must partition the full execution surface.
// The split is still real even though this binary registers both halves:
// RegisterDelegationStack contributes the domain-agnostic core and the profile
// adds the perps writes separately. What this pins is that a core-only registry
// leaves the perps writes *unknown* rather than refusing — the difference
// between a card that never advertises perps execution and one that advertises
// it and says no.
func TestExecutionCorePerpsSplit(t *testing.T) {
	if got := sorted(executionTools); len(got) != len(executionCoreTools)+len(executionPerpsTools) {
		t.Fatalf("core+perps do not partition executionTools: %v", got)
	}

	core := NewEmpty()
	core.RegisterExecutionCore(nil)
	for _, tool := range executionCoreTools {
		op, ok := core.Lookup(tool)
		if !ok {
			t.Errorf("core tool %q missing", tool)
			continue
		}
		if _, err := op.Call(nil, nil); err == nil || !strings.Contains(err.Error(), "operator key") {
			t.Errorf("keyless core %q must refuse naming the operator-key requirement, got %v", tool, err)
		}
	}
	for _, tool := range executionPerpsTools {
		if _, ok := core.Lookup(tool); ok {
			t.Errorf("perps write %q must be unknown on a core-only registry, not registered", tool)
		}
	}

	perps := NewEmpty()
	perps.RegisterExecutionPerps(nil)
	for _, tool := range executionPerpsTools {
		if _, ok := perps.Lookup(tool); !ok {
			t.Errorf("perps tool %q missing", tool)
		}
	}

	full := NewEmpty()
	full.RegisterExecution(nil)
	for _, tool := range executionTools {
		if _, ok := full.Lookup(tool); !ok {
			t.Errorf("full execution tool %q missing", tool)
		}
	}
}
