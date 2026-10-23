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
	"github.com/stretchr/testify/require"
)

type engineAwareTestRule struct {
	seen *semantic.Engine
}

func (r *engineAwareTestRule) Name() string          { return "max-line-length" }
func (r *engineAwareTestRule) Describe() string      { return "test" }
func (r *engineAwareTestRule) RequiresEngineSchema() {}
func (r *engineAwareTestRule) Check(context *Context, _ *project.Script) []Diagnostic {
	r.seen = context.Engine()
	return nil
}

type ordinaryTestRule struct{}

func (ordinaryTestRule) Name() string     { return "max-line-length" }
func (ordinaryTestRule) Describe() string { return "test" }
func (ordinaryTestRule) Check(*Context, *project.Script) []Diagnostic {
	return nil
}

type pendingEngineAwareTestRule struct{}

func (pendingEngineAwareTestRule) Name() string                                 { return "no-engine-logging" }
func (pendingEngineAwareTestRule) Describe() string                             { return "test" }
func (pendingEngineAwareTestRule) PendingSince() string                         { return "test" }
func (pendingEngineAwareTestRule) RequiresEngineSchema()                        {}
func (pendingEngineAwareTestRule) Check(*Context, *project.Script) []Diagnostic { return nil }

func TestExtensionAPIPathValidation(t *testing.T) {
	valid := "tools/godot/extension_api.json"
	config := DefaultConfig()
	config.ExtensionAPI = &valid
	require.NoError(t, config.Validate())

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
			err := config.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), "extension_api")
		})
	}
}

func TestExtensionAPIRejectsSymlinkDotDotAmbiguityAsInvalidConfig(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideTarget := filepath.Join(outside, "target")
	require.NoError(t, os.Mkdir(outsideTarget, 0o700))
	require.NoError(t, os.Symlink(outsideTarget, filepath.Join(root, "link")))
	// Without canonical path validation, filepath.Join/EvalSymlinks inspect this
	// in-root file while os.Root resolves link before .. and refuses the escape.
	writeEngineOverride(t, root, "extension_api.json", readEngineFixture(t))
	writeEngineOverride(t, outside, "extension_api.json", readEngineFixture(t))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gdkit"), 0o700))
	configPath := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	require.NoError(t, os.WriteFile(configPath, []byte(`{"extension_api":"link/../extension_api.json"}`), 0o600))

	config, err := LoadConfig(root, "")
	if err == nil {
		_, err = newLinterForProject(root, config, nil)
	}
	labelled, ok := failure.Of(err)
	require.True(t, ok)
	require.Equal(t, failure.ConfigInvalid, labelled.Kind)
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
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755))
			name := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
			require.NoError(t, os.WriteFile(name, []byte(test.contents), 0o600))
			config, err := LoadConfig(root, "")
			require.Error(t, err)
			require.Nil(t, config.ExtensionAPI)
			assertFailure(t, err, failure.ConfigParse, name)
		})
	}
}

func TestLoadConfigRejectsCaseVariantExtensionAPIKey(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755))
	name := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	require.NoError(t, os.WriteFile(name, []byte(`{"Extension_API":"tools/extension_api.json"}`), 0o600))
	config, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Nil(t, config.ExtensionAPI)
	assertFailure(t, err, failure.ConfigParse, name)
}

func TestNonSemanticRunsDoNotLoadOrGateOnEngineSchema(t *testing.T) {
	for _, version := range []string{"4.0", "4.6", "4.8", "5.0"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			linter, err := newLinter(config, []Rule{ordinaryTestRule{}})
			require.NoError(t, err)
			report := linter.Lint(emptySnapshot())
			require.Nil(t, report.EngineSchema)
		})
	}
}

func TestDisabledSemanticRuleDoesNotRequestEngineSchema(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "4.6"
	config.Disable = []string{"max-line-length"}
	linter, err := newLinter(config, []Rule{&engineAwareTestRule{}})
	require.NoError(t, err)
	report := linter.Lint(emptySnapshot())
	require.Nil(t, report.EngineSchema)
}

func TestPendingSemanticRuleRequestsSchemaOnlyAfterOptIn(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "4.6"
	linter, err := newLinter(config, []Rule{pendingEngineAwareTestRule{}})
	require.NoError(t, err)
	report := linter.Lint(emptySnapshot())
	require.Nil(t, report.EngineSchema)

	config.Enable = []string{"no-engine-logging"}
	linter, err = newLinter(config, []Rule{pendingEngineAwareTestRule{}})
	require.Error(t, err)
	require.Nil(t, linter)
}

