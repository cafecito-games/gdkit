package assets

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdkit/project"
)

// writeProject builds a throwaway Godot project and returns its root.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return root
}

// check loads a project the way cmd/gdkit does and runs the checker over it.
func check(t *testing.T, root string, config Config) Report {
	t.Helper()
	snapshot, err := project.Load(project.Config{
		Root:      root,
		Inventory: true,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	})
	require.NoError(t, err)
	checker, err := NewChecker(config)
	require.NoError(t, err)
	return checker.Check(snapshot)
}

// findings returns "rule path" for each diagnostic, which is what a test
// asserts on rather than formatted text.
func findings(report Report) []string {
	var result []string
	for _, diagnostic := range report.Diagnostics {
		result = append(result, diagnostic.Rule+" "+diagnostic.Path)
	}
	sort.Strings(result)
	return result
}

func testConfig() Config {
	config := DefaultConfig()
	// The default excludes addons, which most fixtures do not have; keeping
	// it would make a fixture that does silently untested.
	config.Exclude = []string{".git/**", ".godot/**", ".gdkit/**"}
	return config
}

// A live project: everything hangs off the manifest and nothing is reported.
func TestNothingIsReportedWhenEveryFileHangsOffTheManifest(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Script\" path=\"res://main.gd\" id=\"1\"]\n",
		"main.gd":      "extends Node\nconst HERO := preload(\"res://art/hero.png\")\n",
		"art/hero.png": "\x89PNG",
	})

	report := check(t, root, testConfig())
	assert.Empty(t, findings(report))
	assert.False(t, report.Failed())
}

// The tier split: a dead cluster yields exactly one unreferenced finding at
// its head and unreachable findings for what it keeps alive.
func TestADeadClusterReportsItsHeadOnceAndItsMembersSeparately(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn":     "[gd_scene format=3]\n",
		"boss/arena.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Script\" path=\"res://boss/boss.gd\" id=\"1\"]\n",
		"boss/boss.gd":  "extends Node\nconst ART := preload(\"res://boss/boss.png\")\n",
		"boss/boss.png": "\x89PNG",
	})

	report := check(t, root, testConfig())
	assert.Equal(t, []string{
		"asset.unreachable boss/boss.gd",
		"asset.unreachable boss/boss.png",
		"asset.unreferenced boss/arena.tscn",
	}, findings(report))
	// Only the confident tier fails the run by default.
	assert.True(t, report.Failed())
}

// The second tier is advisory by default, so a project adopting the tool is
// not blocked by the findings it has not reviewed yet.
func TestOnlyTheConfidentTierFailsTheRunByDefault(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn":     "[gd_scene format=3]\n",
		"dead/a.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Script\" path=\"res://dead/b.gd\" id=\"1\"]\n",
		"dead/b.gd": "extends Node\n",
	})

	config := testConfig()
	config.Severity = map[string]Severity{RuleUnreferenced: SeverityWarning}
	report := check(t, root, config)
	assert.Equal(t, []string{"asset.unreachable dead/b.gd", "asset.unreferenced dead/a.tscn"}, findings(report))
	assert.False(t, report.Failed())
}

// Exclusion narrows reporting, never the universe: an asset used only from an
// excluded file is still in use, and must not be reported.
func TestAnAssetUsedOnlyFromAnExcludedFileIsNotReported(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"PackedScene\" path=\"res://addons/thing/panel.tscn\" id=\"1\"]\n",
		"addons/thing/panel.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Texture2D\" path=\"res://art/panel.png\" id=\"1\"]\n",
		"art/panel.png": "\x89PNG",
	})

	config := testConfig()
	config.Exclude = append(config.Exclude, "addons/**")
	report := check(t, root, config)
	// The addon is not reportable, and the texture it is the only user of is
	// still reachable through it.
	assert.Empty(t, findings(report))
}

