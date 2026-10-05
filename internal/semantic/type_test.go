package semantic

import (
	"strings"
	"testing"
)

func TestKindsAndSpellings(t *testing.T) {
	intType := Builtin("int")
	stringType := Builtin("String")
	base := Class("Base", nil, false)

	tests := []struct {
		name   string
		type_  Type
		kind   Kind
		string string
	}{
		{"zero", Type{}, KindUnknown, "<unknown: uninitialized type>"},
		{"unknown", Unknown("name did not resolve"), KindUnknown, "<unknown: name did not resolve>"},
		{"variant", Variant(), KindVariant, "Variant"},
		{"void", Void(), KindVoid, "void"},
		{"builtin", Builtin("Vector2"), KindBuiltin, "Vector2"},
		{"class instance", Class("Child", &base, false), KindClass, "Child"},
		{"class meta", Class("Child", &base, true), KindClass, "Child"},
		{"untyped array", Array(nil), KindArray, "Array"},
		{"typed array", Array(&intType), KindArray, "Array[int]"},
		{"untyped dictionary", Dictionary(nil, nil), KindDictionary, "Dictionary"},
		{"typed dictionary", Dictionary(&stringType, &intType), KindDictionary, "Dictionary[String, int]"},
		{"callable", Callable(), KindCallable, "Callable"},
		{"signal", Signal(), KindSignal, "Signal"},
		{"enum", Enum("Direction"), KindEnum, "Direction"},
	}

	seen := make(map[Kind]bool)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.type_.Kind(); got != test.kind {
				t.Fatalf("Kind() = %v, want %v", got, test.kind)
			}
			if got := test.type_.String(); got != test.string {
				t.Fatalf("String() = %q, want %q", got, test.string)
			}
			if got := test.type_.String(); got != test.string {
				t.Fatalf("second String() = %q, want stable %q", got, test.string)
			}
		})
		seen[test.kind] = true
	}

	for kind := KindUnknown; kind <= KindEnum; kind++ {
		if !seen[kind] {
			t.Errorf("kind %v has no vocabulary test", kind)
		}
	}
}

func TestUnknownRequiresAReasonAndZeroFailsClosed(t *testing.T) {
	for _, reason := range []string{"", " ", "\t\n"} {
		t.Run("reason="+reason, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Unknown accepted an empty reason")
				}
			}()
			Unknown(reason)
		})
	}

	var zero Type
	if zero.Kind() != KindUnknown {
		t.Fatalf("zero Kind() = %v, want KindUnknown", zero.Kind())
	}
	if strings.TrimSpace(zero.Reason()) == "" {
		t.Fatal("zero Type has no Unknown reason")
	}
	if got := zero.AssignableTo(Variant()); got != AssignabilityIndeterminate {
		t.Fatalf("zero.AssignableTo(Variant()) = %v, want indeterminate", got)
	}
}

func TestAccessors(t *testing.T) {
	base := Class("Base", nil, false)
	child := Class("Child", &base, true)
	if child.Name() != "Child" || !child.Meta() {
		t.Fatalf("class accessors = (%q, %v), want (Child, true)", child.Name(), child.Meta())
	}
	if got, ok := child.Base(); !ok || !got.Equal(base) {
		t.Fatalf("Base() = (%v, %v), want Base", got, ok)
	}

	intType := Builtin("int")
	array := Array(&intType)
	if got, ok := array.Element(); !ok || !got.Equal(intType) {
		t.Fatalf("Element() = (%v, %v), want int", got, ok)
	}
	if _, ok := Array(nil).Element(); ok {
		t.Fatal("untyped Array reported an element")
	}

	stringType := Builtin("String")
	dictionary := Dictionary(&stringType, &intType)
	if got, ok := dictionary.Key(); !ok || !got.Equal(stringType) {
		t.Fatalf("Key() = (%v, %v), want String", got, ok)
	}
	if got, ok := dictionary.Value(); !ok || !got.Equal(intType) {
		t.Fatalf("Value() = (%v, %v), want int", got, ok)
	}
}

