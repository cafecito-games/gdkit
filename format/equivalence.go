package format

import (
	"strings"
	"unicode/utf8"

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

// stringValueKey identifies a string literal by its prefix and value rather
// than by its spelling. It declines, leaving the caller to compare spellings,
// wherever the formatter keeps the literal as written: under PreserveQuotes
// and for a triple-quoted literal.
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
	value, ok := decodeString(body)
	if !ok {
		return "", false
	}
	return "value\x00" + prefix + "\x00" + value, true
}

// simpleEscapes maps the character after a backslash to the character the
// escape stands for, as Godot's tokenizer does.
var simpleEscapes = map[byte]byte{
	'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v',
	'\'': '\'', '"': '"', '\\': '\\',
}

// decodeString returns the value of the body of a string literal that is not
// raw. It reports false for a body Godot would give no single value: an
// unknown or cut-off escape, an unpaired surrogate, or a code point beyond
// Unicode.
func decodeString(body string) (string, bool) {
	if !strings.Contains(body, `\`) {
		return body, true
	}
	var value strings.Builder
	value.Grow(len(body))
	for index := 0; index < len(body); index++ {
		if body[index] != '\\' {
			value.WriteByte(body[index])
			continue
		}
		index++
		if index >= len(body) {
			return "", false
		}
		switch code := body[index]; code {
		case '\n':
			// A backslash before a line break joins the two lines.
		case '\r':
			if index+1 < len(body) && body[index+1] == '\n' {
				index++
			} else {
				value.WriteByte('\r')
			}
		case 'u', 'U':
			digits := 4
			if code == 'U' {
				digits = 6
			}
			point, ok := hexadecimal(body, index+1, digits)
			if !ok {
				return "", false
			}
			index += digits
			if code == 'u' && utf16HighSurrogate(point) {
				// Godot joins a "\u" pair of UTF-16 surrogates into the one
				// code point they encode.
				if index+2 >= len(body) || body[index+1] != '\\' || body[index+2] != 'u' {
					return "", false
				}
				low, ok := hexadecimal(body, index+3, 4)
				if !ok || !utf16LowSurrogate(low) {
					return "", false
				}
				index += 6
				point = 0x10000 + (point-0xd800)<<10 + (low - 0xdc00)
			}
			if !utf8.ValidRune(point) {
				return "", false
			}
			value.WriteRune(point)
		default:
			decoded, ok := simpleEscapes[code]
			if !ok {
				return "", false
			}
			value.WriteByte(decoded)
		}
	}
	return value.String(), true
}

func utf16HighSurrogate(point rune) bool { return point >= 0xd800 && point <= 0xdbff }
func utf16LowSurrogate(point rune) bool  { return point >= 0xdc00 && point <= 0xdfff }

// hexadecimal reads exactly count hexadecimal digits of text from start.
func hexadecimal(text string, start, count int) (rune, bool) {
	if start+count > len(text) {
		return 0, false
	}
	var value rune
	for _, digit := range []byte(text[start : start+count]) {
		switch {
		case digit >= '0' && digit <= '9':
			value = value<<4 | rune(digit-'0')
		case digit >= 'a' && digit <= 'f':
			value = value<<4 | rune(digit-'a'+10)
		case digit >= 'A' && digit <= 'F':
			value = value<<4 | rune(digit-'A'+10)
		default:
			return 0, false
		}
	}
	return value, true
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
// options lets the formatter turn one into the other. The formatter emits no
// line with trailing whitespace, whatever the options, so a comment's own
// trailing whitespace is not part of what must be kept.
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
