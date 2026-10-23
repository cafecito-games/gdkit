package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/require"
)

const uncleanScript = "extends Node\nvar a=1\nfunc doThing( x ):\n\treturn x+a\n"

// writeIgnoreProject builds a project whose only problem file sits in a
// nested addons directory, which the default exclude globs do not cover.
func writeIgnoreProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "apps/editor/addons/tool/tool.gd", uncleanScript)
	return root
}

func TestRunLintAndFormatHonourIgnoreFile(t *testing.T) {
	commands := [][]string{{"lint", "check"}, {"format", "check"}}
	for _, command := range commands {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			root := writeIgnoreProject(t)
			var stdout, stderr bytes.Buffer
			code := run(append(command, root), &stdout, &stderr)
			require.Equal(t, 1, code)
			require.Contains(t, stdout.String(), "apps/editor/addons/tool/tool.gd")

			writeCLIFile(t, root, project.IgnoreFileName, "addons/\n")
			stdout.Reset()
			stderr.Reset()
			code = run(append(command, root), &stdout, &stderr)
			require.Equal(t, 0, code)
			require.NotContains(t, stdout.String(), "addons")
		})
	}
}

func TestRunFormatWriteLeavesIgnoredFilesUntouched(t *testing.T) {
	root := writeIgnoreProject(t)
	writeCLIFile(t, root, "scripts/enemy.gd", unformattedScript)
	writeCLIFile(t, root, project.IgnoreFileName, "addons/\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	got := readCLIFile(t, root, "apps/editor/addons/tool/tool.gd")
	require.Equal(t, uncleanScript, got)
	got = readCLIFile(t, root, "scripts/enemy.gd")
	require.NotEqual(t, unformattedScript, got)
	want := "reformatted scripts/enemy.gd\nformat write: 1 reformatted, 1 unchanged\n"
	require.Equal(t, want, stdout.String())
}

func TestRunFormatWriteWithoutIgnoreFileRewritesNestedAddons(t *testing.T) {
	root := writeIgnoreProject(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"format", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	got := readCLIFile(t, root, "apps/editor/addons/tool/tool.gd")
	require.NotEqual(t, uncleanScript, got)
}

func TestRunRejectsMalformedIgnoreFile(t *testing.T) {
	commands := [][]string{{"lint", "check"}, {"format", "check"}, {"format", "write"}}
	for _, command := range commands {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			root := writeIgnoreProject(t)
			writeCLIFile(t, root, project.IgnoreFileName, "addons/\n[unterminated\n")
			var stdout, stderr bytes.Buffer
			code := run(append(command, root), &stdout, &stderr)
			require.Equal(t, 2, code)
			require.True(t, strings.HasPrefix(stderr.String(), "gdkit: parse .gdkitignore: line 2: "))
			got := readCLIFile(t, root, "apps/editor/addons/tool/tool.gd")
			require.Equal(t, uncleanScript, got)
		})
	}
}

// Hiding a file from the architecture analyzer would drop its class from the
// index, so arch check must see every file whatever the ignore file says.
func TestRunArchCheckDoesNotReadIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health extends Node\n")
	arguments := []string{"arch", "check", "--format", "json", "--show-edges", root}
	var before, stderr bytes.Buffer
	code := run(arguments, &before, &stderr)
	require.Equal(t, 1, code)

	for _, contents := range []string{"features/\n*.gd\n", "[unterminated\n"} {
		writeCLIFile(t, root, project.IgnoreFileName, contents)
		var after bytes.Buffer
		stderr.Reset()
		code = run(arguments, &after, &stderr)
		require.Equal(t, 1, code)
		require.Equal(t, before.String(), after.String())
	}
}
