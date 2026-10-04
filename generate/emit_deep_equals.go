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

// Emit renders the identity short-circuit, the guard, the ancestor
// composition, and a comparison per field.
//
// Where equals compares an object-valued field with "==" — which is identity in
// Godot 4, and the bug a value object exists to prevent — this asks the value
// what it can do:
//
//	if self.origin is Object and self.origin.has_method("deep_equals"):
//
// Dispatching at runtime rather than resolving each field's type statically is
// what keeps this generator small. It needs no catalogue of builtin Variant
// names, nothing proven in advance about the target class, and no special case
// for a field whose type is unknown — and it uses a field type that has only a
// hand-written equals, which a static design had to refuse.
//
// "is Object" is not optional: has_method is declared on Object, so calling it
// on an int or a String is a runtime error rather than false.
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
		if _, recurse := index.fieldTypeTarget(field); recurse || field.Type == "" || field.Type == "Variant" || field.Inferred {
			writeDeepFieldComparison(&body, field.Name)
			continue
		}
		// A declared type that is not a project class is a builtin or an
		// engine class. Either way "==" is the right comparison: a builtin
		// compares by value, and an engine object has no value equality to
		// recurse into.
		fmt.Fprintf(&body, "\tif self.%s != p_other.%s:\n\t\treturn false\n", field.Name, field.Name)
	}
	body.WriteString("\treturn true\n")
	return body.String(), nil
}

// writeDeepFieldComparison emits the dispatch block for one field.
//
// The block sits inside an inequality test, because "==" being true settles the
// field: identical instances, and equal builtins, never reach dispatch. The
// closing "return false" is a conclusion rather than a fallback — "==" has
// already answered unequal and no deeper answer is available, so unequal
// stands.
func writeDeepFieldComparison(body *strings.Builder, name string) {
	fmt.Fprintf(body, "\tif self.%s != p_other.%s:\n", name, name)
	// null.has_method(...) is a runtime error, so a null is settled before any
	// dispatch. Reaching here means the two differ under "==", so if either is
	// null they are unequal: both being null would have compared equal and
	// never entered this block. That is also why this is an "or" rather than a
	// comparison of two null tests — the latter is correct but the formatter
	// strips its parentheses, leaving "a == null != (b == null)" to read.
	fmt.Fprintf(body, "\t\tif self.%s == null or p_other.%s == null:\n\t\t\treturn false\n", name, name)
	fmt.Fprintf(body, "\t\tif self.%s is Object and self.%s.has_method(\"deep_equals\"):\n", name, name)
	fmt.Fprintf(body, "\t\t\tif not self.%s.deep_equals(p_other.%s):\n\t\t\t\treturn false\n", name, name)
	fmt.Fprintf(body, "\t\telif self.%s is Object and self.%s.has_method(\"equals\"):\n", name, name)
	fmt.Fprintf(body, "\t\t\tif not self.%s.equals(p_other.%s):\n\t\t\t\treturn false\n", name, name)
	body.WriteString("\t\telse:\n\t\t\treturn false\n")
}
