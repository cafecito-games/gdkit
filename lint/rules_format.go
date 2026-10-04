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
	"github.com/cafecito-games/gdparser/lexer"
	"github.com/cafecito-games/gdparser/token"
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

// displayWidth is the width of text in runes, so that non-ASCII source is not
// penalized, counting each tab as the configured tab-characters width.
func displayWidth(text string, tabWidth int) int {
	return len([]rune(strings.ReplaceAll(text, "\t", strings.Repeat(" ", tabWidth))))
}

// indentWidth is the display width of a line's leading whitespace. A line that
// is nothing but whitespace has no indentation, because none of it is there to
// place anything: it is width a rewrite can simply drop.
func indentWidth(text string, tabWidth int) int {
	content := strings.TrimLeftFunc(text, unicode.IsSpace)
	if content == "" {
		return 0
	}
	return displayWidth(text[:len(text)-len(content)], tabWidth)
}

// lineByteRange is the half-open byte range of the one-based line number
// within the script's source, including the line terminator.
func lineByteRange(script *project.Script, number int) (start, end int) {
	if number < 1 || number > len(script.Lines) {
		return 0, 0
	}
	start = script.Lines[number-1]
	end = len(script.Source)
	if number < len(script.Lines) {
		end = script.Lines[number]
	}
	return start, end
}

// atomsOnLine maps a one-based line number to the display width of the widest
// run on that line that holds no place to break it. A run ends wherever a line
// break is already legal, which in GDScript is inside an unclosed "(", "[", or
// "{": a construct the code already brackets offers a break point, and the rule
// asks the reader to use it. Nowhere else does, because breaking a line that
// brackets nothing means introducing parentheses or a backslash, and a width
// limit is not a reason to add syntax. A long bracket-free expression is
// therefore not reported.
//
// A comment is a run of its own, measured by its longest whitespace-free word,
// because prose wraps and a trailing comment can move to the line above. A URL
// or a res:// path in a comment is one word.
//
// Widths come from each token's span rather than from its lexeme, because an
// operator token carries no lexeme, and because a string literal spanning
// several lines must contribute to each line only the part that lies on it.
func atomsOnLine(script *project.Script, tabWidth int) (map[int]int, error) {
	tokens, err := lexer.Lex(script.Source)
	if err != nil {
		return nil, err
	}
	widest := make(map[int]int)
	consider := func(line, from, to int) {
		if line < 1 || from >= to {
			return
		}
		text := strings.TrimRight(string(script.Source[from:to]), "\r\n")
		widest[line] = max(widest[line], displayWidth(text, tabWidth))
	}

	// The run in progress, as the line it lies on and the byte range it covers
	// so far. Line 0 means there is none.
	runLine, runFrom, runTo := 0, 0, 0
	flush := func() {
		consider(runLine, runFrom, runTo)
		runLine = 0
	}
	depth := 0
	for _, current := range tokens {
		switch current.Type {
		case token.Newline, token.Indent, token.Dedent, token.EOF:
			// Layout, not content: a rewrite is free to move all of it.
			flush()
			continue
		}
		last := current.Span.End.Line
		for line := current.Span.Start.Line; line <= last; line++ {
			start, end := lineByteRange(script, line)
			from, to := max(start, current.Span.Start.Offset), min(end, current.Span.End.Offset)
			if from >= to {
				continue
			}
			if current.Type == token.Comment {
				flush()
				text := strings.TrimRight(string(script.Source[from:to]), "\r\n")
				width := 0
				for _, word := range strings.FieldsFunc(text, unicode.IsSpace) {
					width = max(width, displayWidth(word, tabWidth))
				}
				widest[line] = max(widest[line], width)
				continue
			}
			if line != runLine {
				flush()
				runLine, runFrom = line, from
			}
			runTo = to
			if line != last {
				// The token runs on past this line, so nothing else can join
				// what it covers here.
				flush()
			}
		}
		switch current.Type {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth = max(depth-1, 0)
		}
		if depth > 0 {
			flush()
		}
	}
	flush()
	return widest, nil
}

// maxLineLengthRule reports lines longer than the configured limit, measured
// in runes so that non-ASCII source is not penalized. Each tab counts as the
// configured tab-characters width.
//
// A line is only reported when a shorter form of it exists. The narrowest a
// line can be rewritten to is its indentation plus the widest run on it that
// holds no place to break, so a line already over the limit by that measure is
// left alone: a reference to a long class name from generated code or an addon,
// a deep res:// path, or a URL in a documentation comment cannot be shortened,
// and reporting one only asks the reader to suppress it.
//
// Only breaking the line counts as shortening it. Binding a long name to a
// shorter local, extracting a function, and wrapping an expression in
// parentheses to gain a break point deliberately do not, because every line is
// reducible under those and the rule would say nothing at all.
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
	// Lexing cannot fail here, because the linter runs no rule over a script
	// that did not parse. If it ever did, no atom is irreducible and every
	// long line is reported, which is the plain width measurement.
	atoms, _ := atomsOnLine(script, tabWidth)
	var found []Diagnostic
	for number := 1; number <= script.LineCount(); number++ {
		text := script.Line(number)
		if displayWidth(text, tabWidth) <= limit {
			continue
		}
		if indentWidth(text, tabWidth)+atoms[number] > limit {
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
