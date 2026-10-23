package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzerIndexesReferencesAndIgnoresCommentsAndStrings(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/combat/domain/health.gd": `class_name Health
var amount: int
`,
		"features/combat/application/ports/reader.gd": `class_name HealthReader
func read() -> Health:
	return Health.new()
`,
		"features/combat/application/intents/heal.gd": `class_name HealIntent
`,
		"features/combat/infrastructure/store.gd": `class_name InfraStore
var reader: HealthReader
`,
		"features/combat/presentation/view.gd": `class_name CombatView
var intent: HealIntent
var text = "InfraStore"
var delegated = custom.load("res://not-a-static-project-load.tres")
# InfraStore must not become a dependency.
func accepts_shadow(InfraStore):
	return InfraStore
`,
	})

	report := analyzeDefault(t, root)
	require.False(t, report.HasErrors())
	assertEdge(t, report, "features/combat/application/ports/reader.gd", "features/combat/domain/health.gd")
	assertEdge(t, report, "features/combat/infrastructure/store.gd", "features/combat/application/ports/reader.gd")
	assertEdge(t, report, "features/combat/presentation/view.gd", "features/combat/application/intents/heal.gd")
	found := false
	for _, edge := range report.Edges {
		if edge.From == "features/combat/presentation/view.gd" && edge.To == "features/combat/infrastructure/store.gd" {
			found = true
		}
	}
	assert.False(t, found, "class name in a comment or string became an edge")
}

func TestAnalyzerRejectsDirectionCyclesAndEngineAccess(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/combat/domain/a.gd": `class_name A
var b: B
`,
		"features/combat/domain/b.gd": `class_name B
var a: A
`,
		"features/combat/application/service.gd": `class_name CombatService extends Node
signal changed
func tick():
	Input.is_action_pressed("attack")
	get_tree()
`,
		"features/combat/infrastructure/store.gd": `class_name Store
`,
		"features/combat/presentation/view.gd": `class_name View
var store: Store
`,
	})

	report := analyzeDefault(t, root)
	rules := diagnosticRules(report)
	for _, expected := range []string{"dependency.cycle", "dependency.direction", "engine.reference", "engine.signal", "engine.tree"} {
		assert.Contains(t, rules, expected)
	}
}

func TestAnalyzerChecksResourcesAndPureTests(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/combat/domain/tests/health_test.gd": `class_name HealthTest
var scene = preload("res://features/combat/presentation/health.tscn")
func inspect():
	return $Root
`,
		"features/combat/presentation/health.tscn": `[gd_scene format=3]
`,
		"features/combat/infrastructure/broken.gd": `class_name Broken
var missing = load("res://missing.tres")
`,
	})

	report := analyzeDefault(t, root)
	rules := diagnosticRules(report)
	for _, expected := range []string{"dependency.direction", "resource.missing", "test.node_inspection", "test.scene_load"} {
		assert.Contains(t, rules, expected)
	}
	assertEdge(t, report, "features/combat/domain/tests/health_test.gd", "features/combat/presentation/health.tscn")
}

func TestRuntimeBoundaryMayDeclareSignals(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/combat/application/runtime_boundaries/events.gd": `class_name Events
signal happened
`,
	})

	report := analyzeDefault(t, root)
	require.False(t, report.HasErrors())
}

func TestAllowlistRequiresExistingADR(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/combat/infrastructure/store.gd": "class_name Store\n",
		"features/combat/presentation/view.gd":    "class_name View\nvar store: Store\n",
	})
	writeJSON(t, filepath.Join(root, DefaultAllowlistPath), Allowlist{
		Version: 1,
		Exceptions: []Exception{{
			Rule: "dependency.direction", From: "features/combat/presentation/view.gd",
			To: "features/combat/infrastructure/store.gd", Reason: "migration", ADR: "docs/adr/0001.md",
		}},
	})

	report := analyzeDefault(t, root)
	require.Contains(t, diagnosticRules(report), "allowlist.adr")
	require.Contains(t, diagnosticRules(report), "dependency.direction")

	writeProject(t, root, map[string]string{"docs/adr/0001.md": "# Temporary dependency\n"})
	report = analyzeDefault(t, root)
	require.False(t, report.HasErrors())
}

func TestDuplicateClassAndUnclassifiedFile(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"domain/one.gd": "class_name Duplicate\n",
		"domain/two.gd": "class_name Duplicate\n",
		"misc/file.gd":  "class_name Misc\n",
	})

	report := analyzeDefault(t, root)
	rules := diagnosticRules(report)
	require.Contains(t, rules, "class_name.duplicate")
	require.Contains(t, rules, "classification.missing")
}

func TestClassificationCapturesFeature(t *testing.T) {
	config := DefaultConfig()
	got, ok := config.classify("src/features/inventory/domain/item.gd")
	require.True(t, ok)
	require.Equal(t, "domain", got.Layer)
	require.Equal(t, "inventory", got.Feature)
}

func analyzeDefault(t *testing.T, root string) Report {
	t.Helper()
	analyzer, err := NewAnalyzer(root, DefaultConfig())
	require.NoError(t, err)
	report, err := analyzer.Analyze()
	require.NoError(t, err)
	return report
}

func writeProject(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(absolute), 0o755))
		require.NoError(t, os.WriteFile(absolute, []byte(contents), 0o644))
	}
}

func writeJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	writeProject(t, filepath.Dir(filepath.Dir(name)), map[string]string{filepath.Base(filepath.Dir(name)) + "/" + filepath.Base(name): string(data)})
}

func diagnosticRules(report Report) []string {
	rules := make([]string, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		rules = append(rules, diagnostic.Rule)
	}
	return rules
}

func assertEdge(t *testing.T, report Report, from, to string) {
	t.Helper()
	found := false
	for _, edge := range report.Edges {
		if edge.From == from && edge.To == to {
			found = true
		}
	}
	assert.True(t, found, "missing edge %s -> %s", from, to)
}

func TestAnalyzerResolvesIdentifiersDeclaredInsideResources(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/combat/presentation/view.gd": `class_name View
var panel = preload("uid://dpanel")
var theme = load("uid://ctheme")
`,
		"features/combat/presentation/panel.tscn": `[gd_scene load_steps=2 format=3 uid="uid://dpanel"]
`,
		"features/combat/presentation/panel.theme.tres": `[gd_resource type="Theme" format=3 uid="uid://ctheme"]
`,
	})

	report := analyzeDefault(t, root)
	require.False(t, report.HasErrors())
	assertEdge(t, report, "features/combat/presentation/view.gd", "features/combat/presentation/panel.tscn")
	assertEdge(t, report, "features/combat/presentation/view.gd", "features/combat/presentation/panel.theme.tres")
}

// A rule could scope its target with to_paths but not its source, so a port
// permission had to be granted to the port's whole layer.
func TestDependencyFromPathsNarrowsTheRuleSource(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/probe/application/read_models/probe_state.gd": "class_name ProbeState\n",
		"features/probe/application/ports/probe_port.gd": `class_name ProbePort
var state: ProbeState
`,
		"features/probe/application/probe_service.gd": `class_name ProbeService
var state: ProbeState
`,
	})

	widened := configAllowingReadModels(nil)
	require.False(t, analyzeWith(t, root, widened).HasErrors())

	narrowed := configAllowingReadModels([]string{"**/ports/**"})
	report := analyzeWith(t, root, narrowed)
	violations := 0
	for _, diagnostic := range report.Diagnostics {
		require.Equal(t, "dependency.direction", diagnostic.Rule)
		require.Equal(t, "features/probe/application/probe_service.gd", diagnostic.Location.Path)
		violations++
	}
	require.Equal(t, 1, violations)
	assertEdge(t, report, "features/probe/application/ports/probe_port.gd", "features/probe/application/read_models/probe_state.gd")
}

// configAllowingReadModels lets application files name same-feature read
// models, optionally only from fromPaths.
func configAllowingReadModels(fromPaths []string) Config {
	config := DefaultConfig()
	for index, rule := range config.Dependencies {
		if rule.FromLayers[0] != "application" || rule.ToLayers[0] != "application" {
			continue
		}
		config.Dependencies[index].ToPaths = append(slices.Clone(rule.ToPaths), "**/read_models/**")
		config.Dependencies[index].FromPaths = fromPaths
	}
	return config
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	cases := map[string]struct {
		contents string
		wantKey  string
	}{
		"top level": {
			contents: `{"version": 1, "dependancies": []}`,
			wantKey:  `"dependancies"`,
		},
		"dependency rule": {
			contents: `{"version": 1, "dependencies": [{"from_layers": ["domain"], "to_layers": ["domain"], "form_paths": ["**/ports/**"]}]}`,
			wantKey:  `"dependencies[0].form_paths"`,
		},
		"classification entry": {
			contents: `{"version": 1, "classifications": [{"pattern": "aaa/**", "layer": "domain", "feature": "f"}, {"pattern": "bbb/**", "layre": "presentation", "feature": "f"}]}`,
			wantKey:  `"classifications[1].layre"`,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeProject(t, root, map[string]string{DefaultConfigPath: testCase.contents})
			_, err := LoadConfig(root, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.wantKey)
		})
	}
}

func TestLoadConfigRequiresClassificationLayer(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "classifications": [{"pattern": "aaa/**", "feature": "f"}]}`,
	})
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a layer")
}

