package semantic

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
)

func TestAnalyzerReducesScalarAndCollectionLiterals(t *testing.T) {
	source := sources(t, map[string]string{
		"values.gd": "class_name Values\nfunc run():\n\tvar whole := 1\n\tvar decimal := 1.5\n\tvar text := \"text\"\n\tvar name := &\"name\"\n\tvar path := ^\"child\"\n\tvar truth := true\n\tvar nothing := null\n\tvar empty_array := []\n\tvar typed_array := [1, 2]\n\tvar nested_array := [[1], [1]]\n\tvar mixed_array := [1, \"two\"]\n\tvar variant_array := [null, null]\n\tvar unknown_array := [missing]\n\tvar mixed_unknown_array := [1, \"two\", missing]\n\tvar empty_dictionary := {}\n\tvar typed_dictionary := {\"one\": 1, \"two\": 2}\n\tvar variant_dictionary := {null: null}\n\tvar mixed_dictionary := {\"one\": 1, 2: 3}\n\tvar unknown_dictionary := {missing: 1}\n\tvar mixed_unknown_dictionary := {\"one\": 1, 2: 3, \"three\": missing}\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("values.gd")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "whole", want: Builtin("int")},
		{name: "decimal", want: Builtin("float")},
		{name: "text", want: Builtin("String")},
		{name: "name", want: Builtin("StringName")},
		{name: "path", want: Builtin("NodePath")},
		{name: "truth", want: Builtin("bool")},
		{name: "nothing", want: Variant()},
		{name: "empty_array", want: Array(nil)},
		{name: "typed_array", want: reducerArray(Builtin("int"))},
		{name: "nested_array", want: reducerArray(reducerArray(Builtin("int")))},
		{name: "mixed_array", want: Array(nil)},
		{name: "variant_array", want: reducerArray(Variant())},
		{name: "empty_dictionary", want: Dictionary(nil, nil)},
		{name: "typed_dictionary", want: reducerDictionary(Builtin("String"), Builtin("int"))},
		{name: "variant_dictionary", want: reducerDictionary(Variant(), Variant())},
		{name: "mixed_dictionary", want: Dictionary(nil, nil)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
	for _, name := range []string{"unknown_array", "mixed_unknown_array", "unknown_dictionary", "mixed_unknown_dictionary"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
			}
		})
	}
}

func TestAnalyzerFailsClosedForNonRepresentableCollectionComponents(t *testing.T) {
	source := sources(t, map[string]string{
		"collections.gd": "class_name Collections\nenum Choice { READY }\nfunc returns_void() -> void:\n\tpass\nfunc run(choice: Choice):\n\tvar void_array := [returns_void(), returns_void()]\n\tvar meta_array := [Node, Node]\n\tvar void_dictionary := {returns_void(): returns_void()}\n\tvar meta_dictionary := {Node: Node}\n\tvar enum_array := [Choice, Choice]\n\tvar enum_dictionary := {Choice: Choice}\n\tvar enum_value_array := [choice, choice]\n\tvar enum_value_dictionary := {choice: choice}\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("collections.gd")
	for _, name := range []string{"void_array", "meta_array", "void_dictionary", "meta_dictionary", "enum_array", "enum_dictionary"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
			}
		})
	}
	enumValue := Enum("collections.gd.Choice")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "enum_value_array", want: reducerArray(enumValue)},
		{name: "enum_value_dictionary", want: reducerDictionary(enumValue, enumValue)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
}

