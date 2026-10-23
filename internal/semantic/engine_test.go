package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineResolvesClassesMembersAndOperators(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"Variant", "int", "float", "Vector2", "NodePath"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct {
		name, inherits string
	}{
		{name: "Object"},
		{name: "Node", inherits: "Object"},
		{name: "CanvasItem", inherits: "Node"},
		{name: "Node2D", inherits: "CanvasItem"},
		{name: "Sprite2D", inherits: "Node2D"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.inherits))
	}
	require.NoError(t, builder.AddMethod("Node", "get_node", "Node", []EngineArgumentSpec{{Type: "NodePath"}}, false, false))
	require.NoError(t, builder.AddMethod("Object", "call", "Variant", []EngineArgumentSpec{{Type: "Variant"}, {Type: "Variant", HasDefault: true}}, false, true))
	require.NoError(t, builder.AddProperty("Node2D", "position", "Vector2"))
	require.NoError(t, builder.AddOperator("Vector2", "+", "Vector2", "Vector2"))
	require.NoError(t, builder.AddOperator("Vector2", "*", "float", "Vector2"))
	require.NoError(t, builder.AddSingleton("Engine", "Object"))
	require.NoError(t, builder.AddUtility("typeof", "int", []EngineArgumentSpec{{Type: "Variant"}}, false))

	engine, err := builder.Build()
	require.NoError(t, err)

	got := engine.Class("Sprite2D")
	for _, want := range []string{"Sprite2D", "Node2D", "CanvasItem", "Node", "Object"} {
		require.Equal(t, KindClass, got.Kind())
		require.Equal(t, want, got.Name())
		base, ok := got.Base()
		if want == "Object" {
			require.False(t, ok, "Object unexpectedly has base %q", base.Name())
			break
		}
		require.True(t, ok, "%s has no base", want)
		got = base
	}

	getNode, ok := engine.Method("Node", "get_node")
	require.True(t, ok, "Node.get_node was not indexed")
	require.Equal(t, "Node", getNode.Owner())
	require.Equal(t, "get_node", getNode.Name())
	require.Equal(t, "Node", getNode.ReturnType().Name())
	require.False(t, getNode.Static())
	require.False(t, getNode.Vararg())
	arguments := getNode.Arguments()
	require.Len(t, arguments, 1)
	require.Equal(t, "NodePath", arguments[0].Type().Name())
	require.False(t, arguments[0].HasDefault())

	call, ok := engine.Method("Object", "call")
	require.True(t, ok)
	require.Equal(t, KindVariant, call.ReturnType().Kind())
	require.True(t, call.Vararg())
	require.Len(t, call.Arguments(), 2)
	require.True(t, call.Arguments()[1].HasDefault())
	position, ok := engine.Property("Node2D", "position")
	require.True(t, ok)
	require.Equal(t, "Node2D", position.Owner())
	require.Equal(t, "position", position.Name())
	require.Equal(t, "Vector2", position.Type().Name())
	for _, operator := range []struct {
		name, right string
	}{{name: "+", right: "Vector2"}, {name: "*", right: "float"}} {
		result, ok := engine.Operator("Vector2", operator.name, operator.right)
		assert.True(t, ok)
		assert.Equal(t, "Vector2", result.Name())
	}
	singleton, ok := engine.Singleton("Engine")
	assert.True(t, ok)
	assert.Equal(t, "Object", singleton.Name())
	utility, ok := engine.Utility("typeof")
	assert.True(t, ok)
	assert.Equal(t, "int", utility.ReturnType().Name())
}

func TestEngineMissingTypesAreReasonedUnknowns(t *testing.T) {
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddClass("Object", ""))
	require.NoError(t, builder.AddClass("Broken", "MissingBase"))
	require.NoError(t, builder.AddMethod("Object", "mystery", "MissingReturn", []EngineArgumentSpec{{Type: "MissingArgument"}}, false, false))
	engine, err := builder.Build()
	require.NoError(t, err)

	for _, got := range map[string]Type{
		"class": engine.Class("MissingClass"),
		"type":  engine.ResolveType("MissingType"),
	} {
		assert.Equal(t, KindUnknown, got.Kind())
		assert.Contains(t, got.Reason(), "Missing")
	}
	brokenBase, ok := engine.Class("Broken").Base()
	require.True(t, ok)
	require.Equal(t, KindUnknown, brokenBase.Kind())
	require.Contains(t, brokenBase.Reason(), "MissingBase")
	mystery, ok := engine.Method("Object", "mystery")
	require.True(t, ok)
	require.Equal(t, KindUnknown, mystery.ReturnType().Kind())
	require.Contains(t, mystery.ReturnType().Reason(), "MissingReturn")
	argument := mystery.Arguments()[0].Type()
	require.Equal(t, KindUnknown, argument.Kind())
	require.Contains(t, argument.Reason(), "MissingArgument")
}

