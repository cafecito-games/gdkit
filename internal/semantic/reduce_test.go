package semantic

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
)

func TestAnalyzerReducesScalarAndCollectionLiterals(t *testing.T) {
	source := sources(t, map[string]string{
		"values.gd": "class_name Values\nfunc run():\n\tvar whole := 1\n\tvar decimal := 1.5\n\tvar text := \"text\"\n\tvar name := &\"name\"\n\tvar path := ^\"child\"\n\tvar truth := true\n\tvar nothing := null\n\tvar empty_array := []\n\tvar typed_array := [1, 2]\n\tvar nested_array := [[1], [1]]\n\tvar mixed_array := [1, \"two\"]\n\tvar variant_array := [null, null]\n\tvar unknown_array := [missing]\n\tvar empty_dictionary := {}\n\tvar typed_dictionary := {\"one\": 1, \"two\": 2}\n\tvar variant_dictionary := {null: null}\n\tvar mixed_dictionary := {\"one\": 1, 2: 3}\n\tvar unknown_dictionary := {missing: 1}\n",
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
	for _, name := range []string{"unknown_array", "unknown_dictionary"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
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
		"operators.gd": "class_name Operators\nfunc run():\n\tvar promise := 1\n\tvar unary := -1\n\tvar negated := !1\n\tvar awaited := await promise\n\tvar binary := 1 + 2\n\tvar widened := 1 + 2.0\n\tvar casted := 1 as float\n\tvar checked := 1 is int\n\tvar ternary_equal := 1 if true else 1\n\tvar ternary_float := 1 if true else 2.0\n\tvar ternary_conflict := 1 if true else \"two\"\n\tvar missing_operator := 1 * 2\n\tvar missing_unary := ~1\n\tvar meta_operand := Node + 1\n\tvar unsupported_cast := 1 as Missing\n\tvar typed_array: Array[int] = [1]\n\tvar array_item := typed_array[0]\n\tvar plain_array := []\n\tvar plain_item := plain_array[0]\n\tvar typed_dictionary: Dictionary[String, int] = {\"one\": 1}\n\tvar dictionary_item := typed_dictionary[\"one\"]\n\tvar dynamic: Variant\n\tvar variant_item := dynamic[0]\n\tvar missing_index := typed_array[missing]\n",
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
		{name: "casted", want: Builtin("float")},
		{name: "checked", want: Builtin("bool")},
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

func TestAnalyzerReducesIdentifiersDeferredHeadersAndSuper(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":        "class_name Base\nfunc same() -> String:\n\tpass\nfunc named() -> int:\n\tpass\n",
		"child.gd":       "class_name Child extends Base\nfunc same():\n\tvar bare := super()\nfunc caller():\n\tvar named_result := super.named()\n",
		"identifiers.gd": "class_name Identifiers\nvar shadowed: String\nconst TOP := 1 + 2\nfunc values(inferred_default := 1 + 2, ordinary_default = 1 + 2, explicit_default: int = 1 + 2):\n\tvar top_use := TOP\n\tvar inferred := 1 + 2\n\tvar inferred_use := inferred\n\tvar dynamic = 1 + 2\n\tvar dynamic_use := dynamic\n\tvar explicit: float = 1\n\tvar explicit_use := explicit\n\tvar inferred_default_use := inferred_default\n\tvar ordinary_default_use := ordinary_default\n\tvar explicit_default_use := explicit_default\n\tvar before := later\n\tvar later := 1\n\tvar before_use := before\nfunc shadows(shadowed := 2):\n\tvar shadowed_use := shadowed\nfunc default_scope(late_default := later):\n\tvar later := 1\n\tvar late_default_use := late_default\n",
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
	if got := analyzer.TypeOf(reducerVariableValue(t, identifiers, "late_default_use")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("inferred default reduced in later use scope = %s (%q), want reasoned Unknown", got, got.Reason())
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
	for _, name := range []string{"omitted_call", "callable_call", "missing_member", "unknown_receiver", "meta_member", "unknown_argument", "special_call", "load_call", "constructor_call"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
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
	baseKey := reductionKey{expression: expression, scope: scope.ID(), token: reductionContextToken{}}
	overlayKey := reductionKey{expression: expression, scope: scope.ID(), token: overlay}
	if _, found := analyzer.cache[baseKey]; !found {
		t.Fatal("base result was not cached as a completed result")
	}
	if _, found := analyzer.cache[overlayKey]; !found {
		t.Fatal("overlay result reused the base cache key")
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
	} {
		if err := builder.AddOperator(operator.left, operator.operator, operator.right, operator.result); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddMethod("Node", "engine_method", "int", nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddProperty("Node", "engine_property", "int"); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
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
