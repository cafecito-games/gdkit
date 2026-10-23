package semantic

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzerReducesScalarAndCollectionLiterals(t *testing.T) {
	source := sources(t, map[string]string{
		"values.gd": "class_name Values\nfunc run():\n\tvar whole := 1\n\tvar decimal := 1.5\n\tvar text := \"text\"\n\tvar name := &\"name\"\n\tvar path := ^\"child\"\n\tvar truth := true\n\tvar nothing := null\n\tvar empty_array := []\n\tvar typed_array := [1, 2]\n\tvar nested_array := [[1], [1]]\n\tvar mixed_array := [1, \"two\"]\n\tvar variant_array := [null, null]\n\tvar unknown_array := [missing]\n\tvar mixed_unknown_array := [1, \"two\", missing]\n\tvar empty_dictionary := {}\n\tvar typed_dictionary := {\"one\": 1, \"two\": 2}\n\tvar variant_dictionary := {null: null}\n\tvar mixed_dictionary := {\"one\": 1, 2: 3}\n\tvar unknown_dictionary := {missing: 1}\n\tvar mixed_unknown_dictionary := {\"one\": 1, 2: 3, \"three\": missing}\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
			require.True(t, got.Equal(testCase.want))
		})
	}
	for _, name := range []string{"unknown_array", "mixed_unknown_array", "unknown_dictionary", "mixed_unknown_dictionary"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
		})
	}
}

