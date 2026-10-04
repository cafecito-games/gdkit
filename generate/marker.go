package generate

import (
	"fmt"
	"regexp"
	"strings"
)

// The region sentinels. A region's content is owned by gen write, and the
// comment says so in the file itself, which is why no checksum is needed.
const (
	beginSentinel = "# gdkit:generated:begin"
	endSentinel   = "# gdkit:generated:end"
)

// The marker directives borrow the shape of the three suppression directives
// in internal/suppression — "#", gdkit or gdlint, ":", the directive word —
// but they are parsed here, and they are strict where that package is
// deliberately lax. gdlint lets a rule list run to the end of the line, which
// is why a trailing remark silently breaks "# gdkit:ignore"; a gdkit-original
// directive owes gdlint nothing, so a remark is an error.
//
// The class-level and field-level opt-outs are spelled differently on purpose.
// One spelling for both is ambiguous: a standalone comment directly above the
// first var of a class satisfies the definition of both at once, and resolving
// that by position would make inserting a field change the meaning of a
// comment nobody touched.
//
// The two ignore patterns are anchored, which is what keeps "ignore" from
// also matching "ignore-field".
var (
	generatePattern    = regexp.MustCompile(`#\s*(?:gdlint|gdkit)\s*:\s*generate\s*=\s*(.*)$`)
	ignorePattern      = regexp.MustCompile(`#\s*(?:gdlint|gdkit)\s*:\s*generate\s*:\s*ignore\s*$`)
	ignoreFieldPattern = regexp.MustCompile(`#\s*(?:gdlint|gdkit)\s*:\s*generate\s*:\s*ignore-field\s*$`)
)

// MatchGenerate parses an opt-in directive. It returns (nil, nil) when the
// line holds no such directive, and an error when it holds a malformed one, so
// a typo is reported as generate.marker rather than silently doing nothing.
func MatchGenerate(line string) ([]string, error) {
	match := generatePattern.FindStringSubmatch(line)
	if match == nil {
		return nil, nil
	}
	list := strings.TrimSpace(match[1])
	if list == "" {
		return nil, fmt.Errorf("gdkit:generate names no generator")
	}
	names := []string{}
	for _, item := range strings.Split(list, ",") {
		name := strings.TrimSpace(item)
		if name == "" {
			return nil, fmt.Errorf("gdkit:generate has an empty generator name")
		}
		if !isGeneratorName(name) {
			return nil, fmt.Errorf("unknown generator %q, want one of %s",
				name, strings.Join(GeneratorNames(), ", "))
		}
		names = append(names, name)
	}
	return names, nil
}

// MatchIgnore reports the class-level opt-out, which stands alone.
func MatchIgnore(line string) bool { return ignorePattern.MatchString(line) }

// MatchIgnoreField reports the field-level opt-out, which may stand alone
// above a var or trail it.
func MatchIgnoreField(line string) bool { return ignoreFieldPattern.MatchString(line) }

// standsAlone reports whether a line holds nothing but a comment, as opposed
// to a comment trailing code.
func standsAlone(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "#") }
