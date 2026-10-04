package generate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/format"
)

// An empty exclude default would make addons/** a generation target, so a glob
// written for the project's own code could start rewriting a third-party addon.
func TestDefaultExcludeMatchesFormat(t *testing.T) {
	if !slices.Equal(DefaultConfig().Exclude, format.DefaultConfig().Exclude) {
		t.Errorf("Exclude = %v, want format's %v", DefaultConfig().Exclude, format.DefaultConfig().Exclude)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if got := compiled.generatorsFor("domain/hex.gd"); !slices.Equal(got, []string{"to_string", "equals"}) {
		t.Errorf("generatorsFor(domain) = %v, want both in registry order", got)
	}
	if got := compiled.generatorsFor("ui/panel.gd"); !slices.Equal(got, []string{"to_string"}) {
		t.Errorf("generatorsFor(ui) = %v, want only to_string", got)
	}
	// Reversing the entries must not change anything.
	config.Generate[0], config.Generate[1] = config.Generate[1], config.Generate[0]
	reversed, err := config.compile()
	if err != nil {
		t.Fatal(err)
	}
	if got := reversed.generatorsFor("domain/hex.gd"); !slices.Equal(got, []string{"to_string", "equals"}) {
		t.Errorf("order changed the result: %v", got)
	}
}

func TestLoadConfigReturnsDefaultsWhenAbsent(t *testing.T) {
	config, err := LoadConfig(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(config.Exclude, DefaultConfig().Exclude) {
		t.Errorf("Exclude = %v, want the defaults", config.Exclude)
	}
}

func TestLoadConfigRejectsAnUnknownKey(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"generate":[{"paths":["a/**"],"generatorz":["equals"]}]}`)
	if _, err := LoadConfig(root, ""); err == nil {
		t.Error("an unknown key was accepted")
	}
}

// Nothing in this design is version-gated, so there is no such key.
func TestConfigHasNoGodotVersionKey(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"godot_version":"4.7"}`)
	if _, err := LoadConfig(root, ""); err == nil {
		t.Error("godot_version was accepted")
	}
}

func TestLoadConfigRejectsAnUnknownGeneratorName(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"generate":[{"paths":["a/**"],"generators":["hash"]}]}`)
	_, err := LoadConfig(root, "")
	if err == nil || !strings.Contains(err.Error(), "hash") {
		t.Errorf("err = %v, want one naming hash", err)
	}
}

// A declared entry must not inherit fields from a default entry at the same
// index, which is what encoding/json would otherwise do to a slice element.
func TestLoadConfigDoesNotLeakDefaultsIntoADeclaredEntry(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"source_roots":["src"],"generate":[{"paths":["x/**"],"generators":["equals"]}]}`)
	config, err := LoadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(config.SourceRoots, []string{"src"}) {
		t.Errorf("SourceRoots = %v, want [src]", config.SourceRoots)
	}
	// Exclude was omitted, so it keeps the built-in policy.
	if !slices.Equal(config.Exclude, DefaultConfig().Exclude) {
		t.Errorf("Exclude = %v, want the defaults", config.Exclude)
	}
	if len(config.Generate) != 1 || !slices.Equal(config.Generate[0].Generators, []string{"equals"}) {
		t.Errorf("Generate = %+v", config.Generate)
	}
}

func TestValidateRejectsAnEntryMissingItsKeys(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"a/**"}}}
	if err := config.Validate(); err == nil {
		t.Error("an entry with no generators was accepted")
	}
	config.Generate = []Entry{{Generators: []string{"equals"}}}
	if err := config.Validate(); err == nil {
		t.Error("an entry with no paths was accepted")
	}
}

func writeConfig(t *testing.T, root, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
