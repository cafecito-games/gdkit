package generate

import (
	"fmt"
	"strings"
)

// toStringGenerator emits Godot's _to_string virtual.
type toStringGenerator struct{}

func (toStringGenerator) Name() string { return "to_string" }

func (toStringGenerator) Signatures() []Signature { return []Signature{toStringSignature} }

// NeedsInheritanceGraph is false. _to_string neither composes with an ancestor
// nor walks one: an inherited one naming too few fields prints an incomplete
// value, which is wrong output rather than a wrong answer, and Godot's own
// str() on a subclass is no better. So a fieldful ancestor without a
// _to_string, a cycle elsewhere, and an unparseable unrelated file are all
// irrelevant to it.
func (toStringGenerator) NeedsInheritanceGraph() bool { return false }

// Emit renders the class name and every selected field.
//
// There is no configuration for this format. A project wanting a different one
// hand-writes _to_string and does not opt in, which is better than a style
// option nobody can change later without rewriting every region in every
// adopting project.
func (toStringGenerator) Emit(class *Class, _ *Index, _ *Capabilities) (string, []Diagnostic) {
	var body strings.Builder
	body.WriteString("func _to_string() -> String:\n")
	if len(class.Fields) == 0 {
		fmt.Fprintf(&body, "\treturn \"%s()\"\n", class.Name)
		return body.String(), nil
	}
	placeholders := make([]string, 0, len(class.Fields))
	arguments := make([]string, 0, len(class.Fields))
	for _, field := range class.Fields {
		placeholders = append(placeholders, field.Name+"=%s")
		arguments = append(arguments, "self."+field.Name)
	}
	fmt.Fprintf(&body, "\treturn \"%s(%s)\" %% [%s]\n",
		class.Name, strings.Join(placeholders, ", "), strings.Join(arguments, ", "))
	return body.String(), nil
}
