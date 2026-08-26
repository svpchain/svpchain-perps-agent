package toolbridge

import (
	"reflect"
	"sort"
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
