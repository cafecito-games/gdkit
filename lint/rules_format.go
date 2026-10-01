package lint

import (
	"strings"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(trailingWhitespaceRule{})
}

// trailingWhitespaceRule reports any line ending in a space or tab.
type trailingWhitespaceRule struct{}

func (trailingWhitespaceRule) Name() string { return "trailing-whitespace" }

func (trailingWhitespaceRule) Check(context *Context, script *project.Script) []Diagnostic {
	var found []Diagnostic
	for number := 1; number <= script.LineCount(); number++ {
		text := script.Line(number)
		trimmed := strings.TrimRight(text, " \t")
		if len(trimmed) == len(text) {
			continue
		}
		found = append(found, Diagnostic{
			Message: "Trailing whitespace",
			Line:    number,
			Column:  len([]rune(trimmed)) + 1,
		})
	}
	return found
}
