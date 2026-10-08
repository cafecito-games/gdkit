package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdkit/lint"
	"github.com/cafecito-games/gdkit/project"
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

func TestRunLintEnabledCollectionInfersAPopulatedLiteral(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "var items := [1, 2, 3]\n\nfunc run() -> void:\n\tvar local_items := [1, 2, 3]\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", "--enable", "require-typed-collection", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Array has no element type; write Array[T]") ||
		!strings.Contains(stdout.String(), "Array has no element type; write Array[int]") ||
		!strings.Contains(stdout.String(), "(require-typed-collection)") {
		t.Fatalf("unexpected output: %s", stdout.String())
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

// TestLintProjectConfigFollowsSemanticCapability pins the two load shapes lint
// chooses between. A semantic run must move its filters into Selection so the
// excluded scripts stay in the universe; a nonsemantic run must keep them on
// discovery so an excluded file is never read.
func TestLintProjectConfigFollowsSemanticCapability(t *testing.T) {
	config := lint.DefaultConfig()
	config.SourceRoots = []string{"src"}
	config.Exclude = []string{"generated/**"}

	filtered := lintProjectConfig("root", config, false)
	if filtered.Selection != nil {
		t.Fatalf("nonsemantic Selection = %+v, want nil", filtered.Selection)
	}
	if !slices.Equal(filtered.SourceRoots, config.SourceRoots) ||
		!slices.Equal(filtered.Exclude, config.Exclude) || !filtered.HonorIgnoreFile {
		t.Fatalf("nonsemantic config = %+v, want the lint filters on discovery", filtered)
	}

	broad := lintProjectConfig("root", config, true)
	if broad.SourceRoots != nil || broad.Exclude != nil {
		t.Fatalf("semantic discovery = %+v, want an unfiltered universe", broad)
	}
	if broad.Selection == nil {
		t.Fatal("semantic config carried no Selection")
	}
	if !slices.Equal(broad.Selection.SourceRoots, config.SourceRoots) ||
		!slices.Equal(broad.Selection.Exclude, config.Exclude) || !broad.Selection.HonorIgnoreFile {
		t.Fatalf("semantic Selection = %+v, want all three lint filters", broad.Selection)
	}
	if broad.Root != filtered.Root || broad.Identities != filtered.Identities {
		t.Fatalf("the two shapes disagree beyond the filters: %+v vs %+v", broad, filtered)
	}
	// A semantic run is the one read-only caller that needs a mounted addon in
	// its dependency universe. A nonsemantic run reads no excluded file at all
	// and must not start entering mounts to discover one.
	if !broad.FollowDirectorySymlinks {
		t.Error("a semantic run must follow a directory mount into its universe")
	}
	if filtered.FollowDirectorySymlinks {
		t.Error("a nonsemantic run must not follow a directory mount")
	}
}

// TestLintLoadsTheProjectExactlyOnce pins that the capability question costs no
// second walk or parse, in either mode.
func TestLintLoadsTheProjectExactlyOnce(t *testing.T) {
	for _, test := range []struct {
		name          string
		args          []string
		wantSelection bool
	}{
		{name: "nonsemantic", args: nil},
		{name: "semantic", args: []string{"--enable", "require-typed-collection"}, wantSelection: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeCLIFile(t, root, "a.gd", "var a := 1\n")
			var configs []project.Config
			original := lintProjectLoad
			lintProjectLoad = func(config project.Config) (*project.Snapshot, error) {
				configs = append(configs, config)
				return original(config)
			}
			defer func() { lintProjectLoad = original }()

			var stdout, stderr bytes.Buffer
			args := append(append([]string{"lint", "check"}, test.args...), root)
			if code := run(args, &stdout, &stderr); code != 0 {
				t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if len(configs) != 1 {
				t.Fatalf("project loads = %d, want 1", len(configs))
			}
			if (configs[0].Selection != nil) != test.wantSelection {
				t.Fatalf("Selection present = %t, want %t", configs[0].Selection != nil, test.wantSelection)
			}
		})
	}
}

// TestLintSemanticRunResolvesExcludedDependency is the end-to-end contract: an
// excluded generated script supplies the method signature and return type a
// selected file needs, and no diagnostic path names the excluded directory
// even though that file is now walked, parsed, and indexed.
func TestLintSemanticRunResolvesExcludedDependency(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/lint.json", `{"version": 1, "exclude": ["generated/**"]}`)
	writeCLIFile(t, root, "generated/proto.gd",
		"class_name Proto\nfunc label(value: int) -> String:\n\treturn \"x\"\n")
	writeCLIFile(t, root, "a.gd", "func run() -> void:\n\tvar calls := [Proto.new().label(1)]\n")

	report := runLintJSON(t, root, 1, "--enable", "require-typed-collection")
	if report.EngineSchema == nil {
		t.Fatal("semantic run published no engine provenance")
	}
	var messages []string
	for _, diagnostic := range report.Diagnostics {
		messages = append(messages, diagnostic.Path+": "+diagnostic.Message)
	}
	want := []string{"a.gd: Array has no element type; write Array[String]"}
	if !slices.Equal(messages, want) {
		t.Fatalf("diagnostics = %v, want %v", messages, want)
	}
}

// TestLintSemanticRunReportsNothingForExcludedDependencies covers the other
// half: an excluded script that would itself trip rules, carry an unknown
// suppression, and fail to parse produces no diagnostic of any kind once it is
// only a dependency.
func TestLintSemanticRunReportsNothingForExcludedDependencies(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/lint.json", `{"version": 1, "exclude": ["generated/**"]}`)
	writeCLIFile(t, root, ".gdkitignore", "hidden.gd\n")
	writeCLIFile(t, root, "generated/noisy.gd",
		"func label(value: int) -> String:\n\treturn \"x\"\n# gdkit:ignore = not-a-rule\n")
	writeCLIFile(t, root, "generated/broken.gd", "func (((\n")
	writeCLIFile(t, root, "hidden.gd", "func other(value: int) -> void:\n\tpass\n")
	writeCLIFile(t, root, "a.gd", "var a := 1\n")

	report := runLintJSON(t, root, 0, "--enable", "require-typed-collection")
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want none for excluded or ignored dependencies", report.Diagnostics)
	}
	// The same project linted without the filters must be noisy, or the test
	// above would pass on a project that had nothing to report.
	bare := t.TempDir()
	for _, name := range []string{"generated/noisy.gd", "generated/broken.gd", "hidden.gd", "a.gd"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		writeCLIFile(t, bare, name, string(data))
	}
	if noisy := runLintJSON(t, bare, 1, "--enable", "require-typed-collection"); len(noisy.Diagnostics) == 0 {
		t.Fatal("the fixture reports nothing even unfiltered, so the exclusion proves nothing")
	}
}

// runLintJSON runs lint check --format json and decodes the report, asserting
// the exit code so a silent failure cannot pass as an empty report.
func runLintJSON(t *testing.T, root string, wantCode int, extra ...string) lint.Report {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := append(append([]string{"lint", "check", "--format", "json"}, extra...), root)
	if code := run(args, &stdout, &stderr); code != wantCode {
		t.Fatalf("exit %d, want %d: stdout=%s stderr=%s", code, wantCode, stdout.String(), stderr.String())
	}
	var report lint.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, stdout.String())
	}
	return report
}

// TestLintSemanticRunFailsOnAnUnreadableExcludedDependency pins the documented
// consequence of loading a broad universe: a semantic run reads the scripts
// `exclude` keeps out of its findings, so one it cannot read is an exit-2
// project.load failure rather than a clean run. Failing closed is the contract
// — a dependency gdkit cannot read is not one it may guess about — and the
// nonsemantic run below shows the filtered walk still never opens it.
func TestLintSemanticRunFailsOnAnUnreadableExcludedDependency(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable directory anyway")
	}
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/lint.json", `{"version": 1, "exclude": ["generated/**"]}`)
	writeCLIFile(t, root, "generated/proto.gd", "class_name Proto\n")
	writeCLIFile(t, root, "a.gd", "var a := 1\n")
	sealed := filepath.Join(root, "generated")
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root},
		&stdout, &stderr); code != 2 {
		t.Fatalf("semantic exit %d, want 2: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty so a consumer can tell no report from an empty one", stdout.String())
	}
	var envelope struct {
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not a JSON envelope: %v (%s)", err, stderr.String())
	}
	if envelope.Error.Kind != "project.load" {
		t.Fatalf("failure kind = %q, want project.load (%s)", envelope.Error.Kind, stderr.String())
	}
	if !strings.Contains(envelope.Error.Message, "generated") {
		t.Fatalf("failure message = %q, want the unreadable dependency named", envelope.Error.Message)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("nonsemantic exit %d, want 0: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

// mountCLIAddon reproduces the layout Uzir shares a Godot addon with: an
// external directory, and a directory symlink inside the project naming it.
func mountCLIAddon(t *testing.T, root, logicalMount string, files map[string]string) string {
	t.Helper()
	external := t.TempDir()
	for name, contents := range files {
		writeCLIFile(t, external, name, contents)
	}
	absolute := filepath.Join(root, filepath.FromSlash(logicalMount))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, absolute); err != nil {
		t.Skipf("this platform cannot create a directory symlink: %v", err)
	}
	return external
}

// TestRunLintResolvesAClassMountedThroughADirectorySymlink is the capability's
// end-to-end acceptance case, over a synthetic fixture whose target resolves.
// The project excludes addons/** from lint findings and mounts the addon with a
// directory symlink, exactly as Uzir does, and a selected script constructs the
// mounted class.
//
// The observable difference is the fail-closed one the loader exists to remove:
// with the mount in the universe the analyzer resolves PackManifest under its
// logical identity and the rule reports; with the mount gone the constructor's
// type is a reasoned Unknown and the rule stays silent.
func TestRunLintResolvesAClassMountedThroughADirectorySymlink(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/lint.json", `{"exclude":["addons/**"]}`)
	writeCLIFile(t, root, "features/pack_view.gd",
		"func run() -> void:\n\tvar packs := [PackManifest.new()]\n\tprint(packs)\n")
	mountCLIAddon(t, root, "addons/worldmap_runtime", map[string]string{
		"pack_manifest.gd": "class_name PackManifest\nextends RefCounted\n",
		"plugin.cfg":       "[plugin]\nname=\"worldmap_runtime\"\n",
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit %d, want 1: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report lint.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, stdout.String())
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Path != "features/pack_view.gd" ||
		report.Diagnostics[0].Rule != "require-typed-collection" {
		t.Fatalf("diagnostics = %+v, want the selected consumer reported once", report.Diagnostics)
	}
	// The mount is a read-only dependency, so nothing inside it may be
	// reported on however broadly the universe was walked.
	for _, diagnostic := range report.Diagnostics {
		if strings.HasPrefix(diagnostic.Path, "addons/") {
			t.Errorf("an unselected mounted dependency was reported: %+v", diagnostic)
		}
	}

	// Removing the mount leaves the same selected script with an unresolvable
	// constructor, and the rule falls silent rather than guessing.
	if err := os.Remove(filepath.Join(root, "addons", "worldmap_runtime")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, want 0 once the mounted class is unresolvable: stdout=%s stderr=%s",
			code, stdout.String(), stderr.String())
	}
}

// TestRunLintFailsClosedOnAnUnresolvableMount pins the row the pinned Uzir
// corpus is real-producer evidence for: a semantic run over a project whose
// addon mount does not resolve is an exit-2 project.load failure naming the
// logical path, not a clean report over a universe missing the addon.
//
// The mount here sits under addons/, which the default lint config excludes
// from findings, and that is the point rather than an accident. A semantic run
// moves its filters to Selection and walks an unfiltered universe, so lint's
// exclude does not stop the walk entering a mount — it must not, because the
// corpus's one working-in-principle mount sits under exactly that exclusion.
// So an unresolvable link fails the run even where lint reports nothing, which
// is the fail-closed direction: a mount Godot would load but gdkit cannot read
// is not one it may guess about.
func TestRunLintFailsClosedOnAnUnresolvableMount(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "features/pack_view.gd", "func run() -> void:\n\tvar packs := []\n\tprint(packs)\n")
	if err := os.MkdirAll(filepath.Join(root, "addons"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../common/godot-addons/worldmap_runtime",
		filepath.Join(root, "addons", "worldmap_runtime")); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}

	// A nonsemantic run keeps its filters on discovery, never enters the mount,
	// and is unaffected.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("nonsemantic exit %d, want 0: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	body := runFailure(t, "lint", "check", "--format", "json", "--enable", "require-typed-collection", root)
	if body.Kind != "project.load" {
		t.Fatalf("failure kind = %q, want project.load (%+v)", body.Kind, body)
	}
	if !strings.Contains(body.Message, "addons/worldmap_runtime") {
		t.Fatalf("failure message = %q, want it to name the logical mount", body.Message)
	}

	// A cyclic mount under the same exclusion fails the same way, so neither
	// kind of unprovable link can be reached through a project's filters.
	if err := os.Remove(filepath.Join(root, "addons", "worldmap_runtime")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "addons", "worldmap_runtime")); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("nonsemantic exit %d, want 0: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	cyclic := runFailure(t, "lint", "check", "--format", "json", "--enable", "require-typed-collection", root)
	if cyclic.Kind != "project.load" || !strings.Contains(cyclic.Message, "addons/worldmap_runtime") ||
		!strings.Contains(cyclic.Message, "closes a directory cycle") {
		t.Fatalf("cyclic failure = %+v, want a project.load cycle error naming the mount", cyclic)
	}
}

// TestWriteCommandsLeaveAMountedExternalTreeUnchanged is the writer bound. No
// write-capable command opts into the capability, so a mounted external
// checkout is neither discovered nor touched: its file list, contents, modes,
// and modification times are identical afterwards.
func TestWriteCommandsLeaveAMountedExternalTreeUnchanged(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "features/pack_view.gd", "class_name PackView\n")
	// The mount deliberately does not sit under addons/, which every default
	// config already excludes: an excluded mount is pruned before the walk
	// would resolve it, so it proves nothing about the capability. This path is
	// one a writer's default filters admit, so the only thing keeping the
	// external tree intact is that no writer opts in.
	//
	// Every file in it is something a writer would change if it discovered it:
	// badly formatted source with no uid:// sidecar, and a class that opted
	// into generation but holds no generated region.
	external := mountCLIAddon(t, root, "features/shared_runtime", map[string]string{
		"pack_manifest.gd": "class_name PackManifest\nvar a    :=   1\n",
		"packed.gd":        "# gdkit:generate = to_string, equals\nclass_name Packed\nextends RefCounted\nvar b := 2\n",
	})
	before := snapshotTree(t, external)

	for _, command := range [][]string{
		{"format", "write", root},
		{"gen", "write", root},
		{"uid", "write", "--repair", root},
		{"uid", "check", root},
	} {
		var stdout, stderr bytes.Buffer
		run(command, &stdout, &stderr)
		if strings.Contains(stdout.String(), "features/shared_runtime") {
			t.Errorf("%v reported a path inside the mount: %s", command, stdout.String())
		}
		if after := snapshotTree(t, external); !maps.Equal(before, after) {
			t.Fatalf("%v changed the external tree:\nbefore %v\nafter  %v", command, before, after)
		}
	}
}

// snapshotTree records every file under root by relative path, with the
// evidence a write would disturb: content digest, mode, size, and modification
// time.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	if err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		digest := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			digest = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		tree[filepath.ToSlash(relative)] = fmt.Sprintf("%s mode=%v size=%d mtime=%s",
			digest, info.Mode(), info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return tree
}

// TestRunLintDoesNotReportInsideANestedMount is the end-to-end bound on the
// capability. Enabling one semantic rule puts a mounted addon in the
// dependency universe, which is the point — but it must not change what every
// *other* rule reports on. A project would otherwise see new findings in an
// external checkout it does not own from turning on an unrelated rule.
func TestRunLintDoesNotReportInsideANestedMount(t *testing.T) {
	root := t.TempDir()
	// Nothing excludes the mount, and the mounted script carries a finding any
	// default rule would report: the only thing keeping it quiet is that a
	// nonsemantic run could never have reached it.
	writeCLIFile(t, root, "features/pack_view.gd",
		"func run() -> void:\n\tvar packs := [PackManifest.new()]\n\tprint(packs)\n")
	mountCLIAddon(t, root, "features/shared_runtime", map[string]string{
		"pack_manifest.gd": "class_name PackManifest\nextends RefCounted\n\n\nfunc BadName() -> void:\n\tpass\n",
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"lint", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("nonsemantic exit %d, want 0: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("semantic exit %d, want 1: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report lint.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, stdout.String())
	}
	// The mounted class resolved, which is what the universe walk is for.
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Path != "features/pack_view.gd" ||
		report.Diagnostics[0].Rule != "require-typed-collection" {
		t.Fatalf("diagnostics = %+v, want only the selected consumer's finding", report.Diagnostics)
	}
	for _, diagnostic := range report.Diagnostics {
		if strings.HasPrefix(diagnostic.Path, "features/shared_runtime/") {
			t.Errorf("a rule reported inside the mount: %+v", diagnostic)
		}
	}
}
