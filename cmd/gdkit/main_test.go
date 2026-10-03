package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInitAndCleanCheck(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "init", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}
	for _, name := range []string{".gdkit/architecture.json", ".gdkit/allowlist.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("starter %s: %v", name, err)
		}
	}
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health\n")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("check exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "architecture check passed") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunCheckViolationExit(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health extends Node\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("check exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[engine.reference]") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

// A config a user cannot trust is worse than no config, so a typo'd key or a
// classification with no layer fails as a configuration error rather than
// silently changing what the analyzer enforces.
func TestRunArchCheckExitsTwoOnConfigErrors(t *testing.T) {
	cases := map[string]struct {
		config string
		want   string
	}{
		"unknown key in a classification": {
			config: `{"version": 1, "classifications": [{"pattern": "aaa/**", "layre": "presentation", "feature": "f"}]}`,
			want:   `unknown key "classifications[0].layre"`,
		},
		"unknown top-level key": {
			config: `{"version": 1, "dependancies": []}`,
			want:   `unknown key "dependancies"`,
		},
		"classification with no layer": {
			config: `{"version": 1, "classifications": [{"pattern": "aaa/**", "feature": "f"}]}`,
			want:   "requires a layer",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeCLIFile(t, root, ".gdkit/architecture.json", testCase.config)
			writeCLIFile(t, root, "aaa/thing.gd", "class_name AThing\n")
			var stdout, stderr bytes.Buffer
			if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 2 {
				t.Fatalf("check exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("stderr %q does not mention %q", stderr.String(), testCase.want)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version", "--format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit %d: %s", code, stderr.String())
	}
	for _, field := range []string{`"version"`, `"dirty"`, `"go_version"`} {
		if !strings.Contains(stdout.String(), field) {
			t.Errorf("version JSON does not contain %s: %s", field, stdout.String())
		}
	}

	stdout.Reset()
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--version exit %d: %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "gdkit version ") {
		t.Fatalf("unexpected version output: %s", stdout.String())
	}
}

func writeCLIFile(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunLintCleanAndFindings(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc do_thing() -> void:\n\tpass\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("clean exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "lint check passed") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}

	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("findings exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	want := `player.gd:4: Error: Function name "doThing" is not valid (function-name)`
	if !strings.Contains(stdout.String(), want) || !strings.Contains(stdout.String(), "lint check failed (1 diagnostics)") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunBareLintRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "(function-name)") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunLintJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", "--format", "json", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "\"diagnostics\": []") {
		t.Fatalf("clean JSON should hold an empty array: %s", stdout.String())
	}

	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	stdout.Reset()
	if code := run([]string{"lint", "check", "--format", "json", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "  \"diagnostics\": [\n") || !strings.Contains(stdout.String(), `"rule": "function-name"`) {
		t.Fatalf("unexpected JSON: %s", stdout.String())
	}
}

func TestRunLintUsageErrors(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	cases := map[string][]string{
		"unknown format":    {"lint", "check", "--format", "xml", root},
		"too many roots":    {"lint", "check", root, root},
		"unknown disable":   {"lint", "check", "--disable", "no-such-rule", root},
		"unknown enable":    {"lint", "check", "--enable", "no-such-rule", root},
		"missing config":    {"lint", "check", "--config", "missing.json", root},
		"unknown lint verb": {"lint", "bogus"},
		"init extra roots":  {"lint", "init", root, root},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stdout=%s stderr=%s)", name, code, stdout.String(), stderr.String())
		}
	}

	writeCLIFile(t, root, ".gdkit/lint.json", "{not json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 2 {
		t.Errorf("bad config: exit %d, want 2", code)
	}
}

func TestRunLintDisable(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", "--disable", "function-name", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "function-name") {
		t.Fatalf("rule was not disabled: %s", stdout.String())
	}
}

// --enable mirrors --disable. It names rules that ship inert, so enabling one
// that already runs is accepted and changes nothing.
func TestRunLintEnableAcceptsARunningRule(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", "--enable", "function-name", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "function-name") {
		t.Fatalf("enabling a running rule silenced it: %s", stdout.String())
	}
}

func TestRunLintWarningSeverityDoesNotFail(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	writeCLIFile(t, root, ".gdkit/lint.json", `{"severity": {"function-name": "warning"}}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `player.gd:4: Warning: Function name "doThing" is not valid (function-name)`) {
		t.Fatalf("warning missing from output: %s", stdout.String())
	}
}

func TestRunLintInit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "init", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}
	name := filepath.Join(root, ".gdkit", "lint.json")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"function-name\"") {
		t.Fatalf("unexpected config: %s", data)
	}
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("check with written config exit %d: %s", code, stderr.String())
	}

	if err := os.WriteFile(name, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := run([]string{"lint", "init", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("clobber exit %d, want 2", code)
	}
	if got, _ := os.ReadFile(name); string(got) != "custom" {
		t.Fatalf("existing config was overwritten: %s", got)
	}
	if code := run([]string{"lint", "init", "--force", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("force exit %d: %s", code, stderr.String())
	}
	if got, _ := os.ReadFile(name); string(got) == "custom" {
		t.Fatal("--force did not replace the config")
	}
}

func TestLintCheckWarningsPassButAreReported(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"version": 1, "severity": {"function-name": "warning"}}`
	if err := os.WriteFile(filepath.Join(root, ".gdkit", "lint.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.gd"), []byte("func doThing():\n\tpass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("a warning must not fail the run, got exit %d: %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Warning:") {
		t.Errorf("the warning should still be reported: %q", output)
	}
	if strings.Contains(output, "failed") {
		t.Errorf("the summary must not say the run failed: %q", output)
	}
	if !strings.Contains(output, "1 warning)") {
		t.Errorf("the summary should count one warning, singular: %q", output)
	}
}
