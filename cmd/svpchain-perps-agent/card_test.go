package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"

	"github.com/svpchain/svpchain-perps-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
	"github.com/svpchain/svpchain-perps-agent/internal/wire"
)

// ★ The card this agent serves is hashed into its on-chain registration, and a
// verifier recomputes that hash from a live fetch. So the card is an interface,
// not an implementation detail: it must not change by accident — not when this
// repo is edited, and not when the core library it composes is upgraded. A
// deliberate change is fine; it just has to be deliberate, and followed by
// `deploy.sh --register` on every deployment. Re-record the golden with:
//
//	go test ./cmd/... -run TestCardMatchesGolden -update-goldens
var updateGoldens = flag.Bool("update-goldens", false, "rewrite the card golden")

func TestCardMatchesGolden(t *testing.T) {
	reg := toolbridge.NewEmpty()
	wire.PerpsProfile.Register(reg, &tools.Handlers{})

	got, err := json.Marshal(a2aserver.BuildAgentCardFor(identity, "https://agents.example.test/perps", reg))
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", "card.json")
	if *updateGoldens {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Errorf("card bytes changed — the on-chain capability hash no longer matches "+
			"until `deploy.sh --register` runs.\n got: %s\nwant: %s", got, want)
	}
}

// The card must advertise exactly what the executor will dispatch: every tool
// in the registry named in its skill's description, and no skill on the card
// that the registry does not serve.
func TestCardMatchesRegistry(t *testing.T) {
	reg := toolbridge.NewEmpty()
	wire.PerpsProfile.Register(reg, &tools.Handlers{})
	card := a2aserver.BuildAgentCardFor(identity, "https://agents.example.test/perps", reg)

	bySkill := reg.BySkill()
	seen := map[string]bool{}
	for _, sk := range card.Skills {
		seen[sk.ID] = true
		for _, tool := range bySkill[sk.ID] {
			if !strings.Contains(sk.Description, tool) {
				t.Errorf("skill %s description does not name tool %s", sk.ID, tool)
			}
		}
	}
	for id := range bySkill {
		if !seen[id] {
			t.Errorf("registry skill %s missing from card", id)
		}
	}
}
