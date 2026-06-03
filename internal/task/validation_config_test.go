package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadValidationConfigWritesDefaultsOnFirstRun(t *testing.T) {
	// Not parallel: mutates process env (XDG_CONFIG_HOME) and package-level
	// sync.Once cache.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir) // macOS UserConfigDir falls back to $HOME/Library/...

	base, err := os.UserConfigDir()
	require.NoError(t, err)
	path := filepath.Join(base, "afk", "validation.yaml")

	// File absent -> loader returns defaults and writes them.
	cfg := loadValidationConfig()
	require.Equal(t, defaultValidationConfig(), cfg)
	require.FileExists(t, path)

	// File now present -> loader reads it back equal to defaults.
	cfg2 := loadValidationConfig()
	require.Equal(t, defaultValidationConfig(), cfg2)
}

func TestLoadValidationConfigReadsCustomFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	base, err := os.UserConfigDir()
	require.NoError(t, err)
	path := filepath.Join(base, "afk", "validation.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("exact_bodies:\n  - custom phrase\n"), 0o644))

	cfg := loadValidationConfig()
	require.Equal(t, []string{"custom phrase"}, cfg.ExactBodies)
}
