package generate

import (
	"fmt"
	"strings"
)

// maxReturnsRule is lint's name for the rule that counts a function's returns.
// It is a public contract on the lint side, which is why it is spelled out here
// rather than imported: generate takes no dependency on lint.
const maxReturnsRule = "max-returns"

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
// The method returns once per field plus four or five fixed times, so any class
// with a few fields exceeds lint's max-returns default in code the project did
// not write. It carries a suppression for that rule alone, so it cannot hide an
// unrelated finding. max-returns reports at the last return, which is always
// the final "return true", so a line-scoped ignore there covers it exactly. A
// "disable"/"enable" pair around the function was rejected: lint clips every
// disable of a rule at that rule's earliest enable, so a pair here would also
// cut short, or void, a project's own disable of max-returns elsewhere in the
// file.
//
// The call is qualified with helpersClassName, which is also how the class is
// resolved, so a project cannot rename it: the generated code and the lookup
// have to agree on one global, and the addon is the thing that declares it.
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
	for _, field := range class.Fields {
		// Every reference is self-qualified: a field named p_other would
		// otherwise be shadowed by the parameter and read the argument.
		fmt.Fprintf(&body, "\tif not %s.deep_equals(self.%s, p_other.%s):\n\t\treturn false\n",
			helpersClassName, field.Name, field.Name)
	}
	body.WriteString("\treturn true  # gdkit:ignore = " + maxReturnsRule + "\n")
	return body.String(), nil
}
