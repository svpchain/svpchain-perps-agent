package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/operator"
)

// What lands on disk has to be what the agent boots on, and nothing else: the
// deploy ships this file verbatim as the compose secret and agent.toml points
// key_file at the mount.
func TestRunWritesALoadableKeyAt0600(t *testing.T) {
	t.Setenv(operator.KeyEnvVar, "") // the file is under test, not the env override

	path := filepath.Join(t.TempDir(), "operator.key")
	if err := run(path); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %04o, want 0600", perm)
	}

	priv, addr, err := operator.Load(config.Operator{KeyFile: path})
	if err != nil {
		t.Fatalf("the agent cannot load the key this wrote: %v", err)
	}
	if priv == nil || addr == "" {
		t.Fatal("loaded no key")
	}
}

// O_EXCL is the safety property, not a convenience: a second run must not be
// able to replace a key an agent is already registered under, because the
// registration and the bond posted against it would be stranded with nothing
// on either side reporting a problem.
func TestRunRefusesToOverwriteAnExistingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.key")
	existing := []byte("do not clobber me\n")
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run(path); err == nil {
		t.Fatal("run overwrote an existing key file")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(existing) {
		t.Errorf("existing key file was modified: %q", got)
	}
}

// Without -out the tool would have to print the key, and a secret on stdout
// ends up in scrollback, a CI log, or a pipe. Refusing is the feature.
func TestRunRequiresAnOutputPath(t *testing.T) {
	if err := run(""); err == nil {
		t.Fatal("run accepted an empty -out")
	}
}
