package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
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
	if report.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", report.Diagnostics)
	}
	assertEdge(t, report, "features/combat/application/ports/reader.gd", "features/combat/domain/health.gd")
	assertEdge(t, report, "features/combat/infrastructure/store.gd", "features/combat/application/ports/reader.gd")
	assertEdge(t, report, "features/combat/presentation/view.gd", "features/combat/application/intents/heal.gd")
	for _, edge := range report.Edges {
		if edge.From == "features/combat/presentation/view.gd" && edge.To == "features/combat/infrastructure/store.gd" {
			t.Fatal("class name in a comment or string became an edge")
		}
	}
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
		if !slices.Contains(rules, expected) {
			t.Errorf("missing %s in diagnostics: %#v", expected, report.Diagnostics)
		}
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
		if !slices.Contains(rules, expected) {
			t.Errorf("missing %s in diagnostics: %#v", expected, report.Diagnostics)
		}
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
	if report.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", report.Diagnostics)
	}
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
	if !slices.Contains(diagnosticRules(report), "allowlist.adr") || !slices.Contains(diagnosticRules(report), "dependency.direction") {
		t.Fatalf("missing ADR should fail and leave violation active: %#v", report.Diagnostics)
	}

	writeProject(t, root, map[string]string{"docs/adr/0001.md": "# Temporary dependency\n"})
	report = analyzeDefault(t, root)
	if report.HasErrors() {
		t.Fatalf("valid ADR-backed exception was not applied: %#v", report.Diagnostics)
	}
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
	if !slices.Contains(rules, "class_name.duplicate") || !slices.Contains(rules, "classification.missing") {
		t.Fatalf("unexpected diagnostics: %#v", report.Diagnostics)
	}
}

func TestClassificationCapturesFeature(t *testing.T) {
	config := DefaultConfig()
	got, ok := config.classify("src/features/inventory/domain/item.gd")
	if !ok || got.Layer != "domain" || got.Feature != "inventory" {
		t.Fatalf("classification = %#v, %v", got, ok)
	}
}

func analyzeDefault(t *testing.T, root string) Report {
	t.Helper()
	analyzer, err := NewAnalyzer(root, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	report, err := analyzer.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func writeProject(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
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
	for _, edge := range report.Edges {
		if edge.From == from && edge.To == to {
			return
		}
	}
	t.Errorf("missing edge %s -> %s in %#v", from, to, report.Edges)
}
