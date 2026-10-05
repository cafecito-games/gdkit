package lint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
	"github.com/cafecito-games/gdkit/project"
)

type engineAwareTestRule struct {
	seen *semantic.Engine
}

func (r *engineAwareTestRule) Name() string          { return "max-line-length" }
func (r *engineAwareTestRule) RequiresEngineSchema() {}
func (r *engineAwareTestRule) Check(context *Context, _ *project.Script) []Diagnostic {
	r.seen = context.Engine()
	return nil
}

type ordinaryTestRule struct{}

func (ordinaryTestRule) Name() string { return "max-line-length" }
func (ordinaryTestRule) Check(*Context, *project.Script) []Diagnostic {
	return nil
}

type pendingEngineAwareTestRule struct{}

func (pendingEngineAwareTestRule) Name() string                                 { return "no-engine-logging" }
func (pendingEngineAwareTestRule) PendingSince() string                         { return "test" }
func (pendingEngineAwareTestRule) RequiresEngineSchema()                        {}
func (pendingEngineAwareTestRule) Check(*Context, *project.Script) []Diagnostic { return nil }

func TestExtensionAPIPathValidation(t *testing.T) {
	valid := "tools/godot/extension_api.json"
	config := DefaultConfig()
	config.ExtensionAPI = &valid
	if err := config.Validate(); err != nil {
		t.Fatalf("valid extension_api: %v", err)
	}

	for _, value := range []string{"", "/tmp/extension_api.json", "../extension_api.json", "tools/../../extension_api.json", `tools\extension_api.json`, "C:/extension_api.json", "C:extension_api.json", "bad\x00path"} {
		t.Run(strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			config := DefaultConfig()
			config.ExtensionAPI = &value
			if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "extension_api") {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsNullExtensionAPIInsteadOfTreatingItAsAbsent(t *testing.T) {
	for _, contents := range []string{`{"extension_api":null}`, `{"extension_api":null} {}`} {
		t.Run(contents, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
			if err := os.WriteFile(name, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadConfig(root, "")
			if err == nil || config.ExtensionAPI != nil {
				t.Fatalf("LoadConfig() = %+v, %v", config, err)
			}
			assertFailure(t, err, failure.ConfigParse, name)
		})
	}
}

func TestNonSemanticRunsDoNotLoadOrGateOnEngineSchema(t *testing.T) {
	for _, version := range []string{"4.0", "4.6", "4.8", "5.0"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			linter, err := newLinter(config, []Rule{ordinaryTestRule{}})
			if err != nil {
				t.Fatal(err)
			}
			report := linter.Lint(emptySnapshot())
			if report.EngineSchema != nil {
				t.Fatalf("engine schema loaded for ordinary %s run: %+v", version, report.EngineSchema)
			}
		})
	}
}

func TestDisabledSemanticRuleDoesNotRequestEngineSchema(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "4.6"
	config.Disable = []string{"max-line-length"}
	linter, err := newLinter(config, []Rule{&engineAwareTestRule{}})
	if err != nil {
		t.Fatal(err)
	}
	if report := linter.Lint(emptySnapshot()); report.EngineSchema != nil {
		t.Fatalf("disabled rule loaded schema: %+v", report.EngineSchema)
	}
}

func TestPendingSemanticRuleRequestsSchemaOnlyAfterOptIn(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "4.6"
	if linter, err := newLinter(config, []Rule{pendingEngineAwareTestRule{}}); err != nil {
		t.Fatal(err)
	} else if report := linter.Lint(emptySnapshot()); report.EngineSchema != nil {
		t.Fatalf("inert pending rule loaded schema: %+v", report.EngineSchema)
	}

	config.Enable = []string{"no-engine-logging"}
	if linter, err := newLinter(config, []Rule{pendingEngineAwareTestRule{}}); err == nil || linter != nil {
		t.Fatalf("enabled pending rule = %+v, %v", linter, err)
	}
}

func TestSemanticRuleSelectsExactEmbeddedMinorAndPublishesProvenance(t *testing.T) {
	for _, version := range []string{"4.7", "4.7.0", "4.7.99"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			rule := &engineAwareTestRule{}
			linter, err := newLinter(config, []Rule{rule})
			if err != nil {
				t.Fatal(err)
			}
			report := linter.Lint(snapshotWithOneScript())
			if rule.seen == nil || rule.seen.Class("Animation").Kind() != semantic.KindClass {
				t.Fatal("semantic rule did not receive the embedded engine")
			}
			if report.EngineSchema == nil || report.EngineSchema.Source != engineschema.SourceEmbedded ||
				report.EngineSchema.Version != (engineschema.Version{Major: 4, Minor: 7, Patch: 2}) {
				t.Fatalf("engine_schema = %+v", report.EngineSchema)
			}
		})
	}
}