func TestAnalyzerFailsClosedForNonRepresentableCollectionComponents(t *testing.T) {
	source := sources(t, map[string]string{
		"collections.gd": "class_name Collections\nenum Choice { READY }\nfunc returns_void() -> void:\n\tpass\nfunc run(choice: Choice):\n\tvar void_array := [returns_void(), returns_void()]\n\tvar meta_array := [Node, Node]\n\tvar void_dictionary := {returns_void(): returns_void()}\n\tvar meta_dictionary := {Node: Node}\n\tvar enum_array := [Choice, Choice]\n\tvar enum_dictionary := {Choice: Choice}\n\tvar enum_value_array := [choice, choice]\n\tvar enum_value_dictionary := {choice: choice}\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("collections.gd")
	for _, name := range []string{"void_array", "meta_array", "void_dictionary", "meta_dictionary", "enum_array", "enum_dictionary"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
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
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestAnalyzerFailsClosedForUnsupportedOrUnavailableExpressionForms(t *testing.T) {
	source := sources(t, map[string]string{
		"forms.gd": "class_name Forms\nfunc run():\n\tvar literal := 1\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	literal := reducerVariableValue(t, source.File("forms.gd"), "literal").(*ast.Literal)
	literal.Kind = ast.LiteralKind("future_literal")
	got := analyzer.TypeOf(literal)
	require.Equal(t, KindUnknown, got.Kind())
	require.Equal(t, `literal kind "future_literal" is unsupported`, got.Reason())

	var unavailable *ast.Literal
	got = analyzer.TypeOf(unavailable)
	require.Equal(t, KindUnknown, got.Kind())
	require.Equal(t, "expression form *ast.Literal is unavailable", got.Reason())
}

func TestAnalyzerReducesOperatorsTernariesAndSubscripts(t *testing.T) {
	source := sources(t, map[string]string{
		"operators.gd": "class_name Operators\nfunc run():\n\tvar promise := 1\n\tvar unary := -1\n\tvar negated := !1\n\tvar awaited := await promise\n\tvar binary := 1 + 2\n\tvar widened := 1 + 2.0\n\tvar symbolic_and := true && false\n\tvar symbolic_or := true || false\n\tvar word_and := true and false\n\tvar word_or := true or false\n\tvar casted := 1 as float\n\tvar checked := 1 is int\n\tvar checked_not := 1 is not int\n\tvar not_in := 1 not in 2\n\tvar ternary_equal := 1 if true else 1\n\tvar ternary_float := 1 if true else 2.0\n\tvar ternary_conflict := 1 if true else \"two\"\n\tvar missing_operator := 1 * 2\n\tvar missing_unary := ~1\n\tvar meta_operand := Node + 1\n\tvar unsupported_cast := 1 as Missing\n\tvar typed_array: Array[int] = [1]\n\tvar array_item := typed_array[0]\n\tvar plain_array := []\n\tvar plain_item := plain_array[0]\n\tvar typed_dictionary: Dictionary[String, int] = {\"one\": 1}\n\tvar dictionary_item := typed_dictionary[\"one\"]\n\tvar dynamic: Variant\n\tvar variant_item := dynamic[0]\n\tvar missing_index := typed_array[missing]\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
			require.True(t, got.Equal(testCase.want))
		})
	}
	for _, name := range []string{"missing_operator", "missing_unary", "meta_operand", "unsupported_cast", "missing_index"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
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
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	engine := reducerTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	file := source.File("strings.gd")
	stringType := engine.ResolveType("String")
	for _, name := range []string{"direct", "literal", "chained", "inferred_return"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			require.True(t, got.Equal(stringType))
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
			require.Equal(t, KindUnknown, got.Kind())
			require.Equal(t, testCase.wantReason, got.Reason())
		})
	}

	direct := reducerVariableValue(t, file, "direct")
	scope, ok := analyzer.scopes.ScopeAt(direct)
	require.True(t, ok, "direct String subscript has no recorded scope")
	result := analyzer.typeOfIn(direct, scope, reductionContextToken{})
	require.True(t, result.typeValue.Equal(stringType))
	require.Nil(t, result.member)
	require.Empty(t, result.special)
	require.False(t, result.hasConstructor)
	require.Equal(t, KindUnknown, result.constructor.Kind())
	repeated := analyzer.TypeOf(direct)
	require.True(t, repeated.Equal(stringType))
	_, cached := analyzer.cache[reductionKey{expression: direct, scope: scope.ID()}]
	require.True(t, cached, "completed String subscript was not published to the shared cache")

	foreignSource := sources(t, map[string]string{
		"strings.gd": "class_name Strings\nfunc run(text: String):\n\tvar direct := text[0]\n",
	})
	foreign := reducerVariableValue(t, foreignSource.File("strings.gd"), "direct")
	got := analyzer.TypeOf(foreign)
	require.Equal(t, KindUnknown, got.Kind())
	require.Equal(t, "expression is not indexed by this source snapshot", got.Reason())

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
		assert.Fail(t, problem)
	}
}

func TestAnalyzerStringSubscriptRequiresSelectedEngineCanonicalType(t *testing.T) {
	source := sources(t, map[string]string{
		"strings.gd": "class_name Strings\nfunc run():\n\tvar literal := \"text\"[0]\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	expression := reducerVariableValue(t, source.File("strings.gd"), "literal")

	missingBuilder := NewEngineBuilder()
	missing, err := missingBuilder.Build()
	require.NoError(t, err)
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
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
		})
	}
}

func TestAnalyzerReducesIdentifiersDeferredHeadersAndSuper(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":        "class_name Base\nfunc same() -> String:\n\tpass\nfunc named() -> int:\n\tpass\n",
		"child.gd":       "class_name Child extends Base\nfunc same():\n\tvar bare := super()\nfunc caller():\n\tvar named_result := super.named()\n",
		"identifiers.gd": "class_name Identifiers\nvar shadowed: String\nconst TOP := 1 + 2\nconst BROKEN: Missing = 1\nfunc helper() -> int:\n\tpass\nfunc values(inferred_default := 1 + 2, ordinary_default = 1 + 2, explicit_default: int = 1 + 2):\n\tvar top_use := TOP\n\tvar broken_use := BROKEN\n\tvar inferred := 1 + 2\n\tvar inferred_use := inferred\n\tvar dynamic = 1 + 2\n\tvar dynamic_use := dynamic\n\tvar explicit: float = 1\n\tvar explicit_use := explicit\n\tvar inferred_default_use := inferred_default\n\tvar ordinary_default_use := ordinary_default\n\tvar explicit_default_use := explicit_default\n\tvar inferred_method := helper\n\tvar inferred_method_call := inferred_method()\n\tvar before := later\n\tvar later := 1\n\tvar before_use := before\nfunc shadows(shadowed := 2):\n\tvar shadowed_use := shadowed\nfunc callable_default(callback := helper):\n\tvar callback_call := callback()\nfunc default_scope(late_default := later):\n\tvar later := 1\n\tvar late_default_use := late_default\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
			require.True(t, got.Equal(testCase.want))
		})
	}
	got := analyzer.TypeOf(reducerVariableValue(t, identifiers, "before_use"))
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	got = analyzer.TypeOf(reducerVariableValue(t, identifiers, "broken_use"))
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	got = analyzer.TypeOf(reducerVariableValue(t, identifiers, "late_default_use"))
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	for _, name := range []string{"inferred_method_call", "callback_call"} {
		got = analyzer.TypeOf(reducerVariableValue(t, identifiers, name))
		require.Equal(t, KindUnknown, got.Kind())
		require.NotEmpty(t, got.Reason())
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
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestAnalyzerReducesDeferredMemberHeaders(t *testing.T) {
	source := sources(t, map[string]string{
		"members.gd": "class_name Members\nconst LIMIT := 1 + 2\nvar inferred := 1 + 2\nvar dynamic = 1 + 2\nfunc values(other: Members):\n\tvar bare_inferred := inferred\n\tvar self_inferred := self.inferred\n\tvar other_inferred := other.inferred\n\tvar bare_limit := LIMIT\n\tvar self_limit := self.LIMIT\n\tvar other_limit := other.LIMIT\n\tvar bare_dynamic := dynamic\n\tvar self_dynamic := self.dynamic\n\tvar other_dynamic := other.dynamic\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestAnalyzerReducesMembersCallsAndDeferredSpecials(t *testing.T) {
	source := sources(t, map[string]string{
		"calls.gd": "class_name Calls\nvar node: Node\nvar dynamic: Variant\nvar callable: Callable\nfunc user() -> float:\n\tpass\nfunc omitted():\n\tpass\nfunc run():\n\tvar member := node.engine_method\n\tvar engine_property := node.engine_property\n\tvar engine_call := node.engine_method()\n\tvar user_call := user()\n\tvar omitted_call := omitted()\n\tvar dynamic_member := dynamic.anything\n\tvar dynamic_call := dynamic()\n\tvar callable_call := callable()\n\tvar missing_member := node.missing\n\tvar unknown_receiver := missing.member\n\tvar meta_member := Node.missing\n\tvar unknown_argument := user(missing)\n\tvar special_call := preload(\"res://thing.gd\")\n\tvar load_call := load(\"res://thing.gd\")\n\tvar constructor_call := Node()\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
			require.True(t, got.Equal(testCase.want))
		})
	}
	for _, name := range []string{"callable_call", "missing_member", "unknown_receiver", "meta_member", "unknown_argument", "special_call", "load_call", "constructor_call"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
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
	require.Empty(t, source.ParseFailures(), "builtin constructor fixture did not parse")
	engine := builtinConstructorTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	file := source.File("constructors.gd")
	for index, name := range mandatory {
		got := analyzer.TypeOf(reducerVariableValue(t, file, fmt.Sprintf("mandatory_%d", index)))
		want := engine.ResolveType(name)
		assert.True(t, got.Equal(want))
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
		assert.True(t, got.Equal(testCase.want))
	}
	for _, name := range []string{"dormant", "missing", "unknown_argument", "bare", "bare_member", "bare_member_call", "member_chain_call"} {
		got := analyzer.TypeOf(reducerVariableValue(t, file, name))
		assert.Equal(t, KindUnknown, got.Kind())
		assert.NotEmpty(t, got.Reason())
	}
	for fileName, name := range map[string]string{
		"local_shadow.gd":  "local_shadowed",
		"member_shadow.gd": "member_shadowed",
	} {
		got := analyzer.TypeOf(reducerVariableValue(t, source.File(fileName), name))
		assert.Equal(t, KindUnknown, got.Kind())
		assert.NotEmpty(t, got.Reason())
	}
	unknownArgument := analyzer.TypeOf(reducerVariableValue(t, file, "unknown_argument"))
	require.Contains(t, unknownArgument.Reason(), "not_retained")
}

func TestAnalyzerKeepsBuiltinConstructorCapabilityEngineAndSnapshotBound(t *testing.T) {
	parse := func(t *testing.T) (*memorySources, *ast.CallExpression) {
		t.Helper()
		source := sources(t, map[string]string{
			"constructors.gd": "class_name BuiltinCalls\nfunc run():\n\tvar value := Vector2(1, 2)\n",
		})
		require.Empty(t, source.ParseFailures(), "builtin constructor fixture did not parse")
		call, ok := reducerVariableValue(t, source.File("constructors.gd"), "value").(*ast.CallExpression)
		require.True(t, ok)
		return source, call
	}

	source, call := parse(t)
	engine := builtinConstructorTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	want := engine.ResolveType("Vector2")
	got := analyzer.TypeOf(call.Callee)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	for attempt := 0; attempt < 2; attempt++ {
		got = analyzer.TypeOf(call)
		require.True(t, got.Equal(want))
	}
	calleeScope, ok := analyzer.scopes.ScopeAt(call.Callee)
	require.True(t, ok, "constructor callee has no scope")
	callScope, ok := analyzer.scopes.ScopeAt(call)
	require.True(t, ok, "constructor call has no scope")
	for _, token := range []reductionContextToken{{overlay: &reductionOverlay{}}, {overlay: &reductionOverlay{}}} {
		got := analyzer.typeOfIn(call, callScope, token)
		require.True(t, got.typeValue.Equal(want))
		_, cached := analyzer.cache[reductionKey{expression: call, scope: callScope.ID(), token: token}]
		require.True(t, cached, "constructor overlay result did not retain distinct cache identity")
	}
	original := analyzer.typeOfIn(call.Callee, calleeScope, reductionContextToken{})
	cloned := cloneReductionResult(original)
	require.NotNil(t, original.builtinConstructor)
	require.NotNil(t, cloned.builtinConstructor)
	require.False(t, original.builtinConstructor == cloned.builtinConstructor)
	cycleSource, cycleCall := parse(t)
	cycleAnalyzer := NewAnalyzer(cycleSource, builtinConstructorTestEngine(t))
	cycleCallScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleCall)
	require.True(t, ok, "cycle constructor call has no scope")
	cycleCalleeScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleCall.Callee)
	require.True(t, ok, "cycle constructor callee has no scope")
	cycleKey := reductionKey{expression: cycleCall.Callee, scope: cycleCalleeScope.ID()}
	request := &reductionRequest{active: map[reductionKey]bool{cycleKey: true}}
	cycled := cycleAnalyzer.reduce(cycleCall, reductionContext{scope: cycleCallScope}, request)
	require.Equal(t, KindUnknown, cycled.typeValue.Kind())
	require.Equal(t, "expression reduction cycle", cycled.typeValue.Reason())
	got = cycleAnalyzer.TypeOf(cycleCall)
	require.True(t, got.Equal(cycleAnalyzer.engine.ResolveType("Vector2")))

	foreignSource, foreignCall := parse(t)
	got = analyzer.TypeOf(foreignCall)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	foreignEngine := builtinConstructorTestEngine(t)
	foreignAnalyzer := NewAnalyzer(foreignSource, engine)
	foreignCalleeScope, ok := foreignAnalyzer.scopes.ScopeAt(foreignCall.Callee)
	require.True(t, ok, "foreign constructor callee has no scope")
	foreignResult, ok := foreignEngine.builtinConstructor("Vector2")
	require.True(t, ok, "foreign Engine has no Vector2 constructor")
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
	got = foreignAnalyzer.TypeOf(foreignCall)
	require.Equal(t, KindUnknown, got.Kind())
	require.Contains(t, got.Reason(), "another Engine")

	invalidSource, invalidCall := parse(t)
	invalid := NewAnalyzer(invalidSource, engine)
	invalidCalleeScope, ok := invalid.scopes.ScopeAt(invalidCall.Callee)
	require.True(t, ok, "invalid constructor callee has no scope")
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
	got = invalid.TypeOf(invalidCall)
	require.Equal(t, KindUnknown, got.Kind())
	require.Contains(t, got.Reason(), "canonical")

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
		assert.Fail(t, problem)
	}
}

