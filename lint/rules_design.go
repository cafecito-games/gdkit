package lint

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(maxReturnsRule{})
	register(maxPublicMethodsRule{})
	register(functionArgumentsNumberRule{})
}

// classFunctions lists the functions gdlint's design rules examine for a class
// body: the direct, non-static member functions. Static functions are never
// seen by gdlint's syntax tree, so they count toward no design limit.
func classFunctions(body []ast.Statement) []*ast.FunctionDeclaration {
	var functions []*ast.FunctionDeclaration
	for _, statement := range body {
		if function, ok := statement.(*ast.FunctionDeclaration); ok && !function.Static {
			functions = append(functions, function)
		}
	}
	return functions
}

// designFunctions calls visit for every examined function in the script,
// including those of inner classes at any depth.
func designFunctions(statements []ast.Statement, visit func(*ast.FunctionDeclaration)) {
	for _, function := range classFunctions(statements) {
		visit(function)
	}
	for _, statement := range statements {
		if class, ok := statement.(*ast.ClassDeclaration); ok {
			designFunctions(class.Body, visit)
		}
	}
}

// returnStatements collects the returns reachable through nested "if", "while",
// "for", and "match" blocks. Lambdas and property accessors are not entered,
// so their returns belong to no function's budget.
func returnStatements(statements []ast.Statement, found []*ast.ReturnStatement) []*ast.ReturnStatement {
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *ast.ReturnStatement:
			found = append(found, statement)
		case *ast.IfStatement:
			for _, branch := range statement.Branches {
				found = returnStatements(branch.Body, found)
			}
			found = returnStatements(statement.Else, found)
		case *ast.WhileStatement:
			found = returnStatements(statement.Body, found)
		case *ast.ForStatement:
			found = returnStatements(statement.Body, found)
		case *ast.MatchStatement:
			for _, matchCase := range statement.Cases {
				found = returnStatements(matchCase.Body, found)
			}
		}
	}
	return found
}

func startDiagnostic(script *project.Script, message string, start token.Position) Diagnostic {
	return Diagnostic{Message: message, Line: start.Line, Column: runeColumn(script, start)}
}

// maxReturnsRule reports a function with more returns than the limit, at its
// last return.
type maxReturnsRule struct{}

func (maxReturnsRule) Name() string { return "max-returns" }

func (maxReturnsRule) Check(context *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	limit := context.Config.MaxReturns
	var found []Diagnostic
	designFunctions(script.File.Statements, func(function *ast.FunctionDeclaration) {
		returns := returnStatements(function.Body, nil)
		if len(returns) > limit {
			message := fmt.Sprintf(`Function "%s" has more than %d return statements`, function.Name, limit)
			found = append(found, startDiagnostic(script, message, returns[len(returns)-1].Span().Start))
		}
	})
	return found
}

// maxPublicMethodsRule reports a class, the script itself included, with more
// public functions than the limit. A function is public unless its name starts
// with an underscore.
type maxPublicMethodsRule struct{}

func (maxPublicMethodsRule) Name() string { return "max-public-methods" }

func (maxPublicMethodsRule) Check(context *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	limit := context.Config.MaxPublicMethods
	var found []Diagnostic
	check := func(label string, start token.Position, body []ast.Statement) {
		public := 0
		for _, function := range classFunctions(body) {
			if !strings.HasPrefix(function.Name, "_") {
				public++
			}
		}
		if public > limit {
			message := fmt.Sprintf(`"%s" has more than %d public methods (functions)`, label, limit)
			found = append(found, startDiagnostic(script, message, start))
		}
	}
	var walk func(body []ast.Statement)
	walk = func(body []ast.Statement) {
		for _, statement := range body {
			if class, ok := statement.(*ast.ClassDeclaration); ok {
				check("Class "+class.Name, class.KeywordSpan.Start, class.Body)
				walk(class.Body)
			}
		}
	}
	check("Class global scope", token.Position{Line: 1, Column: 1}, script.File.Statements)
	walk(script.File.Statements)
	return found
}

// functionArgumentsNumberRule reports a function with more parameters than the
// limit, at its "func" keyword. A variadic parameter counts as one.
type functionArgumentsNumberRule struct{}

func (functionArgumentsNumberRule) Name() string { return "function-arguments-number" }

func (functionArgumentsNumberRule) Check(context *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	limit := context.Config.FunctionArgumentsNumber
	var found []Diagnostic
	designFunctions(script.File.Statements, func(function *ast.FunctionDeclaration) {
		if len(function.Parameters) > limit {
			message := fmt.Sprintf(`Function "%s" has more than %d arguments`, function.Name, limit)
			found = append(found, startDiagnostic(script, message, function.KeywordSpan.Start))
		}
	})
	return found
}
