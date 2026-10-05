package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestEngineResolvesClassesMembersAndOperators(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"Variant", "int", "float", "Vector2", "NodePath"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
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
		if err := builder.AddClass(class.name, class.inherits); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddMethod("Node", "get_node", "Node", []EngineArgumentSpec{{Type: "NodePath"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("Object", "call", "Variant", []EngineArgumentSpec{{Type: "Variant"}, {Type: "Variant", HasDefault: true}}, false, true); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddProperty("Node2D", "position", "Vector2"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddOperator("Vector2", "+", "Vector2", "Vector2"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddOperator("Vector2", "*", "float", "Vector2"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddSingleton("Engine", "Object"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddUtility("typeof", "int", []EngineArgumentSpec{{Type: "Variant"}}, false); err != nil {
		t.Fatal(err)
	}

	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Class("Sprite2D")
	for _, want := range []string{"Sprite2D", "Node2D", "CanvasItem", "Node", "Object"} {
		if got.Kind() != KindClass || got.Name() != want {
			t.Fatalf("class chain at %q = %v %q (%q)", want, got.Kind(), got.Name(), got.Reason())
		}
		base, ok := got.Base()
		if want == "Object" {
			if ok {
				t.Fatalf("Object unexpectedly has base %q", base.Name())
			}
			break
		}
		if !ok {
			t.Fatalf("%s has no base", want)
		}
		got = base
	}

	getNode, ok := engine.Method("Node", "get_node")
	if !ok {
		t.Fatal("Node.get_node was not indexed")
	}
	if getNode.Owner() != "Node" || getNode.Name() != "get_node" || getNode.ReturnType().Name() != "Node" || getNode.Static() || getNode.Vararg() {
		t.Fatalf("Node.get_node = %#v", getNode)
	}
	arguments := getNode.Arguments()
	if len(arguments) != 1 || arguments[0].Type().Name() != "NodePath" || arguments[0].HasDefault() {
		t.Fatalf("Node.get_node arguments = %#v", arguments)
	}

	call, ok := engine.Method("Object", "call")
	if !ok || call.ReturnType().Kind() != KindVariant || !call.Vararg() || len(call.Arguments()) != 2 || !call.Arguments()[1].HasDefault() {
		t.Fatalf("Object.call = %#v, %t", call, ok)
	}
	position, ok := engine.Property("Node2D", "position")
	if !ok || position.Owner() != "Node2D" || position.Name() != "position" || position.Type().Name() != "Vector2" {
		t.Fatalf("Node2D.position = %#v, %t", position, ok)
	}
	for _, operator := range []struct {
		name, right string
	}{{name: "+", right: "Vector2"}, {name: "*", right: "float"}} {
		result, ok := engine.Operator("Vector2", operator.name, operator.right)
		if !ok || result.Name() != "Vector2" {
			t.Errorf("Vector2 %s %s = %q, %t", operator.name, operator.right, result.Name(), ok)
		}
	}
	if singleton, ok := engine.Singleton("Engine"); !ok || singleton.Name() != "Object" {
		t.Errorf("Engine singleton = %q, %t", singleton.Name(), ok)
	}
	if utility, ok := engine.Utility("typeof"); !ok || utility.ReturnType().Name() != "int" {
		t.Errorf("typeof = %#v, %t", utility, ok)
	}
}

func TestEngineMissingTypesAreReasonedUnknowns(t *testing.T) {
	builder := NewEngineBuilder()
	if err := builder.AddClass("Object", ""); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddClass("Broken", "MissingBase"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("Object", "mystery", "MissingReturn", []EngineArgumentSpec{{Type: "MissingArgument"}}, false, false); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string]Type{
		"class": engine.Class("MissingClass"),
		"type":  engine.ResolveType("MissingType"),
	} {
		if got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "Missing") {
			t.Errorf("missing %s = kind %v reason %q", name, got.Kind(), got.Reason())
		}
	}
	brokenBase, ok := engine.Class("Broken").Base()
	if !ok || brokenBase.Kind() != KindUnknown || !strings.Contains(brokenBase.Reason(), "MissingBase") {
		t.Fatalf("Broken base = kind %v reason %q, %t", brokenBase.Kind(), brokenBase.Reason(), ok)
	}
	mystery, ok := engine.Method("Object", "mystery")
	if !ok || mystery.ReturnType().Kind() != KindUnknown || !strings.Contains(mystery.ReturnType().Reason(), "MissingReturn") {
		t.Fatalf("mystery return = %#v, %t", mystery, ok)
	}
	if argument := mystery.Arguments()[0].Type(); argument.Kind() != KindUnknown || !strings.Contains(argument.Reason(), "MissingArgument") {
		t.Fatalf("mystery argument = kind %v reason %q", argument.Kind(), argument.Reason())
	}
}

func TestEngineResolvesContainerAndEnumSpellings(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"Array", "Dictionary", "Callable", "Signal", "String", "int"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddClass("Object", ""); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	array := engine.ResolveType("typedarray::Object")
	if element, ok := array.Element(); array.Kind() != KindArray || !ok || element.Name() != "Object" {
		t.Fatalf("typed array = kind %v element %q, %t", array.Kind(), element.Name(), ok)
	}
	dictionary := engine.ResolveType("typeddictionary::int;String")
	key, keyOK := dictionary.Key()
	value, valueOK := dictionary.Value()
	if dictionary.Kind() != KindDictionary || !keyOK || !valueOK || key.Name() != "int" || value.Name() != "String" {
		t.Fatalf("typed dictionary = kind %v key %q/%t value %q/%t", dictionary.Kind(), key.Name(), keyOK, value.Name(), valueOK)
	}
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
		if resolved.Kind() != KindArray || !ok || element.Kind() != want.kind || (want.name != "" && element.Name() != want.name) {
			t.Errorf("%s = kind %v element %v %q, %t", spelling, resolved.Kind(), element.Kind(), element.Name(), ok)
		}
	}
	nested := engine.ResolveType("typedarray::typeddictionary::int;String")
	nestedDictionary, ok := nested.Element()
	if !ok {
		t.Fatal("nested typed dictionary has no array element")
	}
	nestedKey, keyOK := nestedDictionary.Key()
	nestedValue, valueOK := nestedDictionary.Value()
	if !keyOK || !valueOK || nestedKey.Name() != "int" || nestedValue.Name() != "String" {
		t.Fatalf("nested typed dictionary = key %q/%t value %q/%t", nestedKey.Name(), keyOK, nestedValue.Name(), valueOK)
	}
	for _, spelling := range []string{"enum::Object.Mode", "bitfield::Object.Flags"} {
		if got := engine.ResolveType(spelling); got.Kind() != KindEnum || got.Name() != strings.SplitN(spelling, "::", 2)[1] {
			t.Errorf("%s = kind %v name %q", spelling, got.Kind(), got.Name())
		}
	}
	if got := engine.ResolveType("enum::Error"); got.Kind() != KindEnum || got.Name() != "Error" {
		t.Errorf("global enum = kind %v name %q", got.Kind(), got.Name())
	}
	for _, spelling := range []string{"enum::Missing.Mode", "bitfield::Missing.Flags"} {
		got := engine.ResolveType(spelling)
		if got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "Missing") {
			t.Errorf("%s = kind %v reason %q", spelling, got.Kind(), got.Reason())
		}
	}
	if engine.ResolveType("Callable").Kind() != KindCallable || engine.ResolveType("Signal").Kind() != KindSignal {
		t.Error("Callable or Signal did not use its dedicated semantic kind")
	}
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
	if !reflect.DeepEqual(godotVariantTypeNames, wantNames) {
		t.Fatalf("Godot Variant::Type mapping = %#v", godotVariantTypeNames)
	}
	builder := NewEngineBuilder()
	if err := builder.AddClass("Object", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range wantNames {
		if name == "Variant" || name == "Object" {
			continue
		}
		if err := builder.AddBuiltin(name); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	for code, want := range wantNames {
		array := engine.ResolveType(fmt.Sprintf("typedarray::%d/0:", code))
		element, ok := array.Element()
		expected := engine.ResolveType(want)
		if array.Kind() != KindArray || !ok || element.Kind() != expected.Kind() || element.Name() != expected.Name() {
			t.Errorf("Variant::Type %d = array %v element %v %q, %t; want %v %q", code, array.Kind(), element.Kind(), element.Name(), ok, expected.Kind(), expected.Name())
		}
	}
	malformed := engine.ResolveType("typedarray::999/0:")
	element, ok := malformed.Element()
	if !ok || element.Kind() != KindUnknown || !strings.Contains(element.Reason(), "999/0:") {
		t.Fatalf("out-of-range Variant::Type = element %v reason %q, %t", element.Kind(), element.Reason(), ok)
	}
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
			if err := builder.AddBuiltin("int"); err != nil {
				t.Fatal(err)
			}
			if err := builder.AddClass("Object", ""); err != nil {
				t.Fatal(err)
			}
			if err := builder.AddMethod("Object", "f", "int", nil, false, false); err != nil {
				t.Fatal(err)
			}
			if err := builder.AddProperty("Object", "value", "int"); err != nil {
				t.Fatal(err)
			}
			if err := builder.AddOperator("int", "+", "int", "int"); err != nil {
				t.Fatal(err)
			}
			if err := builder.AddSingleton("Engine", "Object"); err != nil {
				t.Fatal(err)
			}
			if err := builder.AddUtility("typeof", "int", nil, false); err != nil {
				t.Fatal(err)
			}
			if err := test.add(builder); err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate %s = %v", test.name, err)
			}
		})
	}
}

func TestEngineReturnedArgumentsCannotMutateTheIndex(t *testing.T) {
	builder := NewEngineBuilder()
	if err := builder.AddBuiltin("int"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddClass("Object", ""); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("Object", "f", "int", []EngineArgumentSpec{{Type: "int"}}, false, false); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	method, _ := engine.Method("Object", "f")
	arguments := method.Arguments()
	arguments[0] = EngineArgument{}
	again, _ := engine.Method("Object", "f")
	if got := again.Arguments()[0].Type(); got.Name() != "int" {
		t.Fatalf("mutated stored argument to kind %v name %q", got.Kind(), got.Name())
	}
}

func TestEngineRejectsInheritanceCyclesBeforePublication(t *testing.T) {
	builder := NewEngineBuilder()
	if err := builder.AddClass("A", "B"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddClass("B", "A"); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err == nil || engine != nil || !strings.Contains(err.Error(), "inheritance cycle") {
		t.Fatalf("Build() = %+v, %v", engine, err)
	}
}
