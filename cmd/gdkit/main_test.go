package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdkit/lint"
	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunInitAndCleanCheck(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "init", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	for _, name := range []string{".gdkit/architecture.json", ".gdkit/allowlist.json"} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
		assert.NoError(t, err, name)
	}
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health\n")
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"arch", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "architecture check passed")
}

func TestRunCheckViolationExit(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health extends Node\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "[engine.reference]")
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
			code := run([]string{"arch", "check", root}, &stdout, &stderr)
			require.Equal(t, 2, code)
			require.Contains(t, stderr.String(), testCase.want)
		})
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"version", "--format", "json"}, &stdout, &stderr)
	require.Equal(t, 0, code)
	for _, field := range []string{`"version"`, `"dirty"`, `"go_version"`} {
		assert.Contains(t, stdout.String(), field)
	}

	stdout.Reset()
	code = run([]string{"--version"}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.True(t, strings.HasPrefix(stdout.String(), "gdkit version "))
}

func writeCLIFile(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(absolute), 0o755))
	require.NoError(t, os.WriteFile(absolute, []byte(contents), 0o644))
}

func TestRunLintCleanAndFindings(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc do_thing() -> void:\n\tpass\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "lint check passed")

	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	want := `player.gd:4: Error: Function name "doThing" is not valid (function-name)`
	require.Contains(t, stdout.String(), want)
	require.Contains(t, stdout.String(), "lint check failed (1 diagnostics)")
}

func TestRunBareLintRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint"}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "(function-name)")
}

func TestRunLintJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), "\"diagnostics\": []")

	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	stdout.Reset()
	code = run([]string{"lint", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "  \"diagnostics\": [\n")
	require.Contains(t, stdout.String(), `"rule": "function-name"`)
}

func TestRunLintRules(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "rules"}, &stdout, &stderr)
	require.Equal(t, 0, code)
	output := stdout.String()
	require.Contains(t, output, "function-name")
	require.Contains(t, output, "Checks a function's name")
	require.Contains(t, output, "unused-private-member")
	require.Contains(t, output, "[ships inert]")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"lint", "rules", "--format", "json"}, &stdout, &stderr)
	require.Equal(t, 0, code)
	var infos []lint.RuleInfo
	err := json.Unmarshal(stdout.Bytes(), &infos)
	require.NoError(t, err, "decode rules")
	require.NotEmpty(t, infos)
	found := false
	for _, info := range infos {
		if info.Name != "function-name" {
			continue
		}
		found = true
		assert.NotEmpty(t, info.Description)
		assert.False(t, info.Pending)
	}
	require.True(t, found)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"lint", "rules", "extra"}, &stdout, &stderr)
	require.Equal(t, 2, code)
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
		code := run(args, &stdout, &stderr)
		assert.Equal(t, 2, code, name)
	}

	writeCLIFile(t, root, ".gdkit/lint.json", "{not json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", root}, &stdout, &stderr)
	assert.Equal(t, 2, code)
}

func TestRunLintDisable(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--disable", "function-name", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.NotContains(t, stdout.String(), "function-name")
}

// --enable mirrors --disable. It names rules that ship inert, so enabling one
// that already runs is accepted and changes nothing.
func TestRunLintEnableAcceptsARunningRule(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--enable", "function-name", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "function-name")
}

func TestRunLintEnabledCollectionInfersAPopulatedLiteral(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "var items := [1, 2, 3]\n\nfunc run() -> void:\n\tvar local_items := [1, 2, 3]\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--enable", "require-typed-collection", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Empty(t, stderr.String())
	require.Contains(t, stdout.String(), "Array has no element type; write Array[T]")
	require.Contains(t, stdout.String(), "Array has no element type; write Array[int]")
	require.Contains(t, stdout.String(), "(require-typed-collection)")
}