func TestAnalyzerRejectsBuiltinConstructorCategoryCollisions(t *testing.T) {
	builder := NewEngineBuilder()
	for _, name := range []string{"Vector2", "Collision", "int"} {
		require.NoError(t, builder.AddBuiltin(name))
	}
	for _, name := range []string{"Vector2", "Collision"} {
		require.NoError(t, builder.AddBuiltinConstructor(name))
	}
	require.NoError(t, builder.AddClass("Object", ""))
	require.NoError(t, builder.AddClass("RefCounted", "Object"))
	require.NoError(t, builder.AddUtility("Vector2", "int", nil, false))
	require.NoError(t, builder.AddSingleton("Collision", "Object"))
	engine, err := builder.Build()
	require.NoError(t, err)
	source := sources(t, map[string]string{
		"collisions.gd": "class_name Collisions\nfunc run():\n\tvar utility_collision := Vector2()\n\tvar singleton_collision := Collision()\n",
	})
	analyzer := NewAnalyzer(source, engine)
	for _, name := range []string{"utility_collision", "singleton_collision"} {
		got := analyzer.TypeOf(reducerVariableValue(t, source.File("collisions.gd"), name))
		assert.Equal(t, KindUnknown, got.Kind())
		assert.Contains(t, got.Reason(), "multiple categories")
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
	require.Empty(t, base.ParseFailures(), "real parser fixture failed")
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
	require.True(t, ok, "Actor interface is unavailable")
	broken, ok := analyzer.interfaces.Class("broken.gd")
	require.True(t, ok, "Broken interface is unavailable")
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
			require.True(t, got.Equal(testCase.want))
		})
	}
	for _, name := range []string{"missing", "parse_failed", "string_name", "wrong_arity", "dynamic_load", "interpolated_load", "unknown_argument", "alias_load", "instance", "instance_method", "invalid_new", "direct"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
		})
	}
	for _, target := range []string{"res://shadowed.gd", "res://resource-loader.gd", "res://string-name.gd", "res://wrong-arity.gd", "res://dynamic.gd", "res://%s.gd", "res://alias.gd"} {
		require.Equal(t, 0, source.callsFor("loader.gd", target))
	}
	cacheSource := &resourceTestSources{memorySources: base, resolutions: source.resolutions}
	cacheAnalyzer := NewAnalyzer(cacheSource, reducerTestEngine(t))
	cacheExpression := reducerVariableValue(t, file, "script")
	for attempt := 0; attempt < 2; attempt++ {
		got := cacheAnalyzer.TypeOf(cacheExpression)
		require.True(t, got.Equal(Class("actors/enemy.gd", nil, true)))
	}
	require.Equal(t, 1, cacheSource.callsFor("loader.gd", "res://actors/enemy.gd"))
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
	require.Empty(t, base.ParseFailures(), "real parser fixture failed")
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
			require.True(t, got.Equal(testCase.want))
		})
	}
	missing := analyzer.TypeOf(reducerVariableValue(t, file, "missing"))
	require.Equal(t, KindUnknown, missing.Kind())
	require.Contains(t, missing.Reason(), `member "MISSING" is absent from named enum`)
	arbitrary := analyzer.TypeOf(reducerVariableValue(t, file, "arbitrary_member"))
	require.Equal(t, KindUnknown, arbitrary.Kind())
	require.Contains(t, arbitrary.Reason(), `member "IDLE" is absent from Dictionary`)
}

