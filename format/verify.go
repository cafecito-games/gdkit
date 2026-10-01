package format

import (
	"errors"
	"fmt"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// errChangedTree reports formatted output that does not say what its source
// said.
var errChangedTree = errors.New("formatting changed the syntax tree")

// verify reports whether formatted keeps the syntax tree of source. It parses
// source and defers to verifyTree.
func verify(path string, source, formatted []byte, options gdformat.Options) error {
	before, err := gdparser.ParseFile(path, source)
	if err != nil {
		return fmt.Errorf("source does not parse: %w", err)
	}
	return verifyTree(path, before, formatted, options)
}

// verifyTree reports whether formatted keeps the syntax tree before, which is
// left untouched. The output is parsed afresh and the two trees are compared
// with positions and blank-line counts left out and with the spellings options
// asks the formatter to normalise treated as equal. Any other difference means
// the formatter changed what the file says, so the caller must refuse the
// rewrite rather than write it.
func verifyTree(path string, before *ast.File, formatted []byte, options gdformat.Options) error {
	after, err := gdparser.ParseFile(path, formatted)
	if err != nil {
		return fmt.Errorf("formatted output does not parse: %w", err)
	}
	if !sameTree(before, after, options) {
		return errChangedTree
	}
	return nil
}

// canonicalOperator is the spelling WordOperators gives a boolean operator.
func canonicalOperator(operator string, options gdformat.Options) string {
	if options.Operators != gdformat.WordOperators {
		return operator
	}
	switch operator {
	case "&&":
		return "and"
	case "||":
		return "or"
	case "!":
		return "not"
	}
	return operator
}