func TestAnalyzerFailsClosedForUnsupportedOrUnavailableExpressionForms(t *testing.T) {
	source := sources(t, map[string]string{
		"forms.gd": "class_name Forms\nfunc run():\n\tvar literal := 1\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	literal := reducerVariableValue(t, source.File("forms.gd"), "literal").(*ast.Literal)
	literal.Kind = ast.LiteralKind("future_literal")
	if got := analyzer.TypeOf(literal); got.Kind() != KindUnknown || got.Reason() != `literal kind "future_literal" is unsupported` {
		t.Fatalf("future literal = %s (%q), want named reasoned Unknown", got, got.Reason())
	}

	var unavailable *ast.Literal
	if got := analyzer.TypeOf(unavailable); got.Kind() != KindUnknown || got.Reason() != "expression form *ast.Literal is unavailable" {
		t.Fatalf("unavailable literal = %s (%q), want form-named Unknown", got, got.Reason())
	}
}

func TestAnalyzerReducesOperatorsTernariesAndSubscripts(t *testing.T) {
	source := sources(t, map[string]string{
		"operators.gd": "class_name Operators\nfunc run():\n\tvar promise := 1\n\tvar unary := -1\n\tvar negated := !1\n\tvar awaited := await promise\n\tvar binary := 1 + 2\n\tvar widened := 1 + 2.0\n\tvar symbolic_and := true && false\n\tvar symbolic_or := true || false\n\tvar word_and := true and false\n\tvar word_or := true or false\n\tvar casted := 1 as float\n\tvar checked := 1 is int\n\tvar checked_not := 1 is not int\n\tvar not_in := 1 not in 2\n\tvar ternary_equal := 1 if true else 1\n\tvar ternary_float := 1 if true else 2.0\n\tvar ternary_conflict := 1 if true else \"two\"\n\tvar missing_operator := 1 * 2\n\tvar missing_unary := ~1\n\tvar meta_operand := Node + 1\n\tvar unsupported_cast := 1 as Missing\n\tvar typed_array: Array[int] = [1]\n\tvar array_item := typed_array[0]\n\tvar plain_array := []\n\tvar plain_item := plain_array[0]\n\tvar typed_dictionary: Dictionary[String, int] = {\"one\": 1}\n\tvar dictionary_item := typed_dictionary[\"one\"]\n\tvar dynamic: Variant\n\tvar variant_item := dynamic[0]\n\tvar missing_index := typed_array[missing]\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("operators.gd")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "unary", want: Builtin("int")},
		{name: "negated", want: Builtin("bool")},
		{name: "awaited", want: Builtin("int")},
		{name: "binary", want: Builtin("int")},
		{name: "widened", want: Builtin("float")},
		{name: "symbolic_and", want: Builtin("bool")},
		{name: "symbolic_or", want: Builtin("bool")},
		{name: "word_and", want: Builtin("bool")},
		{name: "word_or", want: Builtin("bool")},
		{name: "casted", want: Builtin("float")},
		{name: "checked", want: Builtin("bool")},
		{name: "checked_not", want: Builtin("bool")},
		{name: "not_in", want: Builtin("bool")},
		{name: "ternary_equal", want: Builtin("int")},
		{name: "ternary_float", want: Builtin("float")},
		{name: "ternary_conflict", want: Variant()},
		{name: "array_item", want: Builtin("int")},
		{name: "plain_item", want: Variant()},
		{name: "dictionary_item", want: Builtin("int")},
		{name: "variant_item", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
	for _, name := range []string{"missing_operator", "missing_unary", "meta_operand", "unsupported_cast", "missing_index"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
			}
		})
	}
}

func TestAnalyzerReducesCanonicalStringSubscripts(t *testing.T) {
	source := sources(t, map[string]string{
		"strings.gd": `class_name Strings
func first(text: String):
	return text[0]
func run(text: String):
	var direct := text[0]
	var literal := "text"[0]
	var chained := text[0].to_upper()
	var inferred_return := first(text)
	var unknown_receiver := missing_receiver[missing_index]
	var unknown_index := text[missing_index]
	var unsupported := 1[0]
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	engine := reducerTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	file := source.File("strings.gd")
	stringType := engine.ResolveType("String")
	for _, name := range []string{"direct", "literal", "chained", "inferred_return"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if !got.Equal(stringType) {
				t.Fatalf("TypeOf(%s) = %s (%q), want selected-engine %s", name, got, got.Reason(), stringType)
			}
		})
	}

	for _, testCase := range []struct {
		name       string
		wantReason string
	}{
		{name: "unknown_receiver", wantReason: `global identifier "missing_receiver" is not retained by the selected engine schema`},
		{name: "unknown_index", wantReason: `global identifier "missing_index" is not retained by the selected engine schema`},
		{name: "unsupported", wantReason: "int receiver is not subscriptable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if got.Kind() != KindUnknown || got.Reason() != testCase.wantReason {
				t.Fatalf("TypeOf(%s) = %s (%q), want Unknown %q", testCase.name, got, got.Reason(), testCase.wantReason)
			}
		})
	}

	direct := reducerVariableValue(t, file, "direct")
	scope, ok := analyzer.scopes.ScopeAt(direct)
	if !ok {
		t.Fatal("direct String subscript has no recorded scope")
	}
	result := analyzer.typeOfIn(direct, scope, reductionContextToken{})
	if !result.typeValue.Equal(stringType) || result.member != nil || result.special != "" || result.hasConstructor || result.constructor.Kind() != KindUnknown {
		t.Fatalf("direct String subscript retained provenance: %#v", result)
	}
	if repeated := analyzer.TypeOf(direct); !repeated.Equal(stringType) {
		t.Fatalf("repeated String subscript = %s (%q), want %s", repeated, repeated.Reason(), stringType)
	}
	if _, cached := analyzer.cache[reductionKey{expression: direct, scope: scope.ID()}]; !cached {
		t.Fatal("completed String subscript was not published to the shared cache")
	}

	foreignSource := sources(t, map[string]string{
		"strings.gd": "class_name Strings\nfunc run(text: String):\n\tvar direct := text[0]\n",
	})
	foreign := reducerVariableValue(t, foreignSource.File("strings.gd"), "direct")
	if got := analyzer.TypeOf(foreign); got.Kind() != KindUnknown || got.Reason() != "expression is not indexed by this source snapshot" {
		t.Fatalf("foreign String subscript = %s (%q), want snapshot-bound Unknown", got, got.Reason())
	}

	cold := NewAnalyzer(source, engine)
	const readers = 16
	start := make(chan struct{})
	problems := make(chan string, readers)
	var done sync.WaitGroup
	done.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer done.Done()
			<-start
			if got := cold.TypeOf(direct); !got.Equal(stringType) {
				problems <- fmt.Sprintf("%s (%q)", got, got.Reason())
			}
		}()
	}
	close(start)
	done.Wait()
	close(problems)
	for problem := range problems {
		t.Errorf("concurrent cold String subscript = %s, want %s", problem, stringType)
	}
}

func TestAnalyzerStringSubscriptRequiresSelectedEngineCanonicalType(t *testing.T) {
	source := sources(t, map[string]string{
		"strings.gd": "class_name Strings\nfunc run():\n\tvar literal := \"text\"[0]\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	expression := reducerVariableValue(t, source.File("strings.gd"), "literal")

	missingBuilder := NewEngineBuilder()
	missing, err := missingBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}
	wrongKind := reducerTestEngine(t)
	wrongKind.classes["String"] = Class("String", nil, false)
	malformed := reducerTestEngine(t)
	malformed.classes["String"] = Type{node: &typeNode{kind: KindBuiltin, name: "String", meta: true}}
	noncanonical := reducerTestEngine(t)
	noncanonical.classes["String"] = Builtin("SelectedStringAlias")

	for _, testCase := range []struct {
		name   string
		engine *Engine
	}{
		{name: "nil engine", engine: nil},
		{name: "missing String", engine: missing},
		{name: "wrong selected kind", engine: wrongKind},
		{name: "malformed selected type", engine: malformed},
		{name: "builtin receiver name without selected canonical equality", engine: noncanonical},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := NewAnalyzer(source, testCase.engine).TypeOf(expression)
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("String subscript = %s (%q), want reasoned Unknown", got, got.Reason())
			}
		})
	}
}

func TestAnalyzerReducesIdentifiersDeferredHeadersAndSuper(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":        "class_name Base\nfunc same() -> String:\n\tpass\nfunc named() -> int:\n\tpass\n",
		"child.gd":       "class_name Child extends Base\nfunc same():\n\tvar bare := super()\nfunc caller():\n\tvar named_result := super.named()\n",
		"identifiers.gd": "class_name Identifiers\nvar shadowed: String\nconst TOP := 1 + 2\nconst BROKEN: Missing = 1\nfunc helper() -> int:\n\tpass\nfunc values(inferred_default := 1 + 2, ordinary_default = 1 + 2, explicit_default: int = 1 + 2):\n\tvar top_use := TOP\n\tvar broken_use := BROKEN\n\tvar inferred := 1 + 2\n\tvar inferred_use := inferred\n\tvar dynamic = 1 + 2\n\tvar dynamic_use := dynamic\n\tvar explicit: float = 1\n\tvar explicit_use := explicit\n\tvar inferred_default_use := inferred_default\n\tvar ordinary_default_use := ordinary_default\n\tvar explicit_default_use := explicit_default\n\tvar inferred_method := helper\n\tvar inferred_method_call := inferred_method()\n\tvar before := later\n\tvar later := 1\n\tvar before_use := before\nfunc shadows(shadowed := 2):\n\tvar shadowed_use := shadowed\nfunc callable_default(callback := helper):\n\tvar callback_call := callback()\nfunc default_scope(late_default := later):\n\tvar later := 1\n\tvar late_default_use := late_default\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	identifiers := source.File("identifiers.gd")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "top_use", want: Builtin("int")},
		{name: "inferred_use", want: Builtin("int")},
		{name: "dynamic_use", want: Variant()},
		{name: "explicit_use", want: Builtin("float")},
		{name: "inferred_default_use", want: Builtin("int")},
		{name: "ordinary_default_use", want: Variant()},
		{name: "explicit_default_use", want: Builtin("int")},
		{name: "shadowed_use", want: Builtin("int")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, identifiers, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, identifiers, "before_use")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("use before declaration = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, identifiers, "broken_use")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("unresolved constant annotation = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, identifiers, "late_default_use")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("inferred default reduced in later use scope = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	for _, name := range []string{"inferred_method_call", "callback_call"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, identifiers, name)); got.Kind() != KindUnknown || got.Reason() == "" {
			t.Fatalf("deferred callable %s = %s (%q), want reasoned Unknown", name, got, got.Reason())
		}
	}

	child := source.File("child.gd")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "bare", want: Builtin("String")},
		{name: "named_result", want: Builtin("int")},
	} {
		t.Run("super "+testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, child, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
}

func TestAnalyzerReducesDeferredMemberHeaders(t *testing.T) {
	source := sources(t, map[string]string{
		"members.gd": "class_name Members\nconst LIMIT := 1 + 2\nvar inferred := 1 + 2\nvar dynamic = 1 + 2\nfunc values(other: Members):\n\tvar bare_inferred := inferred\n\tvar self_inferred := self.inferred\n\tvar other_inferred := other.inferred\n\tvar bare_limit := LIMIT\n\tvar self_limit := self.LIMIT\n\tvar other_limit := other.LIMIT\n\tvar bare_dynamic := dynamic\n\tvar self_dynamic := self.dynamic\n\tvar other_dynamic := other.dynamic\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("members.gd")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "bare_inferred", want: Builtin("int")},
		{name: "self_inferred", want: Builtin("int")},
		{name: "other_inferred", want: Builtin("int")},
		{name: "bare_limit", want: Builtin("int")},
		{name: "self_limit", want: Builtin("int")},
		{name: "other_limit", want: Builtin("int")},
		{name: "bare_dynamic", want: Variant()},
		{name: "self_dynamic", want: Variant()},
		{name: "other_dynamic", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
}

func TestAnalyzerReducesMembersCallsAndDeferredSpecials(t *testing.T) {
	source := sources(t, map[string]string{
		"calls.gd": "class_name Calls\nvar node: Node\nvar dynamic: Variant\nvar callable: Callable\nfunc user() -> float:\n\tpass\nfunc omitted():\n\tpass\nfunc run():\n\tvar member := node.engine_method\n\tvar engine_property := node.engine_property\n\tvar engine_call := node.engine_method()\n\tvar user_call := user()\n\tvar omitted_call := omitted()\n\tvar dynamic_member := dynamic.anything\n\tvar dynamic_call := dynamic()\n\tvar callable_call := callable()\n\tvar missing_member := node.missing\n\tvar unknown_receiver := missing.member\n\tvar meta_member := Node.missing\n\tvar unknown_argument := user(missing)\n\tvar special_call := preload(\"res://thing.gd\")\n\tvar load_call := load(\"res://thing.gd\")\n\tvar constructor_call := Node()\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("calls.gd")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "member", want: Callable()},
		{name: "engine_property", want: Builtin("int")},
		{name: "engine_call", want: Builtin("int")},
		{name: "user_call", want: Builtin("float")},
		{name: "omitted_call", want: Void()},
		{name: "dynamic_member", want: Variant()},
		{name: "dynamic_call", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
	for _, name := range []string{"callable_call", "missing_member", "unknown_receiver", "meta_member", "unknown_argument", "special_call", "load_call", "constructor_call"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
			}
		})
	}
}

func TestAnalyzerReducesSchemaBackedBuiltinConstructors(t *testing.T) {
	mandatory := []string{
		"Vector2", "Vector2i", "Vector3", "Vector3i", "Color", "Rect2", "Rect2i",
		"Transform2D", "Transform3D", "Basis", "Plane", "Quaternion", "AABB",
		"String", "StringName", "NodePath", "RID",
	}
	var program strings.Builder
	program.WriteString("class_name BuiltinCalls\nvar dynamic: Variant\nfunc inferred():\n\treturn Vector2(1, 2)\nfunc shadow(Vector2):\n\tvar shadowed := Vector2(1, 2)\nfunc run(value: Variant):\n")
	for index, name := range mandatory {
		fmt.Fprintf(&program, "\tvar mandatory_%d := %s()\n", index, name)
	}
	program.WriteString("\tvar direct := Vector2(1, 2)\n\tvar nested := Vector2i(Vector2(1, 2))\n\tvar packed := PackedByteArray([])\n\tvar future := FutureValue()\n\tvar dormant := Dormant()\n\tvar missing := MissingBuiltin()\n\tvar unknown_argument := Vector2(not_retained)\n\tvar bare := Vector2\n\tvar bare_member := Vector2.ZERO\n\tvar bare_member_call := Vector2.ZERO()\n\tvar member_chain_call := Vector2.ZERO.length()\n\tvar sum := Vector2(1, 2) + Vector2(3, 4)\n\tvar length := Vector2(1, 2).length()\n\tvar collection := [Vector2(1, 2)]\n\tvar returned := inferred()\n\tif value is Vector2:\n\t\tvar narrowed := value\n")
	source := sources(t, map[string]string{
		"constructors.gd":  program.String(),
		"local_shadow.gd":  "class_name LocalShadow\nfunc run():\n\tvar Vector2: Callable\n\tvar local_shadowed := Vector2()\n",
		"member_shadow.gd": "class_name MemberShadow\nvar Vector2: Callable\nfunc run():\n\tvar member_shadowed := Vector2()\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("builtin constructor fixture did not parse: %v", failures)
	}
	engine := builtinConstructorTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	file := source.File("constructors.gd")
	for index, name := range mandatory {
		got := analyzer.TypeOf(reducerVariableValue(t, file, fmt.Sprintf("mandatory_%d", index)))
		want := engine.ResolveType(name)
		if !got.Equal(want) {
			t.Errorf("%s constructor = %s (%q), want %s", name, got, got.Reason(), want)
		}
	}
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "direct", want: engine.ResolveType("Vector2")},
		{name: "nested", want: engine.ResolveType("Vector2i")},
		{name: "packed", want: engine.ResolveType("PackedByteArray")},
		{name: "future", want: engine.ResolveType("FutureValue")},
		{name: "sum", want: engine.ResolveType("Vector2")},
		{name: "length", want: engine.ResolveType("float")},
		{name: "collection", want: reducerArray(engine.ResolveType("Vector2"))},
		{name: "returned", want: engine.ResolveType("Vector2")},
		{name: "narrowed", want: engine.ResolveType("Vector2")},
		{name: "shadowed", want: Variant()},
	} {
		got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
		if !got.Equal(testCase.want) {
			t.Errorf("%s = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
		}
	}
	for _, name := range []string{"dormant", "missing", "unknown_argument", "bare", "bare_member", "bare_member_call", "member_chain_call"} {
		got := analyzer.TypeOf(reducerVariableValue(t, file, name))
		if got.Kind() != KindUnknown || got.Reason() == "" {
			t.Errorf("%s = %s (%q), want reasoned Unknown", name, got, got.Reason())
		}
	}
	for fileName, name := range map[string]string{
		"local_shadow.gd":  "local_shadowed",
		"member_shadow.gd": "member_shadowed",
	} {
		got := analyzer.TypeOf(reducerVariableValue(t, source.File(fileName), name))
		if got.Kind() != KindUnknown || got.Reason() == "" {
			t.Errorf("%s = %s (%q), want shadowed reasoned Unknown", name, got, got.Reason())
		}
	}
	unknownArgument := analyzer.TypeOf(reducerVariableValue(t, file, "unknown_argument"))
	if !strings.Contains(unknownArgument.Reason(), "not_retained") {
		t.Fatalf("unknown constructor argument reason = %q, want originating identifier", unknownArgument.Reason())
	}
}

func TestAnalyzerKeepsBuiltinConstructorCapabilityEngineAndSnapshotBound(t *testing.T) {
	parse := func(t *testing.T) (*memorySources, *ast.CallExpression) {
		t.Helper()
		source := sources(t, map[string]string{
			"constructors.gd": "class_name BuiltinCalls\nfunc run():\n\tvar value := Vector2(1, 2)\n",
		})
		if failures := source.ParseFailures(); len(failures) != 0 {
			t.Fatalf("builtin constructor fixture did not parse: %v", failures)
		}
		call, ok := reducerVariableValue(t, source.File("constructors.gd"), "value").(*ast.CallExpression)
		if !ok {
			t.Fatalf("value expression = %T, want CallExpression", reducerVariableValue(t, source.File("constructors.gd"), "value"))
		}
		return source, call
	}

	source, call := parse(t)
	engine := builtinConstructorTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	want := engine.ResolveType("Vector2")
	if got := analyzer.TypeOf(call.Callee); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("bare constructor queried first = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	for attempt := 0; attempt < 2; attempt++ {
		if got := analyzer.TypeOf(call); !got.Equal(want) {
			t.Fatalf("cached constructor = %s (%q), want %s", got, got.Reason(), want)
		}
	}
	calleeScope, ok := analyzer.scopes.ScopeAt(call.Callee)
	if !ok {
		t.Fatal("constructor callee has no scope")
	}
	callScope, ok := analyzer.scopes.ScopeAt(call)
	if !ok {
		t.Fatal("constructor call has no scope")
	}
	for _, token := range []reductionContextToken{{overlay: &reductionOverlay{}}, {overlay: &reductionOverlay{}}} {
		got := analyzer.typeOfIn(call, callScope, token)
		if !got.typeValue.Equal(want) {
			t.Fatalf("overlay constructor = %s (%q), want %s", got.typeValue, got.typeValue.Reason(), want)
		}
		if _, cached := analyzer.cache[reductionKey{expression: call, scope: callScope.ID(), token: token}]; !cached {
			t.Fatal("constructor overlay result did not retain distinct cache identity")
		}
	}
	original := analyzer.typeOfIn(call.Callee, calleeScope, reductionContextToken{})
	cloned := cloneReductionResult(original)
	if original.builtinConstructor == nil || cloned.builtinConstructor == nil || original.builtinConstructor == cloned.builtinConstructor {
		t.Fatal("cloneReductionResult did not defensively copy builtin constructor provenance")
	}
	cycleSource, cycleCall := parse(t)
	cycleAnalyzer := NewAnalyzer(cycleSource, builtinConstructorTestEngine(t))
	cycleCallScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleCall)
	if !ok {
		t.Fatal("cycle constructor call has no scope")
	}
	cycleCalleeScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleCall.Callee)
	if !ok {
		t.Fatal("cycle constructor callee has no scope")
	}
	cycleKey := reductionKey{expression: cycleCall.Callee, scope: cycleCalleeScope.ID()}
	request := &reductionRequest{active: map[reductionKey]bool{cycleKey: true}}
	cycled := cycleAnalyzer.reduce(cycleCall, reductionContext{scope: cycleCallScope}, request)
	if cycled.typeValue.Kind() != KindUnknown || cycled.typeValue.Reason() != "expression reduction cycle" {
		t.Fatalf("cycle-tainted constructor = %s (%q), want cycle Unknown", cycled.typeValue, cycled.typeValue.Reason())
	}
	if got := cycleAnalyzer.TypeOf(cycleCall); !got.Equal(cycleAnalyzer.engine.ResolveType("Vector2")) {
		t.Fatalf("clean read after cycle-tainted constructor = %s (%q)", got, got.Reason())
	}

	foreignSource, foreignCall := parse(t)
	if got := analyzer.TypeOf(foreignCall); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("foreign parsed call = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	foreignEngine := builtinConstructorTestEngine(t)
	foreignAnalyzer := NewAnalyzer(foreignSource, engine)
	foreignCalleeScope, ok := foreignAnalyzer.scopes.ScopeAt(foreignCall.Callee)
	if !ok {
		t.Fatal("foreign constructor callee has no scope")
	}
	foreignResult, ok := foreignEngine.builtinConstructor("Vector2")
	if !ok {
		t.Fatal("foreign Engine has no Vector2 constructor")
	}
	foreignAnalyzer.cache[reductionKey{expression: foreignCall.Callee, scope: foreignCalleeScope.ID()}] = reductionResult{
		typeValue:      Unknown(`engine type "Vector2" has no class-object representation`),
		constructor:    foreignResult,
		hasConstructor: true,
		builtinConstructor: &builtinConstructorCapability{
			engine: foreignEngine,
			index:  foreignAnalyzer.scopes,
			name:   "Vector2",
			result: foreignResult,
		},
	}
	if got := foreignAnalyzer.TypeOf(foreignCall); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "another Engine") {
		t.Fatalf("foreign Engine capability = %s (%q), want engine-bound Unknown", got, got.Reason())
	}

	invalidSource, invalidCall := parse(t)
	invalid := NewAnalyzer(invalidSource, engine)
	invalidCalleeScope, ok := invalid.scopes.ScopeAt(invalidCall.Callee)
	if !ok {
		t.Fatal("invalid constructor callee has no scope")
	}
	invalid.cache[reductionKey{expression: invalidCall.Callee, scope: invalidCalleeScope.ID()}] = reductionResult{
		typeValue:      Unknown(`engine type "Vector2" has no class-object representation`),
		constructor:    Builtin("float"),
		hasConstructor: true,
		builtinConstructor: &builtinConstructorCapability{
			engine: engine,
			index:  invalid.scopes,
			name:   "Vector2",
			result: Builtin("float"),
		},
	}
	if got := invalid.TypeOf(invalidCall); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "canonical") {
		t.Fatalf("malformed constructor capability = %s (%q), want canonical-type Unknown", got, got.Reason())
	}

	coldSource, coldCall := parse(t)
	cold := NewAnalyzer(coldSource, builtinConstructorTestEngine(t))
	const readers = 24
	start := make(chan struct{})
	problems := make(chan string, readers)
	var done sync.WaitGroup
	done.Add(readers)
	for range readers {
		go func() {
			defer done.Done()
			<-start
			got := cold.TypeOf(coldCall)
			if !got.Equal(cold.engine.ResolveType("Vector2")) {
				problems <- got.String()
			}
		}()
	}
	close(start)
	done.Wait()
	close(problems)
	for problem := range problems {
		t.Errorf("concurrent cold builtin constructor = %s", problem)
	}
}

func TestAnalyzerRejectsBuiltinConstructorCategoryCollisions(t *testing.T) {
	builder := NewEngineBuilder()
	for _, name := range []string{"Vector2", "Collision", "int"} {
		if err := builder.AddBuiltin(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Vector2", "Collision"} {
		if err := builder.AddBuiltinConstructor(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddClass("Object", ""); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddClass("RefCounted", "Object"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddUtility("Vector2", "int", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddSingleton("Collision", "Object"); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	source := sources(t, map[string]string{
		"collisions.gd": "class_name Collisions\nfunc run():\n\tvar utility_collision := Vector2()\n\tvar singleton_collision := Collision()\n",
	})
	analyzer := NewAnalyzer(source, engine)
	for _, name := range []string{"utility_collision", "singleton_collision"} {
		got := analyzer.TypeOf(reducerVariableValue(t, source.File("collisions.gd"), name))
		if got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "multiple categories") {
			t.Errorf("%s = %s (%q), want collision Unknown", name, got, got.Reason())
		}
	}
}

func TestAnalyzerResolvesLanguageSpecialResourcesAndMetaMembers(t *testing.T) {
	// Godot v4.7.2 reports typeof(Actor.Mode) as Dictionary. In particular,
	// Array[Actor.Mode] rejects [Actor.Mode] because that annotation denotes
	// the enum's int value type rather than the Dictionary enum object.
	base := sources(t, map[string]string{
		"actors/enemy.gd": "class_name Actor\nstatic var static_value: int\nvar instance_value: int\nstatic func static_method() -> int:\n\tpass\nfunc instance_method() -> int:\n\tpass\nenum Mode { IDLE }\nenum { READY }\nclass Inner:\n\tpass\n",
		"broken.gd":       "class_name Broken extends Missing\n",
		"loader.gd":       "class_name Loader\nvar actor_instance: Actor\nfunc shadow(load):\n\tvar shadowed := load(\"res://shadowed.gd\")\nfunc run():\n\tvar script := preload(\"res://actors/enemy.gd\")\n\tvar raw_script := load(r\"res://actors/enemy.gd\")\n\tvar triple_script := load(\"\"\"res://actors/enemy.gd\"\"\")\n\tvar created := preload(\"res://actors/enemy.gd\").new()\n\tvar scene := load(\"res://levels/arena.tscn\")\n\tvar scene_instance := load(\"res://levels/arena.tscn\").instantiate()\n\tvar text := preload(\"res://data/settings.tres\")\n\tvar static_method := Actor.static_method()\n\tvar static_value := Actor.static_value\n\tvar enum_type := Actor.Mode\n\tvar enum_type_array := [Actor.Mode]\n\tvar enum_type_dictionary := {Actor.Mode: 1}\n\tvar resource_enum_type := preload(\"res://actors/enemy.gd\").Mode\n\tvar resource_enum_type_array := [preload(\"res://actors/enemy.gd\").Mode]\n\tvar enum_value := Actor.READY\n\tvar inner := Actor.Inner\n\tvar actor := Actor.new()\n\tvar incomplete := Broken.new()\n\tvar node := Node.new()\n\tvar engine_static := Node.engine_static()\n\tvar resource_loader := ResourceLoader.load(\"res://resource-loader.gd\")\n\tvar missing := preload(\"res://gone.gd\")\n\tvar parse_failed := load(\"res://actors/broken.gd\")\n\tvar string_name := load(&\"res://string-name.gd\")\n\tvar wrong_arity := load(\"res://wrong-arity.gd\", \"res://wrong-arity.gd\")\n\tvar named := \"res://dynamic.gd\"\n\tvar dynamic_load := load(named)\n\tvar interpolated_load := load(\"res://%s.gd\" % \"dynamic\")\n\tvar unknown_argument := load(missing_argument)\n\tvar load_alias := load\n\tvar alias_load := load_alias(\"res://alias.gd\")\n\tvar instance := Actor.instance_value\n\tvar instance_method := Actor.instance_method()\n\tvar invalid_new := actor_instance.new()\n\tvar direct := Actor()\n",
	})
	if failures := base.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	source := &resourceTestSources{
		memorySources: base,
		resolutions: map[string]ResourceResolution{
			"res://actors/enemy.gd":    FoundResource(ResourceScript, "res://actors/enemy.gd", "actors/enemy.gd", ResourceLiteralPath),
			"res://actors/broken.gd":   FoundResource(ResourceScript, "res://actors/broken.gd", "actors/broken.gd", ResourceLiteralPath),
			"res://levels/arena.tscn":  FoundResource(ResourceScene, "res://levels/arena.tscn", "levels/arena.tscn", ResourceLiteralPath),
			"res://data/settings.tres": FoundResource(ResourceText, "res://data/settings.tres", "data/settings.tres", ResourceLiteralPath),
			"res://gone.gd":            UnresolvedResource(ResourceMissing, ResourceUnknown, "res://gone.gd", ResourceLiteralPath, "resource is absent"),
		},
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("loader.gd")
	actor, ok := analyzer.interfaces.Class("actors/enemy.gd")
	if !ok {
		t.Fatal("Actor interface is unavailable")
	}
	broken, ok := analyzer.interfaces.Class("broken.gd")
	if !ok {
		t.Fatal("Broken interface is unavailable")
	}
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "script", want: Class("actors/enemy.gd", nil, true)},
		{name: "raw_script", want: Class("actors/enemy.gd", nil, true)},
		{name: "triple_script", want: Class("actors/enemy.gd", nil, true)},
		{name: "created", want: actor.Type()},
		{name: "scene", want: reducerTestEngine(t).Class("PackedScene")},
		{name: "scene_instance", want: reducerTestEngine(t).Class("Node")},
		{name: "text", want: reducerTestEngine(t).Class("Resource")},
		{name: "static_method", want: Builtin("int")},
		{name: "static_value", want: Builtin("int")},
		{name: "enum_type", want: Dictionary(nil, nil)},
		{name: "enum_type_array", want: reducerArray(Dictionary(nil, nil))},
		{name: "enum_type_dictionary", want: reducerDictionary(Dictionary(nil, nil), Builtin("int"))},
		{name: "resource_enum_type", want: Dictionary(nil, nil)},
		{name: "resource_enum_type_array", want: reducerArray(Dictionary(nil, nil))},
		{name: "enum_value", want: Builtin("int")},
		{name: "inner", want: Class("actors/enemy.gd#Inner", nil, true)},
		{name: "actor", want: actor.Type()},
		{name: "incomplete", want: broken.Type()},
		{name: "node", want: reducerTestEngine(t).Class("Node")},
		{name: "engine_static", want: Builtin("int")},
		{name: "resource_loader", want: reducerTestEngine(t).Class("Resource")},
		{name: "shadowed", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
	for _, name := range []string{"missing", "parse_failed", "string_name", "wrong_arity", "dynamic_load", "interpolated_load", "unknown_argument", "alias_load", "instance", "instance_method", "invalid_new", "direct"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
			}
		})
	}
	for _, target := range []string{"res://shadowed.gd", "res://resource-loader.gd", "res://string-name.gd", "res://wrong-arity.gd", "res://dynamic.gd", "res://%s.gd", "res://alias.gd"} {
		if got := source.callsFor("loader.gd", target); got != 0 {
			t.Fatalf("ordinary/non-literal loader %q queried ResourceResolver %d times", target, got)
		}
	}
	cacheSource := &resourceTestSources{memorySources: base, resolutions: source.resolutions}
	cacheAnalyzer := NewAnalyzer(cacheSource, reducerTestEngine(t))
	cacheExpression := reducerVariableValue(t, file, "script")
	for attempt := 0; attempt < 2; attempt++ {
		got := cacheAnalyzer.TypeOf(cacheExpression)
		if !got.Equal(Class("actors/enemy.gd", nil, true)) {
			t.Fatalf("cached resource reduction = %s (%q), want Actor meta class", got, got.Reason())
		}
	}
	if got := cacheSource.callsFor("loader.gd", "res://actors/enemy.gd"); got != 1 {
		t.Fatalf("resource resolver calls = %d, want one completed-result cache lookup", got)
	}
}

func TestAnalyzerReducesNamedEnumObjectMembers(t *testing.T) {
	base := sources(t, map[string]string{
		"actors/actor.gd": "class_name Actor\nenum Mode { IDLE, RUNNING }\nenum { ANONYMOUS }\nclass Inner:\n\tenum State { READY }\n",
		"actors/child.gd": "class_name Child extends Actor\n",
		"generated.gd":    "class_name Generated\nenum Result { OK }\n",
		"proto.gd":        "class_name ProtoCoreUtils\nenum ProtobufError { NO_ERRORS }\n",
		"loader.gd": `class_name Loader
func run():
	var bare := Actor.Mode
	var container := [Actor.Mode]
	var direct := Actor.Mode.IDLE
	var preloaded := preload("res://actors/actor.gd").Mode.RUNNING
	var inner := Actor.Inner.State.READY
	var inherited := Child.Mode.IDLE
	var generated := Generated.Result.OK
	var protobuf := ProtoCoreUtils.ProtobufError.NO_ERRORS
	var anonymous := Actor.ANONYMOUS
	var missing := Actor.Mode.MISSING
	var arbitrary := {"IDLE": 1}
	var arbitrary_member := arbitrary.IDLE
`,
	})
	if failures := base.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	source := &resourceTestSources{
		memorySources: base,
		resolutions: map[string]ResourceResolution{
			"res://actors/actor.gd": FoundResource(ResourceScript, "res://actors/actor.gd", "actors/actor.gd", ResourceLiteralPath),
		},
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("loader.gd")
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "bare", want: Dictionary(nil, nil)},
		{name: "container", want: reducerArray(Dictionary(nil, nil))},
		{name: "direct", want: Enum("actors/actor.gd.Mode")},
		{name: "preloaded", want: Enum("actors/actor.gd.Mode")},
		{name: "inner", want: Enum("actors/actor.gd#Inner.State")},
		{name: "inherited", want: Enum("actors/actor.gd.Mode")},
		{name: "generated", want: Enum("generated.gd.Result")},
		{name: "protobuf", want: Enum("proto.gd.ProtobufError")},
		{name: "anonymous", want: Builtin("int")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			if !got.Equal(testCase.want) {
				t.Fatalf("TypeOf(%s) = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
	missing := analyzer.TypeOf(reducerVariableValue(t, file, "missing"))
	if missing.Kind() != KindUnknown || !strings.Contains(missing.Reason(), `member "MISSING" is absent from named enum`) {
		t.Fatalf("missing named-enum member = %s (%q), want enum-specific reasoned Unknown", missing, missing.Reason())
	}
	arbitrary := analyzer.TypeOf(reducerVariableValue(t, file, "arbitrary_member"))
	if arbitrary.Kind() != KindUnknown || !strings.Contains(arbitrary.Reason(), `member "IDLE" is absent from Dictionary`) {
		t.Fatalf("arbitrary Dictionary member = %s (%q), want ordinary Dictionary Unknown", arbitrary, arbitrary.Reason())
	}
}

func TestAnalyzerKeepsNamedEnumProvenanceSnapshotAndCacheBound(t *testing.T) {
	program := map[string]string{
		"actor.gd":  "class_name Actor\nenum Mode { IDLE }\n",
		"loader.gd": "class_name Loader\nfunc run():\n\tvar selected := Actor.Mode.IDLE\n",
	}
	parse := func(t *testing.T) (*memorySources, *ast.MemberExpression, *ast.MemberExpression) {
		t.Helper()
		source := sources(t, program)
		if failures := source.ParseFailures(); len(failures) != 0 {
			t.Fatalf("real parser fixture failed: %v", failures)
		}
		selected, ok := reducerVariableValue(t, source.File("loader.gd"), "selected").(*ast.MemberExpression)
		if !ok {
			t.Fatalf("selected expression = %T, want MemberExpression", reducerVariableValue(t, source.File("loader.gd"), "selected"))
		}
		enumObject, ok := selected.Object.(*ast.MemberExpression)
		if !ok {
			t.Fatalf("selected receiver = %T, want MemberExpression", selected.Object)
		}
		return source, selected, enumObject
	}
	want := Enum("actor.gd.Mode")

	source, selected, enumObject := parse(t)
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	if got := analyzer.TypeOf(enumObject); !got.Equal(Dictionary(nil, nil)) {
		t.Fatalf("enum object queried first = %s (%q), want Dictionary", got, got.Reason())
	}
	for attempt := 0; attempt < 2; attempt++ {
		if got := analyzer.TypeOf(selected); !got.Equal(want) {
			t.Fatalf("cached named enum selection = %s (%q), want %s", got, got.Reason(), want)
		}
	}
	scope, ok := analyzer.scopes.ScopeAt(selected)
	if !ok {
		t.Fatal("selected expression has no recorded scope")
	}
	for _, token := range []reductionContextToken{
		{overlay: &reductionOverlay{}},
		{overlay: &reductionOverlay{}},
	} {
		if got := analyzer.typeOfIn(selected, scope, token); !got.typeValue.Equal(want) {
			t.Fatalf("overlay named enum selection = %s (%q), want %s", got.typeValue, got.typeValue.Reason(), want)
		}
		if _, cached := analyzer.cache[reductionKey{expression: selected, scope: scope.ID(), token: token}]; !cached {
			t.Fatal("named enum overlay result did not retain its distinct cache identity")
		}
	}

	cloneSource, _, cloneEnumObject := parse(t)
	cloneAnalyzer := NewAnalyzer(cloneSource, reducerTestEngine(t))
	cloneScope, ok := cloneAnalyzer.scopes.ScopeAt(cloneEnumObject)
	if !ok {
		t.Fatal("enum object has no recorded scope")
	}
	original := cloneAnalyzer.typeOfIn(cloneEnumObject, cloneScope, reductionContextToken{})
	cloned := cloneReductionResult(original)
	if original.namedEnumObject == nil || cloned.namedEnumObject == nil || original.namedEnumObject == cloned.namedEnumObject {
		t.Fatal("cloneReductionResult did not defensively copy named enum provenance")
	}
	if cloned.namedEnumObject.set != original.namedEnumObject.set || cloned.namedEnumObject.declaration != original.namedEnumObject.declaration {
		t.Fatal("cloneReductionResult changed immutable named enum identity")
	}

	cycleSource, cycleSelected, cycleEnumObject := parse(t)
	cycleAnalyzer := NewAnalyzer(cycleSource, reducerTestEngine(t))
	cycleScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleSelected)
	if !ok {
		t.Fatal("cycle fixture selected expression has no scope")
	}
	cycleEnumScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleEnumObject)
	if !ok {
		t.Fatal("cycle fixture enum object has no scope")
	}
	cycleKey := reductionKey{expression: cycleEnumObject, scope: cycleEnumScope.ID()}
	request := &reductionRequest{active: map[reductionKey]bool{cycleKey: true}}
	cycled := cycleAnalyzer.reduce(cycleSelected, reductionContext{scope: cycleScope}, request)
	if cycled.typeValue.Kind() != KindUnknown || cycled.typeValue.Reason() != "expression reduction cycle" {
		t.Fatalf("cycle-tainted named enum selection = %s (%q), want cycle Unknown", cycled.typeValue, cycled.typeValue.Reason())
	}
	if got := cycleAnalyzer.TypeOf(cycleSelected); !got.Equal(want) {
		t.Fatalf("clean read after cycle-tainted request = %s (%q), want %s", got, got.Reason(), want)
	}

	foreignSource, foreignSelected, foreignEnumObject := parse(t)
	foreignAnalyzer := NewAnalyzer(foreignSource, reducerTestEngine(t))
	if got := analyzer.TypeOf(foreignSelected); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("foreign parsed expression = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	foreignScope, ok := foreignAnalyzer.scopes.ScopeAt(foreignEnumObject)
	if !ok {
		t.Fatal("foreign enum object has no scope")
	}
	foreignKey := reductionKey{expression: foreignEnumObject, scope: foreignScope.ID()}
	foreignAnalyzer.cache[foreignKey] = reductionResult{
		typeValue:          Dictionary(nil, nil),
		namedEnumObject:    original.namedEnumObject,
		hasNamedEnumObject: true,
	}
	foreign := foreignAnalyzer.TypeOf(foreignSelected)
	if foreign.Kind() != KindUnknown || !strings.Contains(foreign.Reason(), "another interface set") {
		t.Fatalf("foreign cached provenance = %s (%q), want interface-specific Unknown", foreign, foreign.Reason())
	}

	invalidSource, invalidSelected, invalidEnumObject := parse(t)
	invalidAnalyzer := NewAnalyzer(invalidSource, reducerTestEngine(t))
	invalidScope, ok := invalidAnalyzer.scopes.ScopeAt(invalidEnumObject)
	if !ok {
		t.Fatal("invalid-cache enum object has no scope")
	}
	invalidAnalyzer.cache[reductionKey{expression: invalidEnumObject, scope: invalidScope.ID()}] = reductionResult{
		typeValue:          Dictionary(nil, nil),
		hasNamedEnumObject: true,
	}
	invalid := invalidAnalyzer.TypeOf(invalidSelected)
	if invalid.Kind() != KindUnknown || !strings.Contains(invalid.Reason(), "provenance is unavailable") {
		t.Fatalf("invalid cached provenance = %s (%q), want provenance-specific Unknown", invalid, invalid.Reason())
	}

	coldSource, coldSelected, _ := parse(t)
	cold := NewAnalyzer(coldSource, reducerTestEngine(t))
	const readers = 24
	start := make(chan struct{})
	problems := make(chan string, readers)
	var done sync.WaitGroup
	done.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer done.Done()
			<-start
			if got := cold.TypeOf(coldSelected); !got.Equal(want) {
				problems <- got.String()
			}
		}()
	}
	close(start)
	done.Wait()
	close(problems)
	for problem := range problems {
		t.Errorf("concurrent cold named enum selection = %s, want %s", problem, want)
	}
}

func TestAnalyzerFailsClosedForUnavailableInvalidAndUnsupportedResourceEvidence(t *testing.T) {
	base := sources(t, map[string]string{
		"loader.gd": "class_name Loader\nfunc run():\n\tvar unavailable := load(\"res://levels/main.tscn\")\n\tvar invalid := load(\"res://invalid.tres\")\n\tvar mismatched := load(\"res://mismatched.tres\")\n\tvar wrong_provenance := load(\"res://wrong-provenance.gd\")\n\tvar scene := load(\"res://levels/main.tscn\")\n\tvar text := load(\"res://theme.tres\")\n",
	})
	if failures := base.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	minimal := resourceMinimalEngine(t)
	withoutResolver := NewAnalyzer(base, minimal)
	if got := withoutResolver.TypeOf(reducerVariableValue(t, base.File("loader.gd"), "unavailable")); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "resolver") {
		t.Fatalf("unavailable resolver = %s (%q), want resolver-specific Unknown", got, got.Reason())
	}
	source := &resourceTestSources{
		memorySources: base,
		resolutions: map[string]ResourceResolution{
			"res://invalid.tres":        {},
			"res://mismatched.tres":     FoundResource(ResourceText, "res://other.tres", "other.tres", ResourceLiteralPath),
			"res://wrong-provenance.gd": FoundResource(ResourceScript, "res://wrong-provenance.gd", "loader.gd", ResourceUIDClaim),
			"res://levels/main.tscn":    FoundResource(ResourceScene, "res://levels/main.tscn", "levels/main.tscn", ResourceLiteralPath),
			"res://theme.tres":          FoundResource(ResourceText, "res://theme.tres", "theme.tres", ResourceLiteralPath),
		},
	}
	analyzer := NewAnalyzer(source, minimal)
	file := source.File("loader.gd")
	for _, name := range []string{"invalid", "mismatched", "wrong_provenance", "scene", "text"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || strings.TrimSpace(got.Reason()) == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
			}
		})
	}
}

func TestReducerMemberLookupStateClosure(t *testing.T) {
	member := Member{name: "value", kind: MemberEngineProperty, typeValue: Builtin("int")}
	for _, testCase := range []struct {
		name       string
		lookup     LookupResult
		want       Type
		wantReason string
	}{
		{name: "found", lookup: LookupResult{state: LookupFound, member: &member}, want: Builtin("int")},
		{name: "unknown", lookup: LookupResult{state: LookupUnknown, reason: "owner chain is incomplete"}, wantReason: "owner chain is incomplete"},
		{name: "absent", lookup: LookupResult{state: LookupAbsent}, wantReason: `member "value" is absent from Node`},
		{name: "invalid", lookup: LookupResult{state: LookupState(99)}, wantReason: `member lookup for "value" returned invalid state`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := reduceMemberLookup(Class("Node", nil, false), "value", testCase.lookup)
			if testCase.wantReason != "" {
				if got.typeValue.Kind() != KindUnknown || got.typeValue.Reason() != testCase.wantReason {
					t.Fatalf("result = %s (%q), want Unknown(%q)", got.typeValue, got.typeValue.Reason(), testCase.wantReason)
				}
				return
			}
			if !got.typeValue.Equal(testCase.want) {
				t.Fatalf("result = %s (%q), want %s", got.typeValue, got.typeValue.Reason(), testCase.want)
			}
		})
	}
}

func TestAnalyzerHandlesLambdaNodePathAndPatternOnlyForms(t *testing.T) {
	source := sources(t, map[string]string{
		"forms.gd": "class_name Forms\nfunc run(value):\n\tvar closure = func(default := 1 + 2): return default\n\tvar closure_unknown = func(default := missing): return default\n\tvar shortcut = $Child\n\tvar cast = value as int\n\tmatch value:\n\t\t[var captured, _, ..]:\n\t\t\tpass\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("forms.gd")
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "closure")); !got.Equal(Callable()) {
		t.Fatalf("lambda = %s (%q), want Callable", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "closure_unknown")); !got.Equal(Callable()) {
		t.Fatalf("lambda with unavailable default = %s (%q), want Callable", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "shortcut")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("node-path shorthand = %s (%q), want reasoned Unknown", got, got.Reason())
	}

	var typeExpression ast.Expression
	patterns := []ast.Expression{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.TypeExpression:
			typeExpression = node
		case *ast.BindingPattern, *ast.WildcardPattern, *ast.RestPattern:
			patterns = append(patterns, node.(ast.Expression))
		}
		return true
	})
	if typeExpression == nil {
		t.Fatal("real parser fixture did not produce TypeExpression")
	}
	if got := analyzer.TypeOf(typeExpression); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("TypeExpression as value = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	if len(patterns) != 3 {
		t.Fatalf("real parser fixture patterns = %d, want binding/wildcard/rest", len(patterns))
	}
	for _, pattern := range patterns {
		if got := analyzer.TypeOf(pattern); got.Kind() != KindUnknown || got.Reason() == "" {
			t.Errorf("%T as value = %s (%q), want reasoned Unknown", pattern, got, got.Reason())
		}
	}
}

func TestAnalyzerIsSnapshotBoundCachesCompletedResultsAndTerminatesCycles(t *testing.T) {
	source := sources(t, map[string]string{
		"cache.gd": "class_name Cache\nfunc run():\n\tvar expression := 1 + 2\n\tvar cycle := -1\n",
	})
	file := source.File("cache.gd")
	before, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	after, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("NewAnalyzer mutated the parsed AST")
	}

	expression := reducerVariableValue(t, file, "expression")
	scope, ok := analyzer.scopes.ScopeAt(expression)
	if !ok {
		t.Fatal("expression has no recorded scope")
	}
	otherExpression := reducerVariableValue(t, file, "cycle")
	otherScope, ok := analyzer.scopes.ScopeAt(otherExpression)
	if !ok || otherScope.ID() == scope.ID() {
		t.Fatal("fixture did not produce a distinct recorded lexical scope")
	}
	if wrongContext := analyzer.typeOfIn(expression, otherScope, reductionContextToken{}); wrongContext.typeValue.Kind() != KindUnknown || wrongContext.typeValue.Reason() != "expression scope does not match this analyzer snapshot" {
		t.Fatalf("mismatched internal context = %s (%q), want scope-bound Unknown", wrongContext.typeValue, wrongContext.typeValue.Reason())
	}
	base := analyzer.typeOfIn(expression, scope, reductionContextToken{})
	if !base.typeValue.Equal(Builtin("int")) {
		t.Fatalf("base reduction = %s (%q), want int", base.typeValue, base.typeValue.Reason())
	}
	if repeated := analyzer.TypeOf(expression); !repeated.Equal(base.typeValue) || repeated.Reason() != base.typeValue.Reason() {
		t.Fatalf("repeated reduction = %s (%q), want %s (%q)", repeated, repeated.Reason(), base.typeValue, base.typeValue.Reason())
	}
	afterQuery, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(afterQuery) {
		t.Fatal("TypeOf mutated the parsed AST")
	}
	overlay := reductionContextToken{overlay: &reductionOverlay{}}
	covered := analyzer.typeOfIn(expression, scope, overlay)
	if !covered.typeValue.Equal(Builtin("int")) {
		t.Fatalf("overlay reduction = %s (%q), want int", covered.typeValue, covered.typeValue.Reason())
	}
	secondOverlay := reductionContextToken{overlay: &reductionOverlay{}}
	if overlay == secondOverlay {
		t.Fatal("distinct immutable overlays share a cache token identity")
	}
	secondCovered := analyzer.typeOfIn(expression, scope, secondOverlay)
	if !secondCovered.typeValue.Equal(Builtin("int")) {
		t.Fatalf("second overlay reduction = %s (%q), want int", secondCovered.typeValue, secondCovered.typeValue.Reason())
	}
	baseKey := reductionKey{expression: expression, scope: scope.ID(), token: reductionContextToken{}}
	overlayKey := reductionKey{expression: expression, scope: scope.ID(), token: overlay}
	secondOverlayKey := reductionKey{expression: expression, scope: scope.ID(), token: secondOverlay}
	if _, found := analyzer.cache[baseKey]; !found {
		t.Fatal("base result was not cached as a completed result")
	}
	if _, found := analyzer.cache[overlayKey]; !found {
		t.Fatal("overlay result reused the base cache key")
	}
	if overlayKey == secondOverlayKey {
		t.Fatal("distinct overlays reused one cache key")
	}
	if _, found := analyzer.cache[secondOverlayKey]; !found {
		t.Fatal("second overlay result reused another overlay cache key")
	}

	secondScopes := BuildScopes(analyzer.interfaces)
	secondScope, ok := secondScopes.ScopeAt(expression)
	if !ok {
		t.Fatal("repeat scope build lost expression")
	}
	if scope.ID().String() != secondScope.ID().String() {
		t.Fatalf("repeat scope build IDs no longer share their ordinal spelling: %q / %q", scope.ID(), secondScope.ID())
	}
	if scope.ID() == secondScope.ID() {
		t.Fatal("full ScopeID identity collided across scope builds")
	}
	if baseKey == (reductionKey{expression: expression, scope: secondScope.ID()}) {
		t.Fatal("reduction key compared ScopeID.String rather than full ScopeID")
	}

	const readers = 24
	const readsPerReader = 100
	problems := make(chan string, readers)
	var done sync.WaitGroup
	done.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer done.Done()
			for attempt := 0; attempt < readsPerReader; attempt++ {
				if got := analyzer.TypeOf(expression); !got.Equal(Builtin("int")) {
					problems <- got.String()
					return
				}
			}
		}()
	}
	done.Wait()
	close(problems)
	for problem := range problems {
		t.Errorf("concurrent read = %s, want int", problem)
	}

	cold := NewAnalyzer(source, reducerTestEngine(t))
	start := make(chan struct{})
	problems = make(chan string, readers)
	done = sync.WaitGroup{}
	done.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer done.Done()
			<-start
			if got := cold.TypeOf(expression); !got.Equal(Builtin("int")) {
				problems <- got.String()
			}
		}()
	}
	close(start)
	done.Wait()
	close(problems)
	for problem := range problems {
		t.Errorf("concurrent cold reduction = %s, want int", problem)
	}

	foreignSource := sources(t, map[string]string{
		"cache.gd": "class_name Cache\nfunc run():\n\tvar expression := 1 + 2\n",
	})
	foreign := reducerVariableValue(t, foreignSource.File("cache.gd"), "expression")
	if got := analyzer.TypeOf(foreign); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("foreign expression = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	var absent *ast.Literal
	if got := analyzer.TypeOf(absent); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("typed nil expression = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	if got := NewAnalyzer(nil, reducerTestEngine(t)).TypeOf(expression); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("nil source analyzer = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	var typedNilSource *memorySources
	if got := NewAnalyzer(typedNilSource, reducerTestEngine(t)).TypeOf(expression); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("typed nil source analyzer = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	var unavailable *Analyzer
	if got := unavailable.TypeOf(expression); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("nil analyzer = %s (%q), want reasoned Unknown", got, got.Reason())
	}

	cycle := reducerVariableValue(t, file, "cycle").(*ast.UnaryExpression)
	cycle.Operand = cycle
	if got := analyzer.TypeOf(cycle); got.Kind() != KindUnknown || got.Reason() != "expression reduction cycle" {
		t.Fatalf("recursive expression = %s (%q), want cycle Unknown", got, got.Reason())
	}
}

func TestAnalyzerReducesDeferredHeadersUnderTheBaseOverlay(t *testing.T) {
	source := sources(t, map[string]string{
		"overlay.gd": "class_name Overlay\nvar field := 1 + 2\nfunc locals():\n\tvar local := 1 + 2\n\tvar local_use := local\nfunc members():\n\tvar field_use := self.field\nfunc defaults(parameter := 1 + 2):\n\tvar parameter_use := parameter\nfunc lambdas():\n\tvar closure := func(default := 1 + 2): return default\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("overlay.gd")
	var parameterDefault ast.Expression
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FunctionDeclaration)
		if ok && function.Name == "defaults" && len(function.Parameters) == 1 {
			parameterDefault = function.Parameters[0].Default
		}
		return true
	})
	if parameterDefault == nil {
		t.Fatal("real parser fixture did not retain the inferred parameter default")
	}
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	if !ok || len(lambda.Parameters) != 1 || lambda.Parameters[0].Default == nil {
		t.Fatalf("real parser fixture did not retain the lambda default: %#v", lambda)
	}
	for _, testCase := range []struct {
		name     string
		use      ast.Expression
		deferred ast.Expression
		want     Type
	}{
		{name: "local", use: reducerVariableValue(t, file, "local_use"), deferred: reducerVariableValue(t, file, "local"), want: Builtin("int")},
		{name: "member", use: reducerVariableValue(t, file, "field_use"), deferred: reducerVariableValue(t, file, "field"), want: Builtin("int")},
		{name: "parameter", use: reducerVariableValue(t, file, "parameter_use"), deferred: parameterDefault, want: Builtin("int")},
		{name: "lambda", use: lambda, deferred: lambda.Parameters[0].Default, want: Callable()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := NewAnalyzer(source, reducerTestEngine(t))
			scope, ok := analyzer.scopes.ScopeAt(testCase.use)
			if !ok {
				t.Fatal("use expression has no recorded scope")
			}
			overlay := reductionContextToken{overlay: &reductionOverlay{}}
			if got := analyzer.typeOfIn(testCase.use, scope, overlay); !got.typeValue.Equal(testCase.want) {
				t.Fatalf("overlay reduction = %s (%q), want %s", got.typeValue, got.typeValue.Reason(), testCase.want)
			}
			deferredScope, ok := analyzer.scopes.ScopeAt(testCase.deferred)
			if !ok {
				t.Fatal("deferred expression has no recorded scope")
			}
			baseKey := reductionKey{expression: testCase.deferred, scope: deferredScope.ID(), token: reductionContextToken{}}
			overlayKey := reductionKey{expression: testCase.deferred, scope: deferredScope.ID(), token: overlay}
			if _, found := analyzer.cache[baseKey]; !found {
				t.Fatal("deferred expression was not cached under its base context")
			}
			if _, found := analyzer.cache[overlayKey]; found {
				t.Fatal("deferred expression inherited the use-site overlay context")
			}
		})
	}
}

func TestAnalyzerNarrowingKeepsDeferredInitializersAndDefaultsAtBase(t *testing.T) {
	source := sources(t, map[string]string{
		"deferred_narrow.gd": "class_name DeferredNarrow\nfunc run(value: Variant):\n\tif value is Node:\n\t\tvar inferred := value\n\t\tvar through_inferred := inferred\n\t\tvar closure := func(default := value): return default\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("deferred_narrow.gd")
	inferredValue := reducerVariableValue(t, file, "inferred")
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	if !ok || len(lambda.Parameters) != 1 || lambda.Parameters[0].Default == nil {
		t.Fatalf("real parser fixture lambda = %#v, want one retained default", lambda)
	}
	defaultValue := lambda.Parameters[0].Default
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "through_inferred")); !got.Equal(Variant()) {
		t.Fatalf("deferred initializer under guard = %s (%q), want base Variant", got, got.Reason())
	}
	if got := analyzer.TypeOf(lambda); !got.Equal(Callable()) {
		t.Fatalf("lambda with deferred default = %s (%q), want Callable", got, got.Reason())
	}
	for _, expression := range []ast.Expression{inferredValue, defaultValue} {
		scope, ok := analyzer.scopes.ScopeAt(expression)
		if !ok {
			t.Fatalf("deferred expression %T has no recorded scope", expression)
		}
		overlay := analyzer.narrow.tokenAt(expression)
		if overlay.overlay == nil {
			t.Fatalf("deferred expression %T was not written under the guarded source view", expression)
		}
		baseKey := reductionKey{expression: expression, scope: scope.ID(), token: reductionContextToken{}}
		overlayKey := reductionKey{expression: expression, scope: scope.ID(), token: overlay}
		if _, found := analyzer.cache[baseKey]; !found {
			t.Fatalf("deferred expression %T was not reduced under base context", expression)
		}
		if _, found := analyzer.cache[overlayKey]; found {
			t.Fatalf("deferred expression %T inherited a guarded overlay", expression)
		}
	}
	if got := analyzer.TypeOf(inferredValue); !got.Equal(reducerTestEngine(t).Class("Node")) {
		t.Fatalf("direct source query of guarded initializer = %s (%q), want Node", got, got.Reason())
	}
}

func TestAnalyzerDoesNotPublishCycleTaintedLambdaDefaults(t *testing.T) {
	source := sources(t, map[string]string{
		"cycles.gd": "class_name Cycles\nfunc run():\n\tvar holder := missing\n\tvar closure := func(default := holder): return default\n\tvar observed := holder\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("cycles.gd")
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	if !ok || len(lambda.Parameters) != 1 || lambda.Parameters[0].Default == nil {
		t.Fatalf("real parser fixture did not produce one lambda default: %#v", lambda)
	}
	defaultValue := lambda.Parameters[0].Default
	observed := reducerVariableValue(t, file, "observed")
	var holder *ast.VariableDeclaration
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.VariableDeclaration)
		if ok && declaration.Name == "holder" {
			holder = declaration
		}
		return true
	})
	if holder == nil || !holder.Inferred {
		t.Fatalf("real parser fixture did not produce inferred holder: %#v", holder)
	}
	originalHolderValue := holder.Value

	for _, testCase := range []struct {
		name  string
		order []ast.Expression
	}{
		{name: "use then default", order: []ast.Expression{observed, defaultValue}},
		{name: "default then use", order: []ast.Expression{defaultValue, observed}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			holder.Value = originalHolderValue
			analyzer := NewAnalyzer(source, reducerTestEngine(t))
			// Build scopes from the valid parser fixture, then form the malformed
			// retained-header cycle that the reducer must terminate without
			// publishing a query-order-dependent child result.
			holder.Value = lambda
			t.Cleanup(func() { holder.Value = originalHolderValue })
			for _, expression := range testCase.order {
				if got := analyzer.TypeOf(expression); !got.Equal(Callable()) {
					t.Fatalf("TypeOf(%T) = %s (%q), want Callable", expression, got, got.Reason())
				}
			}
			scope, found := analyzer.scopes.ScopeAt(defaultValue)
			if !found {
				t.Fatal("lambda default has no recorded scope")
			}
			if _, found := analyzer.cache[reductionKey{expression: defaultValue, scope: scope.ID()}]; found {
				t.Fatal("cycle-tainted lambda default was published to the completed cache")
			}
		})
	}
}

func TestAnalyzerMemoizesCycleTaintedFanoutWithinOneRequest(t *testing.T) {
	const fanoutDepth = 30
	var program strings.Builder
	program.WriteString("class_name Fanout\nfunc run():\n\tvar holder := missing\n\tvar closure := func(default := holder): return default\n")
	for level := 0; level <= fanoutDepth; level++ {
		child := "holder"
		if level > 0 {
			child = fmt.Sprintf("a%d", level-1)
		}
		fmt.Fprintf(&program, "\tvar a%d := [%s, %s]\n", level, child, child)
	}
	source := sources(t, map[string]string{"fanout.gd": program.String()})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("fanout.gd")
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	if !ok {
		t.Fatalf("real parser fixture did not produce a lambda: %#v", lambda)
	}
	var holder *ast.VariableDeclaration
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.VariableDeclaration)
		if ok && declaration.Name == "holder" {
			holder = declaration
		}
		return true
	})
	if holder == nil || !holder.Inferred {
		t.Fatalf("real parser fixture did not produce inferred holder: %#v", holder)
	}
	originalHolderValue := holder.Value
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	holder.Value = lambda
	t.Cleanup(func() { holder.Value = originalHolderValue })
	top := reducerVariableValue(t, file, fmt.Sprintf("a%d", fanoutDepth))
	want := Callable()
	for level := 0; level <= fanoutDepth; level++ {
		want = reducerArray(want)
	}
	if got := analyzer.TypeOf(top); !got.Equal(want) {
		t.Fatalf("fanout TypeOf = %s (%q), want %s", got, got.Reason(), want)
	}
	firstFanout, ok := reducerVariableValue(t, file, "a0").(*ast.ArrayLiteral)
	if !ok || len(firstFanout.Elements) != 2 {
		t.Fatalf("real parser fixture did not produce two fanout consumers: %#v", firstFanout)
	}
	for _, consumer := range firstFanout.Elements {
		consumerScope, found := analyzer.scopes.ScopeAt(consumer)
		if !found {
			t.Fatal("fanout consumer has no recorded scope")
		}
		consumerKey := reductionKey{expression: consumer, scope: consumerScope.ID()}
		if _, found := analyzer.cache[consumerKey]; found {
			t.Fatal("consumer of a cycle-tainted local result escaped into the shared cache")
		}
	}
	scope, found := analyzer.scopes.ScopeAt(top)
	if !found {
		t.Fatal("fanout expression has no recorded scope")
	}
	request := &reductionRequest{active: map[reductionKey]bool{}}
	if got := analyzer.reduce(top, reductionContext{scope: scope}, request); !got.typeValue.Equal(want) {
		t.Fatalf("fanout request result = %s (%q), want %s", got.typeValue, got.typeValue.Reason(), want)
	}
	key := reductionKey{expression: top, scope: scope.ID()}
	if _, found := request.local[key]; !found {
		t.Fatal("cycle-tainted fanout was not memoized within its request")
	}
	if _, found := analyzer.cache[key]; found {
		t.Fatal("cycle-tainted fanout escaped into the shared completed cache")
	}
}

func TestAnalyzerUnifiesTernaryClassesOnlyAcrossCompleteAncestry(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":   "class_name Base\n",
		"left.gd":   "class_name Left extends Base\n",
		"right.gd":  "class_name Right extends Base\n",
		"broken.gd": "class_name Broken extends MissingBase\n",
		"merge.gd":  "class_name Merge\nfunc run(left: Left, right: Right, broken: Broken):\n\tvar common := left if true else right\n\tvar unknown_condition := 1 if missing else 2\n\tvar incomplete := broken if true else left\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("merge.gd")
	wantBase := analyzer.interfaces.ResolveType("merge.gd", "Base")
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "common")); !got.Equal(wantBase) {
		t.Fatalf("common class ternary = %s (%q), want %s", got, got.Reason(), wantBase)
	}
	for _, name := range []string{"unknown_condition", "incomplete"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); got.Kind() != KindUnknown || got.Reason() == "" {
			t.Errorf("%s = %s (%q), want reasoned Unknown", name, got, got.Reason())
		}
	}

	loopNode := &typeNode{kind: KindClass, name: "Loop"}
	loop := Type{node: loopNode}
	loopNode.base, loopNode.hasBase = loop, true
	if got := analyzer.commonType(loop, wantBase); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("cyclic class ancestry = %s (%q), want reasoned Unknown", got, got.Reason())
	}
}

func reducerTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "float", "String", "StringName", "NodePath", "bool", "Array", "Dictionary", "Callable", "Signal", "Variant"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "Resource", parent: "RefCounted"},
		{name: "PackedScene", parent: "Resource"},
		{name: "ResourceLoader", parent: "Object"},
	} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
	}
	for _, operator := range []struct {
		left, operator, right, result string
	}{
		{left: "int", operator: "unary-", result: "int"},
		{left: "int", operator: "not", result: "bool"},
		{left: "int", operator: "+", right: "int", result: "int"},
		{left: "int", operator: "+", right: "float", result: "float"},
		{left: "bool", operator: "and", right: "bool", result: "bool"},
		{left: "bool", operator: "or", right: "bool", result: "bool"},
		{left: "int", operator: "in", right: "int", result: "bool"},
	} {
		if err := builder.AddOperator(operator.left, operator.operator, operator.right, operator.result); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddMethod("Node", "engine_method", "int", nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("String", "to_upper", "String", nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("Node", "engine_static", "int", nil, true, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("Node", "engine_instance", "int", nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddProperty("Node", "engine_property", "int"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("PackedScene", "instantiate", "Node", nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("ResourceLoader", "load", "Resource", nil, true, false); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func builtinConstructorTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	mandatory := []string{
		"Vector2", "Vector2i", "Vector3", "Vector3i", "Color", "Rect2", "Rect2i",
		"Transform2D", "Transform3D", "Basis", "Plane", "Quaternion", "AABB",
		"String", "StringName", "NodePath", "RID",
	}
	for _, name := range append([]string{
		"Variant", "bool", "int", "float", "Array", "Dictionary", "Callable", "Signal",
		"PackedByteArray", "FutureValue", "Dormant",
	}, mandatory...) {
		if err := builder.AddBuiltin(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range append([]string{"PackedByteArray", "FutureValue"}, mandatory...) {
		if err := builder.AddBuiltinConstructor(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
	} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddOperator("Vector2", "+", "Vector2", "Vector2"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("Vector2", "length", "float", nil, false, false); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func resourceMinimalEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, class := range []struct{ name, parent string }{{name: "Object"}, {name: "RefCounted", parent: "Object"}} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

type resourceTestSources struct {
	*memorySources
	resolutions map[string]ResourceResolution
	mu          sync.Mutex
	calls       map[string]int
}

func (s *resourceTestSources) ResolvePreloadResource(from, target string) ResourceResolution {
	return s.resolveResource("preload", from, target)
}

func (s *resourceTestSources) ResolveLoadResource(from, target string) ResourceResolution {
	return s.resolveResource("load", from, target)
}

func (s *resourceTestSources) resolveResource(special, from, target string) ResourceResolution {
	s.mu.Lock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[special+"\x00"+from+"\x00"+target]++
	s.mu.Unlock()
	if resolution, ok := s.resolutions[target]; ok {
		return resolution
	}
	return UnresolvedResource(ResourceMissing, ResourceUnknown, target, ResourceLiteralPath, "resource is absent")
}

func (s *resourceTestSources) callsFor(from, target string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls["preload\x00"+from+"\x00"+target] + s.calls["load\x00"+from+"\x00"+target]
}

func reducerVariableValue(t *testing.T, file *ast.File, name string) ast.Expression {
	t.Helper()
	var declarations []*ast.VariableDeclaration
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.VariableDeclaration)
		if ok && declaration.Name == name {
			declarations = append(declarations, declaration)
		}
		return true
	})
	if len(declarations) != 1 || declarations[0].Value == nil {
		t.Fatalf("variable %q declarations = %#v, want one initialized declaration", name, declarations)
	}
	return declarations[0].Value
}

func reducerArray(element Type) Type {
	return Array(&element)
}

func reducerDictionary(key, value Type) Type {
	return Dictionary(&key, &value)
}
