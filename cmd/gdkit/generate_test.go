package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/cafecito-games/gdkit/generate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const optedInScript = "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar q: int\nvar r: int\n"

func TestRunGenCheckThenWriteThenCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)

	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "would generate hex.gd")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"gen", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "generated hex.gd")
	written := readCLIFile(t, root, "hex.gd")
	assert.Contains(t, written, "func _to_string() -> String:")
	assert.Contains(t, written, "func equals(")

	stdout.Reset()
	code = run([]string{"gen", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "gen check passed")
}

func TestRunBareGenRunsCheck(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
}

func TestRunGenCheckCleanOnAProjectThatOptedIntoNothing(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "plain.gd", "class_name Plain\nextends RefCounted\n\nvar q: int\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
}

func TestRunGenCheckDiff(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "check", "--diff", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "+# gdkit:generated:begin")
}

func TestRunGenRejectsDiffWithJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "check", "--diff", "--format", "json", t.TempDir()}, &stdout, &stderr)
	assert.Equal(t, 2, code)
}

func TestRunGenJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	var report generate.Report
	err := json.Unmarshal(stdout.Bytes(), &report)
	require.NoError(t, err, "stdout is not a report")
	assert.True(t, report.HasChanges())
}

// An exit-2 failure under --format json is an envelope on stderr, with stdout
// empty so a consumer can tell "no report" from "an empty report".
func TestRunGenJSONFailureGoesToStderrWithEmptyStdout(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/generate.json", `{"not_a_key": 1}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.Empty(t, stdout.String())
	var envelope struct {
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	err := json.Unmarshal(stderr.Bytes(), &envelope)
	require.NoError(t, err, "stderr is not an envelope")
	assert.NotEmpty(t, envelope.Error.Kind)
}

// A refused class fails write; staleness that write fixed does not.
func TestRunGenWriteFailsOnlyOnARefusal(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd",
		"class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n"+
			"static func equals(a, b) -> bool:\n\treturn true\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "write", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "generate.conflict")
}

func TestRunGenWritePrune(t *testing.T) {
	root := t.TempDir()
	orphan := "class_name Hex\nextends RefCounted\n\nvar q: int\n\n\n" +
		"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Hex()\"\n\n\n# gdkit:generated:end\n"
	writeCLIFile(t, root, "hex.gd", orphan)

	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "write", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	assert.Equal(t, orphan, readCLIFile(t, root, "hex.gd"))

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"gen", "write", "--prune", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	got := readCLIFile(t, root, "hex.gd")
	assert.Equal(t, "class_name Hex\nextends RefCounted\n\nvar q: int\n", got)
}

func TestRunGenInit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "init", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), generate.DefaultConfigPath)
	var config generate.Config
	err := json.Unmarshal([]byte(readCLIFile(t, root, generate.DefaultConfigPath)), &config)
	require.NoError(t, err, "the written config is not valid JSON")
	assert.NoError(t, config.Validate())
	// A second init must not clobber without --force.
	code = run([]string{"gen", "init", root}, &stdout, &stderr)
	assert.NotEqual(t, 0, code)
}

func TestRunGenUnknownSubcommandFlagIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "check", "--nope"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
}

// The generated region must be canonical in the project's own style, so that
// gen write introduces no new format check finding.
func TestRunGenWriteLeavesFormatCheckClean(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "hex.gd", optedInScript)
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	stdout.Reset()
	code = run([]string{"format", "check", root}, &stdout, &stderr)
	assert.Equal(t, 0, code)
}