func TestAKeptFileIsARootAndKeepsItsOwnDependencies(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot":        "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn":            "[gd_scene format=3]\n",
		"tests/combat_test.gd": "extends Node\nconst FIXTURE := preload(\"res://tests/fixture.tres\")\n",
		"tests/fixture.tres":   "[gd_resource type=\"Resource\" format=3]\n",
	})

	config := testConfig()
	config.Keep = []Keep{{Pattern: "tests/**", Reason: "GUT discovers these by scanning the directory"}}
	report := check(t, root, config)
	// Pinning the test without keeping its fixture would make the key useless.
	assert.Empty(t, findings(report))
}

func TestADynamicLoadKeepsThePrefixItNamesAndDisclosesItself(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Script\" path=\"res://spawner.gd\" id=\"1\"]\n",
		"spawner.gd": "extends Node\n" +
			"func spawn(id: String) -> Resource:\n" +
			"\treturn load(\"res://data/\" + id + \".tres\")\n",
		"data/goblin.tres": "[gd_resource type=\"Resource\" format=3]\n",
		"data/orc.tres":    "[gd_resource type=\"Resource\" format=3]\n",
		"art/unused.png":   "\x89PNG",
	})

	report := check(t, root, testConfig())
	assert.Equal(t, []string{"asset.dynamic spawner.gd", "asset.unreferenced art/unused.png"}, findings(report))
	// The directory the expression named is kept; a directory it did not name
	// is not.
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == RuleDynamic {
			assert.Equal(t, "data/", diagnostic.Target)
		}
	}
}

func TestAFormatStringLoadNamesItsDirectory(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Script\" path=\"res://spawner.gd\" id=\"1\"]\n",
		"spawner.gd":       "extends Node\nfunc get_one(id: String) -> Resource:\n\treturn load(\"res://data/%s.tres\" % id)\n",
		"data/goblin.tres": "[gd_resource type=\"Resource\" format=3]\n",
	})

	report := check(t, root, testConfig())
	assert.Equal(t, []string{"asset.dynamic spawner.gd"}, findings(report))
}

func TestAnOpaqueLoadKeepsNothingAndSaysSo(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Script\" path=\"res://spawner.gd\" id=\"1\"]\n",
		"spawner.gd":     "extends Node\nvar path: String\nfunc spawn() -> Resource:\n\treturn load(path)\n",
		"art/unused.png": "\x89PNG",
	})

	report := check(t, root, testConfig())
	assert.Equal(t, []string{"asset.dynamic spawner.gd", "asset.unreferenced art/unused.png"}, findings(report))
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == RuleDynamic {
			assert.Empty(t, diagnostic.Target)
			assert.Contains(t, diagnostic.Message, "nothing here can be proven unused")
		}
	}
}

func TestAReferenceToANonexistentFileIsReportedAtItsSite(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn": "[gd_scene load_steps=2 format=3]\n\n" +
			"[ext_resource type=\"Texture2D\" path=\"res://art/heart.png\" id=\"1\"]\n",
	})

	report := check(t, root, testConfig())
	require.Len(t, report.Diagnostics, 1)
	assert.Equal(t, RuleMissing, report.Diagnostics[0].Rule)
	assert.Equal(t, "main.tscn", report.Diagnostics[0].Path)
	assert.Equal(t, 3, report.Diagnostics[0].Line)
	assert.Equal(t, "res://art/heart.png", report.Diagnostics[0].Target)
}

// An inferred reference may keep a file alive but must never be the evidence
// that one is missing: a version string reads as a path.
func TestAnInferredReferenceNeverReportsSomethingMissing(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot":           "[application]\n",
		"addons/thing/plugin.cfg": "[plugin]\nname=\"Thing\"\nversion=\"1.0\"\nscript=\"plugin.gd\"\n",
		"addons/thing/plugin.gd":  "@tool\nextends EditorPlugin\n",
	})

	report := check(t, root, testConfig())
	for _, diagnostic := range report.Diagnostics {
		assert.NotEqual(t, RuleMissing, diagnostic.Rule, "inferred reference reported as missing")
	}
}