func TestEngineResolvesContainerAndEnumSpellings(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"Array", "Dictionary", "Callable", "Signal", "String", "int"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	require.NoError(t, builder.AddClass("Object", ""))
	engine, err := builder.Build()
	require.NoError(t, err)

	array := engine.ResolveType("typedarray::Object")
	element, ok := array.Element()
	require.Equal(t, KindArray, array.Kind())
	require.True(t, ok)
	require.Equal(t, "Object", element.Name())
	dictionary := engine.ResolveType("typeddictionary::int;String")
	key, keyOK := dictionary.Key()
	value, valueOK := dictionary.Value()
	require.Equal(t, KindDictionary, dictionary.Kind())
	require.True(t, keyOK)
	require.True(t, valueOK)
	require.Equal(t, "int", key.Name())
	require.Equal(t, "String", value.Name())
	for spelling, want := range map[string]struct {
		kind Kind
		name string
	}{
		"typedarray::enum::Object.Mode":           {kind: KindEnum, name: "Object.Mode"},
		"typedarray::bitfield::Object.Flags":      {kind: KindEnum, name: "Object.Flags"},
		"typedarray::typeddictionary::int;String": {kind: KindDictionary},
		"typedarray::2/2:First,Second":            {kind: KindBuiltin, name: "int"},
		"typedarray::24/17:Object":                {kind: KindClass, name: "Object"},
		"typedarray::24/34:Object":                {kind: KindClass, name: "Object"},
		"typedarray::27/0:":                       {kind: KindDictionary},
	} {
		resolved := engine.ResolveType(spelling)
		element, ok := resolved.Element()
		assert.Equal(t, KindArray, resolved.Kind())
		assert.True(t, ok)
		assert.Equal(t, want.kind, element.Kind())
		if want.name != "" {
			assert.Equal(t, want.name, element.Name())
		}
	}
	nested := engine.ResolveType("typedarray::typeddictionary::int;String")
	nestedDictionary, ok := nested.Element()
	require.True(t, ok, "nested typed dictionary has no array element")
	nestedKey, keyOK := nestedDictionary.Key()
	nestedValue, valueOK := nestedDictionary.Value()
	require.True(t, keyOK)
	require.True(t, valueOK)
	require.Equal(t, "int", nestedKey.Name())
	require.Equal(t, "String", nestedValue.Name())
	for _, spelling := range []string{"enum::Object.Mode", "bitfield::Object.Flags"} {
		got := engine.ResolveType(spelling)
		assert.Equal(t, KindEnum, got.Kind())
		assert.Equal(t, strings.SplitN(spelling, "::", 2)[1], got.Name())
	}
	got := engine.ResolveType("enum::Error")
	assert.Equal(t, KindEnum, got.Kind())
	assert.Equal(t, "Error", got.Name())
	for _, spelling := range []string{"enum::Missing.Mode", "bitfield::Missing.Flags"} {
		got := engine.ResolveType(spelling)
		assert.Equal(t, KindUnknown, got.Kind())
		assert.Contains(t, got.Reason(), "Missing")
	}
	assert.Equal(t, KindCallable, engine.ResolveType("Callable").Kind())
	assert.Equal(t, KindSignal, engine.ResolveType("Signal").Kind())
}

