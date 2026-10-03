// Package versiongate compares gdkit release versions so a repository can
// refuse to run a binary it has not audited.
//
// Comparison is on the numeric major.minor.patch triple alone. Prerelease and
// build metadata are ignored, so a GoReleaser snapshot of 0.2.1 satisfies a
// floor of 0.2.1 rather than failing the way strict semver precedence would.
// gdkit has never published a prerelease; if it ever does, this is the one
// place that decision lives.
package versiongate

import (
	"fmt"
	"strings"
)

// AllowDevelopmentEnvironmentVariable names the escape hatch that lets a
// development build satisfy a minimum version. Without it, every contributor to
// a project that pins a floor would be unable to run any check from a local
// build, and a gate people route around is worse than no gate.
const AllowDevelopmentEnvironmentVariable = "GDKIT_ALLOW_DEV_VERSION"

// developmentVersion is what buildinfo reports when no release version was
// injected by the linker and none is recorded in the build settings.
const developmentVersion = "dev"

// Version is a major.minor.patch release version.
type Version struct {
	Major int
	Minor int
	Patch int
}

// Parse reads the major.minor.patch triple from a version a binary reports
// about itself. It accepts an optional leading "v" and ignores any prerelease
// or build metadata suffix.
func Parse(value string) (Version, error) {
	trimmed := strings.TrimPrefix(value, "v")
	if cut := strings.IndexAny(trimmed, "-+"); cut >= 0 {
		trimmed = trimmed[:cut]
	}
	return parseTriple(trimmed, value)
}

// ParseRequirement reads a minimum version a caller demands of the running
// binary. It is stricter than Parse: a floor is written by hand into a config
// file or a command line, where a leading "v" or a prerelease suffix is a
// mistake worth naming rather than silently accepting.
func ParseRequirement(value string) (Version, error) {
	return parseTriple(value, value)
}

// Less reports whether v precedes other.
func (v Version) Less(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

// String renders the version as a major.minor.patch triple.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// IsDevelopment reports whether a reported build version carries no release
// version, which is what a local `go build` or `go run` produces.
func IsDevelopment(value string) bool {
	return value == "" || value == developmentVersion
}

// parseTriple decodes exactly three dot-separated decimal components. The
// original input is reported in the error so a caller can echo what it read.
func parseTriple(value, original string) (Version, error) {
	components := strings.Split(value, ".")
	if len(components) != 3 {
		return Version{}, notATriple(original)
	}
	numbers := make([]int, len(components))
	for index, component := range components {
		number, ok := decimal(component)
		if !ok {
			return Version{}, notATriple(original)
		}
		numbers[index] = number
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

// decimal parses an unsigned decimal component. strconv.Atoi would accept a
// leading sign and surrounding space, neither of which belongs in a version.
func decimal(value string) (int, bool) {
	if value == "" || len(value) > 9 {
		return 0, false
	}
	number := 0
	for _, digit := range []byte(value) {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		number = number*10 + int(digit-'0')
	}
	return number, true
}

func notATriple(value string) error {
	return fmt.Errorf("%q is not a major.minor.patch version", value)
}
