package format

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// verify reports whether formatted keeps the syntax tree of source. Both are
// parsed afresh and compared with positions and blank-line counts removed and
// with the spellings options asks the formatter to normalise brought to their
// canonical form. Any other difference means the formatter changed what the
// file says, so the caller must refuse the rewrite rather than write it.
func verify(path string, source, formatted []byte, options gdformat.Options) error {
	before, err := gdparser.ParseFile(path, source)
	if err != nil {
		return fmt.Errorf("source does not parse: %w", err)
	}
	after, err := gdparser.ParseFile(path, formatted)
	if err != nil {
		return fmt.Errorf("formatted output does not parse: %w", err)
	}
	canonicalize(before, options)
	canonicalize(after, options)
	if !reflect.DeepEqual(structure(ast.JSONValue(before), true), structure(ast.JSONValue(after), true)) {
		return errors.New("formatting changed the syntax tree")
	}
	return nil
}

// canonicalize rewrites, in place, the spellings the formatter is allowed to
// change under options: literal spelling, comment spacing, and the boolean
// operators. Comparing source spellings would report each of those as lost
// structure; comparing canonical spellings still fails when the literal,
// comment, or operator itself changes. The canonical spellings are computed
// here rather than by the formatter, whose output is what is being checked.
func canonicalize(file *ast.File, options gdformat.Options) {
	ast.Inspect(file, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.Literal:
			current.Raw = literalKey(current, options)
			// The quote metadata restates the spelling Raw now carries
			// canonically, so it would only repeat a permitted difference.
			current.Quote, current.Triple, current.RawPrefix = 0, false, false
		case *ast.Comment:
			current.Text = commentKey(current.Text, options)
		case *ast.BinaryExpression:
			current.Operator = canonicalOperator(current.Operator, options)
		case *ast.UnaryExpression:
			current.Operator = canonicalOperator(current.Operator, options)
		}
		return true
	})
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

// structure strips source metadata from a JSON tree: every span, the blank
// line counts, and the file name held at the root.
func structure(value any, root bool) any {
	switch current := value.(type) {
	case map[string]any:
		if root {
			delete(current, "name")
		}
		for key, child := range current {
			if key == "span" || strings.HasSuffix(key, "_span") || key == "blank_lines_before" {
				delete(current, key)
				continue
			}
			current[key] = structure(child, false)
		}
	case []any:
		for index, child := range current {
			current[index] = structure(child, false)
		}
	}
	return value
}
