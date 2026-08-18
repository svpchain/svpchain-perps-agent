// Command operator-keygen mints this agent's operator key: it writes a fresh
// eth_secp256k1 key to -out and prints the svp1… address that key derives.
//
// It exists because deriving that address is not something a deploy script can
// do. The key itself is 32 random bytes — `openssl rand -hex 32` would produce
// a perfectly good one — but the address is keccak over the public key, then
// bech32 with the chain's prefix, and the bond has to be funded to that address
// before agent_self_register will succeed. So the same Go that the agent parses
// its key with mints it, and answers the only question that follows.
//
// scripts/deploy.sh --gen-operator-key is the intended caller; running it by
// hand is fine and takes the same one flag.
//
// The key is written, never printed. A secret on stdout ends up in a scrollback
// buffer, a CI log, or a `tee`; the file is created with O_EXCL at 0600 so the
// tool cannot overwrite an existing identity, and a key that is already an
// agent's on-chain id — with a bond posted against it — must never be silently
// replaced.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/svpchain/svpchain-perps-agent/internal/operator"
)

func main() {
	out := flag.String("out", "", "file to write the new key to (created 0600; must not exist)")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "operator-keygen: %v\n", err)
		os.Exit(1)
	}
}

// run mints a key into path and prints its address on stdout. Split from main
// so the refusals are testable without a subprocess.
func run(path string) error {
	if path == "" {
		return fmt.Errorf("-out is required: this tool writes the key to a file rather than printing it")
	}
	key, addr, err := operator.Generate()
	if err != nil {
		return err
	}
	// O_EXCL is the whole safety property: it fails rather than truncating,
	// which is what stops a second run from replacing the key an agent is
	// already registered under. The mode is set at creation, so the file is
	// never briefly 0644 the way a write-then-chmod would leave it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	// A trailing newline so the file is a well-formed text file; both Load and
	// the deploy script trim it.
	if _, err := fmt.Fprintf(f, "%s\n", key); err != nil {
		f.Close()
		return fmt.Errorf("write key file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close key file: %w", err)
	}
	fmt.Println(addr)
	return nil
}
