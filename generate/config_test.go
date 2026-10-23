package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An empty exclude default would make addons/** a generation target, so a glob
// written for the project's own code could start rewriting a third-party addon.
func TestDefaultExcludeMatchesFormat(t *testing.T) {
	assert.Equal(t, format.DefaultConfig().Exclude, DefaultConfig().Exclude)
}

// Union rather than first-match, matching the grain of architecture's
// dependencies, where capability is added by adding a rule. Union is also
// order-independent, so reordering cannot change the output.
func TestGeneratorsForUnionsOverlappingEntries(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{
		{Paths: []string{"**/*.gd"}, Generators: []string{"to_string"}},
		{Paths: []string{"domain/**"}, Generators: []string{"equals"}},
	}
	compiled, err := config.compile()
	require.NoError(t, err)
	got := compiled.generatorsFor("domain/hex.gd")
	assert.Equal(t, []string{"to_string", "equals"}, got)
	got = compiled.generatorsFor("ui/panel.gd")
	assert.Equal(t, []string{"to_string"}, got)
	// Reversing the entries must not change anything.
	config.Generate[0], config.Generate[1] = config.Generate[1], config.Generate[0]
	reversed, err := config.compile()
	require.NoError(t, err)
	got = reversed.generatorsFor("domain/hex.gd")
	assert.Equal(t, []string{"to_string", "equals"}, got)
}

func TestLoadConfigReturnsDefaultsWhenAbsent(t *testing.T) {
	config, err := LoadConfig(t.TempDir(), "")
	require.NoError(t, err)
	assert.Equal(t, DefaultConfig().Exclude, config.Exclude)
}

func TestLoadConfigRejectsAnUnknownKey(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"generate":[{"paths":["a/**"],"generatorz":["equals"]}]}`)
	_, err := LoadConfig(root, "")
	assert.Error(t, err, "an unknown key was accepted")
}

// Nothing in this design is version-gated, so there is no such key.
func TestConfigHasNoGodotVersionKey(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"godot_version":"4.7"}`)
	_, err := LoadConfig(root, "")
	assert.Error(t, err, "godot_version was accepted")
}

func TestLoadConfigRejectsAnUnknownGeneratorName(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"generate":[{"paths":["a/**"],"generators":["hash"]}]}`)
	_, err := LoadConfig(root, "")
	require.Error(t, err, "an unknown generator name was accepted")
	assert.Contains(t, err.Error(), "hash")
}

// A declared entry must not inherit fields from a default entry at the same
// index, which is what encoding/json would otherwise do to a slice element.
func TestLoadConfigDoesNotLeakDefaultsIntoADeclaredEntry(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"source_roots":["src"],"generate":[{"paths":["x/**"],"generators":["equals"]}]}`)
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"src"}, config.SourceRoots)
	// Exclude was omitted, so it keeps the built-in policy.
	assert.Equal(t, DefaultConfig().Exclude, config.Exclude)
	require.Len(t, config.Generate, 1)
	assert.Equal(t, []string{"equals"}, config.Generate[0].Generators)
}

func TestValidateRejectsAnEntryMissingItsKeys(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"a/**"}}}
	err := config.Validate()
	assert.Error(t, err, "an entry with no generators was accepted")
	config.Generate = []Entry{{Generators: []string{"equals"}}}
	err = config.Validate()
	assert.Error(t, err, "an entry with no paths was accepted")
}

func writeConfig(t *testing.T, root, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}
