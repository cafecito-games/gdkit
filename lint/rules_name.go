package lint

import (
	"fmt"
	"unicode/utf8"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/project"
)

// nameMessages maps each name rule to the text gdlint reports for it.
var nameMessages = map[string]string{
	"function-name":                  `Function name "%s" is not valid`,
	"class-name":                     `Class name "%s" is not valid`,
	"sub-class-name":                 `Class name "%s" is not valid`,
	"signal-name":                    `Signal name "%s" is not valid`,
	"class-variable-name":            `Class-scope variable name "%s" is not valid`,
	"class-load-variable-name":       `Class-scope load/preload variable name "%s" is not valid`,
	"function-variable-name":         `Function-scope variable name "%s" is not valid`,
	"function-preload-variable-name": `Function-scope preload variable name "%s" is not valid`,
	"function-argument-name":         `Function argument name "%s" is not valid`,
	"loop-variable-name":             `Loop variable name "%s" is not valid`,
	"enum-name":                      `Enum name "%s" is not valid`,
	"enum-element-name":              `Enum element name "%s" is not valid`,
	"constant-name":                  `Constant name "%s" is not valid`,
	"load-constant-name":             `Constant (load/preload) name "%s" is not valid`,
}

func init() {
	for rule := range nameMessages {
		register(nameRule{rule: rule})
	}
}

// nameRule checks every declared name that gdlint governs with one rule.
type nameRule struct{ rule string }

func (r nameRule) Name() string { return r.rule }

func (r nameRule) Check(context *Context, script *project.Script) []Diagnostic {
	pattern := context.Pattern(r.rule)
	var found []Diagnostic
	for _, declaration := range collectNames(script) {
		if declaration.rule != r.rule || pattern.MatchString(declaration.name) {
			continue
		}
		start, end := declaration.span.Start, declaration.span.End
		found = append(found, Diagnostic{
			Message:   fmt.Sprintf(nameMessages[r.rule], declaration.name),
			Line:      start.Line,
			Column:    runeColumn(script, start),
			EndLine:   end.Line,
			EndColumn: runeColumn(script, end),
		})
	}
	return found
}

// namedDeclaration is one declared identifier and the rule that governs it.
type namedDeclaration struct {
	rule string
	name string
	span token.Span
}

// runeColumn converts a parser position, whose column counts bytes, into the
// rune-based column diagnostics use.
func runeColumn(script *project.Script, position token.Position) int {
	if position.Line < 1 || position.Line > len(script.Lines) {
		return position.Column
	}
	lineStart := script.Lines[position.Line-1]
	if position.Offset < lineStart || position.Offset > len(script.Source) {
		return position.Column
	}
	return utf8.RuneCount(script.Source[lineStart:position.Offset]) + 1
}

type nameCollector struct {
	script *project.Script
	found  []namedDeclaration
}

// collectNames pairs every declared name with the rule that governs it. Class
// bodies and function bodies are walked separately because the same
// declaration keyword is governed by different rules in each. Parameters,
// enum members, and property setters are not ast.Nodes, so they are reached
// from their parent declaration rather than through ast.Inspect.
func collectNames(script *project.Script) []namedDeclaration {
	if script.File == nil {
		return nil
	}
	collector := &nameCollector{script: script}
	collector.classBody(script.File.Statements)
	return collector.found
}

func (c *nameCollector) add(rule, name string, span token.Span) {
	c.found = append(c.found, namedDeclaration{rule: rule, name: name, span: span})
}

func (c *nameCollector) classBody(statements []ast.Statement) {
	for _, statement := range statements {
		switch declaration := statement.(type) {
		case *ast.Directive:
			// gdlint only checks a class_name written on its own line; the
			// combined "class_name X extends Y" form is not checked.
			identifier, isIdentifier := declaration.Value.(*ast.Identifier)
			if declaration.Name == "class_name" && declaration.Extends == nil && isIdentifier {
				c.add("class-name", identifier.Name, identifier.Span())
			}
		case *ast.ClassDeclaration:
			c.add("sub-class-name", declaration.Name, declaration.NameSpan)
			c.classBody(declaration.Body)
		case *ast.FunctionDeclaration:
			// gdlint does not check the name of an abstract function.
			if !declaration.Abstract {
				c.add("function-name", declaration.Name, declaration.NameSpan)
			}
			c.parameters(declaration.Parameters)
			c.functionScope(declaration.Body)
		case *ast.SignalDeclaration:
			c.add("signal-name", declaration.Name, declaration.NameSpan)
		case *ast.EnumDeclaration:
			if declaration.Name != "" {
				c.add("enum-name", declaration.Name, declaration.NameSpan)
			}
			for _, member := range declaration.Members {
				c.add("enum-element-name", member.Name, member.NameSpan)
				c.inspect(member.Value)
			}
		case *ast.VariableDeclaration:
			c.classVariable(declaration)
		}
	}
}