// Godot's real producer feeds typedarray:: with PropertyInfo.hint_string at
// core/extension/extension_api_dump.cpp:53-65 (commit ed1daf0bf). The numeric
// vocabulary is Variant::Type from core/variant/variant.h:96-145 at that commit.
func TestEngineResolvesEveryGodotVariantTypedArrayHint(t *testing.T) {
	wantNames := []string{
		"Variant", "bool", "int", "float", "String", "Vector2", "Vector2i", "Rect2", "Rect2i",
		"Vector3", "Vector3i", "Transform2D", "Vector4", "Vector4i", "Plane", "Quaternion", "AABB",
		"Basis", "Transform3D", "Projection", "Color", "StringName", "NodePath", "RID", "Object", "Callable",
		"Signal", "Dictionary", "Array", "PackedByteArray", "PackedInt32Array", "PackedInt64Array",
		"PackedFloat32Array", "PackedFloat64Array", "PackedStringArray", "PackedVector2Array", "PackedVector3Array",
		"PackedColorArray", "PackedVector4Array",
	}
	require.Equal(t, wantNames, godotVariantTypeNames)
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddClass("Object", ""))
	for _, name := range wantNames {
		if name == "Variant" || name == "Object" {
			continue
		}
		require.NoError(t, builder.AddBuiltin(name))
	}
	engine, err := builder.Build()
	require.NoError(t, err)
	for code, want := range wantNames {
		array := engine.ResolveType(fmt.Sprintf("typedarray::%d/0:", code))
		element, ok := array.Element()
		expected := engine.ResolveType(want)
		assert.Equal(t, KindArray, array.Kind())
		assert.True(t, ok)
		assert.Equal(t, expected.Kind(), element.Kind())
		assert.Equal(t, expected.Name(), element.Name())
	}
	malformed := engine.ResolveType("typedarray::999/0:")
	element, ok := malformed.Element()
	require.True(t, ok)
	require.Equal(t, KindUnknown, element.Kind())
	require.Contains(t, element.Reason(), "999/0:")
}

func TestEngineTypeNestingLimitCoversEveryConsumer(t *testing.T) {
	atLimit := strings.Repeat("typedarray::", EngineTypeNestingLimit) + "int"
	tooDeep := "typedarray::" + atLimit
	tests := []struct {
		name string
		add  func(*EngineBuilder, string) error
	}{
		{name: "method return", add: func(b *EngineBuilder, spelling string) error {
			return b.AddMethod("Object", "f", spelling, nil, false, false)
		}},
		{name: "method argument", add: func(b *EngineBuilder, spelling string) error {
			return b.AddMethod("Object", "f", "int", []EngineArgumentSpec{{Type: spelling}}, false, false)
		}},
		{name: "property", add: func(b *EngineBuilder, spelling string) error {
			return b.AddProperty("Object", "value", spelling)
		}},
		{name: "operator return", add: func(b *EngineBuilder, spelling string) error {
			return b.AddOperator("int", "+", "int", spelling)
		}},
		{name: "singleton", add: func(b *EngineBuilder, spelling string) error {
			return b.AddSingleton("Engine", spelling)
		}},
		{name: "utility return", add: func(b *EngineBuilder, spelling string) error {
			return b.AddUtility("f", spelling, nil, false)
		}},
		{name: "utility argument", add: func(b *EngineBuilder, spelling string) error {
			return b.AddUtility("f", "int", []EngineArgumentSpec{{Type: spelling}}, false)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, test.add(NewEngineBuilder(), atLimit), "at limit")
			err := test.add(NewEngineBuilder(), tooDeep)
			require.Error(t, err)
			require.Contains(t, err.Error(), "nesting exceeds")
		})
	}
}

func TestEngineTypeNestingLimitCoversEveryRecursiveContainerBranch(t *testing.T) {
	withinDictionaryBranch := strings.Repeat("typedarray::", EngineTypeNestingLimit-1) + "int"
	overDictionaryBranch := "typedarray::" + withinDictionaryBranch
	tests := map[string]struct{ atLimit, overLimit string }{
		"array": {
			atLimit:   strings.Repeat("typedarray::", EngineTypeNestingLimit) + "int",
			overLimit: strings.Repeat("typedarray::", EngineTypeNestingLimit+1) + "int",
		},
		"dictionary key": {
			atLimit:   "typeddictionary::" + withinDictionaryBranch + ";int",
			overLimit: "typeddictionary::" + overDictionaryBranch + ";int",
		},
		"dictionary value": {
			atLimit:   "typeddictionary::int;" + withinDictionaryBranch,
			overLimit: "typeddictionary::int;" + overDictionaryBranch,
		},
		"numeric array hint": {
			atLimit:   "typedarray::24/17:" + strings.Repeat("typedarray::", EngineTypeNestingLimit-1) + "Object",
			overLimit: "typedarray::24/17:" + strings.Repeat("typedarray::", EngineTypeNestingLimit) + "Object",
		},
	}
	for name, spellings := range tests {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, engineTypeSpelling("test", spellings.atLimit), "at limit")
			err := engineTypeSpelling("test", spellings.overLimit)
			require.Error(t, err)
			require.Contains(t, err.Error(), "nesting exceeds")
		})
	}
}

