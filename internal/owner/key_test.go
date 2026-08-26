package owner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-perps-agent/internal/owner"
)

func TestGenerateProducesLoadableKey(t *testing.T) {
	hexKey, addr, err := owner.Generate()
	require.NoError(t, err)
	require.Len(t, hexKey, 64, "64 hex characters, no 0x — the form Load reads")
	require.Regexp(t, `^svp1[0-9a-z]+$`, addr)

	// Round-trip: what Generate returns must derive the address it reported,
	// or the operator funds the wrong account.
	t.Setenv(owner.KeyEnvVar, hexKey)
	priv, loaded, err := owner.Load("")
	require.NoError(t, err)
	require.NotNil(t, priv)
	require.Equal(t, addr, loaded)
}

func TestGenerateIsUniquePerCall(t *testing.T) {
	_, a, err := owner.Generate()
	require.NoError(t, err)
	_, b, err := owner.Generate()
	require.NoError(t, err)
	require.NotEqual(t, a, b, "a key is an identity; two calls must not collide")
}

func TestLoadPrefersEnvOverFile(t *testing.T) {
	envKey, envAddr, err := owner.Generate()
	require.NoError(t, err)
	fileKey, _, err := owner.Generate()
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "owner.key")
	require.NoError(t, os.WriteFile(path, []byte(fileKey+"\n"), 0o600))

	t.Setenv(owner.KeyEnvVar, envKey)
	_, addr, err := owner.Load(path)
	require.NoError(t, err)
	require.Equal(t, envAddr, addr)
}

// The natural way to write this key file is `echo` or a here-doc, both of
// which leave a trailing newline; a key that fails to parse over whitespace
// would be a maddening error.
func TestLoadTrimsFileWhitespace(t *testing.T) {
	hexKey, addr, err := owner.Generate()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "owner.key")
	require.NoError(t, os.WriteFile(path, []byte("  "+hexKey+"\n\n"), 0o600))

	t.Setenv(owner.KeyEnvVar, "")
	_, loaded, err := owner.Load(path)
	require.NoError(t, err)
	require.Equal(t, addr, loaded)
}

// Absent from both sources is the ordinary state of a machine that is not
// registering anything, so it is not an error here — the caller decides.
func TestLoadAbsentIsNotAnError(t *testing.T) {
	t.Setenv(owner.KeyEnvVar, "")
	priv, addr, err := owner.Load("")
	require.NoError(t, err)
	require.Nil(t, priv)
	require.Empty(t, addr)
}

func TestLoadRejectsMalformedKey(t *testing.T) {
	t.Setenv(owner.KeyEnvVar, "not-a-key")
	_, _, err := owner.Load("")
	require.ErrorContains(t, err, "parse owner key")
}

func TestLoadReportsMissingFile(t *testing.T) {
	t.Setenv(owner.KeyEnvVar, "")
	_, _, err := owner.Load(filepath.Join(t.TempDir(), "absent.key"))
	require.ErrorContains(t, err, "read owner key file")
}