func TestAnalyzerKeepsNamedEnumProvenanceSnapshotAndCacheBound(t *testing.T) {
	program := map[string]string{
		"actor.gd":  "class_name Actor\nenum Mode { IDLE }\n",
		"loader.gd": "class_name Loader\nfunc run():\n\tvar selected := Actor.Mode.IDLE\n",
	}
	parse := func(t *testing.T) (*memorySources, *ast.MemberExpression, *ast.MemberExpression) {
		t.Helper()
		source := sources(t, program)
		require.Empty(t, source.ParseFailures(), "real parser fixture failed")
		selected, ok := reducerVariableValue(t, source.File("loader.gd"), "selected").(*ast.MemberExpression)
		require.True(t, ok)
		enumObject, ok := selected.Object.(*ast.MemberExpression)
		require.True(t, ok)
		return source, selected, enumObject
	}
	want := Enum("actor.gd.Mode")

	source, selected, enumObject := parse(t)
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	got := analyzer.TypeOf(enumObject)
	require.True(t, got.Equal(Dictionary(nil, nil)))
	for attempt := 0; attempt < 2; attempt++ {
		got = analyzer.TypeOf(selected)
		require.True(t, got.Equal(want))
	}
	scope, ok := analyzer.scopes.ScopeAt(selected)
	require.True(t, ok, "selected expression has no recorded scope")
	for _, token := range []reductionContextToken{
		{overlay: &reductionOverlay{}},
		{overlay: &reductionOverlay{}},
	} {
		got := analyzer.typeOfIn(selected, scope, token)
		require.True(t, got.typeValue.Equal(want))
		_, cached := analyzer.cache[reductionKey{expression: selected, scope: scope.ID(), token: token}]
		require.True(t, cached, "named enum overlay result did not retain its distinct cache identity")
	}

	cloneSource, _, cloneEnumObject := parse(t)
	cloneAnalyzer := NewAnalyzer(cloneSource, reducerTestEngine(t))
	cloneScope, ok := cloneAnalyzer.scopes.ScopeAt(cloneEnumObject)
	require.True(t, ok, "enum object has no recorded scope")
	original := cloneAnalyzer.typeOfIn(cloneEnumObject, cloneScope, reductionContextToken{})
	cloned := cloneReductionResult(original)
	require.NotNil(t, original.namedEnumObject)
	require.NotNil(t, cloned.namedEnumObject)
	require.False(t, original.namedEnumObject == cloned.namedEnumObject)
	require.True(t, cloned.namedEnumObject.set == original.namedEnumObject.set)
	require.True(t, cloned.namedEnumObject.declaration == original.namedEnumObject.declaration)

	cycleSource, cycleSelected, cycleEnumObject := parse(t)
	cycleAnalyzer := NewAnalyzer(cycleSource, reducerTestEngine(t))
	cycleScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleSelected)
	require.True(t, ok, "cycle fixture selected expression has no scope")
	cycleEnumScope, ok := cycleAnalyzer.scopes.ScopeAt(cycleEnumObject)
	require.True(t, ok, "cycle fixture enum object has no scope")
	cycleKey := reductionKey{expression: cycleEnumObject, scope: cycleEnumScope.ID()}
	request := &reductionRequest{active: map[reductionKey]bool{cycleKey: true}}
	cycled := cycleAnalyzer.reduce(cycleSelected, reductionContext{scope: cycleScope}, request)
	require.Equal(t, KindUnknown, cycled.typeValue.Kind())
	require.Equal(t, "expression reduction cycle", cycled.typeValue.Reason())
	got = cycleAnalyzer.TypeOf(cycleSelected)
	require.True(t, got.Equal(want))

	foreignSource, foreignSelected, foreignEnumObject := parse(t)
	foreignAnalyzer := NewAnalyzer(foreignSource, reducerTestEngine(t))
	got = analyzer.TypeOf(foreignSelected)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	foreignScope, ok := foreignAnalyzer.scopes.ScopeAt(foreignEnumObject)
	require.True(t, ok, "foreign enum object has no scope")
	foreignKey := reductionKey{expression: foreignEnumObject, scope: foreignScope.ID()}
	foreignAnalyzer.cache[foreignKey] = reductionResult{
		typeValue:          Dictionary(nil, nil),
		namedEnumObject:    original.namedEnumObject,
		hasNamedEnumObject: true,
	}
	foreign := foreignAnalyzer.TypeOf(foreignSelected)
	require.Equal(t, KindUnknown, foreign.Kind())
	require.Contains(t, foreign.Reason(), "another interface set")

	invalidSource, invalidSelected, invalidEnumObject := parse(t)
	invalidAnalyzer := NewAnalyzer(invalidSource, reducerTestEngine(t))
	invalidScope, ok := invalidAnalyzer.scopes.ScopeAt(invalidEnumObject)
	require.True(t, ok, "invalid-cache enum object has no scope")
	invalidAnalyzer.cache[reductionKey{expression: invalidEnumObject, scope: invalidScope.ID()}] = reductionResult{
		typeValue:          Dictionary(nil, nil),
		hasNamedEnumObject: true,
	}
	invalid := invalidAnalyzer.TypeOf(invalidSelected)
	require.Equal(t, KindUnknown, invalid.Kind())
	require.Contains(t, invalid.Reason(), "provenance is unavailable")

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
		assert.Fail(t, problem)
	}
}