func TestSemanticRuleRejectsEveryUnbundledMinorWithoutFallback(t *testing.T) {
	for _, version := range []string{"4.6", "4.8", "5.0"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			linter, err := newLinter(config, []Rule{&engineAwareTestRule{}})
			if err == nil || linter != nil {
				t.Fatalf("newLinter() = %+v, %v", linter, err)
			}
			labelled, ok := failure.Of(err)
			if !ok || labelled.Kind != failure.ConfigInvalid || !strings.Contains(err.Error(), version) ||
				!strings.Contains(err.Error(), "extension_api") {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

// The copied bytes come from the real Godot producer fixture documented in
// internal/semantic/engineschema/schema_test.go. This exercises the lint
// startup loader with exactly the same bytes accepted by the raw schema path.
func TestExplicitExtensionAPIIsAWholesaleNumericMatchingOverride(t *testing.T) {
	raw := readEngineFixture(t)
	root := t.TempDir()
	writeEngineOverride(t, root, "tools/godot/extension_api.json", raw)
	path := "tools/godot/extension_api.json"

	for _, version := range []string{"4.7", "4.7.2"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			config.ExtensionAPI = &path
			rule := &engineAwareTestRule{}
			linter, err := newLinterForProject(root, config, []Rule{rule})
			if err != nil {
				t.Fatal(err)
			}
			report := linter.Lint(snapshotWithOneScript())
			if report.EngineSchema == nil || report.EngineSchema.Source != engineschema.SourceOverride {
				t.Fatalf("engine_schema = %+v", report.EngineSchema)
			}
			if got := rule.seen.Class("Animation"); got.Kind() != semantic.KindUnknown {
				t.Fatalf("override merged embedded Animation: kind %v name %q", got.Kind(), got.Name())
			}
		})
	}

	// A configured override is validated even when no enabled rule currently
	// consumes it, and its provenance is still part of the resulting report.
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, []Rule{ordinaryTestRule{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := linter.Lint(emptySnapshot()).EngineSchema; got == nil || got.Source != engineschema.SourceOverride {
		t.Fatalf("explicit override provenance = %+v", got)
	}
}

func TestExplicitExtensionAPIAcceptsForkBrandingButNotNumericMismatch(t *testing.T) {
	raw := mutateEngineHeader(t, func(header map[string]any) {
		header["version_status"] = "stable"
		header["version_build"] = "cafecito_custom"
		header["version_full_name"] = "Godot Engine v4.7.2.stable.cafecito_custom"
	})
	root := t.TempDir()
	path := "extension_api.json"
	writeEngineOverride(t, root, path, raw)

	for _, version := range []string{"4.7", "4.7.2"} {
		config := DefaultConfig()
		config.GodotVersion = version
		config.ExtensionAPI = &path
		linter, err := newLinterForProject(root, config, nil)
		if err != nil {
			t.Fatalf("matching %s: %v", version, err)
		}
		if got := linter.Lint(emptySnapshot()).EngineSchema; got == nil || got.Build != "cafecito_custom" {
			t.Fatalf("matching %s provenance = %+v", version, got)
		}
	}

	for _, version := range []string{"4.6", "4.7.1", "4.8", "5.0"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			config.ExtensionAPI = &path
			linter, err := newLinterForProject(root, config, nil)
			if err == nil || linter != nil {
				t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
			}
			assertFailure(t, err, failure.ConfigInvalid, path)
		})
	}
}

func TestExtensionAPIFailuresKeepStableKindsAndPaths(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		path string
		data []byte
		kind string
	}{
		{name: "unreadable", path: "missing.json", kind: failure.ConfigRead},
		{name: "malformed", path: "malformed.json", data: []byte(`{"header":`), kind: failure.ConfigParse},
		{name: "invalid", path: "invalid.json", data: []byte(`{}`), kind: failure.ConfigInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.data != nil {
				writeEngineOverride(t, root, test.path, test.data)
			}
			config := DefaultConfig()
			config.ExtensionAPI = &test.path
			linter, err := newLinterForProject(root, config, nil)
			if err == nil || linter != nil {
				t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
			}
			assertFailure(t, err, test.kind, test.path)
		})
	}
}

func TestExtensionAPIRejectsResolvedSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "extension_api.json")
	if err := os.WriteFile(outside, readEngineFixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	path := "extension_api.json"
	if err := os.Symlink(outside, filepath.Join(root, path)); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	if err == nil || linter != nil {
		t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
	}
	assertFailure(t, err, failure.ConfigInvalid, path)
}

func TestEngineSchemaReportJSONIsOptional(t *testing.T) {
	without, err := json.Marshal(Report{Diagnostics: []Diagnostic{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "engine_schema") {
		t.Fatalf("nil engine schema was not omitted: %s", without)
	}
	with, err := json.Marshal(Report{Diagnostics: []Diagnostic{}, EngineSchema: &engineschema.Provenance{Source: engineschema.SourceEmbedded}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(with), `"engine_schema":{"source":"embedded"`) {
		t.Fatalf("engine schema missing from JSON: %s", with)
	}
}

func assertFailure(t *testing.T, err error, kind, path string) {
	t.Helper()
	labelled, ok := failure.Of(err)
	if !ok || labelled.Kind != kind || labelled.Path != path {
		t.Fatalf("failure = %#v, want kind %q path %q", err, kind, path)
	}
}

func readEngineFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../internal/semantic/engineschema/testdata/extension_api_4_7_2_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mutateEngineHeader(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(readEngineFixture(t), &document); err != nil {
		t.Fatal(err)
	}
	mutate(document["header"].(map[string]any))
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeEngineOverride(t *testing.T, root, name string, data []byte) {
	t.Helper()
	fullName := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(fullName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullName, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func emptySnapshot() *project.Snapshot {
	return &project.Snapshot{Paths: []string{}, Scripts: map[string]*project.Script{}}
}

func snapshotWithOneScript() *project.Snapshot {
	return &project.Snapshot{
		Paths: []string{"empty.gd"},
		Scripts: map[string]*project.Script{
			"empty.gd": {Path: "empty.gd"},
		},
	}
}

var _ EngineSchemaRule = (*engineAwareTestRule)(nil)
