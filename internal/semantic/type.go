// Package semantic defines the resolved type vocabulary shared by gdkit's
// semantic analyses. It deliberately contains no parsing or diagnostic policy.
package semantic

import (
	"fmt"
	"strings"
)

const uninitializedReason = "uninitialized type"

// Kind identifies one category of GDScript type.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindVariant
	KindVoid
	KindBuiltin
	KindClass
	KindArray
	KindDictionary
	KindCallable
	KindSignal
	KindEnum
)

// Assignability is the three-valued result of an assignment check.
type Assignability uint8

const (
	AssignabilityIndeterminate Assignability = iota
	AssignabilityYes
	AssignabilityNo
)

func (a Assignability) String() string {
	switch a {
	case AssignabilityIndeterminate:
		return "indeterminate"
	case AssignabilityYes:
		return "yes"
	case AssignabilityNo:
		return "no"
	default:
		return fmt.Sprintf("Assignability(%d)", a)
	}
}

// Type is an immutable resolved GDScript type. Its zero value is an Unknown
// whose reason is "uninitialized type".
type Type struct {
	node *typeNode
}

type typeNode struct {
	kind   Kind
	name   string
	reason string
	meta   bool

	base    Type
	hasBase bool

	element    Type
	hasElement bool

	key      Type
	value    Type
	hasKey   bool
	hasValue bool
}

// Unknown constructs a failed-analysis type. A failure without an explanation
// is a programming error because consumers must be able to remain silent and
// explain why analysis was inconclusive.
func Unknown(reason string) Type {
	if strings.TrimSpace(reason) == "" {
		panic("semantic.Unknown: reason must not be empty")
	}
	return Type{node: &typeNode{kind: KindUnknown, reason: reason}}
}

// Variant constructs GDScript's dynamic Variant type.
func Variant() Type { return Type{node: &typeNode{kind: KindVariant}} }

// Void constructs GDScript's void return type.
func Void() Type { return Type{node: &typeNode{kind: KindVoid}} }

// Builtin constructs a named built-in GDScript type.
func Builtin(name string) Type {
	requireName("Builtin", name)
	return Type{node: &typeNode{kind: KindBuiltin, name: name}}
}

// Class constructs a named class type. Base is copied when non-nil, so later
// reassignment of the caller's Type variable cannot alter this value. Meta
// distinguishes the class object from instances of the class.
func Class(name string, base *Type, meta bool) Type {
	requireName("Class", name)
	node := &typeNode{kind: KindClass, name: name, meta: meta}
	if base != nil {
		node.base = *base
		node.hasBase = true
	}
	return Type{node: node}
}

// Array constructs an Array type. A nil element denotes an untyped Array and
// is deliberately distinct from Array[Variant].
func Array(element *Type) Type {
	node := &typeNode{kind: KindArray}
	if element != nil {
		node.element = *element
		node.hasElement = true
	}
	return Type{node: node}
}

// Dictionary constructs a Dictionary type. Two nil components denote an
// untyped Dictionary and are deliberately distinct from Variant components.
// A Dictionary cannot be only partially typed.
func Dictionary(key, value *Type) Type {
	if (key == nil) != (value == nil) {
		panic("semantic.Dictionary: key and value must either both be present or both be nil")
	}
	node := &typeNode{kind: KindDictionary}
	if key != nil {
		node.key = *key
		node.value = *value
		node.hasKey = true
		node.hasValue = true
	}
	return Type{node: node}
}

// Callable constructs GDScript's Callable type.
func Callable() Type { return Type{node: &typeNode{kind: KindCallable}} }

// Signal constructs GDScript's Signal type.
func Signal() Type { return Type{node: &typeNode{kind: KindSignal}} }

// Enum constructs a named enum type.
func Enum(name string) Type {
	requireName("Enum", name)
	return Type{node: &typeNode{kind: KindEnum, name: name}}
}

func requireName(constructor, name string) {
	if strings.TrimSpace(name) == "" {
		panic("semantic." + constructor + ": name must not be empty")
	}
}

// Kind returns the type's category. A zero or locally malformed Type is
// reported as Unknown.
func (t Type) Kind() Kind {
	if t.node == nil || localProblem(t.node) != "" {
		return KindUnknown
	}
	return t.node.kind
}

// Name returns the name of a builtin, class, or enum, and the empty string for
// kinds without a name.
func (t Type) Name() string {
	if t.node == nil || localProblem(t.node) != "" {
		return ""
	}
	switch t.node.kind {
	case KindBuiltin, KindClass, KindEnum:
		return t.node.name
	default:
		return ""
	}
}