func TestAnalyzerFailsClosedForUnavailableInvalidAndUnsupportedResourceEvidence(t *testing.T) {
	base := sources(t, map[string]string{
		"loader.gd": "class_name Loader\nfunc run():\n\tvar unavailable := load(\"res://levels/main.tscn\")\n\tvar invalid := load(\"res://invalid.tres\")\n\tvar mismatched := load(\"res://mismatched.tres\")\n\tvar wrong_provenance := load(\"res://wrong-provenance.gd\")\n\tvar scene := load(\"res://levels/main.tscn\")\n\tvar text := load(\"res://theme.tres\")\n",
	})
	require.Empty(t, base.ParseFailures(), "real parser fixture failed")
	minimal := resourceMinimalEngine(t)
	withoutResolver := NewAnalyzer(base, minimal)
	got := withoutResolver.TypeOf(reducerVariableValue(t, base.File("loader.gd"), "unavailable"))
	require.Equal(t, KindUnknown, got.Kind())
	require.Contains(t, got.Reason(), "resolver")
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
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, strings.TrimSpace(got.Reason()))
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
				require.Equal(t, KindUnknown, got.typeValue.Kind())
				require.Equal(t, testCase.wantReason, got.typeValue.Reason())
				return
			}
			require.True(t, got.typeValue.Equal(testCase.want))
		})
	}
}

