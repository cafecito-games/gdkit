package lint

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/gdparser/ast"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(missingDocstringRule{})
}

// Documentation kinds missing-docstring understands. The names are part of the
// configuration contract.
const (
	docKindClass  = "class"
	docKindFunc   = "func"
	docKindSignal = "signal"
	docKindVar    = "var"
	docKindConst  = "const"
	docKindEnum   = "enum"
)

// knownDocKinds is every member kind missing-docstring accepts.
var knownDocKinds = map[string]bool{
	docKindClass: true, docKindFunc: true, docKindSignal: true,
	docKindVar: true, docKindConst: true, docKindEnum: true,
}

// missingDocstringRule reports a public member that carries no "##"
// documentation comment. It is inert until the configuration lists the member
// kinds to check, so a project opts in one kind at a time.
//
// A member is public unless its name begins with an underscore, matching
// max-public-methods. Unlike that rule, a static function is checked: Godot
// publishes it in the class reference like any other.
type missingDocstringRule struct{}

func (missingDocstringRule) Name() string { return "missing-docstring" }

func (missingDocstringRule) Check(context *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	kinds := make(map[string]bool, len(context.Config.MissingDocstring))
	for _, kind := range context.Config.MissingDocstring {
		kinds[kind] = true
	}
	if len(kinds) == 0 {
		return nil
	}

	var found []Diagnostic
	report := func(kind, label, name string, statement ast.Statement) {
		if !kinds[kind] || strings.HasPrefix(name, "_") {
			return
		}
		message := fmt.Sprintf("%s %q is missing a documentation comment", label, name)
		found = append(found, startDiagnostic(script, message, statementStart(statement)))
	}

	if kinds[docKindClass] {
		if directive, name := scriptClassName(script.File.Statements); name != "" && !scriptIsDocumented(script.File.Statements) {
			report(docKindClass, "Class", name, directive)
		}
	}

	var walk func(body []ast.Statement)
	walk = func(body []ast.Statement) {
		for index, statement := range body {
			if documentedAt(body, index) {
				continue
			}
			switch statement := statement.(type) {
			case *ast.ClassDeclaration:
				report(docKindClass, "Class", statement.Name, statement)
			case *ast.FunctionDeclaration:
				report(docKindFunc, "Function", statement.Name, statement)
			case *ast.SignalDeclaration:
				report(docKindSignal, "Signal", statement.Name, statement)
			case *ast.EnumDeclaration:
				// An anonymous enum names nothing the class reference can show.
				if statement.Name != "" {
					report(docKindEnum, "Enum", statement.Name, statement)
				}
			case *ast.VariableDeclaration:
				if statement.Constant {
					report(docKindConst, "Constant", statement.Name, statement)
				} else {
					report(docKindVar, "Variable", statement.Name, statement)
				}
			}
		}
		for _, statement := range body {
			// A private inner class publishes nothing, so neither it nor
			// anything it holds needs documentation.
			if class, ok := statement.(*ast.ClassDeclaration); ok && !strings.HasPrefix(class.Name, "_") {
				walk(class.Body)
			}
		}
	}
	walk(script.File.Statements)
	return found
}

// documentedAt reports whether the statement at index is preceded by a "##"
// comment. The comments immediately above a declaration form one block, so a
// plain "#" note written between the documentation and the declaration — an
// inline suppression, say — does not hide the documentation. A blank line
// anywhere in the run ends the block: Godot does not attach a comment to a
// declaration it is separated from.
func documentedAt(body []ast.Statement, index int) bool {
	for index > 0 {
		if trivia := ast.TriviaOf(body[index]); trivia == nil || trivia.BlankLinesBefore > 0 {
			return false
		}
		comment, ok := body[index-1].(*ast.Comment)
		if !ok {
			return false
		}
		if comment.Documentation {
			return true
		}
		index--
	}
	return false
}

// scriptClassName returns the "class_name" directive of a script and the name
// it declares. A script without a class name is not part of the class
// reference, so it needs no class documentation.
func scriptClassName(statements []ast.Statement) (*ast.Directive, string) {
	for _, statement := range statements {
		directive, ok := statement.(*ast.Directive)
		if !ok || directive.Name != "class_name" {
			continue
		}
		if identifier, ok := directive.Value.(*ast.Identifier); ok {
			return directive, identifier.Name
		}
	}
	return nil, ""
}

// scriptIsDocumented reports whether the script carries a class documentation
// comment. Godot reads it from the top of the file, ahead of every member but
// after "extends" and "class_name", so its position among the leading
// directives and annotations is not fixed.
func scriptIsDocumented(statements []ast.Statement) bool {
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *ast.Comment:
			if statement.Documentation {
				return true
			}
		case *ast.VariableDeclaration, *ast.FunctionDeclaration, *ast.SignalDeclaration,
			*ast.EnumDeclaration, *ast.ClassDeclaration:
			return false
		}
	}
	return false
}