// Reason returns an Unknown's explanation. It also explains a zero or
// malformed value. Non-Unknown, well-formed types return an empty string.
func (t Type) Reason() string {
	if t.node == nil {
		return uninitializedReason
	}
	if problem := localProblem(t.node); problem != "" {
		return problem
	}
	if t.node.kind == KindUnknown {
		return t.node.reason
	}
	return ""
}

// Meta reports whether a class type represents the class object rather than
// an instance.
func (t Type) Meta() bool {
	return t.node != nil && localProblem(t.node) == "" && t.node.kind == KindClass && t.node.meta
}

// Base returns a class's base type, if present.
func (t Type) Base() (Type, bool) {
	if t.node == nil || localProblem(t.node) != "" || t.node.kind != KindClass || !t.node.hasBase {
		return Type{}, false
	}
	return t.node.base, true
}

// Element returns a typed Array's element type. The Boolean is false for an
// untyped Array and for other kinds.
func (t Type) Element() (Type, bool) {
	if t.node == nil || localProblem(t.node) != "" || t.node.kind != KindArray || !t.node.hasElement {
		return Type{}, false
	}
	return t.node.element, true
}

// Key returns a typed Dictionary's key type.
func (t Type) Key() (Type, bool) {
	if t.node == nil || localProblem(t.node) != "" || t.node.kind != KindDictionary || !t.node.hasKey {
		return Type{}, false
	}
	return t.node.key, true
}

// Value returns a typed Dictionary's value type.
func (t Type) Value() (Type, bool) {
	if t.node == nil || localProblem(t.node) != "" || t.node.kind != KindDictionary || !t.node.hasValue {
		return Type{}, false
	}
	return t.node.value, true
}

// Equal reports structural identity. It compares complete class ancestry and
// container structure, not allocation identity, and terminates on cycles in a
// malformed internal graph.
func (t Type) Equal(other Type) bool {
	return equal(t, other, make(map[nodePair]bool))
}

type nodePair struct {
	left  *typeNode
	right *typeNode
}

func equal(left, right Type, seen map[nodePair]bool) bool {
	if left.node == nil || right.node == nil {
		if left.node == nil && right.node == nil {
			return true
		}
		other := right
		if right.node == nil {
			other = left
		}
		return other.node != nil && other.node.kind == KindUnknown && other.node.reason == uninitializedReason
	}
	pair := nodePair{left.node, right.node}
	if seen[pair] {
		return true
	}
	seen[pair] = true

	a, b := left.node, right.node
	if a.kind != b.kind || a.name != b.name || a.reason != b.reason || a.meta != b.meta ||
		a.hasBase != b.hasBase || a.hasElement != b.hasElement ||
		a.hasKey != b.hasKey || a.hasValue != b.hasValue {
		return false
	}
	if a.hasBase && !equal(a.base, b.base, seen) {
		return false
	}
	if a.hasElement && !equal(a.element, b.element, seen) {
		return false
	}
	if a.hasKey && !equal(a.key, b.key, seen) {
		return false
	}
	return !a.hasValue || equal(a.value, b.value, seen)
}

// AssignableTo reports whether a value of t may be assigned to a location of
// target. Unknown or malformed information produces Indeterminate rather than
// a confident yes or no.
func (t Type) AssignableTo(target Type) Assignability {
	if validate(t) != "" || validate(target) != "" {
		return AssignabilityIndeterminate
	}
	return assignable(t, target)
}

func assignable(source, target Type) Assignability {
	sourceKind, targetKind := rawKind(source), rawKind(target)
	if sourceKind == KindUnknown || targetKind == KindUnknown {
		return AssignabilityIndeterminate
	}
	if sourceKind == KindVariant || targetKind == KindVariant {
		return AssignabilityYes
	}
	if sourceKind == KindArray && targetKind == KindArray {
		if !target.node.hasElement {
			return AssignabilityYes
		}
		if !source.node.hasElement {
			return AssignabilityNo
		}
		return assignable(source.node.element, target.node.element)
	}
	if sourceKind == KindDictionary && targetKind == KindDictionary {
		if !target.node.hasKey {
			return AssignabilityYes
		}
		if !source.node.hasKey {
			return AssignabilityNo
		}
		return combine(
			assignable(source.node.key, target.node.key),
			assignable(source.node.value, target.node.value),
		)
	}
	if sourceKind == KindClass && targetKind == KindClass {
		if source.node.meta != target.node.meta {
			return AssignabilityNo
		}
		if source.node.meta {
			if source.Equal(target) {
				return AssignabilityYes
			}
			return AssignabilityNo
		}
		for current := source; ; {
			if current.node.name == target.node.name {
				return AssignabilityYes
			}
			if !current.node.hasBase {
				return AssignabilityNo
			}
			current = current.node.base
			if rawKind(current) == KindUnknown {
				return AssignabilityIndeterminate
			}
			if rawKind(current) != KindClass || current.node.meta {
				return AssignabilityIndeterminate
			}
		}
	}
	if source.Equal(target) {
		return AssignabilityYes
	}
	if sourceKind == KindBuiltin && targetKind == KindBuiltin && source.node.name == "int" && target.node.name == "float" {
		return AssignabilityYes
	}
	return AssignabilityNo
}