// Omitted lists inherit the built-in policy, and a declared entry keeps
// exactly the fields it declares rather than merging with the default entry
// that happens to sit at the same index.
func TestLoadConfigInheritsOmittedListsWithoutMergingEntries(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "dependencies": [{"from_layers": ["domain"], "to_layers": ["domain"]}]}`,
	})
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	require.Len(t, config.Dependencies, 1)
	require.Empty(t, config.Dependencies[0].ToFeatures)
	require.Len(t, config.Classifications, len(DefaultConfig().Classifications))
}

func analyzeWith(t *testing.T, root string, config Config) Report {
	t.Helper()
	analyzer, err := NewAnalyzer(root, config)
	require.NoError(t, err)
	report, err := analyzer.Analyze()
	require.NoError(t, err)
	return report
}

// An annotation may share its line with the class_name or extends it
// decorates. gdparser rejected those scripts until cafecito-games/gdparser#76,
// and a file that does not parse contributes no class_name to the index, so
// every reference to it elsewhere was reported as unresolved. This guards the
// dependency: the name has to be indexed and the reference has to resolve.
func TestAnalyzerIndexesClassNamesDeclaredBesideAnAnnotation(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/catalog/domain/item.gd":     "@tool class_name Item extends RefCounted\n",
		"features/catalog/domain/crate.gd":    "@icon(\"res://icon.svg\") class_name Crate extends RefCounted\n",
		"features/catalog/application/use.gd": "extends RefCounted\n\n\nfunc run():\n\tprint(Item.new(), Crate.new())\n",
	})
	report := analyzeDefault(t, root)
	require.Len(t, diagnosticRules(report), 0)
	assertEdge(t, report, "features/catalog/application/use.gd", "features/catalog/domain/item.gd")
	assertEdge(t, report, "features/catalog/application/use.gd", "features/catalog/domain/crate.gd")
}

// A one-line class body holds one member; what a semicolon separates from it
// belongs to the enclosing scope. The dependency is the file's either way, so
// this guards that reading the member out of the class does not lose its edge.
func TestAnalyzerIndexesReferencesBesideAOneLineClassBody(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"features/catalog/domain/item.gd":     "class_name Item\n",
		"features/catalog/application/use.gd": "class Holder: var held = 1; var made = Item.new()\n",
	})
	report := analyzeDefault(t, root)
	require.Len(t, diagnosticRules(report), 0)
	assertEdge(t, report, "features/catalog/application/use.gd", "features/catalog/domain/item.gd")
}

// The companion addon is not part of the architecture. addons/** is already
// excluded by default, so the class is not classified and a call to it is no
// edge, which is what lets a domain value object call it without any layer
// having to be allowed to depend on it.
func TestTheCompanionAddonIsNotADependency(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		"addons/gdkit/equality_helpers.gd": "class_name GDKitEquality\nextends RefCounted\n\n\n" +
			"static func deep_equals(p_lhs: Variant, p_rhs: Variant) -> bool:\n\treturn p_lhs == p_rhs\n",
		"features/cart/domain/line.gd": "class_name Line\nextends RefCounted\n\nvar count: int\n\n\n" +
			"func deep_equals(p_other: Variant) -> bool:\n" +
			"\treturn GDKitEquality.deep_equals(self.count, p_other.count)\n",
	})
	report := analyzeDefault(t, root)
	assert.Len(t, report.Diagnostics, 0)
	for _, edge := range report.Edges {
		assert.NotContains(t, edge.To, "addons/")
	}
}