func TestEqualIsStructural(t *testing.T) {
	baseA := Class("Base", nil, false)
	baseB := Class("Base", nil, false)
	childA := Class("Child", &baseA, false)
	childB := Class("Child", &baseB, false)
	intA := Builtin("int")
	intB := Builtin("int")
	stringA := Builtin("String")
	stringB := Builtin("String")

	equal := []struct {
		name  string
		left  Type
		right Type
	}{
		{"separate builtins", intA, intB},
		{"separate class graphs", childA, childB},
		{"typed arrays", Array(&intA), Array(&intB)},
		{"typed dictionaries", Dictionary(&stringA, &intA), Dictionary(&stringB, &intB)},
		{"unknown reasons", Unknown("same failure"), Unknown("same failure")},
	}
	for _, test := range equal {
		t.Run(test.name, func(t *testing.T) {
			if !test.left.Equal(test.right) || !test.right.Equal(test.left) {
				t.Fatalf("structurally equal types did not compare equal: %v, %v", test.left, test.right)
			}
		})
	}

	notEqual := []struct {
		name  string
		left  Type
		right Type
	}{
		{"unknown reason", Unknown("a"), Unknown("b")},
		{"kind", Builtin("Callable"), Callable()},
		{"name", Builtin("int"), Builtin("float")},
		{"meta", Class("Thing", nil, false), Class("Thing", nil, true)},
		{"base", Class("Child", &baseA, false), Class("Child", nil, false)},
		{"untyped and Variant array", Array(nil), Array(typePointer(Variant()))},
		{"untyped and Variant dictionary", Dictionary(nil, nil), Dictionary(typePointer(Variant()), typePointer(Variant()))},
	}
	for _, test := range notEqual {
		t.Run(test.name, func(t *testing.T) {
			if test.left.Equal(test.right) || test.right.Equal(test.left) {
				t.Fatalf("different types compared equal: %v, %v", test.left, test.right)
			}
		})
	}
}

func TestAssignableTo(t *testing.T) {
	intType := Builtin("int")
	floatType := Builtin("float")
	stringType := Builtin("String")
	base := Class("Base", nil, false)
	otherBase := Class("OtherBase", nil, false)
	child := Class("Child", &base, false)
	grandchild := Class("Grandchild", &child, false)
	metaBase := Class("Base", nil, true)
	sharedWithBase := Class("Shared", &base, false)
	sharedWithOtherBase := Class("Shared", &otherBase, false)

	tests := []struct {
		name string
		from Type
		to   Type
		want Assignability
	}{
		{"unknown source", Unknown("source failed"), Variant(), AssignabilityIndeterminate},
		{"unknown target", intType, Unknown("target failed"), AssignabilityIndeterminate},
		{"unknown before Variant", Unknown("failed"), Variant(), AssignabilityIndeterminate},
		{"Variant source", Variant(), intType, AssignabilityYes},
		{"Variant target", intType, Variant(), AssignabilityYes},
		{"identity", Signal(), Signal(), AssignabilityYes},
		{"int widens to float", intType, floatType, AssignabilityYes},
		{"float does not narrow to int", floatType, intType, AssignabilityNo},
		{"unrelated builtins", stringType, intType, AssignabilityNo},
		{"class to itself", child, child, AssignabilityYes},
		{"class to parent", child, base, AssignabilityYes},
		{"class to distant ancestor", grandchild, base, AssignabilityYes},
		{"parent to child", base, child, AssignabilityNo},
		{"same name with different ancestry", sharedWithBase, sharedWithOtherBase, AssignabilityNo},
		{"instance to meta", base, metaBase, AssignabilityNo},
		{"meta to instance", metaBase, base, AssignabilityNo},
		{"typed array to untyped", Array(&intType), Array(nil), AssignabilityYes},
		{"untyped array to typed", Array(nil), Array(&intType), AssignabilityNo},
		{"typed array contained widening", Array(&intType), Array(&floatType), AssignabilityYes},
		{"typed array contained narrowing", Array(&floatType), Array(&intType), AssignabilityNo},
		{"typed array contained unknown", Array(typePointer(Unknown("element failed"))), Array(&intType), AssignabilityIndeterminate},
		{"typed dictionary to untyped", Dictionary(&stringType, &intType), Dictionary(nil, nil), AssignabilityYes},
		{"untyped dictionary to typed", Dictionary(nil, nil), Dictionary(&stringType, &intType), AssignabilityNo},
		{"typed dictionary components", Dictionary(&stringType, &intType), Dictionary(&stringType, &floatType), AssignabilityYes},
		{"dictionary key mismatch", Dictionary(&intType, &intType), Dictionary(&stringType, &intType), AssignabilityNo},
		{"different container kinds", Array(&intType), Dictionary(nil, nil), AssignabilityNo},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.from.AssignableTo(test.to); got != test.want {
				t.Fatalf("%v.AssignableTo(%v) = %v, want %v", test.from, test.to, got, test.want)
			}
			if got := test.from.AssignableTo(test.to); got != test.want {
				t.Fatalf("repeated AssignableTo = %v, want stable %v", got, test.want)
			}
		})
	}
}

