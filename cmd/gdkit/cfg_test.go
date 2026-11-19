package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleProjectGodot = `; Engine configuration file.
config_version=5

[animation]

compatibility/default_parent_skeleton_in_mesh_instance_3d=true

[application]

config/name="Uzir"
config/features=PackedStringArray("4.7", "GL Compatibility")
boot_splash/bg_color=Color(0.035, 0.028, 0.022, 1)
boot_splash/show_image=false
`

func TestCfgQueryFormatsAndFlags(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "project.godot", sampleProjectGodot)
	projectPath := filepath.Join(root, "project.godot")

	t.Run("default json query on nested slash key", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"cfg", "query", `.["animation"].compatibility.default_parent_skeleton_in_mesh_instance_3d`, projectPath}, &stdout, &stderr)
		require.Equal(t, 0, code)
		assert.Equal(t, "true\n", stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("pretty json by default and compact with -c", func(t *testing.T) {
		var prettyOut, prettyErr bytes.Buffer
		code := run([]string{"cfg", "query", `.application.config`, projectPath}, &prettyOut, &prettyErr)
		require.Equal(t, 0, code)
		assert.Equal(t, "{\n  \"features\": [\n    \"4.7\",\n    \"GL Compatibility\"\n  ],\n  \"name\": \"Uzir\"\n}\n", prettyOut.String())
		assert.Empty(t, prettyErr.String())

		var compactOut, compactErr bytes.Buffer
		code = run([]string{"cfg", "query", "-c", `.application.config`, projectPath}, &compactOut, &compactErr)
		require.Equal(t, 0, code)
		assert.Equal(t, "{\"features\":[\"4.7\",\"GL Compatibility\"],\"name\":\"Uzir\"}\n", compactOut.String())
		assert.Empty(t, compactErr.String())
	})

	t.Run("raw output with -r and --format raw", func(t *testing.T) {
		for _, flagArgs := range [][]string{{"-r"}, {"--raw-output"}, {"--format", "raw"}, {"--format", "raw", "-r"}} {
			args := append([]string{"cfg", "query"}, flagArgs...)
			args = append(args, `.application.config.name`, projectPath)
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			require.Equal(t, 0, code)
			assert.Equal(t, "Uzir\n", stdout.String())
			assert.Empty(t, stderr.String())
		}
	})

	t.Run("godot output format", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"cfg", "query", "--format", "godot", `.application.boot_splash.bg_color`, projectPath}, &stdout, &stderr)
		require.Equal(t, 0, code)
		assert.Equal(t, "Color(0.035, 0.028, 0.022, 1)\n", stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("flat flag keeps literal slash keys", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"cfg", "query", "--flat", `.animation["compatibility/default_parent_skeleton_in_mesh_instance_3d"]`, projectPath}, &stdout, &stderr)
		require.Equal(t, 0, code)
		assert.Equal(t, "true\n", stdout.String())
		assert.Empty(t, stderr.String())
	})
}

func TestCfgQueryExitStatus(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "project.godot", sampleProjectGodot)
	projectPath := filepath.Join(root, "project.godot")

	cases := map[string]struct {
		filter   string
		wantCode int
		wantOut  string
	}{
		"truthy bool exits 0":      {`.animation.compatibility.default_parent_skeleton_in_mesh_instance_3d`, 0, "true\n"},
		"false bool exits 1":       {`.application.boot_splash.show_image`, 1, "false\n"},
		"missing key null exits 1": {`.application.missing`, 1, "null\n"},
		"empty stream exits 1":     {`empty`, 1, ""},
		"last value decides exit":  {`false, true`, 0, "false\ntrue\n"},
		"last false exits 1":       {`true, false`, 1, "true\nfalse\n"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"cfg", "query", "-e", testCase.filter, projectPath}, &stdout, &stderr)
			assert.Equal(t, testCase.wantCode, code)
			assert.Equal(t, testCase.wantOut, stdout.String())
			assert.Empty(t, stderr.String())
		})
	}
}

