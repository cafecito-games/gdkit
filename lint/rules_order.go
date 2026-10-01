package lint

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(classDefinitionsOrderRule{})
}

// classDefinitionsOrderRule reports class members that appear after a member
// belonging to a later slot of the configured order. The file and every inner
// class are checked independently.
type classDefinitionsOrderRule struct{}

func (classDefinitionsOrderRule) Name() string { return "class-definitions-order" }

// An inner class's own "extends Base" clause is a member of that class, in the
// "extends" slot, exactly as gdlint's tree has it.
func (classDefinitionsOrderRule) Check(context *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	order := context.Config.ClassDefinitionsOrder
	if len(order) == 0 {
		return nil
	}
	rank := make(map[string]int, len(order))
	for index, slot := range order {
		if _, seen := rank[slot]; !seen {
			rank[slot] = index
		}
	}
	var found []Diagnostic
	var visit func(label string, leading []orderedMember, body []ast.Statement)
	visit = func(label string, leading []orderedMember, body []ast.Statement) {
		members := append(leading, orderedMembers(body)...)
		found = append(found, checkClassOrder(script, label, members, order, rank)...)
		for _, statement := range body {
			if class, ok := statement.(*ast.ClassDeclaration); ok {
				var leading []orderedMember
				if class.Extends != "" {
					leading = []orderedMember{{slot: "extends", start: class.ExtendsSpan.Start}}
				}
				visit(class.Name, leading, class.Body)
			}
		}
	}
	visit("global scope", nil, script.File.Statements)
	return found
}

// orderedMember is one statement as gdlint's class check sees it, with the
// slot it occupies. Annotations that gdparser attaches to a declaration are
// split back out, because gdlint pairs them with a declaration itself.
type orderedMember struct {
	slot  string
	start token.Position
}

func checkClassOrder(script *project.Script, label string, members []orderedMember, order []string, rank map[string]int) []Diagnostic {
	var found []Diagnostic
	current := order[0]
	for _, member := range members {
		memberRank, known := rank[member.slot]
		if !known {
			message := fmt.Sprintf("Definition order not specified for '%s' or '%s', please fix/re-generate your gdlintrc file", current, member.slot)
			found = append(found, startDiagnostic(script, message, member.start))
			continue
		}
		if memberRank >= rank[current] {
			current = member.slot
			continue
		}
		found = append(found, startDiagnostic(script, "Definition out of order in "+label, member.start))
	}
	return found
}

// orderedMembers flattens a class body into the statements gdlint orders.
// Standalone annotations other than @tool, "pass", comments, and inner class
// definitions take no slot.
func orderedMembers(body []ast.Statement) []orderedMember {
	var members []orderedMember
	var pending []*ast.Annotation
	// consume feeds annotations through gdlint's pairing: a standalone
	// annotation is a statement of its own and discards whatever annotations
	// were waiting for a declaration.
	consume := func(annotations []*ast.Annotation) {
		for _, annotation := range annotations {
			if !standaloneAnnotation(annotation) {
				pending = append(pending, annotation)
				continue
			}
			pending = nil
			if annotation.Name == "tool" {
				members = append(members, orderedMember{slot: "tools", start: annotation.Span().Start})
			}
		}
	}
	declare := func(slot string, start token.Position) {
		members = append(members, orderedMember{slot: slot, start: start})
		pending = nil
	}
	for _, statement := range body {
		switch statement := statement.(type) {
		case *ast.Annotation:
			consume([]*ast.Annotation{statement})
		case *ast.Comment:
		case *ast.VariableDeclaration:
			consume(statement.Annotations)
			declare(variableSlot(statement, pending), variableStart(statement))
		case *ast.FunctionDeclaration:
			consume(statement.Annotations)
			start := statement.KeywordSpan.Start
			if statement.Static {
				start = statement.StaticSpan.Start
			}
			declare("others", start)
		case *ast.SignalDeclaration:
			consume(statement.Annotations)
			declare("signals", statement.KeywordSpan.Start)
		case *ast.EnumDeclaration:
			consume(statement.Annotations)
			declare("enums", statement.KeywordSpan.Start)
		case *ast.ClassDeclaration:
			consume(statement.Annotations)
			pending = nil
		case *ast.Directive:
			slot := "extends"
			if statement.Name == "class_name" && statement.Extends == nil {
				slot = "classnames"
			}
			declare(slot, statement.KeywordSpan.Start)
		case *ast.ExpressionStatement:
			if literal, ok := statement.Expression.(*ast.Literal); ok && literal.Kind == ast.StringLiteral {
				declare("docstrings", statement.Span().Start)
			} else {
				pending = nil
			}
		default:
			pending = nil
		}
	}
	return members
}

func variableStart(variable *ast.VariableDeclaration) token.Position {
	if variable.Static {
		return variable.StaticSpan.Start
	}
	return variable.KeywordSpan.Start
}

func variableSlot(variable *ast.VariableDeclaration, annotations []*ast.Annotation) string {
	if variable.Constant {
		return "consts"
	}
	if variable.Static {
		return "staticvars"
	}
	for _, annotation := range annotations {
		if strings.HasPrefix(annotation.Name, "export") {
			return "exports"
		}
	}
	visibility := "pub"
	if strings.HasPrefix(variable.Name, "_") {
		visibility = "prv"
	}
	for _, annotation := range annotations {
		if annotation.Name == "onready" {
			return "onready" + visibility + "vars"
		}
	}
	return visibility + "vars"
}

// standaloneAnnotations never attach to the declaration that follows them.
var standaloneAnnotations = map[string]bool{
	"abstract": true, "export_category": true, "export_group": true,
	"export_subgroup": true, "icon": true, "tool": true,
	"warning_ignore_start": true, "warning_ignore_restore": true,
}

// attachingWarnings are the @warning_ignore categories that apply to a single
// declaration, so the annotation attaches to it rather than standing alone.
var attachingWarnings = map[string]bool{
	"unassigned_variable": true, "unassigned_variable_op_assign": true,
	"unused_parameter": true, "shadowed_global_identifier": true,
	"shadowed_variable": true, "shadowed_variable_base_class": true,
	"unreachable_code": true, "unreachable_pattern": true,
	"standalone_expression": true, "standalone_ternary": true,
	"incompatible_ternary": true, "untyped_declaration": true,
	"inferred_declaration": true, "unsafe_property_access": true,
	"unsafe_method_access": true, "unsafe_cast": true,
	"unsafe_call_argument": true, "unsafe_void_return": true,
	"return_value_discarded": true, "static_called_on_instance": true,
	"redundant_await": true, "assert_always_true": true,
	"assert_always_false": true, "integer_division": true,
	"narrowing_conversion": true, "int_as_enum_without_cast": true,
	"int_as_enum_without_match": true, "enum_variable_without_default": true,
	"deprecated_keyword": true, "confusable_identifier": true,
	"confusable_local_declaration": true, "confusable_local_usage": true,
	"confusable_capture_reassignment": true, "inference_on_variant": true,
	"native_method_override": true,
}

func standaloneAnnotation(annotation *ast.Annotation) bool {
	if standaloneAnnotations[annotation.Name] {
		return true
	}
	if annotation.Name != "warning_ignore" {
		return false
	}
	if len(annotation.Arguments) == 0 {
		return true
	}
	literal, ok := annotation.Arguments[0].(*ast.Literal)
	if !ok || literal.Kind != ast.StringLiteral || literal.Quote != '"' {
		return true
	}
	return !attachingWarnings[strings.Trim(literal.Raw, `"`)]
}
