package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/format"
)

const (
	formattedScript   = "extends Node\n\n\nfunc do_thing() -> void:\n\tpass\n"
	unformattedScript = "extends Node\nvar a=1\nfunc f( x ):\n\treturn x+a\n"
	unparseableScript = "func f(:\n"
)

func readCLIFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunFormatCheckClean(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "format check passed (1 files)\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunFormatCheckReportsUnformattedFileWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "clean.gd", formattedScript)
	writeCLIFile(t, root, "scripts/player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	want := "would reformat scripts/player.gd\nformat check failed (1 to reformat, 0 diagnostics)\n"
	if stdout.String() != want {
		t.Fatalf("output = %q, want %q", stdout.String(), want)
	}
	if got := readCLIFile(t, root, "scripts/player.gd"); got != unformattedScript {
		t.Fatalf("check rewrote the file: %q", got)
	}
}

func TestRunBareFormatRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", unformattedScript)
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "would reformat player.gd") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
	if got := readCLIFile(t, root, "player.gd"); got != unformattedScript {
		t.Fatalf("bare format rewrote the file: %q", got)
	}
}

func TestRunFormatCheckReportsUnparseableFile(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "broken.gd", unparseableScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "broken.gd:1: Error: ") || !strings.Contains(output, "(source-parse)") {
		t.Fatalf("parse failure missing from output: %s", output)
	}
	if !strings.Contains(output, "format check failed (0 to reformat, 1 diagnostics)") {
		t.Fatalf("unexpected summary: %s", output)
	}
}

func TestRunFormatCheckDiff(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "check", "--diff", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"would reformat player.gd\n--- a/player.gd\n+++ b/player.gd\n@@ ",
		"\n-var a=1\n",
		"\n+var a = 1\n",
		"\nformat check failed (1 to reformat, 0 diagnostics)\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("diff output does not contain %q:\n%s", want, stdout.String())
		}
	}
	if got := readCLIFile(t, root, "player.gd"); got != unformattedScript {
		t.Fatalf("check --diff rewrote the file: %q", got)
	}
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
	if code := run([]string{"format", "check", "--format", "json", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty project exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "  \"results\": []") || !strings.Contains(stdout.String(), "  \"diagnostics\": []") {
		t.Fatalf("empty JSON should hold empty arrays: %s", stdout.String())
	}

	writeCLIFile(t, root, "broken.gd", unparseableScript)
	writeCLIFile(t, root, "clean.gd", formattedScript)
	writeCLIFile(t, root, "player.gd", unformattedScript)
	for _, command := range []string{"check", "write"} {
		stdout.Reset()
		stderr.Reset()
		if code := run([]string{"format", command, "--format", "json", root}, &stdout, &stderr); code != 1 {
			t.Fatalf("%s exit %d: %s", command, code, stderr.String())
		}
		var decoded report
		if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
			t.Fatalf("%s output is not JSON: %v\n%s", command, err, stdout.String())
		}
		if len(decoded.Results) != 2 || decoded.Results[0].Path != "clean.gd" || decoded.Results[0].Changed ||
			decoded.Results[1].Path != "player.gd" || !decoded.Results[1].Changed {
			t.Errorf("%s results = %+v", command, decoded.Results)
		}
		if len(decoded.Diagnostics) != 1 || decoded.Diagnostics[0].Rule != "source-parse" || decoded.Diagnostics[0].Path != "broken.gd" {
			t.Errorf("%s diagnostics = %+v", command, decoded.Diagnostics)
		}
		if command == "check" && readCLIFile(t, root, "player.gd") != unformattedScript {
			t.Fatal("check --format json rewrote the file")
		}
	}
	if readCLIFile(t, root, "player.gd") == unformattedScript {
		t.Fatal("write --format json did not rewrite the file")
	}
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
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stdout=%s stderr=%s)", name, code, stdout.String(), stderr.String())
		}
		if stderr.Len() == 0 {
			t.Errorf("%s: nothing was written to stderr", name)
		}
	}

	writeCLIFile(t, root, ".gdkit/format.json", "{not json")
	for _, command := range []string{"check", "write"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"format", command, root}, &stdout, &stderr); code != 2 {
			t.Errorf("%s with bad config: exit %d, want 2", command, code)
		}
		if !strings.HasPrefix(stderr.String(), "gdkit: ") {
			t.Errorf("%s with bad config: stderr = %q", command, stderr.String())
		}
	}
	if got := readCLIFile(t, root, "player.gd"); got != unformattedScript {
		t.Fatalf("a failed run rewrote the file: %q", got)
	}
}

