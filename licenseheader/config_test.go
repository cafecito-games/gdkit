package licenseheader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigDefaultsAndOverlaysValues(t *testing.T) {
	root := t.TempDir()
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	require.Equal(t, DefaultConfig(), config)

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gdkit", "license.json"), []byte(`{
  "header": ["Copyright (c) 2026 Cafecito Games", "", "SPDX-License-Identifier: MIT"]
}`), 0o644))
	config, err = LoadConfig(root, "")
	require.NoError(t, err)
	require.Equal(t, 1, config.Version)
	require.Equal(t, []string{"."}, config.SourceRoots)
	require.Len(t, config.Header, 3)
}

func TestConfigRejectsUnsafeValues(t *testing.T) {
	cases := map[string]func(*Config){
		"version":     func(c *Config) { c.Version = 2 },
		"roots empty": func(c *Config) { c.SourceRoots = nil },
		"root escape": func(c *Config) { c.SourceRoots = []string{"../other"} },
		"multiline":   func(c *Config) { c.Header = []string{"first\nsecond"} },
		"marker":      func(c *Config) { c.Header = []string{beginMarker} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := DefaultConfig()
			mutate(&config)
			require.Error(t, config.Validate())
		})
	}
}

func TestLoadConfigRejectsUnknownFieldAndExplicitMissingFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.json"), []byte(`{"holder":"someone"}`), 0o644))
	_, err := LoadConfig(root, "bad.json")
	require.ErrorContains(t, err, "unknown field")
	_, err = LoadConfig(root, "missing.json")
	require.ErrorContains(t, err, "read license config")
}
