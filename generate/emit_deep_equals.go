package generate

import (
	"fmt"
	"strings"
)

// deepEqualsGenerator emits a structural equality method.
type deepEqualsGenerator struct{}

func (deepEqualsGenerator) Name() string { return "deep_equals" }

func (deepEqualsGenerator) Signatures() []Signature { return []Signature{deepEqualsSignature} }

// NeedsInheritanceGraph is true for the same reasons as equals: it composes
// with its ancestor's provider and is refused when a descendant would inherit
// an unsound implementation.
func (deepEqualsGenerator) NeedsInheritanceGraph() bool { return true }

// Emit renders the identity short-circuit, the guards, the ancestor
// composition, and one helper call per field.
//
// Every field goes through the helper, whatever its declared type. Routing
// only the container-shaped fields was considered and rejected: gdkit cannot
// tell statically which fields hold a container, because an untyped field may
// hold one and "Array[Branch]" is only a hint, so a selective rule would leave
// the untyped case comparing by reference while costing a type-spelling parser
// and a second code path. Uniform routing also keeps this emitter free of any
// type analysis at all.
//
// The call is qualified with the helpers class's own class_name rather than a
// fixed spelling, so a project that renames the class keeps working.
func (deepEqualsGenerator) Emit(class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
	var body strings.Builder
	body.WriteString("func deep_equals(p_other: Variant) -> bool:\n")
	// The same instance is strictly equal. This is both the fast path and the
	// reason the realistic recursive shapes terminate: a self-reference, and a
	// sub-object shared by both sides, are settled without recursing.
	body.WriteString("\tif self == p_other:\n\t\treturn true\n")
	body.WriteString("\tif not (p_other is Object):\n\t\treturn false\n")
	body.WriteString("\tif p_other.get_script() != get_script():\n\t\treturn false\n")
	if _, ok := capabilities.Provider(index, class.ParentID, deepEqualsSignature); ok {
		body.WriteString("\tif not super.deep_equals(p_other):\n\t\treturn false\n")
	}
	helpers := index.HelpersClass.Name
	for _, field := range class.Fields {
		// Every reference is self-qualified: a field named p_other would
		// otherwise be shadowed by the parameter and read the argument.
		fmt.Fprintf(&body, "\tif not %s.deep_equals(self.%s, p_other.%s):\n\t\treturn false\n",
			helpers, field.Name, field.Name)
	}
	body.WriteString("\treturn true\n")
	return body.String(), nil
}