func TestRunFormatHonoursConfig(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "func f():\n    pass\n")
	writeCLIFile(t, root, "style.json", `{"indent": "spaces"}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "check", "--config", "style.json", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"format", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("default style exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunFormatWriteThenCheckPasses(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "clean.gd", formattedScript)
	writeCLIFile(t, root, "player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("write exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	want := "reformatted player.gd\nformat write: 1 reformatted, 1 unchanged\n"
	if stdout.String() != want {
		t.Fatalf("output = %q, want %q", stdout.String(), want)
	}
	if got := readCLIFile(t, root, "player.gd"); got != "extends Node\nvar a = 1\n\n\nfunc f(x):\n\treturn x + a\n" {
		t.Fatalf("unexpected rewritten file: %q", got)
	}
	if got := readCLIFile(t, root, "clean.gd"); got != formattedScript {
		t.Fatalf("clean file changed: %q", got)
	}

	stdout.Reset()
	if code := run([]string{"format", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("check after write exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"format", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("second write exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "format write: 0 reformatted, 2 unchanged\n" {
		t.Fatalf("second write output: %q", stdout.String())
	}
}

func TestRunFormatWriteSkipsUnparseableFile(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "broken.gd", unparseableScript)
	writeCLIFile(t, root, "player.gd", unformattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.HasPrefix(output, "reformatted player.gd\nbroken.gd:1: Error: ") || !strings.Contains(output, "(source-parse)") {
		t.Fatalf("unexpected output: %s", output)
	}
	if !strings.HasSuffix(output, "format write: 1 reformatted, 0 unchanged, 1 skipped\n") {
		t.Fatalf("unexpected summary: %s", output)
	}
	if got := readCLIFile(t, root, "broken.gd"); got != unparseableScript {
		t.Fatalf("unparseable file was rewritten: %q", got)
	}
	if got := readCLIFile(t, root, "player.gd"); got == unformattedScript {
		t.Fatal("the formattable file was not rewritten")
	}
}

func TestRunFormatWriteReportsWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop the superuser")
	}
	root := t.TempDir()
	writeCLIFile(t, root, "a.gd", unformattedScript)
	writeCLIFile(t, root, "locked/b.gd", unformattedScript)
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "reformatted a.gd\n") {
		t.Errorf("the file written before the failure is not listed: %s", stdout.String())
	}
	if !strings.HasPrefix(stderr.String(), "gdkit: write locked/b.gd: ") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if got := readCLIFile(t, root, "locked/b.gd"); got != unformattedScript {
		t.Errorf("the file that failed to write changed: %q", got)
	}
}

func TestRunFormatInit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "init", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}
	if stdout.String() != "wrote .gdkit/format.json\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
	config, err := format.LoadConfig(root, "")
	if err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
	if config.LineWidth != format.DefaultConfig().LineWidth || config.Indent != "tabs" {
		t.Fatalf("written config is not the default: %+v", config)
	}
	name := filepath.Join(root, ".gdkit", "format.json")
	if data := readCLIFile(t, root, ".gdkit/format.json"); !strings.HasSuffix(data, "}\n") || !strings.Contains(data, "\n  \"line_width\": 100,\n") {
		t.Fatalf("unexpected config: %s", data)
	}

	if err := os.WriteFile(name, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := run([]string{"format", "init", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("clobber exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "already exists (use --force to replace it)") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got, _ := os.ReadFile(name); string(got) != "custom" {
		t.Fatalf("existing config was overwritten: %s", got)
	}
	if code := run([]string{"format", "init", "--force", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("force exit %d: %s", code, stderr.String())
	}
	if _, err := format.LoadConfig(root, ""); err != nil {
		t.Fatalf("--force did not write a loadable config: %v", err)
	}
}

func TestRunHelpMentionsFormatCommands(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"format", "help"}, {"format", "--help"}, {"format", "-h"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v exit %d: %s", args, code, stderr.String())
		}
		for _, command := range []string{"format check", "format write", "format init"} {
			if !strings.Contains(stdout.String(), command) {
				t.Errorf("%v output does not mention %q:\n%s", args, command, stdout.String())
			}
		}
	}
}

// decodeWritten returns the "written" member of a JSON report, and whether the
// report has one at all.
func decodeWritten(t *testing.T, output []byte) ([]string, bool) {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(output, &members); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, output)
	}
	for _, name := range []string{"results", "diagnostics"} {
		if _, ok := members[name]; !ok {
			t.Fatalf("JSON has no %q member: %s", name, output)
		}
	}
	raw, ok := members["written"]
	if !ok {
		return nil, false
	}
	var written []string
	if err := json.Unmarshal(raw, &written); err != nil || written == nil {
		t.Fatalf("written is not an array: %s", raw)
	}
	return written, true
}

func TestRunFormatWriteJSONListsWrittenPaths(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "clean.gd", formattedScript)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if written, ok := decodeWritten(t, stdout.Bytes()); !ok || len(written) != 0 {
		t.Fatalf("written = %v (present %v), want an empty array: %s", written, ok, stdout.String())
	}
	if !strings.Contains(stdout.String(), "  \"written\": []") {
		t.Fatalf("written should be an empty array: %s", stdout.String())
	}

	writeCLIFile(t, root, "broken.gd", unparseableScript)
	writeCLIFile(t, root, "b/player.gd", unformattedScript)
	writeCLIFile(t, root, "a/player.gd", unformattedScript)
	stdout.Reset()
	if code := run([]string{"format", "check", "--format", "json", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("check exit %d: %s", code, stderr.String())
	}
	if _, ok := decodeWritten(t, stdout.Bytes()); ok {
		t.Fatalf("check reported written files: %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("write exit %d: %s", code, stderr.String())
	}
	written, _ := decodeWritten(t, stdout.Bytes())
	if strings.Join(written, ",") != "a/player.gd,b/player.gd" {
		t.Fatalf("written = %v: %s", written, stdout.String())
	}
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
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if written, _ := decodeWritten(t, stdout.Bytes()); strings.Join(written, ",") != "a.gd" {
		t.Fatalf("written = %v, want only a.gd: %s", written, stdout.String())
	}
	// --format json was asked for, so the failure is an envelope on stderr
	// rather than prose. stdout still carries the report of what was written
	// before the failure, which is why this run is not covered by the
	// empty-stdout rule the other enveloped failures follow.
	body := decodeFailure(t, stderr.Bytes())
	if body.Kind != "file.write" {
		t.Errorf("kind = %q, want file.write", body.Kind)
	}
	if !strings.HasPrefix(body.Message, "write locked/b.gd: ") {
		t.Errorf("message = %q", body.Message)
	}
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
		if code := run([]string{"format", command, root}, &stdout, &stderr); code != 1 {
			t.Fatalf("%s exit %d: stdout=%s stderr=%s", command, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("%s output = %q, want it to contain %q", command, stdout.String(), want)
		}
		if got := readCLIFile(t, root, "text.gd"); got != unsafe {
			t.Fatalf("%s rewrote the refused file: %q", command, got)
		}
	}
	if got := readCLIFile(t, root, "player.gd"); got == unformattedScript {
		t.Fatal("write did not rewrite the safe file")
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", "--format", "json", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("json write exit %d: %s", code, stderr.String())
	}
	if written, _ := decodeWritten(t, stdout.Bytes()); len(written) != 0 {
		t.Fatalf("written = %v, want none", written)
	}
}

func TestRunFormatLeavesOneLineClassWithSeveralMembersAlone(t *testing.T) {
	const source = "class A: var v = 1; var u = 2\n"
	for _, command := range []string{"check", "write"} {
		root := t.TempDir()
		writeCLIFile(t, root, "inner.gd", source)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"format", command, root}, &stdout, &stderr); code != 1 {
			t.Fatalf("%s: exit %d: stdout=%s stderr=%s", command, code, stdout.String(), stderr.String())
		}
		output := stdout.String()
		if !strings.Contains(output, "inner.gd:1: Error: a one-line class body with several members is ambiguous") || !strings.Contains(output, "(format.unsafe)") {
			t.Fatalf("%s: refusal missing from output: %s", command, output)
		}
		if got := readCLIFile(t, root, "inner.gd"); got != source {
			t.Fatalf("%s rewrote the file: %q", command, got)
		}
	}
}
