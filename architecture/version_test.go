package architecture

import (
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/internal/versiongate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pretendVersion makes LoadConfig see a fixed binary version for one test.
func pretendVersion(t *testing.T, value string) {
	t.Helper()
	previous := detectedVersion
	t.Cleanup(func() { detectedVersion = previous })
	detectedVersion = func() string { return value }
}

func TestLoadConfigAcceptsASatisfiedMinimumVersion(t *testing.T) {
	for _, binary := range []string{"0.3.0", "0.3.1", "0.4.0", "1.0.0", "0.3.0-SNAPSHOT-9060007"} {
		t.Run(binary, func(t *testing.T) {
			pretendVersion(t, binary)
			root := t.TempDir()
			writeProject(t, root, map[string]string{
				DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
			})
			config, err := LoadConfig(root, "")
			require.NoError(t, err)
			require.Equal(t, "0.3.0", config.MinimumGDKitVersion)
		})
	}
}

func TestLoadConfigRejectsABinaryBelowTheMinimumVersion(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	for _, want := range []string{"0.3.0", "0.2.0", DefaultConfigPath} {
		assert.Contains(t, err.Error(), want)
	}
}

// The floor must be read before unknown-key reporting. A config written for a
// newer gdkit uses both the new field and the new syntax, and naming the
// unknown key would point the reader at the wrong problem.
func TestLoadConfigReportsTheMinimumVersionBeforeAnUnknownKey(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0", "zones": ["anything"]}`,
	})
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "0.3.0")
	require.NotContains(t, err.Error(), "zones")
}

func TestLoadConfigReportsTheMinimumVersionBeforeAValidationError(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0", "classifications": [{"pattern": "aaa/**", "feature": "f"}]}`,
	})
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "0.3.0")
	require.NotContains(t, err.Error(), "requires a layer")
}

func TestLoadConfigRejectsAMalformedMinimumVersion(t *testing.T) {
	for _, value := range []string{"0.3", "v0.3.0", "dev", "latest", "0.3.0-rc.1"} {
		t.Run(value, func(t *testing.T) {
			pretendVersion(t, "0.2.0")
			root := t.TempDir()
			writeProject(t, root, map[string]string{
				DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "` + value + `"}`,
			})
			_, err := LoadConfig(root, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), "minimum_gdkit_version")
			require.Contains(t, err.Error(), "major.minor.patch")
		})
	}
}

func TestLoadConfigIgnoresAnAbsentMinimumVersion(t *testing.T) {
	pretendVersion(t, "0.0.1")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1}`,
	})
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	require.Empty(t, config.MinimumGDKitVersion)
}

func TestLoadConfigFailsClosedOnADevelopmentBuild(t *testing.T) {
	pretendVersion(t, "dev")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), versiongate.AllowDevelopmentEnvironmentVariable)

	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	_, err = LoadConfig(root, "")
	require.NoError(t, err)
}

// The bypass covers development builds only; it must not excuse a real
// version that is below the floor.
func TestLoadConfigBypassDoesNotExcuseAnOldRelease(t *testing.T) {
	pretendVersion(t, "0.2.0")
	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	_, err := LoadConfig(root, "")
	require.Error(t, err)
}

func TestLoadConfigNamesTheResolvedConfigPath(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	name := filepath.ToSlash(filepath.Join("config", "strict.json"))
	writeProject(t, root, map[string]string{
		name: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	_, err := LoadConfig(root, name)
	require.Error(t, err)
	require.Contains(t, filepath.ToSlash(err.Error()), "strict.json")
}

// A generated config must not pin itself to whichever binary generated it.
func TestDefaultConfigDeclaresNoMinimumVersion(t *testing.T) {
	require.Empty(t, DefaultConfig().MinimumGDKitVersion)
}

// encoding/json's Decoder reads one value and stops, while the whole-document
// checks ahead of it require the file to be exactly one value. Trailing
// content would otherwise skip both of them and load as a valid config,
// letting a binary below the floor analyze the project and emit a report.
func TestLoadConfigRejectsContentAfterTheTopLevelObject(t *testing.T) {
	cases := map[string]string{
		"floor":       `{"version": 1, "minimum_gdkit_version": "0.3.0"} trailing`,
		"unknown key": `{"version": 1, "zones": []} trailing`,
		"second object": `{"version": 1}
{"version": 1}`,
		"bare token": `{"version": 1} null`,
		// json.Decoder.More reports whether another array or object element
		// follows, so it answers false for a closing delimiter and cannot
		// stand in for an end-of-input check.
		"closing brace":   `{"version": 1, "minimum_gdkit_version": "0.3.0"}}`,
		"closing bracket": `{"version": 1, "minimum_gdkit_version": "0.3.0"}]`,
		"two closing":     `{"version": 1, "minimum_gdkit_version": "0.3.0"}}}`,
		"comma":           `{"version": 1, "minimum_gdkit_version": "0.3.0"},`,
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			pretendVersion(t, "0.2.0")
			root := t.TempDir()
			writeProject(t, root, map[string]string{DefaultConfigPath: contents})
			_, err := LoadConfig(root, "")
			require.Error(t, err)
		})
	}
}

// Genuinely malformed JSON keeps the decoder's own message, which carries the
// offset that locates the problem.
func TestLoadConfigReportsMalformedJSONWithItsOffset(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{DefaultConfigPath: `{"version": 1,`})
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse config")
}
