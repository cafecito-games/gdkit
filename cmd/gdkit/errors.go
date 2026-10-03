package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/cafecito-games/gdkit/internal/failure"
)

// exitUsage is the exit code for every configuration, usage, and I/O failure.
const exitUsage = 2

// failureEnvelope is the JSON form of a failure. The single "error" member
// keeps the envelope distinguishable from a report at a glance, and leaves room
// to add sibling members later without changing what a consumer already reads.
type failureEnvelope struct {
	Error failureBody `json:"error"`
}

// failureBody describes one failure. Kind is the public contract; Message is
// the same text the command writes in text mode, so the two never disagree.
type failureBody struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Key     string `json:"key,omitempty"`
}

// reportFailure writes err and returns the usage exit code. fallbackKind
// applies when the package that detected the failure did not label it, so
// every failure carries a kind even where labelling has not reached yet.
func reportFailure(stderr io.Writer, outputFormat, fallbackKind string, err error) int {
	body := failureBody{Kind: fallbackKind, Message: err.Error()}
	if labelled, ok := failure.Of(err); ok {
		body.Kind, body.Path, body.Key = labelled.Kind, labelled.Path, labelled.Key
	}
	return writeFailure(stderr, outputFormat, body)
}

// reportUsage writes a failure the command composed itself.
func reportUsage(stderr io.Writer, outputFormat, kind, message string) int {
	return writeFailure(stderr, outputFormat, failureBody{Kind: kind, Message: message})
}

// writeFailure puts the envelope on stderr, never on stdout: stdout carries the
// report, and a consumer that asked for JSON must be able to tell "no report"
// from "an empty report". Text mode keeps the "gdkit: " prefix every other
// message uses.
//
// A failure writing the envelope falls back to the text form rather than
// reporting a second, different failure about the first one.
func writeFailure(stderr io.Writer, outputFormat string, body failureBody) int {
	if outputFormat == formatJSON {
		encoder := json.NewEncoder(stderr)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(failureEnvelope{Error: body}); err == nil {
			return exitUsage
		}
	}
	fmt.Fprintln(stderr, "gdkit:", body.Message)
	return exitUsage
}

const (
	formatText = "text"
	formatJSON = "json"
)

// checkOutputFormat validates --format before anything else uses it, because
// the value decides how every later failure in the command is reported. It
// returns the empty string when the value is unknown, having reported it.
//
// An unknown value is reported as text: a consumer that misspelled the format
// cannot be assumed to parse the envelope it asked for by mistake.
func checkOutputFormat(value string, stderr io.Writer) (string, bool) {
	switch value {
	case formatText, formatJSON:
		return value, true
	}
	reportUsage(stderr, formatText, failure.UsageFormat,
		fmt.Sprintf("unknown output format %q (want text or json)", value))
	return "", false
}
