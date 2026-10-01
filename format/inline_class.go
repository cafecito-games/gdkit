package format

import (
	"bytes"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/lexer"
	"github.com/cafecito-games/gdparser/token"
)

// ambiguousClassBody is reported for a class whose body follows its header on
// one line and holds more than one member.
const ambiguousClassBody = "a one-line class body with several members is ambiguous; write the class as a block"

// ambiguousClass finds the first class in file whose one-line body the parser
// gave more than one member, and returns the position of its class keyword.
//
// Godot ends a one-line class body at its first member: in
// "class A: var v = 1; var u = 2" the class holds v, and u belongs to whatever
// encloses the class. The parser puts both in the class, and the formatter then
// writes them as a block, which moves u into the class for Godot too. The
// output parses back to the same tree and holds the same tokens, since
// semicolons are layout, so neither later check can see the move. A one-line
// body of another statement, such as "if x: a(); b()", holds every statement
// on the line for Godot and the parser alike, so only classes are looked at.
func ambiguousClass(file *ast.File, source []byte) (line, column int, found bool) {
	var lines lineBreaks
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		class, ok := node.(*ast.ClassDeclaration)
		if !ok {
			return true
		}
		first, count := members(class.Body)
		if count < 2 {
			return true
		}
		keyword, body := class.KeywordSpan.Start, first.Span().Start
		if lines.between(source, keyword, body) {
			return true
		}
		line, column, found = keyword.Line, columnAt(source, keyword.Offset), true
		return false
	})
	return line, column, found
}

// members returns the first statement of a class body that is not a comment,
// and how many such statements the body holds. The comment that ends a header
// line is recorded as the first statement of the body, so comments say nothing
// about how many members the parser read.
func members(body []ast.Statement) (first ast.Statement, count int) {
	for _, statement := range body {
		if _, isComment := statement.(*ast.Comment); isComment {
			continue
		}
		if count == 0 {
			first = statement
		}
		count++
	}
	return first, count
}

// lineBreaks answers whether a class header and its body are on separate
// logical lines. It lexes the source at most once, and only for a header that
// positions alone cannot decide.
type lineBreaks struct {
	lexed   bool
	offsets []int
}

// between reports whether a logical line ends between the class keyword and
// the first member of the body, which is what makes the body a block.
//
// Line numbers decide nearly every class: a member on the keyword's line is
// part of a one-line body, and a member on a later line starts a block unless
// a backslash continued the header onto that line. Only a backslash between
// the two leaves it open, and then the lexer decides, since it emits a line
// break for the end of a logical line and nothing for a continued one. The
// backslash may also be the last character of a comment, where it continues
// nothing.
func (l *lineBreaks) between(source []byte, keyword, body token.Position) bool {
	if body.Line == keyword.Line {
		return false
	}
	if bytes.IndexByte(source[keyword.Offset:body.Offset], '\\') < 0 {
		return true
	}
	if !l.lexed {
		l.lexed = true
		// The source parsed, so it lexes; with no tokens every class in
		// question is taken for a one-line body and refused.
		tokens, _ := lexer.Lex(source)
		for _, current := range tokens {
			if current.Type == token.Newline {
				l.offsets = append(l.offsets, current.Span.Start.Offset)
			}
		}
	}
	for _, offset := range l.offsets {
		if offset >= keyword.Offset && offset < body.Offset {
			return true
		}
	}
	return false
}