func TestSemanticRuleSelectsExactEmbeddedMinorAndPublishesProvenance(t *testing.T) {
	for _, version := range []string{"4.7", "4.7.0", "4.7.99"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			rule := &engineAwareTestRule{}
			linter, err := newLinter(config, []Rule{rule})
			require.NoError(t, err)
			report := linter.Lint(snapshotWithOneScript())
			require.NotNil(t, rule.seen)
			require.Equal(t, semantic.KindClass, rule.seen.Class("Animation").Kind())
			require.NotNil(t, report.EngineSchema)
			require.Equal(t, engineschema.SourceEmbedded, report.EngineSchema.Source)
			require.Equal(t, engineschema.Version{Major: 4, Minor: 7, Patch: 2}, report.EngineSchema.Version)
		})
	}
}

func TestSemanticRuleRejectsEveryUnbundledMinorWithoutFallback(t *testing.T) {
	for _, version := range []string{"4.6", "4.8", "5.0"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			linter, err := newLinter(config, []Rule{&engineAwareTestRule{}})
			require.Error(t, err)
			require.Nil(t, linter)
			labelled, ok := failure.Of(err)
			require.True(t, ok)
			require.Equal(t, failure.ConfigInvalid, labelled.Kind)
			require.Contains(t, err.Error(), version)
			require.Contains(t, err.Error(), "extension_api")
		})
	}
}

func TestEnabledCollectionRuleRejectsAnUnsupportedMinorBeforeLinting(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "4.6"
	config.Enable = []string{ruleRequireTypedCollection}
	linter, err := newLinter(config, []Rule{typingRule{rule: ruleRequireTypedCollection}})
	require.Error(t, err)
	require.Nil(t, linter)
	assertFailure(t, err, failure.ConfigInvalid, "")
}

func TestEnabledCollectionRuleFailsClosedWithoutASelectedEngine(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{ruleRequireTypedCollection}
	linter, err := buildLinterWithSchemaLoader("", false, config,
		[]Rule{typingRule{rule: ruleRequireTypedCollection}},
		func(int, int) (*engineschema.Loaded, error) { return &engineschema.Loaded{}, nil })
	require.Error(t, err)
	require.Nil(t, linter)
	assertFailure(t, err, failure.AnalysisFailed, "")
}

func TestEnabledCollectionRuleUsesTheWholesaleOverrideAndPublishesProvenance(t *testing.T) {
	root := t.TempDir()
	path := "tools/godot/extension_api.json"
	writeEngineOverride(t, root, path, readOfficialEngineFixture(t))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.gd"), []byte("var items := [1]\n"), 0o644))
	config := DefaultConfig()
	config.Enable = []string{ruleRequireTypedCollection}
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, []Rule{typingRule{rule: ruleRequireTypedCollection}})
	require.NoError(t, err)
	snapshot, err := project.Load(lintLoadConfig(root, config, linter))
	require.NoError(t, err)
	report := linter.Lint(snapshot)
	require.NotNil(t, report.EngineSchema)
	require.Equal(t, engineschema.SourceOverride, report.EngineSchema.Source)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "Array has no element type; write Array[T]", report.Diagnostics[0].Message)
}

