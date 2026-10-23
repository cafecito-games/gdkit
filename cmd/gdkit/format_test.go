package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	formattedScript   = "extends Node\n\n\nfunc do_thing() -> void:\n\tpass\n"
	unformattedScript = "extends Node\nvar a=1\nfunc f( x ):\n\treturn x+a\n"
	unparseableScript = "func f(:\n"
)

func readCLIFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	require.NoError(t, err)
	return string(data)
}

func TestRunFormatCheckClean(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "format check passed (1 files)\n", stdout.String())
}

func TestRunFormatCheckReportsUnformattedFileWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "clean.gd", formattedScript)
	writeCLIFile(t, root, "scripts/player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	want := "would reformat scripts/player.gd\nformat check failed (1 to reformat, 0 diagnostics)\n"
	require.Equal(t, want, stdout.String())
	got := readCLIFile(t, root, "scripts/player.gd")
	require.Equal(t, unformattedScript, got)
}

func TestRunBareFormatRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", unformattedScript)
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var stdout, stderr bytes.Buffer
	code := run([]string{"format"}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "would reformat player.gd")
	got := readCLIFile(t, root, "player.gd")
	require.Equal(t, unformattedScript, got)
}

func TestRunFormatCheckReportsUnparseableFile(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "broken.gd", unparseableScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	output := stdout.String()
	require.Contains(t, output, "broken.gd:1: Error: ")
	require.Contains(t, output, "(source-parse)")
	require.Contains(t, output, "format check failed (0 to reformat, 1 diagnostics)")
}

func TestRunFormatCheckDiff(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "check", "--diff", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	for _, want := range []string{
		"would reformat player.gd\n--- a/player.gd\n+++ b/player.gd\n@@ ",
		"\n-var a=1\n",
		"\n+var a = 1\n",
		"\nformat check failed (1 to reformat, 0 diagnostics)\n",
	} {
		assert.Contains(t, stdout.String(), want)
	}
	got := readCLIFile(t, root, "player.gd")
	require.Equal(t, unformattedScript, got)
}

func TestRunFormatJSON(t *testing.T) {
	type report struct {
		Results []struct {
			Path    string `json:"path"`
			Changed bool   `json:"changed"`
		} `json:"results"`
		Diagnostics []struct {
			Rule string `json:"rule"`
			Path string `json:"path"`
		} `json:"diagnostics"`
	}
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "  \"results\": []")
	require.Contains(t, stdout.String(), "  \"diagnostics\": []")

	writeCLIFile(t, root, "broken.gd", unparseableScript)
	writeCLIFile(t, root, "clean.gd", formattedScript)
	writeCLIFile(t, root, "player.gd", unformattedScript)
	for _, command := range []string{"check", "write"} {
		stdout.Reset()
		stderr.Reset()
		code = run([]string{"format", command, "--format", "json", root}, &stdout, &stderr)
		require.Equal(t, 1, code, command)
		var decoded report
		err := json.Unmarshal(stdout.Bytes(), &decoded)
		require.NoError(t, err, command)
		require.Len(t, decoded.Results, 2)
		require.Equal(t, "clean.gd", decoded.Results[0].Path)
		require.False(t, decoded.Results[0].Changed)
		require.Equal(t, "player.gd", decoded.Results[1].Path)
		require.True(t, decoded.Results[1].Changed)
		require.Len(t, decoded.Diagnostics, 1)
		require.Equal(t, "source-parse", decoded.Diagnostics[0].Rule)
		require.Equal(t, "broken.gd", decoded.Diagnostics[0].Path)
		if command == "check" {
			require.Equal(t, unformattedScript, readCLIFile(t, root, "player.gd"))
		}
	}
	require.NotEqual(t, unformattedScript, readCLIFile(t, root, "player.gd"))
}