func TestAnalyzerHandlesLambdaNodePathAndPatternOnlyForms(t *testing.T) {
	source := sources(t, map[string]string{
		"forms.gd": "class_name Forms\nfunc run(value):\n\tvar closure = func(default := 1 + 2): return default\n\tvar closure_unknown = func(default := missing): return default\n\tvar shortcut = $Child\n\tvar cast = value as int\n\tmatch value:\n\t\t[var captured, _, ..]:\n\t\t\tpass\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("forms.gd")
	got := analyzer.TypeOf(reducerVariableValue(t, file, "closure"))
	require.True(t, got.Equal(Callable()))
	got = analyzer.TypeOf(reducerVariableValue(t, file, "closure_unknown"))
	require.True(t, got.Equal(Callable()))
	got = analyzer.TypeOf(reducerVariableValue(t, file, "shortcut"))
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())

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
	require.NotNil(t, typeExpression)
	got = analyzer.TypeOf(typeExpression)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	require.Len(t, patterns, 3)
	for _, pattern := range patterns {
		got = analyzer.TypeOf(pattern)
		assert.Equal(t, KindUnknown, got.Kind())
		assert.NotEmpty(t, got.Reason())
	}
}

func TestAnalyzerIsSnapshotBoundCachesCompletedResultsAndTerminatesCycles(t *testing.T) {
	source := sources(t, map[string]string{
		"cache.gd": "class_name Cache\nfunc run():\n\tvar expression := 1 + 2\n\tvar cycle := -1\n",
	})
	file := source.File("cache.gd")
	before, err := json.Marshal(file)
	require.NoError(t, err)
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	after, err := json.Marshal(file)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "NewAnalyzer mutated the parsed AST")

	expression := reducerVariableValue(t, file, "expression")
	scope, ok := analyzer.scopes.ScopeAt(expression)
	require.True(t, ok, "expression has no recorded scope")
	otherExpression := reducerVariableValue(t, file, "cycle")
	otherScope, ok := analyzer.scopes.ScopeAt(otherExpression)
	require.True(t, ok)
	require.False(t, otherScope.ID() == scope.ID(), "fixture did not produce a distinct recorded lexical scope")
	wrongContext := analyzer.typeOfIn(expression, otherScope, reductionContextToken{})
	require.Equal(t, KindUnknown, wrongContext.typeValue.Kind())
	require.Equal(t, "expression scope does not match this analyzer snapshot", wrongContext.typeValue.Reason())
	base := analyzer.typeOfIn(expression, scope, reductionContextToken{})
	require.True(t, base.typeValue.Equal(Builtin("int")))
	repeated := analyzer.TypeOf(expression)
	require.True(t, repeated.Equal(base.typeValue))
	require.Equal(t, base.typeValue.Reason(), repeated.Reason())
	afterQuery, err := json.Marshal(file)
	require.NoError(t, err)
	require.Equal(t, string(before), string(afterQuery), "TypeOf mutated the parsed AST")
	overlay := reductionContextToken{overlay: &reductionOverlay{}}
	covered := analyzer.typeOfIn(expression, scope, overlay)
	require.True(t, covered.typeValue.Equal(Builtin("int")))
	secondOverlay := reductionContextToken{overlay: &reductionOverlay{}}
	require.False(t, overlay == secondOverlay, "distinct immutable overlays share a cache token identity")
	secondCovered := analyzer.typeOfIn(expression, scope, secondOverlay)
	require.True(t, secondCovered.typeValue.Equal(Builtin("int")))
	baseKey := reductionKey{expression: expression, scope: scope.ID(), token: reductionContextToken{}}
	overlayKey := reductionKey{expression: expression, scope: scope.ID(), token: overlay}
	secondOverlayKey := reductionKey{expression: expression, scope: scope.ID(), token: secondOverlay}
	_, found := analyzer.cache[baseKey]
	require.True(t, found, "base result was not cached as a completed result")
	_, found = analyzer.cache[overlayKey]
	require.True(t, found, "overlay result reused the base cache key")
	require.False(t, overlayKey == secondOverlayKey, "distinct overlays reused one cache key")
	_, found = analyzer.cache[secondOverlayKey]
	require.True(t, found, "second overlay result reused another overlay cache key")

	secondScopes := BuildScopes(analyzer.interfaces)
	secondScope, ok := secondScopes.ScopeAt(expression)
	require.True(t, ok, "repeat scope build lost expression")
	require.Equal(t, secondScope.ID().String(), scope.ID().String())
	require.False(t, scope.ID() == secondScope.ID(), "full ScopeID identity collided across scope builds")
	require.False(t, baseKey == reductionKey{expression: expression, scope: secondScope.ID()}, "reduction key compared ScopeID.String rather than full ScopeID")

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
		assert.Fail(t, problem)
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
		assert.Fail(t, problem)
	}

	foreignSource := sources(t, map[string]string{
		"cache.gd": "class_name Cache\nfunc run():\n\tvar expression := 1 + 2\n",
	})
	foreign := reducerVariableValue(t, foreignSource.File("cache.gd"), "expression")
	got := analyzer.TypeOf(foreign)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	var absent *ast.Literal
	got = analyzer.TypeOf(absent)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	got = NewAnalyzer(nil, reducerTestEngine(t)).TypeOf(expression)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	var typedNilSource *memorySources
	got = NewAnalyzer(typedNilSource, reducerTestEngine(t)).TypeOf(expression)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	var unavailable *Analyzer
	got = unavailable.TypeOf(expression)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())

	cycle := reducerVariableValue(t, file, "cycle").(*ast.UnaryExpression)
	cycle.Operand = cycle
	got = analyzer.TypeOf(cycle)
	require.Equal(t, KindUnknown, got.Kind())
	require.Equal(t, "expression reduction cycle", got.Reason())
}

