package lint

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

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
// The rule ships inert, as a new rule must, so an upgrade cannot change what an
// existing project reports on unchanged configuration. It is also explicitly
// single-file: a reference to a private member from another script — including a
// call("_name") written there — is invisible to it, exactly as it is to every
// other single-file rule.
type unusedPrivateMemberRule struct{}

func (unusedPrivateMemberRule) Name() string { return "unused-private-member" }

func (unusedPrivateMemberRule) Describe() string {
	return "Reports a private member that is never referenced in its file"
}

func (unusedPrivateMemberRule) PendingSince() string { return "0.8.0" }

func (unusedPrivateMemberRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	used := referenceNames(script)
	strings_ := stringLiteralContents(script)

	var found []Diagnostic
	report := func(kind, name string, span token.Span) {
		if used[name] || appearsIn(strings_, name) {
			return
		}
		found = append(found, spanDiagnostic(script, fmt.Sprintf("unused private %s %q", kind, name), span))
	}

	var walk func(body []ast.Statement)
	walk = func(body []ast.Statement) {
		for _, statement := range body {
			switch statement := statement.(type) {
			case *ast.FunctionDeclaration:
				if !strings.HasPrefix(statement.Name, "_") {
					continue
				}
				if engineVirtuals[statement.Name] || strings.HasPrefix(statement.Name, "_on_") {
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
				walk(class.Body)
			}
		}
	}
	walk(script.File.Statements)
	return found
}

// engineVirtuals are the engine callback methods gdkit recognises. A private
// function whose name is one of these is called by the engine, never from
// source, so it is not dead code. The list covers the virtuals of Object, Node,
// CanvasItem, Control, and Resource; it is not exhaustive, and a project using a
// virtual outside it can suppress the finding with a comment.
var engineVirtuals = map[string]bool{
	// Object.
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
	// Node.
	"_ready":               true,
	"_process":             true,
	"_physics_process":     true,
	"_enter_tree":          true,
	"_exit_tree":           true,
	"_input":               true,
	"_unhandled_input":     true,
	"_unhandled_key_input": true,
	"_shortcut_input":      true,
	// CanvasItem.
	"_draw": true,
	// Control.
	"_gui_input":           true,
	"_get_minimum_size":    true,
	"_has_point":           true,
	"_get_drag_data":       true,
	"_can_drop_data":       true,
	"_drop_data":           true,
	"_make_custom_tooltip": true,
	"_get_tooltip":         true,
	"_get_cursor_shape":    true,
	// Resource.
	"_setup_local_to_scene": true,
	// The class-load hook.
	"_static_init": true,
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