func TestEngineTypeResolutionIsBoundedWhenCalledDirectly(t *testing.T) {
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddBuiltin("Array"))
	engine, err := builder.Build()
	require.NoError(t, err)

	resolved := engine.ResolveType(strings.Repeat("typedarray::", EngineTypeNestingLimit+1) + "int")
	for depth := 0; depth < EngineTypeNestingLimit; depth++ {
		var ok bool
		resolved, ok = resolved.Element()
		require.True(t, ok, "container depth %d is not an array", depth)
	}
	require.Equal(t, KindUnknown, resolved.Kind())
	require.Contains(t, resolved.Reason(), "nesting exceeds")
}

func TestEngineRejectsEveryDuplicateIdentity(t *testing.T) {
	tests := []struct {
		name string
		add  func(*EngineBuilder) error
	}{
		{name: "builtin", add: func(b *EngineBuilder) error { return b.AddBuiltin("int") }},
		{name: "class", add: func(b *EngineBuilder) error { return b.AddClass("Object", "") }},
		{name: "method", add: func(b *EngineBuilder) error { return b.AddMethod("Object", "f", "int", nil, false, false) }},
		{name: "property", add: func(b *EngineBuilder) error { return b.AddProperty("Object", "value", "int") }},
		{name: "operator", add: func(b *EngineBuilder) error { return b.AddOperator("int", "+", "int", "int") }},
		{name: "singleton", add: func(b *EngineBuilder) error { return b.AddSingleton("Engine", "Object") }},
		{name: "utility", add: func(b *EngineBuilder) error { return b.AddUtility("typeof", "int", nil, false) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := NewEngineBuilder()
			require.NoError(t, builder.AddBuiltin("int"))
			require.NoError(t, builder.AddClass("Object", ""))
			require.NoError(t, builder.AddMethod("Object", "f", "int", nil, false, false))
			require.NoError(t, builder.AddProperty("Object", "value", "int"))
			require.NoError(t, builder.AddOperator("int", "+", "int", "int"))
			require.NoError(t, builder.AddSingleton("Engine", "Object"))
			require.NoError(t, builder.AddUtility("typeof", "int", nil, false))
			err := test.add(builder)
			require.Error(t, err)
			require.Contains(t, err.Error(), "duplicate")
		})
	}
}

func TestEngineAddVirtualValidatesAndPublishes(t *testing.T) {
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddBuiltin("int"))
	require.NoError(t, builder.AddClass("Object", ""))
	require.NoError(t, builder.AddMethod("Object", "f", "int", nil, false, false))
	err := builder.AddVirtual("Object", "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown method")
	require.NoError(t, builder.AddVirtual("Object", "f"))
	err = builder.AddVirtual("Object", "f")
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")

	engine, err := builder.Build()
	require.NoError(t, err)
	assert.True(t, engine.VirtualMethod("Object", "f"), "Object.f is not recorded virtual")
	assert.False(t, engine.VirtualMethod("Object", "missing"), "Object.missing is wrongly recorded virtual")
}

