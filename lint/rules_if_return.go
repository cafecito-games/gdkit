package lint

import (
	"github.com/cafecito-games/gdparser/ast"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(noElseReturnRule{})
	register(noElifReturnRule{})
}

// alwaysReturns mirrors gdlint's notion of a block that always returns: a
// "return" among its direct statements, an "if" with an "else" whose branches
// all always return, or a "match" with a wildcard case whose cases all always
// return. Only direct statements are examined, so a return buried in a loop
// does not count.
func alwaysReturns(source []byte, statements []ast.Statement) bool {
	for _, statement := range statements {
		if _, ok := statement.(*ast.ReturnStatement); ok {
			return true
		}
	}
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *ast.IfStatement:
			if ifAlwaysReturns(source, statement) {
				return true
			}
		case *ast.MatchStatement:
			if matchAlwaysReturns(source, statement) {
				return true
			}
		}
	}
	return false
}

func ifAlwaysReturns(source []byte, statement *ast.IfStatement) bool {
	if !hasElse(statement) || !branchesAlwaysReturn(source, statement) {
		return false
	}
	return alwaysReturns(source, statement.Else)
}

// hasElse reports whether the statement has an "else" branch. An empty body
// cannot occur in valid source, so the keyword span is what proves it.
func hasElse(statement *ast.IfStatement) bool {
	return statement.ElseKeywordSpan.End.Offset > statement.ElseKeywordSpan.Start.Offset || len(statement.Else) > 0
}

func branchesAlwaysReturn(source []byte, statement *ast.IfStatement) bool {
	for _, branch := range statement.Branches {
		if !alwaysReturns(source, branch.Body) {
			return false
		}
	}
	return true
}

func matchAlwaysReturns(source []byte, statement *ast.MatchStatement) bool {
	wildcard := false
	for _, matchCase := range statement.Cases {
		if len(matchCase.Patterns) != 1 {
			continue
		}
		if identifier, ok := matchCase.Patterns[0].(*ast.Identifier); ok && identifier.Name == "_" && !parenthesized(source, identifier) {
			wildcard = true
		}
	}
	if !wildcard {
		return false
	}
	for _, matchCase := range statement.Cases {
		if !alwaysReturns(source, matchCase.Body) {
			return false
		}
	}
	return true
}

// parenthesized reports whether an opening parenthesis directly precedes the
// identifier. The syntax tree drops parentheses, but gdlint's grammar does not
// treat "(_)" as a wildcard pattern.
func parenthesized(source []byte, identifier *ast.Identifier) bool {
	for offset := identifier.Span().Start.Offset - 1; offset >= 0; offset-- {
		switch source[offset] {
		case ' ', '\t', '\r', '\n':
			continue
		case '(':
			return true
		}
		return false
	}
	return false
}

// blockBodies calls visit with the statements of every block in the script,
// since gdlint scopes variable names to the block that holds them.
func blockBodies(file *ast.File, visit func([]ast.Statement)) {
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FunctionDeclaration:
			visit(node.Body)
		case *ast.LambdaExpression:
			visit(node.Body)
		case *ast.IfStatement:
			for _, branch := range node.Branches {
				visit(branch.Body)
			}
			visit(node.Else)
		case *ast.WhileStatement:
			visit(node.Body)
		case *ast.ForStatement:
			visit(node.Body)
		case *ast.MatchStatement:
			for _, matchCase := range node.Cases {
				visit(matchCase.Body)
			}
		case *ast.VariableDeclaration:
			visit(node.Getter)
			if node.Setter != nil {
				visit(node.Setter.Body)
			}
		}
		return true
	})
}

// declaredVariables names the "var" declarations directly inside a block.
// Constants are not included, matching gdlint.
func declaredVariables(statements []ast.Statement) []string {
	var names []string
	for _, statement := range statements {
		if declaration, ok := statement.(*ast.VariableDeclaration); ok && !declaration.Constant {
			names = append(names, declaration.Name)
		}
	}
	return names
}

// noElseReturnRule reports an "else" that follows branches which all return.
// It stays quiet when the "else" body declares a variable that the enclosing
// block also declares, because removing the "else" would make them collide.
type noElseReturnRule struct{}

func (noElseReturnRule) Name() string { return "no-else-return" }

func (noElseReturnRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	var found []Diagnostic
	blockBodies(script.File, func(statements []ast.Statement) {
		var enclosing map[string]bool
		for _, statement := range statements {
			ifStatement, ok := statement.(*ast.IfStatement)
			if !ok || !hasElse(ifStatement) || !branchesAlwaysReturn(script.Source, ifStatement) {
				continue
			}
			if enclosing == nil {
				enclosing = map[string]bool{}
				for _, name := range declaredVariables(statements) {
					enclosing[name] = true
				}
			}
			collides := false
			for _, name := range declaredVariables(ifStatement.Else) {
				if enclosing[name] {
					collides = true
				}
			}
			if !collides {
				found = append(found, spanDiagnostic(script, `Unnecessary "else" after "return"`, ifStatement.ElseKeywordSpan))
			}
		}
	})
	return found
}

// noElifReturnRule reports each "elif" that follows a branch which always
// returns, stopping at the first branch that does not. The last non-else
// branch is never examined because no "elif" follows it.
type noElifReturnRule struct{}

func (noElifReturnRule) Name() string { return "no-elif-return" }

func (noElifReturnRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	var found []Diagnostic
	ast.Inspect(script.File, func(node ast.Node) bool {
		ifStatement, ok := node.(*ast.IfStatement)
		if !ok {
			return true
		}
		for index := 0; index+1 < len(ifStatement.Branches); index++ {
			if !alwaysReturns(script.Source, ifStatement.Branches[index].Body) {
				break
			}
			found = append(found, spanDiagnostic(script, `Unnecessary "elif" after "return"`, ifStatement.Branches[index+1].KeywordSpan))
		}
		return true
	})
	return found
}
