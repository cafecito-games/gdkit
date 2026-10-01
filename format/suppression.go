package format

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cafecito-games/gdkit/internal/suppression"
)

// suppressionMoved is reported when the formatter would relocate a lint
// suppression comment. The syntax tree cannot show it: a comment trailing a
// block header and the same comment on the body's first line parse alike, yet
// only the first silences a diagnostic on the header.
const suppressionMoved = "formatting would move a lint suppression comment off the line it applies to"

// suppressionComment is one lint suppression directive and how it sits on its
// line, which is what decides the lines it applies to.
type suppressionComment struct {
	kind       suppression.Kind
	names      []string
	standalone bool
	line       int
	column     int
}

// samePlacement reports whether two comments are the same directive placed the
// same way. Positions are left out, since code may move as long as the comment
// moves with it.
func (c suppressionComment) samePlacement(other suppressionComment) bool {
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

// movedSuppression compares the suppression comments of a file before and
// after formatting. When a directive would change, vanish, or switch between
// trailing code and standing alone, it returns the source position of the
// first one affected.
func movedSuppression(source, formatted []byte) (line, column int, moved bool) {
	before, after := suppressionComments(source), suppressionComments(formatted)
	for index, comment := range before {
		if index >= len(after) || !comment.samePlacement(after[index]) {
			return comment.line, comment.column, true
		}
	}
	if len(after) > len(before) {
		return 1, 1, true
	}
	return 0, 0, false
}
