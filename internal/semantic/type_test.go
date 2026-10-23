package semantic

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			kind := test.type_.Kind()
			require.Equal(t, test.kind, kind)
			require.Equal(t, test.string, test.type_.String())
			require.Equal(t, test.string, test.type_.String())
		})
		seen[test.kind] = true
	}

	for kind := KindUnknown; kind <= KindEnum; kind++ {
		assert.True(t, seen[kind])
	}
}

func TestUnknownRequiresAReasonAndZeroFailsClosed(t *testing.T) {
	for _, reason := range []string{"", " ", "\t\n"} {
		t.Run("reason="+reason, func(t *testing.T) {
			assert.Panics(t, func() { Unknown(reason) }, "Unknown accepted an empty reason")
		})
	}

	var zero Type
	require.Equal(t, KindUnknown, zero.Kind())
	require.NotEmpty(t, strings.TrimSpace(zero.Reason()))
	assignability := zero.AssignableTo(Variant())
	require.Equal(t, AssignabilityIndeterminate, assignability)
}

func TestAccessors(t *testing.T) {
	base := Class("Base", nil, false)
	child := Class("Child", &base, true)
	require.Equal(t, "Child", child.Name())
	require.True(t, child.Meta())
	got, ok := child.Base()
	require.True(t, ok)
	require.True(t, got.Equal(base))

	intType := Builtin("int")
	array := Array(&intType)
	got, ok = array.Element()
	require.True(t, ok)
	require.True(t, got.Equal(intType))
	_, ok = Array(nil).Element()
	require.False(t, ok)

	stringType := Builtin("String")
	dictionary := Dictionary(&stringType, &intType)
	got, ok = dictionary.Key()
	require.True(t, ok)
	require.True(t, got.Equal(stringType))
	got, ok = dictionary.Value()
	require.True(t, ok)
	require.True(t, got.Equal(intType))
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
			require.True(t, test.left.Equal(test.right))
			require.True(t, test.right.Equal(test.left))
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
			require.False(t, test.left.Equal(test.right))
			require.False(t, test.right.Equal(test.left))
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
			got := test.from.AssignableTo(test.to)
			require.Equal(t, test.want, got)
			got = test.from.AssignableTo(test.to)
			require.Equal(t, test.want, got)
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
			got := test.source.AssignableTo(test.target)
			require.Equal(t, AssignabilityIndeterminate, got)
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
		got := test.value.String()
		assert.Equal(t, test.want, got)
	}
}

func TestConstructorsCopyTypeArguments(t *testing.T) {
	component := Builtin("int")
	array := Array(&component)
	component = Builtin("String")
	require.Equal(t, "Array[int]", array.String())

	base := Class("Base", nil, false)
	child := Class("Child", &base, false)
	base = Class("Other", nil, false)
	got, ok := child.Base()
	require.True(t, ok)
	require.Equal(t, "Base", got.Name())
}

func TestMalformedTypesFailClosedAndCyclesTerminate(t *testing.T) {
	unsupported := Type{node: &typeNode{kind: Kind(255)}}
	require.Equal(t, KindUnknown, unsupported.Kind())
	require.NotEmpty(t, strings.TrimSpace(unsupported.Reason()))
	assignability := unsupported.AssignableTo(unsupported)
	require.Equal(t, AssignabilityIndeterminate, assignability)
	malformed := Type{node: &typeNode{kind: KindSignal, name: "not allowed"}}
	require.Equal(t, KindUnknown, malformed.Kind())
	require.Equal(t, AssignabilityIndeterminate, malformed.AssignableTo(Signal()))

	cycleA := Type{node: &typeNode{kind: KindClass, name: "Cycle"}}
	cycleA.node.base = cycleA
	cycleA.node.hasBase = true
	cycleB := Type{node: &typeNode{kind: KindClass, name: "Cycle"}}
	cycleB.node.base = cycleB
	cycleB.node.hasBase = true
	require.True(t, cycleA.Equal(cycleB))
	require.Equal(t, AssignabilityIndeterminate, cycleA.AssignableTo(Class("Other", nil, false)))
}

func typePointer(value Type) *Type { return &value }
