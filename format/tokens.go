package format

import (
	"bytes"
	"unicode/utf8"

	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
	"github.com/cafecito-games/gdparser/lexer"
	"github.com/cafecito-games/gdparser/token"
)

// tokensChanged is reported when the formatted output does not hold the tokens
// of its source. It backs up the tree comparison, which can only see what the
// parser chose to record: a token whose choice the tree does not keep changes
// the meaning without changing the tree.
const tokensChanged = "formatting changed the token stream"

// significantToken is one token that says something, reduced to a key that is
// equal for two tokens exactly when options lets the formatter turn one into
// the other, and the place it was written.
type significantToken struct {
	key    string
	offset int
	line   int
}

// changedToken compares the tokens of a file before and after formatting. When
// the output adds, drops, or changes a token that says something, it returns
// the source position of the first token that differs, or the start of the
// file when the source has no such token.
//
// The comparison is of multisets, not of sequences. The formatter reorders
// tokens without changing what they say: it writes a property's getter before
// its setter whatever the source order, and it writes the comment that ended
// a one-line body after the header, ahead of that body. The tree comparison
// already holds every token the tree records in its place, so what is left
// for this check is that no token appears, vanishes, or turns into another,
// which counting decides.
func changedToken(source, formatted []byte, options gdformat.Options) (line, column int, changed bool) {
	before, err := significantTokens(source, options)
	if err != nil {
		return 1, 1, true
	}
	after, err := significantTokens(formatted, options)
	if err != nil {
		return 1, 1, true
	}
	differing := 0
	for differing < len(before) && differing < len(after) && before[differing].key == after[differing].key {
		differing++
	}
	if differing == len(before) && differing == len(after) {
		return 0, 0, false
	}
	if sameCounts(before, after) {
		return 0, 0, false
	}
	if differing == len(before) {
		return 1, 1, true
	}
	first := before[differing]
	return first.line, columnAt(source, first.offset), true
}

// sameCounts reports whether two token lists hold each key the same number of
// times.
func sameCounts(before, after []significantToken) bool {
	if len(before) != len(after) {
		return false
	}
	counts := make(map[string]int, len(before))
	for _, token := range before {
		counts[token.key]++
	}
	for _, token := range after {
		counts[token.key]--
		if counts[token.key] < 0 {
			return false
		}
	}
	return true
}

// columnAt returns the one-based column, counted in characters, of the byte at
// offset in source.
func columnAt(source []byte, offset int) int {
	lineStart := bytes.LastIndexByte(source[:offset], '\n') + 1
	return utf8.RuneCount(source[lineStart:offset]) + 1
}

// significantTokens lexes source and keeps the tokens that say something. It
// leaves out the tokens that only lay the source out, which are line breaks
// and indentation, and the ones the formatter adds and removes without
// changing what the source says:
//
//   - parentheses, which it drops where they are redundant and adds where it
//     breaks a line or an operand needs them;
//   - commas, which it adds and removes after the last item of a broken
//     construct;
//   - semicolons, which it replaces with line breaks.
//
// A line continuation backslash is not a token, so joining continued lines
// changes nothing here.
func significantTokens(source []byte, options gdformat.Options) ([]significantToken, error) {
	tokens, err := lexer.Lex(source)
	if err != nil {
		return nil, err
	}
	significant := make([]significantToken, 0, len(tokens))
	for index := 0; index < len(tokens); index++ {
		current := tokens[index]
		var key string
		switch current.Type {
		case token.Newline, token.Indent, token.Dedent, token.EOF,
			token.LParen, token.RParen, token.Comma, token.Semicolon:
			continue
		case token.Colon:
			key = string(token.Colon)
			// "var a: = 1" infers the type exactly as "var a := 1" does, and
			// the formatter writes the second for both.
			if index+1 < len(tokens) && tokens[index+1].Type == token.Assign {
				key = string(token.InferAssign)
				index++
			}
		case token.Comment:
			key = "comment\x00" + commentKey(current.Lexeme, options)
		case token.Integer, token.Float:
			key = string(current.Type) + "\x00" + current.Lexeme
			if options.Numbers == gdformat.NormalizeNumbers {
				key = string(current.Type) + "\x00" + normalizedNumber(current.Lexeme)
			}
		case token.String:
			key = "string\x00" + literalKey(stringLiteral(current.Lexeme), options)
		case token.Identifier:
			key = "identifier\x00" + current.Lexeme
		case token.And, token.Or, token.Not, token.Bang:
			key = canonicalOperator(current.Lexeme, options)
		default:
			key = string(current.Type)
		}
		significant = append(significant, significantToken{key: key, offset: current.Span.Start.Offset, line: current.Span.Start.Line})
	}
	return significant, nil
}

// stringLiteral describes a string token the way the parser describes the
// literal it makes of one, so that the two are compared by the same rule.
func stringLiteral(lexeme string) *ast.Literal {
	literal := &ast.Literal{Kind: ast.StringLiteral, Raw: lexeme}
	body := lexeme
	if len(body) > 0 && body[0] == 'r' {
		literal.RawPrefix = true
		body = body[1:]
	}
	if len(body) == 0 {
		return literal
	}
	literal.Quote = body[0]
	literal.Triple = len(body) >= 6 && body[1] == literal.Quote && body[2] == literal.Quote
	return literal
}
