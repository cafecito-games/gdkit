package lint

import (
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(inconsistentReturnRule{})
}

// inconsistentReturnRule reports a function that returns a value on one path
// and falls off its end on another, which Godot answers with an implicit null.
// Only a function that returns a value is examined: a function whose returns
// are all bare, or which never returns, is deliberately quiet, because there
// is no value for the missing path to be inconsistent with.
type inconsistentReturnRule struct{}

func (inconsistentReturnRule) Name() string { return "inconsistent-return-statements" }

// PendingSince keeps the rule inert until a project opts in, so upgrading
// gdkit cannot change an existing project's verdict.
func (inconsistentReturnRule) PendingSince() string { return "0.7.0" }

func (inconsistentReturnRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	var found []Diagnostic
	ast.Inspect(script.File, func(node ast.Node) bool {
		var body []ast.Statement
		var keyword token.Span
		switch node := node.(type) {
		case *ast.FunctionDeclaration:
			body, keyword = node.Body, node.KeywordSpan
		case *ast.LambdaExpression:
			body, keyword = node.Body, node.KeywordSpan
		default:
			return true
		}
		if returnsAValue(body) && !terminates(body) {
			found = append(found, spanDiagnostic(script, "Not all code paths return a value", keyword))
		}
		return true
	})
	return found
}

// returnsAValue reports whether the block returns a value anywhere, however
// deeply nested. It does not descend into a lambda, whose returns belong to
// the lambda and not to the function that holds it.
func returnsAValue(statements []ast.Statement) bool {
	for _, statement := range statements {
		if ret, ok := statement.(*ast.ReturnStatement); ok && ret.Value != nil {
			return true
		}
		if returnsAValue(nestedBlocks(statement)) {
			return true
		}
	}
	return false
}

// nestedBlocks flattens the blocks a statement holds. A variable's getter and
// setter are left out along with a lambda's body: each is its own function, so
// its returns are not the enclosing function's.
func nestedBlocks(statement ast.Statement) []ast.Statement {
	switch statement := statement.(type) {
	case *ast.IfStatement:
		var out []ast.Statement
		for _, branch := range statement.Branches {
			out = append(out, branch.Body...)
		}
		return append(out, statement.Else...)
	case *ast.WhileStatement:
		return statement.Body
	case *ast.ForStatement:
		return statement.Body
	case *ast.MatchStatement:
		var out []ast.Statement
		for _, matchCase := range statement.Cases {
			out = append(out, matchCase.Body...)
		}
		return out
	}
	return nil
}

// terminates is alwaysReturns widened with the one other way a block cannot
// fall through: an endless loop. It is kept separate rather than folded into
// alwaysReturns because no-else-return and no-elif-return ask a narrower
// question — whether removing an "else" is safe — and must keep reporting
// exactly what they report today.
func terminates(statements []ast.Statement) bool {
	if alwaysReturns(statements) {
		return true
	}
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *ast.WhileStatement:
			if endless(statement) {
				return true
			}
		case *ast.IfStatement:
			if hasElse(statement) && branchesTerminate(statement) && terminates(statement.Else) {
				return true
			}
		case *ast.MatchStatement:
			if matchTerminates(statement) {
				return true
			}
		}
	}
	return false
}

func branchesTerminate(statement *ast.IfStatement) bool {
	for _, branch := range statement.Branches {
		if !terminates(branch.Body) {
			return false
		}
	}
	return true
}

func matchTerminates(statement *ast.MatchStatement) bool {
	wildcard := false
	for _, matchCase := range statement.Cases {
		if len(matchCase.Patterns) != 1 {
			continue
		}
		if _, ok := matchCase.Patterns[0].(*ast.WildcardPattern); ok {
			wildcard = true
		}
	}
	if !wildcard {
		return false
	}
	for _, matchCase := range statement.Cases {
		if !terminates(matchCase.Body) {
			return false
		}
	}
	return true
}

// endless reports a "while true" that no "break" can leave, which makes the
// statements after it unreachable. The condition must be the literal, not an
// expression that evaluates to it, because the rule reads only the syntax.
func endless(statement *ast.WhileStatement) bool {
	literal, ok := statement.Condition.(*ast.Literal)
	if !ok || literal.Kind != ast.BoolLiteral || literal.Raw != "true" {
		return false
	}
	return !breaks(statement.Body)
}

// breaks reports a "break" that belongs to the loop whose body this is. A
// nested loop is not descended into, because a "break" inside it leaves that
// loop instead.
func breaks(statements []ast.Statement) bool {
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *ast.KeywordStatement:
			if statement.Keyword == "break" {
				return true
			}
		case *ast.IfStatement:
			for _, branch := range statement.Branches {
				if breaks(branch.Body) {
					return true
				}
			}
			if breaks(statement.Else) {
				return true
			}
		case *ast.MatchStatement:
			for _, matchCase := range statement.Cases {
				if breaks(matchCase.Body) {
					return true
				}
			}
		}
	}
	return false
}