// A root under a manifest key gdkit does not name must still be a root. The
// manifest is read for its values, not for a list of keys.
func TestARootUnderAnUnnamedManifestKeyIsStillARoot(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot":    "[application]\n\n[audio]\nbuses/default_bus_layout=\"res://audio/buses.tres\"\n",
		"audio/buses.tres": "[gd_resource type=\"AudioBusLayout\" format=3]\n",
	})

	report := check(t, root, testConfig())
	assert.Empty(t, findings(report))
}

func TestAShaderIncludeKeepsItsTargetAlive(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot":              "[application]\n\n[rendering]\nenvironment/defaults/default_environment=\"res://shaders/water.gdshader\"\n",
		"shaders/water.gdshader":     "shader_type spatial;\n#include \"common.gdshaderinc\"\n",
		"shaders/common.gdshaderinc": "// shared\n",
	})

	report := check(t, root, testConfig())
	assert.Empty(t, findings(report))
}

// A path handed to something other than load is still a dependency.
func TestANonLoadResourcePathKeepsItsTargetAlive(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\n\n[autoload]\nWaves=\"*res://waves.gd\"\n",
		"waves.gd": "extends Node\n" +
			"func read() -> void:\n" +
			"\tFileAccess.open(\"res://data/waves.json\", FileAccess.READ)\n",
		"data/waves.json": "{}\n",
	})

	report := check(t, root, testConfig())
	assert.Empty(t, findings(report))
}

// A file outside asset_extensions is never reported, but still carries its
// references. A clean run that looked at nothing is distinguishable from one
// that looked at everything.
func TestAnUnlistedExtensionIsNeverReportedButStillCarriesReferences(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot":  "[application]\nconfig/icon=\"res://art/hero.png\"\n",
		"readme.md":      "# notes\n",
		"skin.cfg":       "[skin]\nportrait=\"res://art/face.png\"\n",
		"art/hero.png":   "\x89PNG",
		"art/face.png":   "\x89PNG",
		"art/unused.png": "\x89PNG",
	})

	report := check(t, root, testConfig())
	// A .cfg is not an asset extension, so it is never itself reported — but
	// it is read, and the texture it alone names stays alive. Only the
	// genuinely unused one is reported, and the counts say how much of the
	// project was eligible to be.
	assert.Equal(t, []string{
		"asset.unreachable art/face.png",
		"asset.unreferenced art/unused.png",
	}, findings(report))
	assert.Equal(t, 6, report.Inventoried)
	assert.Equal(t, 3, report.Reportable)
}

func TestASelfReferencingSceneIsStillReported(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn":     "[gd_scene format=3]\n",
		"dead.tres":     "[gd_resource type=\"Resource\" format=3]\n\n[resource]\nself_path = \"res://dead.tres\"\n",
	})

	report := check(t, root, testConfig())
	assert.Equal(t, []string{"asset.unreferenced dead.tres"}, findings(report))
}

func TestDiagnosticsAreSortedByPathThenLineThenRule(t *testing.T) {
	root := writeProject(t, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://b.tscn\"\n",
		"b.tscn": "[gd_scene load_steps=3 format=3]\n\n" +
			"[ext_resource type=\"Texture2D\" path=\"res://gone_b.png\" id=\"1\"]\n" +
			"[ext_resource type=\"Texture2D\" path=\"res://gone_a.png\" id=\"2\"]\n",
		"a.png": "\x89PNG",
	})

	report := check(t, root, testConfig())
	var lines []string
	for _, diagnostic := range report.Diagnostics {
		lines = append(lines, diagnostic.Path+":"+strings.Repeat("0", 0)+itoa(diagnostic.Line)+" "+diagnostic.Rule)
	}
	assert.Equal(t, []string{"a.png:0 asset.unreferenced", "b.tscn:3 asset.missing", "b.tscn:4 asset.missing"}, lines)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
