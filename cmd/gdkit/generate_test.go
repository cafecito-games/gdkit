package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/generate"
)

const optedInScript = "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar q: int\nvar r: int\n"

func TestRunGenCheckThenWriteThenCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1 for a stale region: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "would generate hex.gd") {
		t.Errorf("stdout = %q, want the file named", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"gen", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("write exit %d, want 0: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "generated hex.gd") {
		t.Errorf("write did not name the file it changed: %q", stdout.String())
	}
	written := readCLIFile(t, root, "hex.gd")
	if !strings.Contains(written, "func _to_string() -> String:") || !strings.Contains(written, "func equals(") {
		t.Errorf("both methods were not generated:\n%s", written)
	}

	stdout.Reset()
	if code := run([]string{"gen", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d after write, want 0: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "gen check passed") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestRunBareGenRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1: %s %s", code, stdout.String(), stderr.String())
	}
}

func TestRunGenCheckCleanOnAProjectThatOptedIntoNothing(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "plain.gd", "class_name Plain\nextends RefCounted\n\nvar q: int\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, want 0: %s %s", code, stdout.String(), stderr.String())
	}
}

func TestRunGenCheckDiff(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", "--diff", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "+# gdkit:generated:begin") {
		t.Errorf("stdout has no diff of the new region:\n%s", stdout.String())
	}
}

func TestRunGenRejectsDiffWithJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", "--diff", "--format", "json", t.TempDir()}, &stdout, &stderr); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestRunGenJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", "--format", "json", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, stderr.String())
	}
	var report generate.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not a report: %v (%s)", err, stdout.String())
	}
	if !report.HasChanges() {
		t.Errorf("report = %+v, want a change", report)
	}
}

// An exit-2 failure under --format json is an envelope on stderr, with stdout
// empty so a consumer can tell "no report" from "an empty report".
func TestRunGenJSONFailureGoesToStderrWithEmptyStdout(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/generate.json", `{"not_a_key": 1}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", "--format", "json", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	var envelope struct {
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not an envelope: %v (%q)", err, stderr.String())
	}
	if envelope.Error.Kind == "" {
		t.Errorf("the envelope carries no failure kind: %s", stderr.String())
	}
}

// A refused class fails write; staleness that write fixed does not.
func TestRunGenWriteFailsOnlyOnARefusal(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd",
		"class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n"+
			"static func equals(a, b) -> bool:\n\treturn true\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "write", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1 for a conflict: %s %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "generate.conflict") {
		t.Errorf("stdout = %q, want the conflict reported", stdout.String())
	}
}

func TestRunGenWritePrune(t *testing.T) {
	root := t.TempDir()
	orphan := "class_name Hex\nextends RefCounted\n\nvar q: int\n\n\n" +
		"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Hex()\"\n\n\n# gdkit:generated:end\n"
	writeCLIFile(t, root, "hex.gd", orphan)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "write", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1 for an orphan: %s %s", code, stdout.String(), stderr.String())
	}
	if readCLIFile(t, root, "hex.gd") != orphan {
		t.Error("an orphan was modified without --prune")
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"gen", "write", "--prune", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("prune exit %d, want 0: %s %s", code, stdout.String(), stderr.String())
	}
	if got := readCLIFile(t, root, "hex.gd"); got != "class_name Hex\nextends RefCounted\n\nvar q: int\n" {
		t.Errorf("pruned = %q", got)
	}
}

func TestRunGenInit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "init", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), generate.DefaultConfigPath) {
		t.Errorf("stdout = %q", stdout.String())
	}
	var config generate.Config
	if err := json.Unmarshal([]byte(readCLIFile(t, root, generate.DefaultConfigPath)), &config); err != nil {
		t.Fatalf("the written config is not valid JSON: %v", err)
	}
	if err := config.Validate(); err != nil {
		t.Errorf("gen init wrote a config that does not validate: %v", err)
	}
	// A second init must not clobber without --force.
	if code := run([]string{"gen", "init", root}, &stdout, &stderr); code == 0 {
		t.Error("a second init overwrote the config without --force")
	}
}

func TestRunGenUnknownSubcommandFlagIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", "--nope"}, &stdout, &stderr); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

// The generated region must be canonical in the project's own style, so that
// gen write introduces no new format check finding.
func TestRunGenWriteLeavesFormatCheckClean(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("write exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"format", "check", root}, &stdout, &stderr); code != 0 {
		t.Errorf("format check exit %d after gen write: %s", code, stdout.String())
	}
}
