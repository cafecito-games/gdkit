package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// decodeFailure reads the JSON error envelope a command wrote to stderr.
func decodeFailure(t *testing.T, data []byte) failureBody {
	t.Helper()
	var envelope failureEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("stderr is not an error envelope (%v): %s", err, data)
	}
	if envelope.Error.Kind == "" {
		t.Fatalf("envelope carries no kind: %s", data)
	}
	if envelope.Error.Message == "" {
		t.Fatalf("envelope carries no message: %s", data)
	}
	return envelope.Error
}

// runFailure runs args, requires exit 2, and requires stdout to be empty: a
// consumer that asked for JSON must be able to tell "no report" from "an empty
// report", so a failed run must not print one.
func runFailure(t *testing.T, args ...string) failureBody {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout is not empty on a failure: %s", stdout.String())
	}
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
			if body.Kind != test.kind {
				t.Errorf("kind = %q, want %q (message %q)", body.Kind, test.kind, body.Message)
			}
			if body.Key != test.key {
				t.Errorf("key = %q, want %q", body.Key, test.key)
			}
			// Every config failure names the file it is about.
			if !strings.Contains(body.Path, "architecture.json") {
				t.Errorf("path = %q, want the config file", body.Path)
			}
		})
	}
}

func TestJSONFailureKindForAnUnreadableConfig(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	body := runFailure(t, "arch", "check", "--format", "json", "--config", "missing.json", root)
	if body.Kind != "config.read" {
		t.Errorf("kind = %q, want config.read (message %q)", body.Kind, body.Message)
	}
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
			if body.Kind != "usage.version_floor" {
				t.Errorf("kind = %q, want usage.version_floor", body.Kind)
			}
			if !strings.Contains(body.Message, "99.0.0") {
				t.Errorf("message does not name the floor: %q", body.Message)
			}
		})
	}
}

func TestJSONFailureKindForTooManyRoots(t *testing.T) {
	root := t.TempDir()
	body := runFailure(t, "arch", "check", "--format", "json", root, root)
	if body.Kind != "usage.arguments" {
		t.Errorf("kind = %q, want usage.arguments", body.Kind)
	}
}

func TestVersionReportsArgumentFailuresAsJSON(t *testing.T) {
	body := runFailure(t, "version", "--format", "json", "extra")
	if body.Kind != "usage.arguments" {
		t.Errorf("kind = %q, want usage.arguments", body.Kind)
	}
}

// A misspelled format cannot be assumed to parse the envelope it asked for by
// mistake, so an unknown --format is reported as text.
func TestUnknownFormatIsReportedAsText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", "--format", "xml", t.TempDir()}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if got := stderr.String(); !strings.HasPrefix(got, "gdkit: unknown output format") {
		t.Errorf("stderr = %q, want prose", got)
	}
	if json.Valid(bytes.TrimSpace(stderr.Bytes())) {
		t.Error("an unknown format was reported as JSON")
	}
}

// An unknown --format is rejected before analysis runs, so a typo fails fast
// instead of after a whole project has been walked.
func TestUnknownFormatIsRejectedBeforeAnalysis(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "classifications": []}`)
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", "--format", "xml", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("analysis ran before the format was validated: %s", stdout.String())
	}
}

// The envelope's message is the text-mode message without the "gdkit: " prefix,
// so the two output modes can never describe a failure differently.
func TestJSONMessageMatchesTextOutput(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "unclassified": "maybe"}`)

	var textOut, textErr bytes.Buffer
	if code := run([]string{"arch", "check", root}, &textOut, &textErr); code != 2 {
		t.Fatalf("text exit %d, want 2", code)
	}
	body := runFailure(t, "arch", "check", "--format", "json", root)
	want := strings.TrimSuffix(strings.TrimPrefix(textErr.String(), "gdkit: "), "\n")
	if body.Message != want {
		t.Errorf("message = %q, text = %q", body.Message, want)
	}
}

// Text mode is unchanged: no envelope, same prefix as every other message.
func TestTextModeStillReportsProse(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1,`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.HasPrefix(stderr.String(), "gdkit: ") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if json.Valid(bytes.TrimSpace(stderr.Bytes())) {
		t.Error("text mode emitted JSON")
	}
}

// The init commands have no --format, so they are documented as prose-only.
func TestInitFailuresStayProse(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "init", root, root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if json.Valid(bytes.TrimSpace(stderr.Bytes())) {
		t.Error("arch init emitted JSON")
	}
}

// An unknown command is rejected before any --format is parsed, so it cannot be
// enveloped. The README says so rather than promising every exit-2 path.
func TestUnknownCommandStaysProse(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus", "--format", "json"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if json.Valid(bytes.TrimSpace(stderr.Bytes())) {
		t.Error("an unknown command emitted JSON")
	}
}
