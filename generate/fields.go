package generate

import (
	"strings"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

// Field is one selectable member of a class.
type Field struct {
	// Name is the identifier the generated code reads, always as self.Name.
	Name string
	// Type is the declared type, empty when there is none.
	Type string
	// Inferred reports a ":=" declaration. Godot types it statically, but the
	// inferred type is not in the tree, so the name of the type is unknown.
	// Only deep_equals needs the distinction; it is recorded here anyway so
	// that generator needs no change to this file.
	Inferred bool
	// Line is 1-based.
	Line int
}

// SelectFields returns the selectable fields among statements, in declaration
// order.
//
// Never selected: a const, which GDScript types from its value and which is
// not instance state; a static var, which is not instance state either; an
// @onready var, which is node wiring rather than state and is null before
// _ready; and anything inside the generated region, which belongs to gen.
//
// A property with accessors is selected by name. The generated code reads
// self.q, which runs the getter, exactly as hand-written code would.
func SelectFields(statements []ast.Statement, script *project.Script, region Span) []Field {
	fields := []Field{}
	for _, statement := range statements {
		declaration, ok := statement.(*ast.VariableDeclaration)
		if !ok {
			continue
		}
		if declaration.Constant || declaration.Static {
			continue
		}
		if hasAnnotation(declaration.Annotations, "onready") {
			continue
		}
		offset := declaration.Span().Start.Offset
		if region.Start <= offset && offset < region.End {
			continue
		}
		line := lineAt(script, offset)
		if fieldOptedOut(script, line) {
			continue
		}
		fields = append(fields, Field{
			Name:     declaration.Name,
			Type:     declaration.Type,
			Inferred: declaration.Inferred,
			Line:     line,
		})
	}
	return fields
}

// fieldOptedOut reports the field-level opt-out, which may trail the
// declaration or stand alone on the line above it.
func fieldOptedOut(script *project.Script, line int) bool {
	if MatchIgnoreField(script.Line(line)) {
		return true
	}
	if line > 1 {
		above := script.Line(line - 1)
		if standsAlone(above) && MatchIgnoreField(above) {
			return true
		}
	}
	return false
}

// hasAnnotation reports an annotation by name, without its "@".
func hasAnnotation(annotations []*ast.Annotation, name string) bool {
	for _, annotation := range annotations {
		if strings.EqualFold(strings.TrimPrefix(annotation.Name, "@"), name) {
			return true
		}
	}
	return false
}

// lineAt is the 1-based line holding offset.
func lineAt(script *project.Script, offset int) int {
	line := 1
	for index, start := range script.Lines {
		if start > offset {
			break
		}
		line = index + 1
	}
	return line
}
