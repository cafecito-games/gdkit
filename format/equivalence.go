package format

import (
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// The functions in this file decide which respellings keep a file's meaning.
// They restate the formatter's documented behaviour without calling it, so
// that verification checks the formatter rather than agreeing with it.

// literalKey returns a string that is equal for two literals of one kind
// exactly when options lets the formatter turn one into the other.
func literalKey(literal *ast.Literal, options gdformat.Options) string {
	switch literal.Kind {
	case ast.IntegerLiteral, ast.FloatLiteral:
		if options.Numbers == gdformat.NormalizeNumbers {
			return normalizedNumber(literal.Raw)
		}
	case ast.StringLiteral, ast.StringNameLiteral, ast.NodePathLiteral:
		if value, ok := stringValueKey(literal, options); ok {
			return value
		}
	}
	return exactKey(literal.Raw)
}

// exactKey marks a spelling that must be kept as written. The marker keeps it
// apart from every key built from a decoded value.
func exactKey(raw string) string { return "exact\x00" + raw }

// stringValueKey identifies a string literal by its prefix and its body
// spelled without regard to the delimiter. It declines, leaving the caller to
// compare spellings, wherever the formatter keeps the literal as written:
// under PreserveQuotes and for a triple-quoted literal.
func stringValueKey(literal *ast.Literal, options gdformat.Options) (string, bool) {
	if options.QuoteStyle == gdformat.PreserveQuotes || literal.Triple || literal.Quote == 0 {
		return "", false
	}
	open := strings.IndexByte(literal.Raw, literal.Quote)
	if open < 0 || len(literal.Raw) < open+2 || literal.Raw[len(literal.Raw)-1] != literal.Quote {
		return "", false
	}
	prefix, body := literal.Raw[:open], literal.Raw[open+1:len(literal.Raw)-1]
	if literal.RawPrefix {
		// A raw literal has no escapes: its body is its value, backslashes
		// included, whichever quote delimits it.
		return "raw\x00" + prefix + "\x00" + body, true
	}
	return "value\x00" + prefix + "\x00" + delimiterNeutralBody(body), true
}

// delimiterNeutralBody spells the body of a string literal that is not raw
// without regard to the quote that delimits it: an escaped quote becomes the
// quote itself, and every other escape is kept as written. Swapping the
// delimiter and the escapes it calls for is all the formatter does to a
// string, so nothing else may differ, not even between two spellings of one
// value such as "\u0041" and "A".
func delimiterNeutralBody(body string) string {
	if !strings.Contains(body, `\`) {
		return body
	}
	var neutral strings.Builder
	neutral.Grow(len(body))
	for index := 0; index < len(body); index++ {
		if body[index] != '\\' || index+1 >= len(body) {
			neutral.WriteByte(body[index])
			continue
		}
		index++
		if escaped := body[index]; escaped != '\'' && escaped != '"' {
			neutral.WriteByte('\\')
		}
		neutral.WriteByte(body[index])
	}
	return neutral.String()
}

// normalizedNumber is the spelling NormalizeNumbers gives a numeric literal:
// a lowercase radix prefix and lowercase hexadecimal digits, and a zero on
// either side of a decimal point that lacks one. Digit separators and the
// exponent are part of the spelling and are kept.
func normalizedNumber(raw string) string {
	if len(raw) >= 2 && raw[0] == '0' && strings.ContainsRune("xXbB", rune(raw[1])) {
		return strings.ToLower(raw)
	}
	mantissa, exponent := raw, ""
	if marker := strings.IndexAny(raw, "eE"); marker >= 0 {
		mantissa, exponent = raw[:marker], raw[marker:]
	}
	whole, fraction, hasPoint := strings.Cut(mantissa, ".")
	if !hasPoint {
		return raw
	}
	if whole == "" {
		whole = "0"
	}
	if fraction == "" {
		fraction = "0"
	}
	return whole + "." + fraction + exponent
}

// commentKey returns a string that is equal for two comments exactly when
// options lets the formatter turn one into the other. The formatter ends no
// line with whitespace, whatever the options, and a comment runs to the end of
// its line, so a comment's own trailing whitespace is not part of what must be
// kept.
func commentKey(text string, options gdformat.Options) string {
	text = strings.TrimRight(text, " \t")
	if options.CommentSpacing == gdformat.NormalizeComments {
		return normalizedComment(text)
	}
	return text
}

// normalizedComment is the text NormalizeComments gives a comment: one space
// after the "#" or "##" marker when the comment's body does not already start
// with one. A region marker is kept as written.
func normalizedComment(text string) string {
	body, found := strings.CutPrefix(text, "#")
	if !found {
		return text
	}
	marker := "#"
	if rest, documentation := strings.CutPrefix(body, "#"); documentation {
		marker, body = "##", rest
	}
	switch {
	case body == "", body[0] == ' ':
		return text
	case strings.HasPrefix(body, "region"), strings.HasPrefix(body, "endregion"):
		return text
	}
	return marker + " " + body
}