func TestEnginePublishesOnlyExplicitBuiltinConstructors(t *testing.T) {
	builder := NewEngineBuilder()
	for _, name := range []string{"Vector2", "Array", "KnownButNotConstructible", "FutureValue"} {
		require.NoError(t, builder.AddBuiltin(name))
	}
	for _, name := range []string{"Vector2", "Array", "FutureValue"} {
		require.NoError(t, builder.AddBuiltinConstructor(name))
	}
	err := builder.AddBuiltinConstructor("Vector2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
	err = builder.AddBuiltinConstructor("Missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown builtin")

	engine, err := builder.Build()
	require.NoError(t, err)
	for _, name := range []string{"Vector2", "Array", "FutureValue"} {
		got, ok := engine.builtinConstructor(name)
		want := engine.ResolveType(name)
		assert.True(t, ok)
		assert.True(t, got.Equal(want))
		assert.NotEqual(t, KindUnknown, got.Kind())
	}
	got, ok := engine.builtinConstructor("KnownButNotConstructible")
	require.False(t, ok)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())

	require.NoError(t, builder.AddBuiltinConstructor("KnownButNotConstructible"))
	newer, err := builder.Build()
	require.NoError(t, err)
	_, ok = engine.builtinConstructor("KnownButNotConstructible")
	require.False(t, ok, "builder mutation changed an already published Engine")
	got, ok = newer.builtinConstructor("KnownButNotConstructible")
	require.True(t, ok)
	require.True(t, got.Equal(newer.ResolveType("KnownButNotConstructible")))

	otherBuilder := NewEngineBuilder()
	require.NoError(t, otherBuilder.AddBuiltin("Vector2"))
	require.NoError(t, otherBuilder.AddBuiltinConstructor("Vector2"))
	other, err := otherBuilder.Build()
	require.NoError(t, err)
	require.False(t, engine == other, "separately built Engines share identity")
	got, ok = other.builtinConstructor("Vector2")
	require.True(t, ok)
	require.True(t, got.Equal(other.ResolveType("Vector2")))
}

func TestEngineBuiltinConstructorRequiresBuiltinBeforeEligibility(t *testing.T) {
	builder := NewEngineBuilder()
	for _, name := range []string{"", " Vector2", "Vector2 "} {
		assert.Error(t, builder.AddBuiltinConstructor(name))
	}
	require.NoError(t, builder.AddClass("Vector2", ""))
	err := builder.AddBuiltinConstructor("Vector2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown builtin")
}

func TestEngineReturnedArgumentsCannotMutateTheIndex(t *testing.T) {
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddBuiltin("int"))
	require.NoError(t, builder.AddClass("Object", ""))
	require.NoError(t, builder.AddMethod("Object", "f", "int", []EngineArgumentSpec{{Type: "int"}}, false, false))
	engine, err := builder.Build()
	require.NoError(t, err)
	method, _ := engine.Method("Object", "f")
	arguments := method.Arguments()
	arguments[0] = EngineArgument{}
	again, _ := engine.Method("Object", "f")
	got := again.Arguments()[0].Type()
	require.Equal(t, "int", got.Name())
}

func TestEngineRejectsInheritanceCyclesBeforePublication(t *testing.T) {
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddClass("A", "B"))
	require.NoError(t, builder.AddClass("B", "A"))
	engine, err := builder.Build()
	require.Error(t, err)
	require.Nil(t, engine)
	require.Contains(t, err.Error(), "inheritance cycle")
}

func TestEngineInheritanceDepthLimitBoundary(t *testing.T) {
	buildChain := func(t *testing.T, count int) (*Engine, error) {
		t.Helper()
		builder := NewEngineBuilder()
		parent := ""
		for index := 0; index < count; index++ {
			name := fmt.Sprintf("Class%04d", index)
			require.NoError(t, builder.AddClass(name, parent))
			parent = name
		}
		return builder.Build()
	}

	engine, err := buildChain(t, EngineInheritanceDepthLimit)
	require.NoError(t, err)
	require.NotNil(t, engine)
	resolved := engine.Class(fmt.Sprintf("Class%04d", EngineInheritanceDepthLimit-1))
	for depth := 1; depth < EngineInheritanceDepthLimit; depth++ {
		var ok bool
		resolved, ok = resolved.Base()
		require.True(t, ok, "class chain ended at depth %d", depth)
	}
	_, ok := resolved.Base()
	require.False(t, ok, "class chain exceeded the configured boundary")

	engine, err = buildChain(t, EngineInheritanceDepthLimit+1)
	require.Error(t, err)
	require.Nil(t, engine)
	require.Contains(t, err.Error(), "inheritance depth exceeds")
}

func TestEngineInheritanceDepthDoesNotLimitTotalClasses(t *testing.T) {
	builder := NewEngineBuilder()
	for index := 0; index < EngineInheritanceDepthLimit+1; index++ {
		require.NoError(t, builder.AddClass(fmt.Sprintf("Root%04d", index), ""))
	}
	engine, err := builder.Build()
	require.NoError(t, err)
	require.NotNil(t, engine)
}
