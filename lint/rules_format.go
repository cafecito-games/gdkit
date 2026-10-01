package lint

// gdlint reports column 0 for every format problem. gdkit deliberately reports
// a real 1-based column instead: the text output never prints it, and editors
// and CI annotations consuming the JSON output benefit from it.

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(trailingWhitespaceRule{})
	register(maxFileLinesRule{})
	register(maxLineLengthRule{})
	register(mixedTabsAndSpacesRule{})
}

// isPythonLineBreak reports whether r is a character Python's str.splitlines
// treats as a line terminator. gdlint splits source that way, so these never
// appear at the end of a line and are not trailing whitespace for parity.
func isPythonLineBreak(r rune) bool {
	switch r {
	case '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}

func isTrailingSpace(r rune) bool { return unicode.IsSpace(r) && !isPythonLineBreak(r) }

// trailingWhitespaceRule reports any line ending in whitespace, as Unicode
// defines it, except the characters gdlint treats as line terminators.
type trailingWhitespaceRule struct{}

func (trailingWhitespaceRule) Name() string { return "trailing-whitespace" }

func (trailingWhitespaceRule) Check(context *Context, script *project.Script) []Diagnostic {
	var found []Diagnostic
	for number := 1; number <= script.LineCount(); number++ {
		text := script.Line(number)
		trimmed := strings.TrimRightFunc(text, isTrailingSpace)
		if len(trimmed) == len(text) {
			continue
		}
		found = append(found, Diagnostic{
			Message: "Trailing whitespace(s)",
			Line:    number,
			Column:  len([]rune(trimmed)) + 1,
		})
	}
	return found
}

// maxFileLinesRule reports a file longer than the configured limit, once, on
// the file's last line.
type maxFileLinesRule struct{}

func (maxFileLinesRule) Name() string { return "max-file-lines" }

func (maxFileLinesRule) Check(context *Context, script *project.Script) []Diagnostic {
	limit := context.Config.MaxFileLines
	if limit <= 0 || script.LineCount() <= limit {
		return nil
	}
	return []Diagnostic{{
		Message: fmt.Sprintf("Max allowed file lines num (%d) exceeded", limit),
		Line:    script.LineCount(),
		Column:  1,
	}}
}

// maxLineLengthRule reports lines longer than the configured limit, measured
// in runes so that non-ASCII source is not penalized. Each tab counts as the
// configured tab-characters width.
type maxLineLengthRule struct{}

func (maxLineLengthRule) Name() string { return "max-line-length" }

func (maxLineLengthRule) Check(context *Context, script *project.Script) []Diagnostic {
	limit := context.Config.MaxLineLength
	if limit <= 0 {
		return nil
	}
	tabWidth := context.Config.TabCharacters
	if tabWidth < 0 {
		tabWidth = 0
	}
	var found []Diagnostic
	for number := 1; number <= script.LineCount(); number++ {
		text := strings.ReplaceAll(script.Line(number), "\t", strings.Repeat(" ", tabWidth))
		length := len([]rune(text))
		if length <= limit {
			continue
		}
		found = append(found, Diagnostic{
			Message: fmt.Sprintf("Max allowed line length (%d) exceeded", limit),
			Line:    number,
			Column:  limit + 1,
		})
	}
	return found
}

// mixedIndentation matches a line whose own indentation mixes tabs and spaces.
var mixedIndentation = regexp.MustCompile(`^(\t+ +| +\t+)`)

// mixedTabsAndSpacesRule reports each line whose indentation mixes tabs and
// spaces.
type mixedTabsAndSpacesRule struct{}

func (mixedTabsAndSpacesRule) Name() string { return "mixed-tabs-and-spaces" }

func (mixedTabsAndSpacesRule) Check(context *Context, script *project.Script) []Diagnostic {
	var found []Diagnostic
	for number := 1; number <= script.LineCount(); number++ {
		if !mixedIndentation.MatchString(script.Line(number)) {
			continue
		}
		found = append(found, Diagnostic{
			Message: "Mixed tabs and spaces",
			Line:    number,
			Column:  1,
		})
	}
	return found
}