func TestRunLintWarningSeverityDoesNotFail(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n\n\nfunc doThing() -> void:\n\tpass\n")
	writeCLIFile(t, root, ".gdkit/lint.json", `{"severity": {"function-name": "warning"}}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Contains(t, stdout.String(), `player.gd:4: Warning: Function name "doThing" is not valid (function-name)`)
}

func TestRunLintInit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "init", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	name := filepath.Join(root, ".gdkit", "lint.json")
	data, err := os.ReadFile(name)
	require.NoError(t, err)
	require.Contains(t, string(data), "\"function-name\"")
	code = run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)

	require.NoError(t, os.WriteFile(name, []byte("custom"), 0o644))
	stderr.Reset()
	code = run([]string{"lint", "init", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	got, _ := os.ReadFile(name)
	require.Equal(t, "custom", string(got))
	code = run([]string{"lint", "init", "--force", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	got, _ = os.ReadFile(name)
	require.NotEqual(t, "custom", string(got))
}

func TestLintCheckWarningsPassButAreReported(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gdkit"), 0o755))
	config := `{"version": 1, "severity": {"function-name": "warning"}}`
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gdkit", "lint.json"), []byte(config), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.gd"), []byte("func doThing():\n\tpass\n"), 0o644))

	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	output := stdout.String()
	assert.Contains(t, output, "Warning:")
	assert.NotContains(t, output, "failed")
	assert.Contains(t, output, "1 warning)")
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
	require.Nil(t, filtered.Selection)
	require.Equal(t, config.SourceRoots, filtered.SourceRoots)
	require.Equal(t, config.Exclude, filtered.Exclude)
	require.True(t, filtered.HonorIgnoreFile)

	broad := lintProjectConfig("root", config, true)
	require.Nil(t, broad.SourceRoots)
	require.Nil(t, broad.Exclude)
	require.NotNil(t, broad.Selection)
	require.Equal(t, config.SourceRoots, broad.Selection.SourceRoots)
	require.Equal(t, config.Exclude, broad.Selection.Exclude)
	require.True(t, broad.Selection.HonorIgnoreFile)
	require.Equal(t, filtered.Root, broad.Root)
	require.Equal(t, filtered.Identities, broad.Identities)
	// A semantic run is the one read-only caller that needs a mounted addon in
	// its dependency universe. A nonsemantic run reads no excluded file at all
	// and must not start entering mounts to discover one.
	assert.True(t, broad.FollowDirectorySymlinks)
	assert.False(t, filtered.FollowDirectorySymlinks)
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
			code := run(args, &stdout, &stderr)
			require.Equal(t, 0, code)
			require.Len(t, configs, 1)
			require.Equal(t, test.wantSelection, configs[0].Selection != nil)
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
	require.NotNil(t, report.EngineSchema)
	var messages []string
	for _, diagnostic := range report.Diagnostics {
		messages = append(messages, diagnostic.Path+": "+diagnostic.Message)
	}
	want := []string{"a.gd: Array has no element type; write Array[String]"}
	require.Equal(t, want, messages)
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
	require.Empty(t, report.Diagnostics)
	// The same project linted without the filters must be noisy, or the test
	// above would pass on a project that had nothing to report.
	bare := t.TempDir()
	for _, name := range []string{"generated/noisy.gd", "generated/broken.gd", "hidden.gd", "a.gd"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		require.NoError(t, err)
		writeCLIFile(t, bare, name, string(data))
	}
	noisy := runLintJSON(t, bare, 1, "--enable", "require-typed-collection")
	require.NotEmpty(t, noisy.Diagnostics)
}

// runLintJSON runs lint check --format json and decodes the report, asserting
// the exit code so a silent failure cannot pass as an empty report.
func runLintJSON(t *testing.T, root string, wantCode int, extra ...string) lint.Report {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := append(append([]string{"lint", "check", "--format", "json"}, extra...), root)
	code := run(args, &stdout, &stderr)
	require.Equal(t, wantCode, code)
	var report lint.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
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
	require.NoError(t, os.Chmod(sealed, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root},
		&stdout, &stderr)
	require.Equal(t, 2, code)
	require.Empty(t, stdout.String())
	var envelope struct {
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	err := json.Unmarshal(stderr.Bytes(), &envelope)
	require.NoError(t, err, "stderr is not a JSON envelope")
	require.Equal(t, "project.load", envelope.Error.Kind)
	require.Contains(t, envelope.Error.Message, "generated")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
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
	require.NoError(t, os.MkdirAll(filepath.Dir(absolute), 0o755))
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
// type is a reasoned Unknown and the rule degrades to the generic wording
// rather than proposing an exact spelling.
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
	require.Equal(t, 1, code)
	var report lint.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "features/pack_view.gd", report.Diagnostics[0].Path)
	require.Equal(t, "require-typed-collection", report.Diagnostics[0].Rule)
	// The mount is a read-only dependency, so nothing inside it may be
	// reported on however broadly the universe was walked.
	for _, diagnostic := range report.Diagnostics {
		assert.False(t, strings.HasPrefix(diagnostic.Path, "addons/"))
	}

	// Removing the mount leaves the same selected script with an unresolvable
	// constructor. The literal is still an untyped collection, so the rule
	// reports the generic wording instead of staying silent or guessing an
	// exact spelling the analyzer cannot reach.
	require.NoError(t, os.Remove(filepath.Join(root, "addons", "worldmap_runtime")))
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	err = json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "Array has no element type; write Array[T]", report.Diagnostics[0].Message)
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
	require.NoError(t, os.MkdirAll(filepath.Join(root, "addons"), 0o755))
	if err := os.Symlink("../../common/godot-addons/worldmap_runtime",
		filepath.Join(root, "addons", "worldmap_runtime")); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}

	// A nonsemantic run keeps its filters on discovery, never enters the mount,
	// and is unaffected.
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)

	body := runFailure(t, "lint", "check", "--format", "json", "--enable", "require-typed-collection", root)
	require.Equal(t, "project.load", body.Kind)
	require.Contains(t, body.Message, "addons/worldmap_runtime")

	// A cyclic mount under the same exclusion fails the same way, so neither
	// kind of unprovable link can be reached through a project's filters.
	require.NoError(t, os.Remove(filepath.Join(root, "addons", "worldmap_runtime")))
	if err := os.Symlink(root, filepath.Join(root, "addons", "worldmap_runtime")); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	cyclic := runFailure(t, "lint", "check", "--format", "json", "--enable", "require-typed-collection", root)
	require.Equal(t, "project.load", cyclic.Kind)
	require.Contains(t, cyclic.Message, "addons/worldmap_runtime")
	require.Contains(t, cyclic.Message, "closes a directory cycle")
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
		assert.NotContains(t, stdout.String(), "features/shared_runtime", strings.Join(command, " "))
		after := snapshotTree(t, external)
		require.Equal(t, before, after, strings.Join(command, " "))
	}
}

// snapshotTree records every file under root by relative path, with the
// evidence a write would disturb: content digest, mode, size, and modification
// time.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
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
	})
	require.NoError(t, err)
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
	code := run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"lint", "check", "--format", "json", "--enable", "require-typed-collection", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	var report lint.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "decode report")
	// The mounted class resolved, which is what the universe walk is for.
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "features/pack_view.gd", report.Diagnostics[0].Path)
	require.Equal(t, "require-typed-collection", report.Diagnostics[0].Rule)
	for _, diagnostic := range report.Diagnostics {
		assert.False(t, strings.HasPrefix(diagnostic.Path, "features/shared_runtime/"))
	}
}
