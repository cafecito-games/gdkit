package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
	"github.com/cafecito-games/gdkit/lint"
	"github.com/stretchr/testify/require"
)

// The decompressed bytes are the complete official producer output documented
// at internal/semantic/engineschema/schema_test.go. Running them through the
// CLI proves the real config, startup, and JSON report path accepts the exact
// same Godot bytes as the schema loader.
func TestRunLintJSONReportsExplicitEngineSchemaProvenance(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	writeCLIBytes(t, root, "tools/godot/extension_api.json", readCLIEngineFixture(t))
	writeCLIFile(t, root, ".gdkit/lint.json", `{"godot_version":"4.7","extension_api":"tools/godot/extension_api.json"}`)

	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Empty(t, stderr.String())
	var report lint.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	require.NotNil(t, report.EngineSchema)
	require.Equal(t, engineschema.SourceOverride, report.EngineSchema.Source)
	require.Equal(t, engineschema.Version{Major: 4, Minor: 7, Patch: 2}, report.EngineSchema.Version)
	require.NotEmpty(t, report.EngineSchema.RawSHA256)
	require.NotEmpty(t, report.EngineSchema.FullName)
	require.NotContains(t, stdout.String(), "builtin_classes")
	require.NotContains(t, stdout.String(), "Sprite2D")
}

func TestRunLintJSONReportsEnabledCollectionInferenceAndEmbeddedProvenance(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "var lookup := {\"a\": 1}\n\nfunc run() -> void:\n\tvar local_lookup := {\"a\": 1}\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Empty(t, stderr.String())
	var report lint.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	require.NotNil(t, report.EngineSchema)
	require.Equal(t, engineschema.SourceEmbedded, report.EngineSchema.Source)
	require.Equal(t, engineschema.Version{Major: 4, Minor: 7, Patch: 2}, report.EngineSchema.Version)
	require.Len(t, report.Diagnostics, 2)
	require.Equal(t, "Dictionary has no element type; write Dictionary[K, V]", report.Diagnostics[0].Message)
	require.Equal(t, "Dictionary has no element type; write Dictionary[String, int]", report.Diagnostics[1].Message)
}

func TestRunLintEnabledCollectionRejectsUnsupportedMinorAsJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "var items := [1]\n")
	writeCLIFile(t, root, ".gdkit/lint.json", `{"godot_version":"4.6"}`)
	body := runFailure(t, "lint", "check", "--format", "json", "--enable", "require-typed-collection", root)
	require.Equal(t, "config.invalid", body.Kind)
	require.Contains(t, body.Message, "4.6")
	require.Contains(t, body.Message, "extension_api")
}

func TestRunLintExtensionAPIFailuresUseJSONKindsPathsAndEmptyStdout(t *testing.T) {
	fixture := readCLIEngineFixture(t)
	var mismatched map[string]any
	require.NoError(t, json.Unmarshal(fixture, &mismatched))
	mismatched["header"].(map[string]any)["version_minor"] = float64(8)
	mismatchData, err := json.Marshal(mismatched)
	require.NoError(t, err)

	tests := []struct {
		name string
		path string
		data []byte
		kind string
	}{
		{name: "unreadable", path: "missing.json", kind: "config.read"},
		{name: "malformed", path: "malformed.json", data: []byte(`{"header":`), kind: "config.parse"},
		{name: "invalid", path: "invalid.json", data: []byte(`{}`), kind: "config.invalid"},
		{name: "version mismatch", path: "mismatch.json", data: mismatchData, kind: "config.invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeCLIFile(t, root, "player.gd", "extends Node\n")
			if test.data != nil {
				writeCLIBytes(t, root, test.path, test.data)
			}
			config, err := json.Marshal(map[string]any{
				"godot_version": "4.7",
				"extension_api": test.path,
			})
			require.NoError(t, err)
			writeCLIBytes(t, root, ".gdkit/lint.json", config)
			body := runFailure(t, "lint", "check", "--format", "json", root)
			wantPath := filepath.Join(root, filepath.FromSlash(test.path))
			require.Equal(t, test.kind, body.Kind)
			require.Equal(t, wantPath, body.Path)
			require.Contains(t, body.Message, test.path)
		})
	}
}

func TestRunLintInvalidExtensionAPIPathsFailClosedAsJSON(t *testing.T) {
	for _, configuredPath := range []string{"", "/tmp/extension_api.json", "../extension_api.json", "tools/../../extension_api.json"} {
		t.Run(strings.ReplaceAll(configuredPath, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			writeCLIFile(t, root, "player.gd", "extends Node\n")
			config, err := json.Marshal(map[string]any{
				"godot_version": "4.7",
				"extension_api": configuredPath,
			})
			require.NoError(t, err)
			writeCLIBytes(t, root, ".gdkit/lint.json", config)
			body := runFailure(t, "lint", "check", "--format", "json", root)
			require.Equal(t, "config.invalid", body.Kind)
			require.True(t, strings.HasSuffix(filepath.ToSlash(body.Path), "/.gdkit/lint.json"))
		})
	}
}

func TestRunLintResolvedExtensionAPIEscapeFailsClosedAsJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	outside := filepath.Join(t.TempDir(), "extension_api.json")
	require.NoError(t, os.WriteFile(outside, readCLIEngineFixture(t), 0o600))
	configuredPath := "engine.json"
	require.NoError(t, os.Symlink(outside, filepath.Join(root, configuredPath)))
	writeCLIFile(t, root, ".gdkit/lint.json", `{"godot_version":"4.7","extension_api":"engine.json"}`)
	body := runFailure(t, "lint", "check", "--format", "json", root)
	require.Equal(t, "config.invalid", body.Kind)
	require.Equal(t, filepath.Join(root, configuredPath), body.Path)
}

func TestRunLintMalformedExtensionAPIPresenceFailsAsConfigParseJSON(t *testing.T) {
	for _, contents := range []string{
		`{"EXTENSION_API":null}`,
		`{"extension_api":"engine.json","extension_api":null}`,
	} {
		root := t.TempDir()
		writeCLIFile(t, root, "player.gd", "extends Node\n")
		writeCLIFile(t, root, ".gdkit/lint.json", contents)
		body := runFailure(t, "lint", "check", "--format", "json", root)
		require.Equal(t, "config.parse", body.Kind)
		require.True(t, strings.HasSuffix(filepath.ToSlash(body.Path), "/.gdkit/lint.json"))
	}
}

func TestRunLintNonRegularExtensionAPIFailsAsConfigInvalidJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	require.NoError(t, os.Mkdir(filepath.Join(root, "engine.json"), 0o700))
	writeCLIFile(t, root, ".gdkit/lint.json", `{"extension_api":"engine.json"}`)
	body := runFailure(t, "lint", "check", "--format", "json", root)
	require.Equal(t, "config.invalid", body.Kind)
	require.Equal(t, filepath.Join(root, "engine.json"), body.Path)
}

func readCLIEngineFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "semantic", "engineschema", "testdata", "extension_api_4_7_2_official.json.gz"))
	require.NoError(t, err)
	reader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return raw
}

func writeCLIBytes(t *testing.T, root, name string, contents []byte) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(absolute), 0o755))
	require.NoError(t, os.WriteFile(absolute, contents, 0o644))
}