func TestCfgQueryStdinAndMultipleFiles(t *testing.T) {
	t.Run("reads stdin when no file is given", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runWithStdin([]string{"cfg", "query", "-r", `.application.config.name`}, strings.NewReader(sampleProjectGodot), &stdout, &stderr)
		require.Equal(t, 0, code)
		assert.Equal(t, "Uzir\n", stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("reads stdin when path is dash", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runWithStdin([]string{"cfg", "query", `.config_version`, "-"}, strings.NewReader(sampleProjectGodot), &stdout, &stderr)
		require.Equal(t, 0, code)
		assert.Equal(t, "5\n", stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("multiple files stream in order and slurp combines into array", func(t *testing.T) {
		root := t.TempDir()
		writeCLIFile(t, root, "a.cfg", "[app]\nname=\"First\"\n")
		writeCLIFile(t, root, "b.cfg", "[app]\nname=\"Second\"\n")
		pathA := filepath.Join(root, "a.cfg")
		pathB := filepath.Join(root, "b.cfg")

		var streamOut, streamErr bytes.Buffer
		code := run([]string{"cfg", "query", "-r", `.app.name`, pathA, pathB}, &streamOut, &streamErr)
		require.Equal(t, 0, code)
		assert.Equal(t, "First\nSecond\n", streamOut.String())
		assert.Empty(t, streamErr.String())

		var slurpOut, slurpErr bytes.Buffer
		code = run([]string{"cfg", "query", "-s", "-c", `map(.app.name)`, pathA, pathB}, &slurpOut, &slurpErr)
		require.Equal(t, 0, code)
		assert.Equal(t, "[\"First\",\"Second\"]\n", slurpOut.String())
		assert.Empty(t, slurpErr.String())

		var inputsOut, inputsErr bytes.Buffer
		code = run([]string{"cfg", "query", "-c", `[., inputs] | map(.app.name)`, pathA, pathB}, &inputsOut, &inputsErr)
		require.Equal(t, 0, code)
		assert.Equal(t, "[\"First\",\"Second\"]\n", inputsOut.String())
		assert.Empty(t, inputsErr.String())
	})
}

func TestCfgQueryFailuresAndHelp(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "project.godot", sampleProjectGodot)
	writeCLIFile(t, root, "broken.godot", "[unterminated\n")
	projectPath := filepath.Join(root, "project.godot")
	brokenPath := filepath.Join(root, "broken.godot")
	missingPath := filepath.Join(root, "missing.godot")

	t.Run("help and unknown subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 0, run([]string{"cfg"}, &stdout, &stderr))
		assert.Contains(t, stdout.String(), "gdkit cfg query")

		stdout.Reset()
		stderr.Reset()
		assert.Equal(t, 2, run([]string{"cfg", "bogus"}, &stdout, &stderr))
		assert.Contains(t, stderr.String(), `unknown cfg command "bogus"`)
	})

	t.Run("unknown format is reported as text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"cfg", "query", "--format", "xml", `.`, projectPath}, &stdout, &stderr)
		require.Equal(t, 2, code)
		assert.Empty(t, stdout.String())
		assert.True(t, strings.HasPrefix(stderr.String(), "gdkit: unknown output format"))
		assert.False(t, json.Valid(bytes.TrimSpace(stderr.Bytes())))
	})

	t.Run("conflicting flags fail with exit 2", func(t *testing.T) {
		body := runFailure(t, "cfg", "query", "--format", "json", "-r", `.`, projectPath)
		assert.Equal(t, "usage.arguments", body.Kind)

		var stdout, stderr bytes.Buffer
		code := run([]string{"cfg", "query", "--format", "godot", "-r", `.`, projectPath}, &stdout, &stderr)
		require.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "cannot be combined")

		stdout.Reset()
		stderr.Reset()
		code = run([]string{"cfg", "query", "--format", "godot", "-c", `.`, projectPath}, &stdout, &stderr)
		require.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "cannot be combined")
	})

	t.Run("missing filter reports usage.arguments in json envelope", func(t *testing.T) {
		body := runFailure(t, "cfg", "query")
		assert.Equal(t, "usage.arguments", body.Kind)
	})

	t.Run("invalid query reports query.parse", func(t *testing.T) {
		body := runFailure(t, "cfg", "query", `.application | [`, projectPath)
		assert.Equal(t, "query.parse", body.Kind)
	})

	t.Run("runtime error reports query.eval", func(t *testing.T) {
		body := runFailure(t, "cfg", "query", `error("bad config")`, projectPath)
		assert.Equal(t, "query.eval", body.Kind)
		assert.Contains(t, body.Message, "bad config")
	})

	t.Run("missing file reports config.read with path", func(t *testing.T) {
		body := runFailure(t, "cfg", "query", `.`, missingPath)
		assert.Equal(t, "config.read", body.Kind)
		assert.Equal(t, missingPath, body.Path)
	})

	t.Run("broken second file fails before writing stdout", func(t *testing.T) {
		body := runFailure(t, "cfg", "query", `.`, projectPath, brokenPath)
		assert.Equal(t, "config.parse", body.Kind)
		assert.Equal(t, brokenPath, body.Path)
	})

	t.Run("minimum-version gate applies before reading files", func(t *testing.T) {
		pretendVersion(t, "0.2.0")
		body := runFailure(t, "cfg", "query", "--minimum-version", "99.0.0", `.`, missingPath)
		assert.Equal(t, "usage.version_floor", body.Kind)
	})
}
