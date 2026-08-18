package operator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
)

// The point of Generate is that what it mints is what Load reads back — the
// deploy writes the one and the agent boots on the other, in different
// processes on different machines, so a format that only round-trips by
// accident would surface as a keyless agent in production. The address must
// survive the trip too: it is what the operator funds the bond to, and a
// mismatch would send the bond to an address the agent cannot sign for.
func TestGenerateRoundTripsThroughLoad(t *testing.T) {
	t.Setenv(KeyEnvVar, "") // the file path is what is under test, not the env override

	key, addr, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(key) {
		t.Fatalf("generated key is not 64 lowercase hex characters: %q", key)
	}
	if !strings.HasPrefix(addr, "svp1") {
		t.Errorf("derived address %q does not carry the chain's bech32 prefix", addr)
	}

	// Written exactly the way scripts/deploy.sh stages it: hex plus a newline.
	path := filepath.Join(t.TempDir(), "operator.key")
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	priv, loaded, err := Load(config.Operator{KeyFile: path})
	if err != nil {
		t.Fatalf("Load rejected a key Generate produced: %v", err)
	}
	if priv == nil {
		t.Fatal("Load returned no key")
	}
	if loaded != addr {
		t.Errorf("address after round trip = %q, want %q", loaded, addr)
	}
}

// A key is an identity here — the agent id derives from it and the bond is
// posted against it. Two agents handed the same key would collide on one
// registry record, so the one thing this must never do is repeat itself.
func TestGenerateNeverRepeats(t *testing.T) {
	seen := make(map[string]bool, 16)
	for range 16 {
		key, addr, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatal("Generate returned a key it had already returned")
		}
		if seen[addr] {
			t.Fatal("Generate returned an address it had already returned")
		}
		seen[key], seen[addr] = true, true
	}
}
