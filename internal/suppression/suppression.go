// Package suppression recognises the lint suppression comments of a source
// line. It holds the directive grammar once, for the linter that obeys the
// comments and the formatter that must not move them.
package suppression

import (
	"regexp"
	"strings"
)

// Kind is one of the three suppression directives.
type Kind int

const (
	// Ignore silences rules on the comment's own line and the line below it.
	Ignore Kind = iota
	// Disable silences rules from the comment to the matching enable.
	Disable
	// Enable ends a disable.
	Enable
)

// Kinds lists every directive, in the order a line is searched for them.
var Kinds = [...]Kind{Ignore, Disable, Enable}

// Suppression comments follow gdlint exactly. Three directives name rules in a
// comma-separated list:
//
//	# gdlint:ignore  = a, b
//	# gdlint:disable = a, b
//	# gdlint:enable  = a, b
//
// gdkit accepts "gdkit" in place of "gdlint". Like gdlint, the directive is
// found by searching raw lines, so it also matches inside a string literal, and
// the list runs to the end of the line, which means a trailing remark becomes
// part of the last rule name.
var patterns = [...]*regexp.Regexp{
	Ignore:  directivePattern("ignore"),
	Disable: directivePattern("disable"),
	Enable:  directivePattern("enable"),
}

func directivePattern(directive string) *regexp.Regexp {
	return regexp.MustCompile(`#\s*(?:gdlint|gdkit)\s*:\s*` + directive + `\s*=\s*([^,]+(?:,[^,]+)*)`)
}

// Match finds the first directive of the given kind on one source line. It
// returns the byte offset of the comment's "#" and the rule names listed, each
// trimmed of surrounding space.
func Match(kind Kind, line string) (offset int, names []string, ok bool) {
	match := patterns[kind].FindStringSubmatchIndex(line)
	if match == nil {
		return 0, nil, false
	}
	for name := range strings.SplitSeq(line[match[2]:match[3]], ",") {
		names = append(names, strings.TrimSpace(name))
	}
	return match[0], names, true
}

// StandsAlone reports whether a line holds nothing but a comment, as opposed
// to a comment trailing code.
func StandsAlone(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}
