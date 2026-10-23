package lint

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(unusedPrivateMemberRule{})
}

// unusedPrivateMemberRule reports a private class member — a function or
// constant whose name begins with an underscore — that is never referenced
// anywhere in its file.
//
// The rule covers exactly what Godot's own GDScript analyzer leaves unreported.
// Godot warns on an unused private class *variable* (unused_private_class_variable)
// and an unused *signal* (unused_signal), but it has no warning for an unused
// private *function*, and unused_local_constant covers a constant only inside a
// function body, never one at class scope. Those two gaps are this rule.
//
// A member is "used" when its name appears anywhere else in the file, as a
// reference (a bare identifier, a member access, a bare node path, a property
// accessor shorthand) or spelled inside any string-like literal. The string
// test is what makes the rule sound for GDScript's string dispatch: call(),
// connect(), get(), set(), and get_node() all reach a member by name, so a name
// spelled in a string counts as a reference even though no identifier names it.
// The test is deliberately generous — a name that merely appears inside a
// longer string or an unrelated message also counts — because a private member
// wrongly kept is a far smaller harm than one wrongly reported.
//
// Two functions are never reported, because something outside the file can
// reach them and the rule reads one file only: one whose name is an engine
// virtual, which the engine calls with no reference in source, and one named
// like an editor signal handler, "_on_...", which a scene connects by string.
//
// The virtual exemption resolves the script's base class up its native chain
// against the engine schema rather than a hand-maintained list, so it is
// complete for the bundled engine version and stops exempting a name on a class
// that does not declare it — "_toggled" on a RefCounted is dead code, but on a
// Button subclass it is a real override. Object's core callbacks — _init,
// _notification, _to_string, and the property accessors — are absent from
// Godot's extension API dump, so they stay a small fixed set. The "_on_"
// exemption is a different mechanism (a scene connects by string, which no
// schema knows) and stays as it is.
//
// The rule ships inert, as a new rule must, so an upgrade cannot change what an
// existing project reports on unchanged configuration. It is also explicitly
// single-file: a reference to a private member from another script — including a
// call("_name") written there — is invisible to it, exactly as it is to every
// other single-file rule.
type unusedPrivateMemberRule struct{}

func (unusedPrivateMemberRule) Name() string { return "unused-private-member" }

func (unusedPrivateMemberRule) PendingSince() string { return "0.8.0" }

func (unusedPrivateMemberRule) RequiresEngineSchema() {}

func (unusedPrivateMemberRule) Check(context *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	engine := context.Engine()
	used := referenceNames(script)
	strings_ := stringLiteralContents(script)

	var found []Diagnostic
	report := func(kind, name string, span token.Span) {
		if used[name] || appearsIn(strings_, name) {
			return
		}
		found = append(found, spanDiagnostic(script, fmt.Sprintf("unused private %s %q", kind, name), span))
	}

	var walk func(body []ast.Statement, base semantic.Type)
	walk = func(body []ast.Statement, base semantic.Type) {
		for _, statement := range body {
			switch statement := statement.(type) {
			case *ast.FunctionDeclaration:
				if !strings.HasPrefix(statement.Name, "_") {
					continue
				}
				if isEngineCalled(engine, base, statement.Name) {
					continue
				}
				report("function", statement.Name, statement.NameSpan)
			case *ast.VariableDeclaration:
				if statement.Constant && strings.HasPrefix(statement.Name, "_") {
					report("constant", statement.Name, statement.NameSpan)
				}
			}
		}
		for _, statement := range body {
			if class, ok := statement.(*ast.ClassDeclaration); ok {
				walk(class.Body, nativeBaseType(engine, class.Extends))
			}
		}
	}
	name, native := scriptBaseName(script)
	base := semantic.Type{}
	if native {
		base = nativeBaseType(engine, name)
	}
	walk(script.File.Statements, base)
	return found
}

// scriptBaseName returns the base-class spelling of the script's top-level
// class. A script with no extends clause returns "RefCounted", the implicit
// base. The Boolean is false when the base is written in a form that names a
// script class rather than a native engine class — a path literal or a member
// access — which the rule cannot resolve to a native chain.
func scriptBaseName(script *project.Script) (string, bool) {
	if script.File == nil {
		return "", false
	}
	for _, statement := range script.File.Statements {
		directive, ok := statement.(*ast.Directive)
		if !ok {
			continue
		}
		var expression ast.Expression
		switch directive.Name {
		case "extends":
			expression = directive.Value
		case "class_name":
			expression = directive.Extends
		default:
			continue
		}
		if expression == nil {
			continue
		}
		identifier, ok := expression.(*ast.Identifier)
		if !ok {
			return "", false
		}
		return identifier.Name, true
	}
	return "RefCounted", true
}

