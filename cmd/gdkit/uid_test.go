package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/uid"
)

// assertNoFile fails when a project file exists, which is how a test shows
// that a run wrote nothing where it should not have.
func assertNoFile(t *testing.T, root, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
		t.Fatalf("%s exists, want it never written", name)
	}
}

// inDirectory runs the rest of the test with root as the working directory, so
// a command can be given no project root.
func inDirectory(t *testing.T, root string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

func TestRunUIDCheckClean(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "player.gd.uid", "uid://bcd\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "uid check passed (1 files)\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunUIDCheckReportsMissingSidecarWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "scripts/player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "(uid.missing)") {
		t.Fatalf("output = %q, want a uid.missing diagnostic", stdout.String())
	}
	if !strings.Contains(stdout.String(), "uid check failed (1 missing, 0 to repair)") {
		t.Fatalf("output = %q, want a failing summary", stdout.String())
	}
	assertNoFile(t, root, "scripts/player.gd.uid")
}

func TestRunUIDCheckJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "check", "--format", "json", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stderr=%s", code, stderr.String())
	}
	var report uid.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, stdout.String())
	}
	if report.Scripts != 1 {
		t.Errorf("scripts = %d, want 1", report.Scripts)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Rule != uid.RuleMissing {
		t.Fatalf("diagnostics = %+v, want one %s", report.Diagnostics, uid.RuleMissing)
	}
	if report.Diagnostics[0].Path != "player.gd" {
		t.Errorf("path = %q, want player.gd", report.Diagnostics[0].Path)
	}
}

func TestRunUIDWriteCreatesSidecars(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "scripts/enemy.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "uid write: 2 created, 0 unchanged") {
		t.Fatalf("output = %q, want a 2-created summary", stdout.String())
	}
	for _, name := range []string{"player.gd.uid", "scripts/enemy.gd.uid"} {
		contents := readCLIFile(t, root, name)
		if _, valid := uid.Decode(strings.TrimSpace(contents)); !valid {
			t.Errorf("%s = %q, want a usable identifier", name, contents)
		}
		if !strings.HasSuffix(contents, "\n") {
			t.Errorf("%s = %q, want a trailing newline", name, contents)
		}
	}
	// The project is clean afterwards, so a following check passes.
	stdout.Reset()
	if code := run([]string{"uid", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("check after write exited %d: %s", code, stdout.String())
	}
}

func TestRunUIDWriteLeavesExistingSidecarsAlone(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "player.gd.uid", "uid://bcd\n")
	writeCLIFile(t, root, "enemy.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := readCLIFile(t, root, "player.gd.uid"); got != "uid://bcd\n" {
		t.Errorf("player.gd.uid = %q, want it untouched", got)
	}
	if !strings.Contains(stdout.String(), "uid write: 1 created, 1 unchanged") {
		t.Fatalf("output = %q, want a 1-created summary", stdout.String())
	}
}

func TestRunUIDWriteFailsOnDuplicatesItWasNotAskedToRepair(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "a.gd", formattedScript)
	writeCLIFile(t, root, "a.gd.uid", "uid://bcd\n")
	writeCLIFile(t, root, "b.gd", formattedScript)
	writeCLIFile(t, root, "b.gd.uid", "uid://bcd\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "write", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "(uid.duplicate)") {
		t.Fatalf("output = %q, want the duplicate reported", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--repair") {
		t.Fatalf("output = %q, want it to mention --repair", stdout.String())
	}
	if got := readCLIFile(t, root, "b.gd.uid"); got != "uid://bcd\n" {
		t.Errorf("b.gd.uid = %q, want it untouched", got)
	}
	// b.gd is counted as left to repair, not as unchanged.
	if !strings.Contains(stdout.String(), "uid write: 0 created, 1 unchanged, 1 left to repair") {
		t.Fatalf("output = %q, want the repair left out of the unchanged count", stdout.String())
	}
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
	if code := run([]string{"uid", "write", "--repair", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "uid write: 0 created, 2 repaired") {
		t.Fatalf("output = %q, want a 2-repaired summary", stdout.String())
	}
	if got := readCLIFile(t, root, "a.gd.uid"); got != "uid://bcd\n" {
		t.Errorf("a.gd.uid = %q, want the first claimant untouched", got)
	}
	stdout.Reset()
	if code := run([]string{"uid", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("check after repair exited %d: %s", code, stdout.String())
	}
}

func TestRunUIDWriteJSONListsWhatItWrote(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "write", "--format", "json", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stderr=%s", code, stderr.String())
	}
	var report struct {
		uid.Report
		Written []string `json:"written"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, stdout.String())
	}
	if len(report.Written) != 1 || report.Written[0] != "player.gd.uid" {
		t.Fatalf("written = %v, want player.gd.uid", report.Written)
	}
}

func TestRunUIDSkipsIgnoredScripts(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkitignore", "addons/\n")
	writeCLIFile(t, root, "addons/vendor/plugin.gd", formattedScript)
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	assertNoFile(t, root, "addons/vendor/plugin.gd.uid")
	readCLIFile(t, root, "player.gd.uid")
}

func TestRunBareUIDRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	inDirectory(t, root)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "uid check failed") {
		t.Fatalf("output = %q, want a failing check", stdout.String())
	}
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
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stdout=%s stderr=%s)", name, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunUIDHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"uid", "help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "gdkit uid check") {
		t.Fatalf("help does not document uid check: %s", stdout.String())
	}
}