func TestAnalyzerReducesDeferredHeadersUnderTheBaseOverlay(t *testing.T) {
	source := sources(t, map[string]string{
		"overlay.gd": "class_name Overlay\nvar field := 1 + 2\nfunc locals():\n\tvar local := 1 + 2\n\tvar local_use := local\nfunc members():\n\tvar field_use := self.field\nfunc defaults(parameter := 1 + 2):\n\tvar parameter_use := parameter\nfunc lambdas():\n\tvar closure := func(default := 1 + 2): return default\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("overlay.gd")
	var parameterDefault ast.Expression
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FunctionDeclaration)
		if ok && function.Name == "defaults" && len(function.Parameters) == 1 {
			parameterDefault = function.Parameters[0].Default
		}
		return true
	})
	require.NotNil(t, parameterDefault)
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	require.True(t, ok)
	require.Len(t, lambda.Parameters, 1)
	require.NotNil(t, lambda.Parameters[0].Default)
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
			require.True(t, ok, "use expression has no recorded scope")
			overlay := reductionContextToken{overlay: &reductionOverlay{}}
			got := analyzer.typeOfIn(testCase.use, scope, overlay)
			require.True(t, got.typeValue.Equal(testCase.want))
			deferredScope, ok := analyzer.scopes.ScopeAt(testCase.deferred)
			require.True(t, ok, "deferred expression has no recorded scope")
			baseKey := reductionKey{expression: testCase.deferred, scope: deferredScope.ID(), token: reductionContextToken{}}
			overlayKey := reductionKey{expression: testCase.deferred, scope: deferredScope.ID(), token: overlay}
			_, found := analyzer.cache[baseKey]
			require.True(t, found, "deferred expression was not cached under its base context")
			_, found = analyzer.cache[overlayKey]
			require.False(t, found, "deferred expression inherited the use-site overlay context")
		})
	}
}

func TestAnalyzerNarrowingKeepsDeferredInitializersAndDefaultsAtBase(t *testing.T) {
	source := sources(t, map[string]string{
		"deferred_narrow.gd": "class_name DeferredNarrow\nfunc run(value: Variant):\n\tif value is Node:\n\t\tvar inferred := value\n\t\tvar through_inferred := inferred\n\t\tvar closure := func(default := value): return default\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("deferred_narrow.gd")
	inferredValue := reducerVariableValue(t, file, "inferred")
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	require.True(t, ok)
	require.Len(t, lambda.Parameters, 1)
	require.NotNil(t, lambda.Parameters[0].Default)
	defaultValue := lambda.Parameters[0].Default
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	got := analyzer.TypeOf(reducerVariableValue(t, file, "through_inferred"))
	require.True(t, got.Equal(Variant()))
	got = analyzer.TypeOf(lambda)
	require.True(t, got.Equal(Callable()))
	for _, expression := range []ast.Expression{inferredValue, defaultValue} {
		scope, ok := analyzer.scopes.ScopeAt(expression)
		require.True(t, ok)
		overlay := analyzer.narrow.tokenAt(expression)
		require.NotNil(t, overlay.overlay)
		baseKey := reductionKey{expression: expression, scope: scope.ID(), token: reductionContextToken{}}
		overlayKey := reductionKey{expression: expression, scope: scope.ID(), token: overlay}
		_, found := analyzer.cache[baseKey]
		require.True(t, found)
		_, found = analyzer.cache[overlayKey]
		require.False(t, found)
	}
	got = analyzer.TypeOf(inferredValue)
	require.True(t, got.Equal(reducerTestEngine(t).Class("Node")))
}

func TestAnalyzerDoesNotPublishCycleTaintedLambdaDefaults(t *testing.T) {
	source := sources(t, map[string]string{
		"cycles.gd": "class_name Cycles\nfunc run():\n\tvar holder := missing\n\tvar closure := func(default := holder): return default\n\tvar observed := holder\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("cycles.gd")
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	require.True(t, ok)
	require.Len(t, lambda.Parameters, 1)
	require.NotNil(t, lambda.Parameters[0].Default)
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
	require.NotNil(t, holder)
	require.True(t, holder.Inferred)
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
				got := analyzer.TypeOf(expression)
				require.True(t, got.Equal(Callable()))
			}
			scope, found := analyzer.scopes.ScopeAt(defaultValue)
			require.True(t, found, "lambda default has no recorded scope")
			_, found = analyzer.cache[reductionKey{expression: defaultValue, scope: scope.ID()}]
			require.False(t, found, "cycle-tainted lambda default was published to the completed cache")
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
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("fanout.gd")
	lambda, ok := reducerVariableValue(t, file, "closure").(*ast.LambdaExpression)
	require.True(t, ok)
	var holder *ast.VariableDeclaration
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.VariableDeclaration)
		if ok && declaration.Name == "holder" {
			holder = declaration
		}
		return true
	})
	require.NotNil(t, holder)
	require.True(t, holder.Inferred)
	originalHolderValue := holder.Value
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	holder.Value = lambda
	t.Cleanup(func() { holder.Value = originalHolderValue })
	top := reducerVariableValue(t, file, fmt.Sprintf("a%d", fanoutDepth))
	want := Callable()
	for level := 0; level <= fanoutDepth; level++ {
		want = reducerArray(want)
	}
	got := analyzer.TypeOf(top)
	require.True(t, got.Equal(want))
	firstFanout, ok := reducerVariableValue(t, file, "a0").(*ast.ArrayLiteral)
	require.True(t, ok)
	require.Len(t, firstFanout.Elements, 2)
	for _, consumer := range firstFanout.Elements {
		consumerScope, found := analyzer.scopes.ScopeAt(consumer)
		require.True(t, found, "fanout consumer has no recorded scope")
		consumerKey := reductionKey{expression: consumer, scope: consumerScope.ID()}
		_, found = analyzer.cache[consumerKey]
		require.False(t, found, "consumer of a cycle-tainted local result escaped into the shared cache")
	}
	scope, found := analyzer.scopes.ScopeAt(top)
	require.True(t, found, "fanout expression has no recorded scope")
	request := &reductionRequest{active: map[reductionKey]bool{}}
	fanoutResult := analyzer.reduce(top, reductionContext{scope: scope}, request)
	require.True(t, fanoutResult.typeValue.Equal(want))
	key := reductionKey{expression: top, scope: scope.ID()}
	_, found = request.local[key]
	require.True(t, found, "cycle-tainted fanout was not memoized within its request")
	_, found = analyzer.cache[key]
	require.False(t, found, "cycle-tainted fanout escaped into the shared completed cache")
}