func TestRunFormatUsageErrors(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", unformattedScript)
	cases := map[string][]string{
		"check unknown format": {"format", "check", "--format", "yaml", root},
		"write unknown format": {"format", "write", "--format", "yaml", root},
		"diff with json":       {"format", "check", "--diff", "--format", "json", root},
		"write rejects diff":   {"format", "write", "--diff", root},
		"check too many roots": {"format", "check", root, root},
		"write too many roots": {"format", "write", root, root},
		"init too many roots":  {"format", "init", root, root},
		"missing config":       {"format", "check", "--config", "missing.json", root},
		"missing root":         {"format", "check", filepath.Join(root, "absent")},
		"unknown format verb":  {"format", "bogus"},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		assert.Equal(t, 2, code, name)
		assert.NotEmpty(t, stderr.String(), name)
	}

	writeCLIFile(t, root, ".gdkit/format.json", "{not json")
	for _, command := range []string{"check", "write"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"format", command, root}, &stdout, &stderr)
		assert.Equal(t, 2, code, command)
		assert.True(t, strings.HasPrefix(stderr.String(), "gdkit: "), command)
	}
	got := readCLIFile(t, root, "player.gd")
	require.Equal(t, unformattedScript, got)
}

func TestRunFormatHonoursConfig(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "func f():\n    pass\n")
	writeCLIFile(t, root, "style.json", `{"indent": "spaces"}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "check", "--config", "style.json", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	stdout.Reset()
	code = run([]string{"format", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
}

func TestRunFormatWriteThenCheckPasses(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "clean.gd", formattedScript)
	writeCLIFile(t, root, "player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	want := "reformatted player.gd\nformat write: 1 reformatted, 1 unchanged\n"
	require.Equal(t, want, stdout.String())
	got := readCLIFile(t, root, "player.gd")
	require.Equal(t, "extends Node\nvar a = 1\n\n\nfunc f(x):\n\treturn x + a\n", got)
	got = readCLIFile(t, root, "clean.gd")
	require.Equal(t, formattedScript, got)

	stdout.Reset()
	code = run([]string{"format", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	stdout.Reset()
	code = run([]string{"format", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "format write: 0 reformatted, 2 unchanged\n", stdout.String())
}

func TestRunFormatWriteSkipsUnparseableFile(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "broken.gd", unparseableScript)
	writeCLIFile(t, root, "player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	output := stdout.String()
	require.True(t, strings.HasPrefix(output, "reformatted player.gd\nbroken.gd:1: Error: "))
	require.Contains(t, output, "(source-parse)")
	require.True(t, strings.HasSuffix(output, "format write: 1 reformatted, 0 unchanged, 1 skipped\n"))
	got := readCLIFile(t, root, "broken.gd")
	require.Equal(t, unparseableScript, got)
	got = readCLIFile(t, root, "player.gd")
	require.NotEqual(t, unformattedScript, got)
}

func TestRunFormatWriteReportsWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop the superuser")
	}
	root := t.TempDir()
	writeCLIFile(t, root, "a.gd", unformattedScript)
	writeCLIFile(t, root, "locked/b.gd", unformattedScript)
	locked := filepath.Join(root, "locked")
	require.NoError(t, os.Chmod(locked, 0o555))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.Contains(t, stdout.String(), "reformatted a.gd\n")
	assert.True(t, strings.HasPrefix(stderr.String(), "gdkit: write locked/b.gd: "))
	got := readCLIFile(t, root, "locked/b.gd")
	assert.Equal(t, unformattedScript, got)
}

func TestRunFormatInit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "init", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "wrote .gdkit/format.json\n", stdout.String())
	config, err := format.LoadConfig(root, "")
	require.NoError(t, err)
	require.Equal(t, format.DefaultConfig().LineWidth, config.LineWidth)
	require.Equal(t, "tabs", config.Indent)
	name := filepath.Join(root, ".gdkit", "format.json")
	data := readCLIFile(t, root, ".gdkit/format.json")
	require.True(t, strings.HasSuffix(data, "}\n"))
	require.Contains(t, data, "\n  \"line_width\": 100,\n")

	require.NoError(t, os.WriteFile(name, []byte("custom"), 0o644))
	stderr.Reset()
	code = run([]string{"format", "init", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "already exists (use --force to replace it)")
	got, _ := os.ReadFile(name)
	require.Equal(t, "custom", string(got))
	code = run([]string{"format", "init", "--force", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	_, err = format.LoadConfig(root, "")
	require.NoError(t, err)
}

func TestRunHelpMentionsFormatCommands(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"format", "help"}, {"format", "--help"}, {"format", "-h"}} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		require.Equal(t, 0, code)
		for _, command := range []string{"format check", "format write", "format init"} {
			assert.Contains(t, stdout.String(), command)
		}
	}
}

// decodeWritten returns the "written" member of a JSON report, and whether the
// report has one at all.
func decodeWritten(t *testing.T, output []byte) ([]string, bool) {
	t.Helper()
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(output, &members))
	for _, name := range []string{"results", "diagnostics"} {
		require.Contains(t, members, name)
	}
	raw, ok := members["written"]
	if !ok {
		return nil, false
	}
	var written []string
	err := json.Unmarshal(raw, &written)
	require.NoError(t, err)
	require.NotNil(t, written)
	return written, true
}

func TestRunFormatWriteJSONListsWrittenPaths(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "clean.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	written, ok := decodeWritten(t, stdout.Bytes())
	require.True(t, ok)
	require.Empty(t, written)
	require.Contains(t, stdout.String(), "  \"written\": []")

	writeCLIFile(t, root, "broken.gd", unparseableScript)
	writeCLIFile(t, root, "b/player.gd", unformattedScript)
	writeCLIFile(t, root, "a/player.gd", unformattedScript)
	stdout.Reset()
	code = run([]string{"format", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	_, ok = decodeWritten(t, stdout.Bytes())
	require.False(t, ok)
	stdout.Reset()
	code = run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	written, _ = decodeWritten(t, stdout.Bytes())
	require.Equal(t, "a/player.gd,b/player.gd", strings.Join(written, ","))
}

func TestRunFormatWriteJSONListsWrittenPathsAfterAFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop the superuser")
	}
	root := t.TempDir()
	writeCLIFile(t, root, "a.gd", unformattedScript)
	writeCLIFile(t, root, "locked/b.gd", unformattedScript)
	writeCLIFile(t, root, "z.gd", unformattedScript)
	locked := filepath.Join(root, "locked")
	require.NoError(t, os.Chmod(locked, 0o555))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	written, _ := decodeWritten(t, stdout.Bytes())
	require.Equal(t, "a.gd", strings.Join(written, ","))
	// --format json was asked for, so the failure is an envelope on stderr
	// rather than prose. stdout still carries the report of what was written
	// before the failure, which is why this run is not covered by the
	// empty-stdout rule the other enveloped failures follow.
	body := decodeFailure(t, stderr.Bytes())
	assert.Equal(t, "file.write", body.Kind)
	assert.True(t, strings.HasPrefix(body.Message, "write locked/b.gd: "))
}

func TestRunFormatRefusesAnUnsafeRewrite(t *testing.T) {
	// Splitting the two statements would leave the second on a line the
	// directive no longer reaches.
	const unsafe = "var BadOne = 1; var BadTwo = 2 # gdlint:ignore=class-variable-name\n"
	root := t.TempDir()
	writeCLIFile(t, root, "text.gd", unsafe)
	writeCLIFile(t, root, "player.gd", unformattedScript)
	want := "text.gd:1: Error: formatting would change the code a lint suppression comment applies to (format.unsafe)\n"
	for _, command := range []string{"check", "write"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"format", command, root}, &stdout, &stderr)
		require.Equal(t, 1, code, command)
		assert.Contains(t, stdout.String(), want, command)
		got := readCLIFile(t, root, "text.gd")
		require.Equal(t, unsafe, got, command)
	}
	got := readCLIFile(t, root, "player.gd")
	require.NotEqual(t, unformattedScript, got)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	written, _ := decodeWritten(t, stdout.Bytes())
	require.Empty(t, written)
}

// A one-line class body holds one member, so writing the body as a block has
// to leave what a semicolon separated from it outside the class.
func TestRunFormatWritesOneLineClassMembersInTheirOwnScope(t *testing.T) {
	const source = "class A: var v = 1; var u = 2\n"
	const want = "class A:\n\tvar v = 1\n\n\nvar u = 2\n"
	root := t.TempDir()
	writeCLIFile(t, root, "inner.gd", source)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	got := readCLIFile(t, root, "inner.gd")
	require.Equal(t, want, got)
}