func combine(left, right Assignability) Assignability {
	if left == AssignabilityNo || right == AssignabilityNo {
		return AssignabilityNo
	}
	if left == AssignabilityIndeterminate || right == AssignabilityIndeterminate {
		return AssignabilityIndeterminate
	}
	return AssignabilityYes
}

func rawKind(t Type) Kind {
	if t.node == nil {
		return KindUnknown
	}
	return t.node.kind
}

// String returns GDScript spelling for writable types and an explicitly
// non-writable diagnostic spelling for Unknown or malformed values.
func (t Type) String() string {
	if problem := validate(t); problem != "" {
		return "<unknown: " + problem + ">"
	}
	switch rawKind(t) {
	case KindUnknown:
		return "<unknown: " + t.Reason() + ">"
	case KindVariant:
		return "Variant"
	case KindVoid:
		return "void"
	case KindBuiltin, KindClass, KindEnum:
		return t.node.name
	case KindArray:
		if !t.node.hasElement {
			return "Array"
		}
		return "Array[" + t.node.element.String() + "]"
	case KindDictionary:
		if !t.node.hasKey {
			return "Dictionary"
		}
		return "Dictionary[" + t.node.key.String() + ", " + t.node.value.String() + "]"
	case KindCallable:
		return "Callable"
	case KindSignal:
		return "Signal"
	default:
		return "<unknown: unsupported type kind>"
	}
}

func validate(t Type) string {
	return validateNode(t, make(map[*typeNode]bool), make(map[*typeNode]bool))
}

func validateNode(t Type, visiting, checked map[*typeNode]bool) string {
	if t.node == nil {
		return ""
	}
	if problem := localProblem(t.node); problem != "" {
		return problem
	}
	if checked[t.node] {
		return ""
	}
	if visiting[t.node] {
		return "cyclic type structure"
	}
	visiting[t.node] = true
	defer delete(visiting, t.node)

	var children []Type
	switch t.node.kind {
	case KindClass:
		if t.node.hasBase {
			baseKind := rawKind(t.node.base)
			if baseKind != KindClass && baseKind != KindUnknown {
				return "class base is not a class"
			}
			children = append(children, t.node.base)
		}
	case KindArray:
		if t.node.hasElement {
			children = append(children, t.node.element)
		}
	case KindDictionary:
		if t.node.hasKey {
			children = append(children, t.node.key, t.node.value)
		}
	}
	for _, child := range children {
		if problem := validateNode(child, visiting, checked); problem != "" {
			return problem
		}
	}
	checked[t.node] = true
	return ""
}

func localProblem(node *typeNode) string {
	if node == nil {
		return uninitializedReason
	}
	if node.kind > KindEnum {
		return "unsupported type kind"
	}
	if node.kind == KindUnknown && strings.TrimSpace(node.reason) == "" {
		return "unknown type without a reason"
	}
	if (node.kind == KindBuiltin || node.kind == KindClass || node.kind == KindEnum) && strings.TrimSpace(node.name) == "" {
		return "named type without a name"
	}
	if node.kind == KindDictionary && node.hasKey != node.hasValue {
		return "partially typed Dictionary"
	}
	if node.kind != KindUnknown && node.reason != "" {
		return "reason on non-Unknown type"
	}
	if node.kind != KindBuiltin && node.kind != KindClass && node.kind != KindEnum && node.name != "" {
		return "name on unnamed type"
	}
	if node.kind != KindClass && (node.meta || node.hasBase || node.base.node != nil) {
		return "class data on non-class type"
	}
	if node.kind == KindClass && !node.hasBase && node.base.node != nil {
		return "class base present without marker"
	}
	if node.kind != KindArray && (node.hasElement || node.element.node != nil) {
		return "Array data on non-Array type"
	}
	if node.kind == KindArray && !node.hasElement && node.element.node != nil {
		return "Array element present without marker"
	}
	if node.kind != KindDictionary && (node.hasKey || node.hasValue || node.key.node != nil || node.value.node != nil) {
		return "Dictionary data on non-Dictionary type"
	}
	if node.kind == KindDictionary && !node.hasKey && (node.key.node != nil || node.value.node != nil) {
		return "Dictionary component present without marker"
	}
	return ""
}
