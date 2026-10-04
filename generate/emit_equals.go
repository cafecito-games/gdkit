package generate

import (
	"fmt"
	"strings"
)

// equalsGenerator emits a value-equality method.
type equalsGenerator struct{}

func (equalsGenerator) Name() string { return "equals" }

func (equalsGenerator) Signatures() []Signature { return []Signature{equalsSignature} }

// NeedsInheritanceGraph is true: equals composes with its ancestor's provider
// and is refused when a descendant would inherit an unsound implementation, so
// both answers need the whole graph to be visible and trustworthy.
func (equalsGenerator) NeedsInheritanceGraph() bool { return true }

// Emit renders the guard, the ancestor composition, and the field comparison.
//
// The guard is script identity rather than "is <ClassName>" for two reasons. It
// works for a class declaring no class_name, which "is" cannot name at all. And
// it is symmetric: two instances are equal candidates only when they are the
// same script, so a.equals(b) == b.equals(a) always holds, where "is" answers
// true one way and false the other across a subclass.
//
// Script identity alone is not sufficient, which is why the composition
// matters: two instances of a subclass answer the same get_script(), so an
// inherited comparison would pass the guard and then ignore every field the
// subclass added. Composing with the parent's provider means every field in the
// ancestry is compared, each by the class that declares it. Capability
// resolution refuses the cases where no such provider exists.
//
// Object-valued fields compare by reference, because that is what == does to an
// Object in Godot 4. Array and Dictionary fields compare by value, because that
// is what == does to those. Structural comparison of a nested value object is
// deep_equals's job.
func (equalsGenerator) Emit(class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
	var body strings.Builder
	body.WriteString("func equals(p_other: Variant) -> bool:\n")
	body.WriteString("\tif not (p_other is Object):\n\t\treturn false\n")
	body.WriteString("\tif p_other.get_script() != get_script():\n\t\treturn false\n")
	if _, ok := capabilities.Provider(index, class.ParentID, equalsSignature); ok {
		body.WriteString("\tif not super.equals(p_other):\n\t\treturn false\n")
	}
	if len(class.Fields) == 0 {
		// The guards have already established that both operands are the same
		// script, and a class with no state of its own has nothing further to
		// compare.
		body.WriteString("\treturn true\n")
		return body.String(), nil
	}
	comparisons := make([]string, 0, len(class.Fields))
	for _, field := range class.Fields {
		// Every reference is self-qualified: a field named p_other would
		// otherwise be shadowed by the parameter and read the argument.
		comparisons = append(comparisons, fmt.Sprintf("self.%s == p_other.%s", field.Name, field.Name))
	}
	// Emitted on one line; the formatter wraps it to the project's line_width.
	fmt.Fprintf(&body, "\treturn %s\n", strings.Join(comparisons, " and "))
	return body.String(), nil
}
