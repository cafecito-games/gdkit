package generate

import "strings"

// helpersGenerator emits the project-wide utility class.
//
// It is registered so the seed in Resolve, the conflict check, and candidate
// emission all find it by name, and it is internal so that name is not a
// configuration or directive spelling: the file is named by helpers_path, and
// a class that asked for a region of shared infrastructure would get one it
// has no business holding.
type helpersGenerator struct{}

func (helpersGenerator) Name() string { return "helpers" }

func (helpersGenerator) Signatures() []Signature { return []Signature{helpersSignature} }

// NeedsInheritanceGraph is false. The helper composes with nothing and walks
// no ancestry; it is a static function on a class nothing extends.
func (helpersGenerator) NeedsInheritanceGraph() bool { return false }

// Internal marks this emitter as unnameable in configuration and directives.
func (helpersGenerator) Internal() {}

// Emit renders the shared deep comparison.
//
// It asks each value what it can do rather than resolving types, which is what
// lets one function serve every field of every class: a container is walked
// element by element, and anything else is handed to its own deep_equals or
// equals. The alternative — emitting a comparison per field shape into each
// class — cannot recurse to arbitrary depth and cannot see an untyped field's
// contents at all.
//
// "p_lhs == p_rhs" opens the function for the same reason the per-class method
// opens with "self == p_other": it is the fast path, and a container is only
// walked once "==" has already answered unequal.
//
// A PackedVector2Array is not "is Array" and compares by value under "==", so
// it is settled by the first test or by the final return without a special
// case.
//
// Dictionary keys are matched with has(), which is hashing and, for an object,
// reference identity. Two equal-but-distinct value objects used as keys do not
// pair up. Deep key matching needs quadratic pairing with no sound answer when
// several keys are mutually equal, so this is a stated limit.
func (helpersGenerator) Emit(class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
	var body strings.Builder
	body.WriteString("static func deep_equals(p_lhs: Variant, p_rhs: Variant) -> bool:\n")
	body.WriteString("\tif p_lhs == p_rhs:\n\t\treturn true\n")
	body.WriteString("\tif p_lhs is Array and p_rhs is Array:\n")
	body.WriteString("\t\tif p_lhs.size() != p_rhs.size():\n\t\t\treturn false\n")
	body.WriteString("\t\tfor index in p_lhs.size():\n")
	body.WriteString("\t\t\tif not deep_equals(p_lhs[index], p_rhs[index]):\n\t\t\t\treturn false\n")
	body.WriteString("\t\treturn true\n")
	body.WriteString("\tif p_lhs is Dictionary and p_rhs is Dictionary:\n")
	body.WriteString("\t\tif p_lhs.size() != p_rhs.size():\n\t\t\treturn false\n")
	body.WriteString("\t\tfor key in p_lhs:\n")
	body.WriteString("\t\t\tif not p_rhs.has(key):\n\t\t\t\treturn false\n")
	body.WriteString("\t\t\tif not deep_equals(p_lhs[key], p_rhs[key]):\n\t\t\t\treturn false\n")
	body.WriteString("\t\treturn true\n")
	// null.has_method(...) is a runtime error, so a null is settled before any
	// dispatch. Reaching here means the two differ under "==", so if either is
	// null they are unequal: both being null would have compared equal above.
	body.WriteString("\tif p_lhs == null or p_rhs == null:\n\t\treturn false\n")
	// "is Object" is not optional: has_method is declared on Object, so
	// calling it on an int or a String is a runtime error rather than false.
	body.WriteString("\tif p_lhs is Object and p_lhs.has_method(\"deep_equals\"):\n")
	body.WriteString("\t\treturn p_lhs.deep_equals(p_rhs)\n")
	body.WriteString("\tif p_lhs is Object and p_lhs.has_method(\"equals\"):\n")
	body.WriteString("\t\treturn p_lhs.equals(p_rhs)\n")
	body.WriteString("\treturn false\n")
	return body.String(), nil
}
