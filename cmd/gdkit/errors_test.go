package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeFailure reads the JSON error envelope a command wrote to stderr.
func decodeFailure(t *testing.T, data []byte) failureBody {
	t.Helper()
	var envelope failureEnvelope
	require.NoError(t, json.Unmarshal(data, &envelope))
	require.NotEmpty(t, envelope.Error.Kind)
	require.NotEmpty(t, envelope.Error.Message)
	return envelope.Error
}

// runFailure runs args, requires exit 2, and requires stdout to be empty: a
// consumer that asked for JSON must be able to tell "no report" from "an empty
// report", so a failed run must not print one.
func runFailure(t *testing.T, args ...string) failureBody {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Empty(t, stdout.String())
	return decodeFailure(t, stderr.Bytes())
}

func TestJSONFailureKindsForArchConfig(t *testing.T) {
	cases := map[string]struct {
		config string
		kind   string
		key    string
	}{
		"malformed json":   {`{"version": 1,`, "config.parse", ""},
		"trailing content": {`{"version": 1} nonsense`, "config.parse", ""},
		"unknown key": {
			`{"version": 1, "classifications": [{"pattern": "a/**", "layre": "domain", "feature": "f"}]}`,
			"config.unknown_key", "classifications[0].layre",
		},
		"invalid values": {`{"version": 1, "unclassified": "maybe"}`, "config.invalid", ""},
		"version floor":  {`{"version": 1, "minimum_gdkit_version": "99.0.0"}`, "config.version_floor", "minimum_gdkit_version"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeCLIFile(t, root, "player.gd", "extends Node\n")
			writeCLIFile(t, root, ".gdkit/architecture.json", test.config)
			body := runFailure(t, "arch", "check", "--format", "json", root)
			assert.Equal(t, test.kind, body.Kind)
			assert.Equal(t, test.key, body.Key)
			// Every config failure names the file it is about.
			assert.Contains(t, body.Path, "architecture.json")
		})
	}
}

func TestJSONFailureKindForAnUnreadableConfig(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	body := runFailure(t, "arch", "check", "--format", "json", "--config", "missing.json", root)
	assert.Equal(t, "config.read", body.Kind)
}

// Every command that offers --format json reports its failures the same way.
func TestJSONFailureEnvelopeOnEveryCommand(t *testing.T) {
	for _, args := range [][]string{
		{"arch", "check"},
		{"lint", "check"},
		{"format", "check"},
		{"format", "write"},
		{"uid", "check"},
		{"uid", "write"},
	} {
		name := strings.Join(args, " ")
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeCLIFile(t, root, "player.gd", "extends Node\n")
			command := append(append([]string{}, args...), "--format", "json", "--minimum-version", "99.0.0", root)
			body := runFailure(t, command...)
			assert.Equal(t, "usage.version_floor", body.Kind)
			assert.Contains(t, body.Message, "99.0.0")
		})
	}
}

func TestJSONFailureKindForTooManyRoots(t *testing.T) {
	root := t.TempDir()
	body := runFailure(t, "arch", "check", "--format", "json", root, root)
	assert.Equal(t, "usage.arguments", body.Kind)
}

func TestVersionReportsArgumentFailuresAsJSON(t *testing.T) {
	body := runFailure(t, "version", "--format", "json", "extra")
	assert.Equal(t, "usage.arguments", body.Kind)
}

// A misspelled format cannot be assumed to parse the envelope it asked for by
// mistake, so an unknown --format is reported as text.
func TestUnknownFormatIsReportedAsText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", "--format", "xml", t.TempDir()}, &stdout, &stderr)
	require.Equal(t, 2, code)
	got := stderr.String()
	assert.True(t, strings.HasPrefix(got, "gdkit: unknown output format"))
	assert.False(t, json.Valid(bytes.TrimSpace(stderr.Bytes())))
}

// An unknown --format is rejected before analysis runs, so a typo fails fast
// instead of after a whole project has been walked.
func TestUnknownFormatIsRejectedBeforeAnalysis(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "classifications": []}`)
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", "--format", "xml", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.Empty(t, stdout.String())
}

// The envelope's message is the text-mode message without the "gdkit: " prefix,
// so the two output modes can never describe a failure differently.
func TestJSONMessageMatchesTextOutput(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "unclassified": "maybe"}`)

	var textOut, textErr bytes.Buffer
	code := run([]string{"arch", "check", root}, &textOut, &textErr)
	require.Equal(t, 2, code)
	body := runFailure(t, "arch", "check", "--format", "json", root)
	want := strings.TrimSuffix(strings.TrimPrefix(textErr.String(), "gdkit: "), "\n")
	assert.Equal(t, want, body.Message)
}

// Text mode is unchanged: no envelope, same prefix as every other message.
func TestTextModeStillReportsProse(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1,`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.True(t, strings.HasPrefix(stderr.String(), "gdkit: "))
	assert.False(t, json.Valid(bytes.TrimSpace(stderr.Bytes())))
}

// The init commands have no --format, so they are documented as prose-only.
func TestInitFailuresStayProse(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "init", root, root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.False(t, json.Valid(bytes.TrimSpace(stderr.Bytes())))
}

// An unknown command is rejected before any --format is parsed, so it cannot be
// enveloped. The README says so rather than promising every exit-2 path.
func TestUnknownCommandStaysProse(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus", "--format", "json"}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.False(t, json.Valid(bytes.TrimSpace(stderr.Bytes())))
}
