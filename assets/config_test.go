package assets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdkit/internal/failure"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, DefaultConfigPath), []byte(body), 0o644))
	return root
}

func kindOf(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	wrapped, ok := failure.Of(err)
	require.True(t, ok, "failure is unlabelled: %v", err)
	return string(wrapped.Kind)
}

func TestMissingConfigIsTheDefaultPolicy(t *testing.T) {
	config, err := LoadConfig(t.TempDir(), "")
	require.NoError(t, err)
	assert.Equal(t, DefaultConfig(), config)
}

func TestAnExplicitlyNamedMissingConfigIsAnError(t *testing.T) {
	_, err := LoadConfig(t.TempDir(), "nowhere.json")
	assert.Equal(t, string(failure.ConfigRead), kindOf(t, err))
}

func TestOmittedListsKeepTheirDefaults(t *testing.T) {
	root := writeConfig(t, `{"version": 1, "keep": [{"pattern": "tests/**", "reason": "scanned"}]}`)
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	assert.Equal(t, DefaultConfig().Exclude, config.Exclude)
	assert.Equal(t, DefaultConfig().AssetExtensions, config.AssetExtensions)
	assert.Equal(t, []Keep{{Pattern: "tests/**", Reason: "scanned"}}, config.Keep)
}

// A declared entry must not inherit fields from the default entry sitting at
// the same index, which is what encoding/json does to a slice it decodes onto.
func TestADeclaredKeepEntryInheritsNothing(t *testing.T) {
	root := writeConfig(t, `{"version": 1, "keep": [{"pattern": "a/**", "reason": "x"}]}`)
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	require.Len(t, config.Keep, 1)
	assert.Equal(t, Keep{Pattern: "a/**", Reason: "x"}, config.Keep[0])
}

func TestUnknownKeysAreRejected(t *testing.T) {
	root := writeConfig(t, `{"version": 1, "asset_extensons": [".png"]}`)
	_, err := LoadConfig(root, "")
	assert.Equal(t, string(failure.ConfigParse), kindOf(t, err))
}

// A decoder reads only the first JSON value, so without an explicit check a
// config followed by anything else would load with its floor unapplied.
func TestContentAfterTheTopLevelObjectIsRejected(t *testing.T) {
	root := writeConfig(t, `{"version": 1}{"version": 2}`)
	_, err := LoadConfig(root, "")
	assert.Equal(t, string(failure.ConfigParse), kindOf(t, err))
}

// A config written for a newer gdkit carries both the floor and the syntax
// that needed it, so the floor must be reported first or the reader is sent
// hunting for a typo instead of upgrading.
func TestTheVersionFloorIsReportedBeforeAnUnknownKey(t *testing.T) {
	previous := detectedVersion
	detectedVersion = func() string { return "0.1.0" }
	t.Cleanup(func() { detectedVersion = previous })

	root := writeConfig(t, `{"version": 1, "minimum_gdkit_version": "9.0.0", "future_key": true}`)
	_, err := LoadConfig(root, "")
	assert.Equal(t, string(failure.ConfigVersionFloor), kindOf(t, err))
}

func TestASatisfiedFloorLoads(t *testing.T) {
	previous := detectedVersion
	detectedVersion = func() string { return "1.2.3" }
	t.Cleanup(func() { detectedVersion = previous })

	root := writeConfig(t, `{"version": 1, "minimum_gdkit_version": "1.0.0"}`)
	_, err := LoadConfig(root, "")
	assert.NoError(t, err)
}

func TestInvalidConfigurationsAreRejected(t *testing.T) {
	cases := map[string]string{
		"unsupported version":     `{"version": 2}`,
		"no source roots":         `{"version": 1, "source_roots": []}`,
		"source root escapes":     `{"version": 1, "source_roots": ["../elsewhere"]}`,
		"keep without a reason":   `{"version": 1, "keep": [{"pattern": "a/**"}]}`,
		"keep without a pattern":  `{"version": 1, "keep": [{"reason": "x"}]}`,
		"extension without a dot": `{"version": 1, "asset_extensions": ["png"]}`,
		"severity names no rule":  `{"version": 1, "severity": {"asset.unrefernced": "warning"}}`,
		"severity is not a level": `{"version": 1, "severity": {"asset.unreferenced": "info"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfig(writeConfig(t, body), "")
			assert.Equal(t, string(failure.ConfigInvalid), kindOf(t, err))
		})
	}
}

func TestSeverityOverridesApplyAndTheRestKeepTheirDefaults(t *testing.T) {
	root := writeConfig(t, `{"version": 1, "severity": {"asset.unreachable": "error"}}`)
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	compiled, err := config.validate()
	require.NoError(t, err)
	assert.Equal(t, SeverityError, compiled.severity[RuleUnreachable])
	assert.Equal(t, SeverityWarning, compiled.severity[RuleDynamic])
	assert.Equal(t, SeverityError, compiled.severity[RuleUnreferenced])
}

func TestEveryRuleHasADefaultSeverity(t *testing.T) {
	for _, rule := range Rules {
		assert.Contains(t, defaultSeverity, rule, "rule %q has no default severity", rule)
	}
	assert.Len(t, defaultSeverity, len(Rules))
}
