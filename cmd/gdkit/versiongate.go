package main

import (
	"fmt"
	"io"
	"os"

	"github.com/cafecito-games/gdkit/internal/buildinfo"
	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/versiongate"
)

// minimumVersionUsage documents the --minimum-version flag. Every command that
// offers the flag shares this text so the contract reads the same everywhere.
const minimumVersionUsage = "fail unless the running gdkit is this major.minor.patch release or newer"

// detectedVersion reports the running binary's version. It is a variable so a
// test can exercise the gate as an arbitrary release would.
var detectedVersion = func() string { return buildinfo.Current().Version }

// checkMinimumVersion reports whether the running binary satisfies the floor
// the caller asked for, writing a usage error when it does not. An empty floor
// imposes no requirement.
//
// Callers run this before resolving configuration or reading any source, so a
// binary that is too old to be trusted never emits a report. Failures are a
// configuration or usage error: exit 2, plain text on stderr.
func checkMinimumVersion(minimum, outputFormat string, stderr io.Writer) bool {
	if minimum == "" {
		return true
	}
	required, err := versiongate.ParseRequirement(minimum)
	if err != nil {
		reportUsage(stderr, outputFormat, failure.UsageVersionFloor,
			fmt.Sprintf("--minimum-version %s", err))
		return false
	}
	reported := detectedVersion()
	if versiongate.IsDevelopment(reported) {
		if os.Getenv(versiongate.AllowDevelopmentEnvironmentVariable) != "" {
			return true
		}
		reportUsage(stderr, outputFormat, failure.UsageVersionFloor,
			fmt.Sprintf("--minimum-version %s is not satisfied by a development build; set %s=1 to bypass",
				required, versiongate.AllowDevelopmentEnvironmentVariable))
		return false
	}
	current, err := versiongate.Parse(reported)
	if err != nil {
		reportUsage(stderr, outputFormat, failure.UsageVersionFloor,
			fmt.Sprintf("--minimum-version %s is not satisfied: this binary reports an unrecognized version %q", required, reported))
		return false
	}
	if current.Less(required) {
		reportUsage(stderr, outputFormat, failure.UsageVersionFloor,
			fmt.Sprintf("--minimum-version %s requires gdkit %s or newer, but this binary is %s", required, required, reported))
		return false
	}
	return true
}
