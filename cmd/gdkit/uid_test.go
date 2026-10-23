package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/uid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertNoFile fails when a project file exists, which is how a test shows
// that a run wrote nothing where it should not have.
func assertNoFile(t *testing.T, root, name string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
	require.Error(t, err)
}

// inDirectory runs the rest of the test with root as the working directory, so
// a command can be given no project root.
func inDirectory(t *testing.T, root string) {
	t.Helper()
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

func TestRunUIDCheckClean(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "player.gd.uid", "uid://bcd\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "uid check passed (1 files)\n", stdout.String())
}

func TestRunUIDCheckReportsMissingSidecarWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "scripts/player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "(uid.missing)")
	require.Contains(t, stdout.String(), "uid check failed (1 missing, 0 to repair)")
	assertNoFile(t, root, "scripts/player.gd.uid")
}

func TestRunUIDCheckJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	var report uid.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	assert.Equal(t, 1, report.Scripts)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, uid.RuleMissing, report.Diagnostics[0].Rule)
	assert.Equal(t, "player.gd", report.Diagnostics[0].Path)
}

func TestRunUIDWriteCreatesSidecars(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "scripts/enemy.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "uid write: 2 created, 0 unchanged")
	for _, name := range []string{"player.gd.uid", "scripts/enemy.gd.uid"} {
		contents := readCLIFile(t, root, name)
		_, valid := uid.Decode(strings.TrimSpace(contents))
		assert.True(t, valid, name)
		assert.True(t, strings.HasSuffix(contents, "\n"), name)
	}
	// The project is clean afterwards, so a following check passes.
	stdout.Reset()
	code = run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
}

func TestRunUIDWriteLeavesExistingSidecarsAlone(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "player.gd.uid", "uid://bcd\n")
	writeCLIFile(t, root, "enemy.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	got := readCLIFile(t, root, "player.gd.uid")
	assert.Equal(t, "uid://bcd\n", got)
	require.Contains(t, stdout.String(), "uid write: 1 created, 1 unchanged")
}

func TestRunUIDWriteFailsOnDuplicatesItWasNotAskedToRepair(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "a.gd", formattedScript)
	writeCLIFile(t, root, "a.gd.uid", "uid://bcd\n")
	writeCLIFile(t, root, "b.gd", formattedScript)
	writeCLIFile(t, root, "b.gd.uid", "uid://bcd\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "(uid.duplicate)")
	require.Contains(t, stdout.String(), "--repair")
	got := readCLIFile(t, root, "b.gd.uid")
	assert.Equal(t, "uid://bcd\n", got)
	// b.gd is counted as left to repair, not as unchanged.
	require.Contains(t, stdout.String(), "uid write: 0 created, 1 unchanged, 1 left to repair")
}

func TestRunUIDWriteRepairs(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "a.gd", formattedScript)
	writeCLIFile(t, root, "a.gd.uid", "uid://bcd\n")
	writeCLIFile(t, root, "b.gd", formattedScript)
	writeCLIFile(t, root, "b.gd.uid", "uid://bcd\n")
	writeCLIFile(t, root, "c.gd", formattedScript)
	writeCLIFile(t, root, "c.gd.uid", "nonsense\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", "--repair", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "uid write: 0 created, 2 repaired")
	got := readCLIFile(t, root, "a.gd.uid")
	assert.Equal(t, "uid://bcd\n", got)
	stdout.Reset()
	code = run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
}

func TestRunUIDWriteJSONListsWhatItWrote(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	var report struct {
		uid.Report
		Written []string `json:"written"`
	}
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	require.Len(t, report.Written, 1)
	require.Equal(t, "player.gd.uid", report.Written[0])
}

func TestRunUIDSkipsIgnoredScripts(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkitignore", "addons/\n")
	writeCLIFile(t, root, "addons/vendor/plugin.gd", formattedScript)
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	assertNoFile(t, root, "addons/vendor/plugin.gd.uid")
	readCLIFile(t, root, "player.gd.uid")
}

func TestRunBareUIDRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	inDirectory(t, root)
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid"}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "uid check failed")
}

func TestRunUIDUsageFailures(t *testing.T) {
	cases := map[string][]string{
		"unknown subcommand":   {"uid", "nope"},
		"unknown format":       {"uid", "check", "--format", "yaml"},
		"too many roots":       {"uid", "check", "a", "b"},
		"unknown flag":         {"uid", "check", "--nope"},
		"write unknown format": {"uid", "write", "--format", "yaml"},
		"write too many roots": {"uid", "write", "a", "b"},
		"missing root":         {"uid", "check", "does-not-exist"},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		assert.Equal(t, 2, code, name)
	}
}

func TestRunUIDHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "help"}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "gdkit uid check")
}

// sceneWithScript renders a .tscn whose only ext_resource names a script by
// identity and path.
func sceneWithScript(identity, reference, path string) string {
	return "[gd_scene load_steps=2 format=3 uid=\"" + identity + "\"]\n\n" +
		"[ext_resource type=\"Script\" uid=\"" + reference + "\" path=\"res://" + path + "\" id=\"1_a\"]\n\n" +
		"[node name=\"Root\" type=\"Node\"]\n"
}

func TestRunUIDCheckReportsBrokenReferences(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "player.gd.uid", "uid://bbb\n")
	writeCLIFile(t, root, "main.tscn", sceneWithScript("uid://ddd", "uid://xxx", "player.gd"))
	writeCLIFile(t, root, "loader.gd", "extends Node\n\nconst Scene := preload(\"uid://xyy\")\n")
	writeCLIFile(t, root, "loader.gd.uid", "uid://ccc\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	output := stdout.String()
	for _, want := range []string{
		"main.tscn:3: Error:", "(uid.dangling)", "loader.gd:3: Error:",
		"uid check failed (0 missing, 0 to repair, 2 broken references)",
	} {
		assert.Contains(t, output, want)
	}
	// check writes nothing.
	got := readCLIFile(t, root, "main.tscn")
	assert.Contains(t, got, "uid://xxx")
}

func TestRunUIDCheckJSONCarriesAReferencePosition(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "player.gd.uid", "uid://bbb\n")
	writeCLIFile(t, root, "enemy.gd", formattedScript)
	writeCLIFile(t, root, "enemy.gd.uid", "uid://ccc\n")
	writeCLIFile(t, root, "main.tscn", sceneWithScript("uid://ddd", "uid://ccc", "player.gd"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	var report uid.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	require.Len(t, report.Diagnostics, 1)
	diagnostic := report.Diagnostics[0]
	assert.Equal(t, uid.RuleCrossed, diagnostic.Rule)
	assert.Equal(t, "main.tscn", diagnostic.Path)
	assert.Equal(t, 3, diagnostic.Line)
	assert.Equal(t, "player.gd", diagnostic.Target)
	assert.Equal(t, "uid://ccc", diagnostic.UID)
}

func TestRunUIDWriteRepointsBrokenReferences(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "player.gd.uid", "uid://bbb\n")
	writeCLIFile(t, root, "main.tscn", sceneWithScript("uid://ddd", "uid://xxx", "player.gd"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "wrote main.tscn")
	require.Contains(t, stdout.String(), "1 references rewritten")
	got := readCLIFile(t, root, "main.tscn")
	assert.Contains(t, got, "uid://bbb")
	stdout.Reset()
	code = run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
}

// A pathless reference cannot be repaired, so the run reports it and fails
// even though it had nothing else to leave behind.
func TestRunUIDWriteFailsOnAReferenceItCannotRepair(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "loader.gd", "extends Node\n\nconst Scene := preload(\"uid://xyy\")\n")
	writeCLIFile(t, root, "loader.gd.uid", "uid://bbb\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", "--repair", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	output := stdout.String()
	require.Contains(t, output, "(uid.dangling)")
	require.Contains(t, output, "1 to fix by hand")
}

// Repairing a malformed scene identity moves the references to it in the same
// run, so the old value is left nowhere in the project.
func TestRunUIDWriteRepairMovesReferencesToAReissuedIdentity(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "main.tscn", "[gd_scene format=3 uid=\"uid://b_local_screen\"]\n")
	writeCLIFile(t, root, "other.tscn",
		"[gd_scene load_steps=2 format=3 uid=\"uid://ccc\"]\n\n"+
			"[ext_resource type=\"PackedScene\" uid=\"uid://b_local_screen\" path=\"res://main.tscn\" id=\"1_a\"]\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "write", "--repair", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	for _, name := range []string{"main.tscn", "other.tscn"} {
		got := readCLIFile(t, root, name)
		assert.NotContains(t, got, "b_local_screen", name)
	}
	stdout.Reset()
	code = run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
}

// An ignored addon still owns its identifier, so a reference to it is not
// dangling. Nothing about it is written either.
func TestRunUIDResolvesAgainstAnIgnoredClaimant(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkitignore", "addons/\n")
	writeCLIFile(t, root, "addons/vendor/plugin.gd", formattedScript)
	writeCLIFile(t, root, "addons/vendor/plugin.gd.uid", "uid://fff\n")
	writeCLIFile(t, root, "main.tscn", sceneWithScript("uid://ddd", "uid://fff", "addons/vendor/plugin.gd"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"uid", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "uid check passed (0 files)\n", stdout.String())
}