func TestEmbeddedSchemaCorruptionIsAnAnalysisFailure(t *testing.T) {
	config := DefaultConfig()
	compiled, err := config.validate()
	require.NoError(t, err)
	loaded, err := prepareEngineSchemaWithLoader("", false, config, compiled, []Rule{&engineAwareTestRule{}},
		func(int, int) (*engineschema.Loaded, error) {
			return nil, errors.New("embedded artifact digest mismatch")
		})
	require.Error(t, err)
	require.Nil(t, loaded)
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
			require.NoError(t, err)
			report := linter.Lint(snapshotWithOneScript())
			require.NotNil(t, report.EngineSchema)
			require.Equal(t, engineschema.SourceOverride, report.EngineSchema.Source)
			got := rule.seen.Class("Animation")
			require.Equal(t, semantic.KindUnknown, got.Kind())
		})
	}

	// A configured override is validated even when no enabled rule currently
	// consumes it, and its provenance is still part of the resulting report.
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, []Rule{ordinaryTestRule{}})
	require.NoError(t, err)
	got := linter.Lint(emptySnapshot()).EngineSchema
	require.NotNil(t, got)
	require.Equal(t, engineschema.SourceOverride, got.Source)
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
	require.NoError(t, err)
	provenance := linter.Lint(emptySnapshot()).EngineSchema
	require.NotNil(t, provenance)
	require.Equal(t, "d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986", provenance.RawSHA256)
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
		require.NoError(t, err, "matching %s", version)
		got := linter.Lint(emptySnapshot()).EngineSchema
		require.NotNil(t, got)
		require.Equal(t, "cafecito_custom", got.Build)
	}

	for _, version := range []string{"4.6", "4.7.1", "4.8", "5.0"} {
		t.Run(version, func(t *testing.T) {
			config := DefaultConfig()
			config.GodotVersion = version
			config.ExtensionAPI = &path
			linter, err := newLinterForProject(root, config, nil)
			require.Error(t, err)
			require.Nil(t, linter)
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
			require.Error(t, err)
			require.Nil(t, linter)
			assertFailure(t, err, test.kind, filepath.Join(root, filepath.FromSlash(test.path)))
		})
	}
}

func TestExtensionAPIRejectsResolvedSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "extension_api.json")
	require.NoError(t, os.WriteFile(outside, readEngineFixture(t), 0o600))
	path := "extension_api.json"
	require.NoError(t, os.Symlink(outside, filepath.Join(root, path)))
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	require.Error(t, err)
	require.Nil(t, linter)
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}

func TestExtensionAPIAcceptsAbsoluteSymlinkToFileWithinRoot(t *testing.T) {
	root := t.TempDir()
	target := "tools/extension_api.json"
	writeEngineOverride(t, root, target, readEngineFixture(t))
	path := "extension_api.json"
	require.NoError(t, os.Symlink(filepath.Join(root, filepath.FromSlash(target)), filepath.Join(root, path)))
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	require.NoError(t, err)
	require.NotNil(t, linter)
	provenance := linter.Lint(emptySnapshot()).EngineSchema
	require.NotNil(t, provenance)
	require.Equal(t, engineschema.SourceOverride, provenance.Source)
}

func TestExtensionAPIRejectsNonRegularInput(t *testing.T) {
	root := t.TempDir()
	path := "extension_api.json"
	require.NoError(t, os.Mkdir(filepath.Join(root, path), 0o700))
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	require.Error(t, err)
	require.Nil(t, linter)
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}

func TestExtensionAPIRejectsOversizedInputAsInvalid(t *testing.T) {
	root := t.TempDir()
	path := "extension_api.json"
	file, err := os.Create(filepath.Join(root, path))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(engineschema.MaxRawJSONBytes+1))
	require.NoError(t, file.Close())
	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	require.Error(t, err)
	require.Nil(t, linter)
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}

func TestEngineSchemaReportJSONIsOptional(t *testing.T) {
	without, err := json.Marshal(Report{Diagnostics: []Diagnostic{}})
	require.NoError(t, err)
	require.NotContains(t, string(without), "engine_schema")
	with, err := json.Marshal(Report{Diagnostics: []Diagnostic{}, EngineSchema: &engineschema.Provenance{Source: engineschema.SourceEmbedded}})
	require.NoError(t, err)
	require.Contains(t, string(with), `"engine_schema":{"source":"embedded"`)
}

func assertFailure(t *testing.T, err error, kind, path string) {
	t.Helper()
	labelled, ok := failure.Of(err)
	require.True(t, ok)
	require.Equal(t, kind, labelled.Kind)
	require.Equal(t, path, labelled.Path)
}

func readEngineFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../internal/semantic/engineschema/testdata/extension_api_4_7_2_sample.json")
	require.NoError(t, err)
	return data
}

func readOfficialEngineFixture(t *testing.T) []byte {
	t.Helper()
	compressed, err := os.ReadFile("../internal/semantic/engineschema/testdata/extension_api_4_7_2_official.json.gz")
	require.NoError(t, err)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return raw
}

func mutateEngineHeader(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	require.NoError(t, json.Unmarshal(readEngineFixture(t), &document))
	mutate(document["header"].(map[string]any))
	data, err := json.Marshal(document)
	require.NoError(t, err)
	return data
}

func writeEngineOverride(t *testing.T, root, name string, data []byte) {
	t.Helper()
	fullName := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullName), 0o755))
	require.NoError(t, os.WriteFile(fullName, data, 0o600))
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
