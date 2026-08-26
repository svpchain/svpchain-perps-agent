package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/owner"
)

func TestRunWritesKeyAt0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.key")
	require.NoError(t, run(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the file must never be readable by other users, not even briefly")

	// What landed must be what Load reads back.
	t.Setenv(owner.KeyEnvVar, "")
	priv, addr, err := owner.Load(path)
	require.NoError(t, err)
	require.NotNil(t, priv)
	require.NotEmpty(t, addr)
}

// The O_EXCL refusal is the whole safety property: this key is both the
// agent's on-chain id and the account holding its bond, so silently truncating
// one would strand a registration with nothing reporting a fault.
func TestRunRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.key")
	require.NoError(t, run(path))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.ErrorContains(t, run(path), "create key file")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "the existing key must be left untouched")
}

func TestRunRequiresOut(t *testing.T) {
	require.ErrorContains(t, run(""), "-out is required")
}
