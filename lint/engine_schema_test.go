package lint

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
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

	for _, value := range []string{
		"",
		"/tmp/extension_api.json",
		"../extension_api.json",
		"tools/../../extension_api.json",
		"./extension_api.json",
		"tools/../extension_api.json",
		"tools//extension_api.json",
		"tools/godot/",
		`tools\extension_api.json`,
		"C:/extension_api.json",
		"C:extension_api.json",
		"bad\x00path",
	} {
		t.Run(strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			config := DefaultConfig()
			config.ExtensionAPI = &value
			if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "extension_api") {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestExtensionAPIRejectsSymlinkDotDotAmbiguityAsInvalidConfig(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideTarget := filepath.Join(outside, "target")
	if err := os.Mkdir(outsideTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideTarget, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// Without canonical path validation, filepath.Join/EvalSymlinks inspect this
	// in-root file while os.Root resolves link before .. and refuses the escape.
	writeEngineOverride(t, root, "extension_api.json", readEngineFixture(t))
	writeEngineOverride(t, outside, "extension_api.json", readEngineFixture(t))
	if err := os.MkdirAll(filepath.Join(root, ".gdkit"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	if err := os.WriteFile(configPath, []byte(`{"extension_api":"link/../extension_api.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfig(root, "")
	if err == nil {
		_, err = newLinterForProject(root, config, nil)
	}
	labelled, ok := failure.Of(err)
	if !ok || labelled.Kind != failure.ConfigInvalid {
		t.Fatalf("ambiguous extension_api failure = %#v, want %q", err, failure.ConfigInvalid)
	}
}

func TestLoadConfigRejectsNullExtensionAPIInsteadOfTreatingItAsAbsent(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "null", contents: `{"extension_api":null}`},
		{name: "trailing document", contents: `{"extension_api":null} {}`},
		{name: "case variant null", contents: `{"EXTENSION_API":null}`},
		{name: "case variant after value", contents: `{"extension_api":"tools/extension_api.json","Extension_API":null}`},
		{name: "case variant before value", contents: `{"Extension_API":null,"extension_api":"tools/extension_api.json"}`},
		{name: "duplicate null after value", contents: `{"extension_api":"tools/extension_api.json","extension_api":null}`},
		{name: "duplicate value after null", contents: `{"extension_api":null,"extension_api":"tools/extension_api.json"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
			if err := os.WriteFile(name, []byte(test.contents), 0o600); err != nil {
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

func TestLoadConfigRejectsCaseVariantExtensionAPIKey(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	if err := os.WriteFile(name, []byte(`{"Extension_API":"tools/extension_api.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(root, "")
	if err == nil || config.ExtensionAPI != nil {
		t.Fatalf("LoadConfig() = %+v, %v", config, err)
	}
	assertFailure(t, err, failure.ConfigParse, name)
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

func TestEnabledCollectionRuleRejectsAnUnsupportedMinorBeforeLinting(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "4.6"
	config.Enable = []string{ruleRequireTypedCollection}
	linter, err := newLinter(config, []Rule{typingRule{rule: ruleRequireTypedCollection}})
	if err == nil || linter != nil {
		t.Fatalf("newLinter() = %+v, %v", linter, err)
	}
	assertFailure(t, err, failure.ConfigInvalid, "")
}

func TestEnabledCollectionRuleFailsClosedWithoutASelectedEngine(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{ruleRequireTypedCollection}
	linter, err := buildLinterWithSchemaLoader("", false, config,
		[]Rule{typingRule{rule: ruleRequireTypedCollection}},
		func(int, int) (*engineschema.Loaded, error) { return &engineschema.Loaded{}, nil })
	if err == nil || linter != nil {
		t.Fatalf("buildLinterWithSchemaLoader() = %+v, %v", linter, err)
	}
	assertFailure(t, err, failure.AnalysisFailed, "")
}

func TestEnabledCollectionRuleUsesTheWholesaleOverrideAndPublishesProvenance(t *testing.T) {
	root := t.TempDir()
	path := "tools/godot/extension_api.json"
	writeEngineOverride(t, root, path, readOfficialEngineFixture(t))
	if err := os.WriteFile(filepath.Join(root, "a.gd"), []byte("var items := [1]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Enable = []string{ruleRequireTypedCollection}
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, []Rule{typingRule{rule: ruleRequireTypedCollection}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := project.Load(lintLoadConfig(root, config, linter))
	if err != nil {
		t.Fatal(err)
	}
	report := linter.Lint(snapshot)
	if report.EngineSchema == nil || report.EngineSchema.Source != engineschema.SourceOverride {
		t.Fatalf("engine_schema = %+v", report.EngineSchema)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Message != "Array has no element type; write Array[T]" {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func TestEmbeddedSchemaCorruptionIsAnAnalysisFailure(t *testing.T) {
	config := DefaultConfig()
	compiled, err := config.validate()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := prepareEngineSchemaWithLoader("", false, config, compiled, []Rule{&engineAwareTestRule{}},
		func(int, int) (*engineschema.Loaded, error) {
			return nil, errors.New("embedded artifact digest mismatch")
		})
	if err == nil || loaded != nil {
		t.Fatalf("prepareEngineSchemaWithLoader() = %+v, %v", loaded, err)
	}
	assertFailure(t, err, failure.AnalysisFailed, "")
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

// The decompressed fixture is the complete byte-for-byte output of the
// official Godot 4.7.2 producer cited in engineschema/schema_test.go. This
// exercises the project-root adapter, not only the underlying raw parser.
func TestNewForProjectAcceptsOfficialProducerDump(t *testing.T) {
	raw := readOfficialEngineFixture(t)
	root := t.TempDir()
	path := "tools/godot/extension_api.json"
	writeEngineOverride(t, root, path, raw)
	config := DefaultConfig()
	config.GodotVersion = "4.7.2"
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	provenance := linter.Lint(emptySnapshot()).EngineSchema
	if provenance == nil || provenance.RawSHA256 != "d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986" {
		t.Fatalf("engine_schema = %+v", provenance)
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
			assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, filepath.FromSlash(path)))
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
			assertFailure(t, err, test.kind, filepath.Join(root, filepath.FromSlash(test.path)))
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
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}

func TestExtensionAPIAcceptsAbsoluteSymlinkToFileWithinRoot(t *testing.T) {
	root := t.TempDir()
	target := "tools/extension_api.json"
	writeEngineOverride(t, root, target, readEngineFixture(t))
	path := "extension_api.json"
	if err := os.Symlink(filepath.Join(root, filepath.FromSlash(target)), filepath.Join(root, path)); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	if err != nil || linter == nil {
		t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
	}
	if provenance := linter.Lint(emptySnapshot()).EngineSchema; provenance == nil || provenance.Source != engineschema.SourceOverride {
		t.Fatalf("engine_schema = %+v", provenance)
	}
}

func TestExtensionAPIRejectsNonRegularInput(t *testing.T) {
	root := t.TempDir()
	path := "extension_api.json"
	if err := os.Mkdir(filepath.Join(root, path), 0o700); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	if err == nil || linter != nil {
		t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
	}
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}

func TestExtensionAPIRejectsOversizedInputAsInvalid(t *testing.T) {
	root := t.TempDir()
	path := "extension_api.json"
	file, err := os.Create(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(engineschema.MaxRawJSONBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	if err == nil || linter != nil {
		t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
	}
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
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

func readOfficialEngineFixture(t *testing.T) []byte {
	t.Helper()
	compressed, err := os.ReadFile("../internal/semantic/engineschema/testdata/extension_api_4_7_2_official.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return raw
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

// A hand-built snapshot must satisfy the loader invariant: Selected names the
// scripts the caller acts on, and project.Load makes it Paths itself when it
// was given no Selection. Lint acts on Selected, so a fixture that populated
// only Paths would silently lint nothing.
func emptySnapshot() *project.Snapshot {
	return &project.Snapshot{
		Paths:    []string{},
		Scripts:  map[string]*project.Script{},
		Selected: []string{},
	}
}

func snapshotWithOneScript() *project.Snapshot {
	return &project.Snapshot{
		Paths: []string{"empty.gd"},
		Scripts: map[string]*project.Script{
			"empty.gd": {Path: "empty.gd"},
		},
		Selected: []string{"empty.gd"},
	}
}

var _ EngineSchemaRule = (*engineAwareTestRule)(nil)