// nativeBaseType resolves a base-class spelling to its native class type. An
// empty spelling means a class with no extends clause, which inherits RefCounted.
// A spelling that names no native class resolves to the zero Type (Unknown), so
// the caller reports only what it can prove.
func nativeBaseType(engine *semantic.Engine, name string) semantic.Type {
	if engine == nil {
		return semantic.Type{}
	}
	if name == "" {
		name = "RefCounted"
	}
	return engine.Class(name)
}

// isEngineCalled reports whether a private function named name is reached by the
// engine rather than by source: an editor signal handler ("_on_..."), one of
// Object's core callbacks the schema does not record, or a virtual method
// declared by the script's native base chain.
func isEngineCalled(engine *semantic.Engine, base semantic.Type, name string) bool {
	if strings.HasPrefix(name, "_on_") {
		return true
	}
	if objectVirtuals[name] {
		return true
	}
	return virtualInChain(engine, base, name)
}

// virtualInChain reports whether name is a virtual method declared by base or
// any of its native ancestors.
func virtualInChain(engine *semantic.Engine, base semantic.Type, name string) bool {
	if engine == nil {
		return false
	}
	for current := base; current.Kind() == semantic.KindClass; {
		if engine.VirtualMethod(current.Name(), name) {
			return true
		}
		parent, ok := current.Base()
		if !ok {
			return false
		}
		current = parent
	}
	return false
}

// objectVirtuals are the Object virtuals Godot's extension API dump does not mark
// is_virtual. The dump records the virtuals of classes like Node and Control, but
// Object's core callbacks — _init, _notification, _to_string, and the property
// accessors — are dispatched by the engine directly rather than through
// ClassDB's virtual-method table, so they never appear in the dump. Every script
// class derives from Object, so these names are engine-called regardless of the
// declared base. _static_init is the class-load hook, which the dump also omits.
var objectVirtuals = map[string]bool{
	"_init":                       true,
	"_notification":               true,
	"_get":                        true,
	"_set":                        true,
	"_get_property_list":          true,
	"_property_can_revert":        true,
	"_property_get_revert":        true,
	"_to_string":                  true,
	"_validate_property":          true,
	"_get_configuration_warnings": true,
	"_static_init":                true,
}

// referenceNames returns every name the file mentions in a reference position: a
// bare identifier, a member-access property, a bare node-path name, or a
// property accessor shorthand. Declarations store their names as string fields
// rather than Identifier nodes, so a member's own declaration contributes
// nothing here.
func referenceNames(script *project.Script) map[string]bool {
	used := map[string]bool{}
	ast.Inspect(script.File, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Identifier:
			used[node.Name] = true
		case *ast.MemberExpression:
			used[node.Property] = true
		case *ast.NodePathExpression:
			for _, name := range bareNodePathNames(node.Path) {
				used[name] = true
			}
		case *ast.VariableDeclaration:
			if node.GetterName != "" {
				used[node.GetterName] = true
			}
			if node.SetterName != "" {
				used[node.SetterName] = true
			}
		}
		return true
	})
	return used
}

// stringLiteralContents returns the raw spelling of every string-like literal in
// the file, quotes included. A member reached by string dispatch is referenced
// through one of these.
func stringLiteralContents(script *project.Script) []string {
	var contents []string
	ast.Inspect(script.File, func(node ast.Node) bool {
		literal, ok := node.(*ast.Literal)
		if ok && stringLikeLiteral(literal.Kind) {
			contents = append(contents, literal.Raw)
		}
		return true
	})
	return contents
}

func stringLikeLiteral(kind ast.LiteralKind) bool {
	switch kind {
	case ast.StringLiteral, ast.StringNameLiteral, ast.NodePathLiteral:
		return true
	}
	return false
}

// appearsIn reports whether name is a substring of any of contents.
func appearsIn(contents []string, name string) bool {
	for _, content := range contents {
		if strings.Contains(content, name) {
			return true
		}
	}
	return false
}
