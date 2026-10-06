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
	if code := run([]string{"lint", "check", "--format", "json", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %s", stderr.String())
	}
	var report lint.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, stdout.String())
	}
	if report.EngineSchema == nil || report.EngineSchema.Source != engineschema.SourceOverride ||
		report.EngineSchema.Version != (engineschema.Version{Major: 4, Minor: 7, Patch: 2}) ||
		report.EngineSchema.RawSHA256 == "" || report.EngineSchema.FullName == "" {
		t.Fatalf("engine_schema = %+v", report.EngineSchema)
	}
	if strings.Contains(stdout.String(), "builtin_classes") || strings.Contains(stdout.String(), "Sprite2D") {
		t.Fatalf("report leaked raw engine API: %s", stdout.String())
	}
}

func TestRunLintJSONReportsEnabledCollectionInferenceAndEmbeddedProvenance(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "var lookup := {\"a\": 1}\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %s", stderr.String())
	}
	var report lint.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, stdout.String())
	}
	if report.EngineSchema == nil || report.EngineSchema.Source != engineschema.SourceEmbedded ||
		report.EngineSchema.Version != (engineschema.Version{Major: 4, Minor: 7, Patch: 2}) {
		t.Fatalf("engine_schema = %+v", report.EngineSchema)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Message != "Dictionary has no element type; write Dictionary[String, int]" {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func TestRunLintEnabledCollectionRejectsUnsupportedMinorAsJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "var items := [1]\n")
	writeCLIFile(t, root, ".gdkit/lint.json", `{"godot_version":"4.6"}`)
	body := runFailure(t, "lint", "check", "--format", "json", "--enable", "require-typed-collection", root)
	if body.Kind != "config.invalid" || !strings.Contains(body.Message, "4.6") || !strings.Contains(body.Message, "extension_api") {
		t.Fatalf("failure = %+v", body)
	}
}

func TestRunLintExtensionAPIFailuresUseJSONKindsPathsAndEmptyStdout(t *testing.T) {
	fixture := readCLIEngineFixture(t)
	var mismatched map[string]any
	if err := json.Unmarshal(fixture, &mismatched); err != nil {
		t.Fatal(err)
	}
	mismatched["header"].(map[string]any)["version_minor"] = float64(8)
	mismatchData, err := json.Marshal(mismatched)
	if err != nil {
		t.Fatal(err)
	}

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
			if err != nil {
				t.Fatal(err)
			}
			writeCLIBytes(t, root, ".gdkit/lint.json", config)
			body := runFailure(t, "lint", "check", "--format", "json", root)
			wantPath := filepath.Join(root, filepath.FromSlash(test.path))
			if body.Kind != test.kind || body.Path != wantPath || !strings.Contains(body.Message, test.path) {
				t.Fatalf("failure = %+v, want kind %q path %q", body, test.kind, wantPath)
			}
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
			if err != nil {
				t.Fatal(err)
			}
			writeCLIBytes(t, root, ".gdkit/lint.json", config)
			body := runFailure(t, "lint", "check", "--format", "json", root)
			if body.Kind != "config.invalid" || !strings.HasSuffix(filepath.ToSlash(body.Path), "/.gdkit/lint.json") {
				t.Fatalf("failure = %+v", body)
			}
		})
	}
}

func TestRunLintResolvedExtensionAPIEscapeFailsClosedAsJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	outside := filepath.Join(t.TempDir(), "extension_api.json")
	if err := os.WriteFile(outside, readCLIEngineFixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	configuredPath := "engine.json"
	if err := os.Symlink(outside, filepath.Join(root, configuredPath)); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, root, ".gdkit/lint.json", `{"godot_version":"4.7","extension_api":"engine.json"}`)
	body := runFailure(t, "lint", "check", "--format", "json", root)
	if body.Kind != "config.invalid" || body.Path != filepath.Join(root, configuredPath) {
		t.Fatalf("failure = %+v", body)
	}
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
		if body.Kind != "config.parse" || !strings.HasSuffix(filepath.ToSlash(body.Path), "/.gdkit/lint.json") {
			t.Fatalf("failure = %+v", body)
		}
	}
}

func TestRunLintNonRegularExtensionAPIFailsAsConfigInvalidJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	if err := os.Mkdir(filepath.Join(root, "engine.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, root, ".gdkit/lint.json", `{"extension_api":"engine.json"}`)
	body := runFailure(t, "lint", "check", "--format", "json", root)
	if body.Kind != "config.invalid" || body.Path != filepath.Join(root, "engine.json") {
		t.Fatalf("failure = %+v", body)
	}
}

func readCLIEngineFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "semantic", "engineschema", "testdata", "extension_api_4_7_2_official.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
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

func writeCLIBytes(t *testing.T, root, name string, contents []byte) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