func TestAnalyzerUnifiesTernaryClassesOnlyAcrossCompleteAncestry(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":   "class_name Base\n",
		"left.gd":   "class_name Left extends Base\n",
		"right.gd":  "class_name Right extends Base\n",
		"broken.gd": "class_name Broken extends MissingBase\n",
		"merge.gd":  "class_name Merge\nfunc run(left: Left, right: Right, broken: Broken):\n\tvar common := left if true else right\n\tvar unknown_condition := 1 if missing else 2\n\tvar incomplete := broken if true else left\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	file := source.File("merge.gd")
	wantBase := analyzer.interfaces.ResolveType("merge.gd", "Base")
	got := analyzer.TypeOf(reducerVariableValue(t, file, "common"))
	require.True(t, got.Equal(wantBase))
	for _, name := range []string{"unknown_condition", "incomplete"} {
		got = analyzer.TypeOf(reducerVariableValue(t, file, name))
		assert.Equal(t, KindUnknown, got.Kind())
		assert.NotEmpty(t, got.Reason())
	}

	loopNode := &typeNode{kind: KindClass, name: "Loop"}
	loop := Type{node: loopNode}
	loopNode.base, loopNode.hasBase = loop, true
	got = analyzer.commonType(loop, wantBase)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
}

func reducerTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "float", "String", "StringName", "NodePath", "bool", "Array", "Dictionary", "Callable", "Signal", "Variant"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "Resource", parent: "RefCounted"},
		{name: "PackedScene", parent: "Resource"},
		{name: "ResourceLoader", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
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
		require.NoError(t, builder.AddOperator(operator.left, operator.operator, operator.right, operator.result))
	}
	require.NoError(t, builder.AddMethod("Node", "engine_method", "int", nil, false, false))
	require.NoError(t, builder.AddMethod("String", "to_upper", "String", nil, false, false))
	require.NoError(t, builder.AddMethod("Node", "engine_static", "int", nil, true, false))
	require.NoError(t, builder.AddMethod("Node", "engine_instance", "int", nil, false, false))
	require.NoError(t, builder.AddProperty("Node", "engine_property", "int"))
	require.NoError(t, builder.AddMethod("PackedScene", "instantiate", "Node", nil, false, false))
	require.NoError(t, builder.AddMethod("ResourceLoader", "load", "Resource", nil, true, false))
	engine, err := builder.Build()
	require.NoError(t, err)
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
		require.NoError(t, builder.AddBuiltin(name))
	}
	for _, name := range append([]string{"PackedByteArray", "FutureValue"}, mandatory...) {
		require.NoError(t, builder.AddBuiltinConstructor(name))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddOperator("Vector2", "+", "Vector2", "Vector2"))
	require.NoError(t, builder.AddMethod("Vector2", "length", "float", nil, false, false))
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}

func resourceMinimalEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, class := range []struct{ name, parent string }{{name: "Object"}, {name: "RefCounted", parent: "Object"}} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	engine, err := builder.Build()
	require.NoError(t, err)
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
	require.Len(t, declarations, 1)
	require.NotNil(t, declarations[0].Value)
	return declarations[0].Value
}

func reducerArray(element Type) Type {
	return Array(&element)
}

func reducerDictionary(key, value Type) Type {
	return Dictionary(&key, &value)
}
