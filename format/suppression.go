package format

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cafecito-games/gdkit/internal/suppression"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// suppressionMoved is reported when the formatter would change the code a lint
// suppression comment applies to. The syntax tree cannot show it: a directive
// reaches the lines around it, and the tree records what a file says, not
// which line says it.
const suppressionMoved = "formatting would change the code a lint suppression comment applies to"

// suppressionComment is one lint suppression directive: its kind, the rules it
// names, whether it stands alone on its line or trails code, and where it was
// written. The code it reaches is not kept here; movedSuppression looks that
// up from the position.
type suppressionComment struct {
	kind       suppression.Kind
	names      []string
	standalone bool
	line       int
	column     int
}

// sameDirective reports whether two comments are the same directive placed the
// same way. Positions are left out, since code may move as long as the comment
// moves with it.
func (c suppressionComment) sameDirective(other suppressionComment) bool {
	return c.kind == other.kind && c.standalone == other.standalone && slices.Equal(c.names, other.names)
}

// suppressionComments lists the suppression directives in source, in order.
// Lines are searched the way gdkit lint searches them, so the two agree on
// what is a directive.
func suppressionComments(source []byte) []suppressionComment {
	// Nearly every file has none, and the per-line search is the costly part.
	if !bytes.Contains(source, []byte("gdlint")) && !bytes.Contains(source, []byte("gdkit")) {
		return nil
	}
	var found []suppressionComment
	number := 0
	for line := range strings.SplitSeq(string(source), "\n") {
		number++
		line = strings.TrimSuffix(line, "\r")
		for _, kind := range suppression.Kinds {
			offset, names, ok := suppression.Match(kind, line)
			if !ok {
				continue
			}
			for index, name := range names {
				names[index] = quoteNeutral(name)
			}
			found = append(found, suppressionComment{
				kind:       kind,
				names:      names,
				standalone: suppression.StandsAlone(line),
				line:       number,
				column:     utf8.RuneCountInString(line[:offset]) + 1,
			})
		}
	}
	return found
}

// quoteNeutral spells a listed rule name without regard to quote characters.
// Lint searches raw lines, so a directive may sit inside a string literal,
// where the last name runs on through the closing quote. Requoting the literal
// then changes that name's spelling but not what it silences: no rule has a
// quote in its name, so the name matches no rule either way.
func quoteNeutral(name string) string {
	if !strings.ContainsAny(name, `'"`) {
		return name
	}
	return quoteNeutralizer.Replace(name)
}

var quoteNeutralizer = strings.NewReplacer(`\'`, `"`, `\"`, `"`, `'`, `"`)

// movedSuppression compares the suppression comments of a file before and
// after formatting, and returns the source position of the first directive
// whose reach would change.
//
// A directive reaches lines, not syntax: an ignore silences its own line and
// the line below it, and a disable or an enable takes effect at its own line
// or the one below. So a directive is kept only when it is the same directive,
// still trailing code or still standing alone, and the tokens on its line and
// on the line below it are the same before and after. That refuses some
// rewrites that would have been harmless, such as wrapping a long line below a
// disable, in exchange for a rule that needs no knowledge of where each lint
// rule reports.
func movedSuppression(source, formatted []byte, options gdformat.Options) (line, column int, moved bool) {
	before, after := suppressionComments(source), suppressionComments(formatted)
	if len(before) == 0 && len(after) == 0 {
		return 0, 0, false
	}
	linesBefore, linesAfter := tokenLines(source, options), tokenLines(formatted, options)
	for index, comment := range before {
		if index >= len(after) || !comment.sameDirective(after[index]) {
			return comment.line, comment.column, true
		}
		for _, offset := range []int{0, 1} {
			if !slices.Equal(linesBefore[comment.line+offset], linesAfter[after[index].line+offset]) {
				return comment.line, comment.column, true
			}
		}
	}
	if len(after) > len(before) {
		return 1, 1, true
	}
	return 0, 0, false
}

// tokenLines groups the keys of the significant tokens of source by the line
// each token starts on. A source that does not lex has no lines, which
// changedToken has refused before the suppression comments are looked at.
func tokenLines(source []byte, options gdformat.Options) map[int][]string {
	tokens, err := significantTokens(source, options)
	if err != nil {
		return nil
	}
	lines := map[int][]string{}
	for _, token := range tokens {
		lines[token.line] = append(lines[token.line], token.key)
	}
	return lines
}