func TestAssignableToFailsClosedOnUnknownTargetAncestry(t *testing.T) {
	unknownBase := Unknown("target base did not resolve")
	object := Class("Object", nil, false)
	knownBase := Class("Base", &object, false)
	knownMeta := Class("Base", &object, true)

	tests := []struct {
		name   string
		source Type
		target Type
	}{
		{
			name:   "instance class",
			source: Class("Child", &knownBase, false),
			target: Class("Base", &unknownBase, false),
		},
		{
			name:   "meta class",
			source: knownMeta,
			target: Class("Base", &unknownBase, true),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.source.AssignableTo(test.target); got != AssignabilityIndeterminate {
				t.Fatalf("%v.AssignableTo(%v) = %v, want indeterminate", test.source, test.target, got)
			}
		})
	}
}

func TestAssignabilityVocabulary(t *testing.T) {
	tests := []struct {
		value Assignability
		want  string
	}{
		{AssignabilityIndeterminate, "indeterminate"},
		{AssignabilityYes, "yes"},
		{AssignabilityNo, "no"},
	}
	for _, test := range tests {
		if got := test.value.String(); got != test.want {
			t.Errorf("Assignability(%d).String() = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestConstructorsCopyTypeArguments(t *testing.T) {
	component := Builtin("int")
	array := Array(&component)
	component = Builtin("String")
	if got := array.String(); got != "Array[int]" {
		t.Fatalf("caller mutation changed Array to %q", got)
	}

	base := Class("Base", nil, false)
	child := Class("Child", &base, false)
	base = Class("Other", nil, false)
	if got, ok := child.Base(); !ok || got.Name() != "Base" {
		t.Fatalf("caller mutation changed class base to (%v, %v)", got, ok)
	}
}

func TestMalformedTypesFailClosedAndCyclesTerminate(t *testing.T) {
	unsupported := Type{node: &typeNode{kind: Kind(255)}}
	if unsupported.Kind() != KindUnknown || strings.TrimSpace(unsupported.Reason()) == "" {
		t.Fatalf("unsupported kind did not degrade to reasoned Unknown: %v", unsupported)
	}
	if got := unsupported.AssignableTo(unsupported); got != AssignabilityIndeterminate {
		t.Fatalf("unsupported kind assignability = %v, want indeterminate", got)
	}
	malformed := Type{node: &typeNode{kind: KindSignal, name: "not allowed"}}
	if malformed.Kind() != KindUnknown || malformed.AssignableTo(Signal()) != AssignabilityIndeterminate {
		t.Fatalf("malformed shape did not fail closed: %v", malformed)
	}

	cycleA := Type{node: &typeNode{kind: KindClass, name: "Cycle"}}
	cycleA.node.base = cycleA
	cycleA.node.hasBase = true
	cycleB := Type{node: &typeNode{kind: KindClass, name: "Cycle"}}
	cycleB.node.base = cycleB
	cycleB.node.hasBase = true
	if !cycleA.Equal(cycleB) {
		t.Fatal("structurally equivalent cycles did not compare equal")
	}
	if got := cycleA.AssignableTo(Class("Other", nil, false)); got != AssignabilityIndeterminate {
		t.Fatalf("cyclic ancestry assignability = %v, want indeterminate", got)
	}
}

func typePointer(value Type) *Type { return &value }