func (c *nameCollector) classVariable(declaration *ast.VariableDeclaration) {
	loads, _ := c.loadKind(declaration)
	switch {
	case declaration.Constant && loads:
		c.add("load-constant-name", declaration.Name, declaration.NameSpan)
	case declaration.Constant:
		c.add("constant-name", declaration.Name, declaration.NameSpan)
	case loads:
		c.add("class-load-variable-name", declaration.Name, declaration.NameSpan)
	default:
		c.add("class-variable-name", declaration.Name, declaration.NameSpan)
	}
	c.inspect(declaration.Value)
	c.functionScope(declaration.Getter)
	if declaration.Setter != nil {
		c.functionScope(declaration.Setter.Body)
	}
}

func (c *nameCollector) parameters(parameters []ast.Parameter) {
	for _, parameter := range parameters {
		c.add("function-argument-name", parameter.Name, parameter.NameSpan)
		c.inspect(parameter.Default)
	}
}

func (c *nameCollector) functionScope(statements []ast.Statement) {
	for _, statement := range statements {
		c.inspect(statement)
	}
}

// inspect walks a subtree that executes inside a function: local variables,
// local constants, loop variables, and lambda parameters.
func (c *nameCollector) inspect(node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.VariableDeclaration:
			c.localVariable(declaration)
		case *ast.ForStatement:
			c.add("loop-variable-name", declaration.Variable, declaration.VariableSpan)
		case *ast.LambdaExpression:
			for _, parameter := range declaration.Parameters {
				c.add("function-argument-name", parameter.Name, parameter.NameSpan)
			}
		}
		return true
	})
}

// localVariable reproduces gdlint's selection: a local constant follows the
// same load-aware split as a class constant, but a local variable initialized
// with load() is governed by no rule at all, since function-variable-name
// excludes it and function-preload-variable-name accepts only preload().
func (c *nameCollector) localVariable(declaration *ast.VariableDeclaration) {
	loads, preloads := c.loadKind(declaration)
	switch {
	case declaration.Constant && loads:
		c.add("load-constant-name", declaration.Name, declaration.NameSpan)
	case declaration.Constant:
		c.add("constant-name", declaration.Name, declaration.NameSpan)
	case preloads:
		c.add("function-preload-variable-name", declaration.Name, declaration.NameSpan)
	case !loads:
		c.add("function-variable-name", declaration.Name, declaration.NameSpan)
	}
}

// loadKind reports whether the declaration's initializer is, exactly, a call
// to load() or preload(). gdlint does not look through a cast, an await, a
// ternary, a method call on the result, or parentheses.
func (c *nameCollector) loadKind(declaration *ast.VariableDeclaration) (loads, preloads bool) {
	call, ok := declaration.Value.(*ast.CallExpression)
	if !ok || c.isParenthesized(declaration, call) {
		return false, false
	}
	callee, ok := call.Callee.(*ast.Identifier)
	if !ok {
		return false, false
	}
	switch callee.Name {
	case "preload":
		return true, true
	case "load":
		return true, false
	}
	return false, false
}

// isParenthesized reports whether the initializer is wrapped in parentheses,
// which the parser drops from the tree but gdlint treats as a different
// expression shape.
func (c *nameCollector) isParenthesized(declaration *ast.VariableDeclaration, value ast.Expression) bool {
	from := declaration.OperatorSpan.End.Offset
	to := value.Span().Start.Offset
	source := c.script.Source
	if from <= 0 || to > len(source) || from > to {
		return false
	}
	for _, character := range source[from:to] {
		if character == '(' {
			return true
		}
	}
	return false
}
