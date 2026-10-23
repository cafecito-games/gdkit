package semantic

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzerInfersOmittedDirectMethodReturn(t *testing.T) {
	source := sources(t, map[string]string{
		"returns.gd": "class_name Returns\nfunc answer():\n\treturn 42\nfunc run():\n\tvar result := answer()\n",
	})
	require.Empty(t, source.ParseFailures())

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	got := analyzer.TypeOf(reducerVariableValue(t, source.File("returns.gd"), "result"))
	require.True(t, got.Equal(Builtin("int")))
}

func TestAnalyzerClassifiesOmittedMethodReturns(t *testing.T) {
	source := sources(t, map[string]string{
		"returns.gd": "class_name Returns\nfunc no_value():\n\tpass\nfunc bare_only():\n\treturn\nfunc same(flag: bool):\n\tif flag:\n\t\treturn 1\n\telse:\n\t\treturn 2\nfunc numeric(flag: bool):\n\tif flag:\n\t\treturn 1\n\telse:\n\t\treturn 2.0\nfunc falls_through(flag: bool):\n\tif flag:\n\t\treturn 1\nfunc run():\n\tvar no_value_result := no_value()\n\tvar bare_result := bare_only()\n\tvar same_result := same(true)\n\tvar numeric_result := numeric(true)\n\tvar falls_through_result := falls_through(true)\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("returns.gd")
	require.NotNil(t, file)
	require.False(t, len(file.Statements) < 6)
	same, ok := file.Statements[3].(*ast.FunctionDeclaration)
	require.True(t, ok)
	require.Len(t, same.Body, 1)
	ifStatement, ok := same.Body[0].(*ast.IfStatement)
	require.True(t, ok)
	require.Len(t, ifStatement.Branches, 1)
	require.Len(t, ifStatement.Branches[0].Body, 1)
	require.Len(t, ifStatement.Else, 1)
	_, ok = ifStatement.Branches[0].Body[0].(*ast.ReturnStatement)
	require.True(t, ok)

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "no_value_result", want: Void()},
		{name: "bare_result", want: Void()},
		{name: "same_result", want: Builtin("int")},
		{name: "numeric_result", want: Builtin("float")},
		{name: "falls_through_result", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestAnalyzerInfersControlFlowReturnsConservatively(t *testing.T) {
	source := sources(t, map[string]string{
		"control.gd": "class_name Control\nfunc elif_total(first: bool, second: bool):\n\tif first:\n\t\treturn 1\n\telif second:\n\t\treturn 2\n\telse:\n\t\treturn 3\nfunc in_while(flag: bool):\n\twhile flag:\n\t\treturn 1\nfunc in_for(values: Array):\n\tfor value in values:\n\t\treturn 1\nfunc matched(value):\n\tmatch value:\n\t\t1:\n\t\t\treturn 1\n\t\t_:\n\t\t\treturn 2\nfunc guarded_match(value):\n\tmatch value:\n\t\t1:\n\t\t\treturn 1\n\t\t_ when true:\n\t\t\treturn 2\nfunc run():\n\tvar elif_result := elif_total(true, false)\n\tvar while_result := in_while(true)\n\tvar for_result := in_for([])\n\tvar matched_result := matched(1)\n\tvar guarded_result := guarded_match(1)\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("control.gd")
	require.NotNil(t, file)
	require.False(t, len(file.Statements) < 7)
	matched, ok := file.Statements[4].(*ast.FunctionDeclaration)
	require.True(t, ok)
	require.Len(t, matched.Body, 1)
	match, ok := matched.Body[0].(*ast.MatchStatement)
	require.True(t, ok)
	require.Len(t, match.Cases, 2)
	require.Len(t, match.Cases[1].Patterns, 1)
	wildcard, ok := match.Cases[1].Patterns[0].(*ast.WildcardPattern)
	require.True(t, ok)
	require.NotNil(t, wildcard)
	require.Nil(t, match.Cases[1].Guard)
	guarded, ok := file.Statements[5].(*ast.FunctionDeclaration)
	require.True(t, ok)
	require.Len(t, guarded.Body, 1)
	guardedMatch, ok := guarded.Body[0].(*ast.MatchStatement)
	require.True(t, ok)
	require.Len(t, guardedMatch.Cases, 2)
	require.NotNil(t, guardedMatch.Cases[1].Guard)

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "elif_result", want: Builtin("int")},
		{name: "while_result", want: Variant()},
		{name: "for_result", want: Variant()},
		{name: "matched_result", want: Builtin("int")},
		{name: "guarded_result", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestAnalyzerUnifiesOmittedMethodReturnTypes(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":    "class_name Base\n",
		"left.gd":    "class_name Left extends Base\n",
		"right.gd":   "class_name Right extends Base\n",
		"broken.gd":  "class_name Broken extends Missing\n",
		"returns.gd": "class_name ReturnTypes\nfunc classes(flag: bool):\n\tif flag:\n\t\treturn Left.new()\n\telse:\n\t\treturn Right.new()\nfunc incomplete_classes(flag: bool):\n\tif flag:\n\t\treturn Broken.new()\n\telse:\n\t\treturn Left.new()\nfunc arrays(flag: bool):\n\tif flag:\n\t\treturn [1]\n\telse:\n\t\treturn [2]\nfunc arrays_mixed(flag: bool):\n\tif flag:\n\t\treturn [1]\n\telse:\n\t\treturn []\nfunc dictionaries(flag: bool):\n\tif flag:\n\t\treturn {\"one\": 1}\n\telse:\n\t\treturn {\"two\": 2}\nfunc dictionaries_mixed(flag: bool):\n\tif flag:\n\t\treturn {\"one\": 1}\n\telse:\n\t\treturn {}\nfunc unknown_value(flag: bool):\n\tif flag:\n\t\treturn missing\n\telse:\n\t\treturn 1\nfunc null_value():\n\treturn null\nfunc run():\n\tvar classes_result := classes(true)\n\tvar incomplete_result := incomplete_classes(true)\n\tvar arrays_result := arrays(true)\n\tvar arrays_mixed_result := arrays_mixed(true)\n\tvar dictionaries_result := dictionaries(true)\n\tvar dictionaries_mixed_result := dictionaries_mixed(true)\n\tvar unknown_result := unknown_value(true)\n\tvar null_result := null_value()\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("returns.gd")
	require.NotNil(t, file)
	require.False(t, len(file.Statements) < 10)
	arrayFunction, ok := file.Statements[3].(*ast.FunctionDeclaration)
	require.True(t, ok)
	require.Len(t, arrayFunction.Body, 1)
	arrayIf, ok := arrayFunction.Body[0].(*ast.IfStatement)
	require.True(t, ok)
	require.Len(t, arrayIf.Branches, 1)
	require.Len(t, arrayIf.Else, 1)
	_, ok = arrayIf.Branches[0].Body[0].(*ast.ReturnStatement)
	require.True(t, ok)

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	base, ok := analyzer.interfaces.Class("base.gd")
	require.True(t, ok, "Base interface is unavailable")
	intArray := Array(typePointer(Builtin("int")))
	stringIntDictionary := Dictionary(typePointer(Builtin("String")), typePointer(Builtin("int")))
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "classes_result", want: base.Type()},
		{name: "arrays_result", want: intArray},
		{name: "arrays_mixed_result", want: Variant()},
		{name: "dictionaries_result", want: stringIntDictionary},
		{name: "dictionaries_mixed_result", want: Variant()},
		{name: "null_result", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			require.True(t, got.Equal(testCase.want))
		})
	}
	for _, name := range []string{"incomplete_result", "unknown_result"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
		})
	}
}

func TestAnalyzerExcludesLambdaBodiesAndPreservesDeclaredReturns(t *testing.T) {
	source := sources(t, map[string]string{
		"ownership.gd": "class_name Ownership\nfunc lambda_only():\n\tvar callback = func(): return 1\nfunc lambda_in_expression():\n\tvar callbacks = [func(): return 1]\nfunc lambda_value():\n\treturn func(): return 1\nfunc default_lambda(callback = func(): return 1):\n\tpass\nfunc declared() -> String:\n\treturn 1\nfunc run():\n\tvar lambda_only_result := lambda_only()\n\tvar lambda_expression_result := lambda_in_expression()\n\tvar lambda_value_result := lambda_value()\n\tvar default_lambda_result := default_lambda()\n\tvar declared_result := declared()\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("ownership.gd")
	require.NotNil(t, file)
	require.False(t, len(file.Statements) < 6)
	lambdaOnly, ok := file.Statements[1].(*ast.FunctionDeclaration)
	require.True(t, ok)
	require.Len(t, lambdaOnly.Body, 1)
	local, ok := lambdaOnly.Body[0].(*ast.VariableDeclaration)
	require.True(t, ok)
	lambda, ok := local.Value.(*ast.LambdaExpression)
	require.True(t, ok)
	require.Len(t, lambda.Body, 1)
	_, ok = lambda.Body[0].(*ast.ReturnStatement)
	require.True(t, ok)
	defaultLambda, ok := file.Statements[4].(*ast.FunctionDeclaration)
	require.True(t, ok)
	require.Len(t, defaultLambda.Parameters, 1)
	_, ok = defaultLambda.Parameters[0].Default.(*ast.LambdaExpression)
	require.True(t, ok)

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "lambda_only_result", want: Void()},
		{name: "lambda_expression_result", want: Void()},
		{name: "lambda_value_result", want: Callable()},
		{name: "default_lambda_result", want: Void()},
		{name: "declared_result", want: Builtin("String")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestAnalyzerFailsClosedForMalformedFunctionReturnOwnership(t *testing.T) {
	source := sources(t, map[string]string{
		"malformed.gd": "class_name Malformed\nfunc nested_function():\n\tpass\nfunc nested_class():\n\tpass\nfunc accessor():\n\tpass\nfunc shared():\n\tpass\nfunc absent_body():\n\tpass\nfunc abstracted():\n\tpass\nfunc later_cycle():\n\tpass\nfunc typed_nil():\n\tpass\nfunc run():\n\tvar nested_function_result := nested_function()\n\tvar nested_class_result := nested_class()\n\tvar accessor_result := accessor()\n\tvar shared_result := shared()\n\tvar absent_body_result := absent_body()\n\tvar abstracted_result := abstracted()\n\tvar later_cycle_result := later_cycle()\n\tvar typed_nil_result := typed_nil()\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("malformed.gd")
	require.NotNil(t, file)
	results := map[string]ast.Expression{}
	for _, name := range []string{
		"nested_function_result",
		"nested_class_result",
		"accessor_result",
		"shared_result",
		"absent_body_result",
		"abstracted_result",
		"later_cycle_result",
		"typed_nil_result",
	} {
		results[name] = reducerVariableValue(t, file, name)
	}
	nestedFunction := returnTestFunction(t, file, "nested_function")
	nestedClass := returnTestFunction(t, file, "nested_class")
	accessor := returnTestFunction(t, file, "accessor")
	shared := returnTestFunction(t, file, "shared")
	absentBody := returnTestFunction(t, file, "absent_body")
	abstracted := returnTestFunction(t, file, "abstracted")
	laterCycle := returnTestFunction(t, file, "later_cycle")
	typedNil := returnTestFunction(t, file, "typed_nil")

	nestedFunction.Body = []ast.Statement{&ast.FunctionDeclaration{
		Name: "inner",
		Body: []ast.Statement{&ast.ReturnStatement{Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}}},
	}}
	nestedClass.Body = []ast.Statement{&ast.ClassDeclaration{
		Name: "Inner",
		Body: []ast.Statement{&ast.FunctionDeclaration{
			Name: "inner",
			Body: []ast.Statement{&ast.ReturnStatement{Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}}},
		}},
	}}
	accessor.Body = []ast.Statement{&ast.VariableDeclaration{
		Name:   "property",
		Getter: []ast.Statement{&ast.ReturnStatement{Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}}},
	}}
	sharedReturn := &ast.ReturnStatement{Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}}
	shared.Body = []ast.Statement{sharedReturn, sharedReturn}
	absentBody.Body = nil
	abstracted.Abstract = true

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	require.True(t, analyzer.scopes.nodeShared(sharedReturn))
	cycle := &ast.IfStatement{Branches: []ast.Branch{{Condition: &ast.Literal{Kind: ast.BoolLiteral, Raw: "true"}}}}
	cycle.Branches[0].Body = []ast.Statement{cycle}
	laterCycle.Body = []ast.Statement{cycle}
	var unavailable *ast.Literal
	typedNil.Body = []ast.Statement{&ast.ReturnStatement{Value: unavailable}}

	for _, name := range []string{
		"nested_function_result",
		"nested_class_result",
		"accessor_result",
		"shared_result",
		"absent_body_result",
		"abstracted_result",
		"later_cycle_result",
		"typed_nil_result",
	} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(results[name])
			require.Equal(t, KindUnknown, got.Kind())
			require.NotEmpty(t, got.Reason())
		})
	}
}

func returnTestFunction(t *testing.T, file *ast.File, name string) *ast.FunctionDeclaration {
	t.Helper()
	for _, statement := range file.Statements {
		function, ok := statement.(*ast.FunctionDeclaration)
		if ok && function.Name == name {
			return function
		}
	}
	require.FailNow(t, "real parser fixture has no direct function "+name)
	return nil
}

func TestAnalyzerFunctionReturnCacheIsSnapshotBoundAndRecursionSafe(t *testing.T) {
	files := map[string]string{
		"functions.gd": "class_name Functions\nfunc stable():\n\treturn 1\nfunc self_recursive():\n\treturn self_recursive()\nfunc first_recursive():\n\treturn second_recursive()\nfunc second_recursive():\n\treturn first_recursive()\nfunc run():\n\tvar stable_result := stable()\n\tvar self_result := self_recursive()\n\tvar mutual_result := first_recursive()\n",
	}
	source := sources(t, files)
	require.Empty(t, source.ParseFailures())
	file := source.File("functions.gd")
	before, err := json.Marshal(file)
	require.NoError(t, err)
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	stableResult := reducerVariableValue(t, file, "stable_result")
	selfResult := reducerVariableValue(t, file, "self_result")
	mutualResult := reducerVariableValue(t, file, "mutual_result")
	stable := returnTestFunction(t, file, "stable")
	self := returnTestFunction(t, file, "self_recursive")
	first := returnTestFunction(t, file, "first_recursive")
	second := returnTestFunction(t, file, "second_recursive")

	got := analyzer.TypeOf(stableResult)
	require.True(t, got.Equal(Builtin("int")))
	_, ok := analyzer.cachedFunctionReturn(stable)
	require.True(t, ok)
	for _, testCase := range []struct {
		name       string
		expression ast.Expression
	}{
		{name: "self", expression: selfResult},
		{name: "mutual", expression: mutualResult},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(testCase.expression)
			require.Equal(t, KindUnknown, got.Kind())
			require.Equal(t, "function return inference cycle", got.Reason())
		})
	}
	for _, function := range []*ast.FunctionDeclaration{self, first, second} {
		_, ok = analyzer.cachedFunctionReturn(function)
		require.False(t, ok)
	}
	repeated := analyzer.TypeOf(stableResult)
	require.True(t, repeated.Equal(Builtin("int")))
	require.Empty(t, repeated.Reason())
	after, err := json.Marshal(file)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))

	const readers = 24
	const readsPerReader = 100
	cold := NewAnalyzer(source, reducerTestEngine(t))
	problems := make(chan string, readers)
	var done sync.WaitGroup
	done.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer done.Done()
			for attempt := 0; attempt < readsPerReader; attempt++ {
				got := cold.TypeOf(stableResult)
				if !got.Equal(Builtin("int")) || got.Reason() != "" {
					problems <- got.String() + ":" + got.Reason()
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
	_, ok = cold.cachedFunctionReturn(stable)
	require.True(t, ok)

	foreignSource := sources(t, files)
	require.Empty(t, foreignSource.ParseFailures())
	foreignFile := foreignSource.File("functions.gd")
	foreignStable := returnTestFunction(t, foreignFile, "stable")
	foreignGot := analyzer.TypeOf(reducerVariableValue(t, foreignFile, "stable_result"))
	require.Equal(t, KindUnknown, foreignGot.Kind())
	require.NotEmpty(t, foreignGot.Reason())
	_, ok = analyzer.cachedFunctionReturn(foreignStable)
	require.False(t, ok)
}

func TestAnalyzerFunctionReturnCyclesStayRequestLocal(t *testing.T) {
	source := sources(t, map[string]string{
		"function_cycles.gd": "class_name FunctionCycles\nfunc first():\n\treturn func(default_value := second()): pass\nfunc second():\n\treturn first()\nfunc run():\n\tvar first_result := first()\n\tvar second_result := second()\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("function_cycles.gd")
	first := returnTestFunction(t, file, "first")
	second := returnTestFunction(t, file, "second")
	require.Len(t, first.Body, 1)
	require.Len(t, second.Body, 1)
	firstReturn, ok := first.Body[0].(*ast.ReturnStatement)
	require.True(t, ok)
	lambda, ok := firstReturn.Value.(*ast.LambdaExpression)
	require.True(t, ok)
	require.Len(t, lambda.Parameters, 1)
	require.NotNil(t, lambda.Parameters[0].Default)
	secondReturn, ok := second.Body[0].(*ast.ReturnStatement)
	require.True(t, ok)
	require.NotNil(t, secondReturn.Value)
	firstResult := reducerVariableValue(t, file, "first_result")
	secondResult := reducerVariableValue(t, file, "second_result")

	for _, testCase := range []struct {
		name  string
		order []ast.Expression
	}{
		{name: "first then second", order: []ast.Expression{firstResult, secondResult}},
		{name: "second then first", order: []ast.Expression{secondResult, firstResult}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := NewAnalyzer(source, reducerTestEngine(t))
			for _, expression := range testCase.order {
				got := analyzer.TypeOf(expression)
				require.True(t, got.Equal(Callable()))
			}
			for _, expression := range []ast.Expression{lambda.Parameters[0].Default, secondReturn.Value} {
				scope, ok := analyzer.scopes.ScopeAt(expression)
				require.True(t, ok)
				key := reductionKey{expression: expression, scope: scope.ID(), token: analyzer.narrow.tokenAt(expression)}
				_, ok = analyzer.cached(key)
				require.False(t, ok)
			}
		})
	}
}

func TestAnalyzerUsesEachReturnExpressionsSourceBoundNarrowingToken(t *testing.T) {
	source := sources(t, map[string]string{
		"tokens.gd": "class_name Tokens\nfunc refined(value: Variant):\n\tif value is Sprite2D:\n\t\treturn value.sprite_only\n\telse:\n\t\treturn 1\nfunc deferred(value: Variant):\n\tif value is Sprite2D:\n\t\tvar captured := value.sprite_only\n\t\treturn captured\n\telse:\n\t\treturn 1\nfunc run():\n\tvar refined_result := refined(null)\n\tvar deferred_result := deferred(null)\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("tokens.gd")
	refined := returnTestFunction(t, file, "refined")
	require.Len(t, refined.Body, 1)
	branch, ok := refined.Body[0].(*ast.IfStatement)
	require.True(t, ok)
	require.Len(t, branch.Branches, 1)
	require.Len(t, branch.Branches[0].Body, 1)
	returned, ok := branch.Branches[0].Body[0].(*ast.ReturnStatement)
	require.True(t, ok)
	require.NotNil(t, returned.Value)

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	token := analyzer.narrow.tokenAt(returned.Value)
	require.NotNil(t, token.overlay)
	refinedResult := reducerVariableValue(t, file, "refined_result")
	scope, ok := analyzer.scopes.ScopeAt(refinedResult)
	require.True(t, ok)
	foreignCallerToken := reductionContextToken{overlay: &reductionOverlay{}}
	got := analyzer.typeOfIn(refinedResult, scope, foreignCallerToken).typeValue
	require.True(t, got.Equal(Builtin("int")))
	got = analyzer.TypeOf(reducerVariableValue(t, file, "deferred_result"))
	require.True(t, got.Equal(Variant()))
}

func TestAnalyzerInfersOnlyExactOmittedUserMethodCalls(t *testing.T) {
	source := sources(t, map[string]string{
		"exact.gd": "class_name Exact\nfunc direct():\n\treturn 1\nstatic func static_direct():\n\treturn 2\nfunc declared() -> String:\n\treturn \"declared\"\nfunc run(other: Exact, callback: Callable):\n\tvar direct_result := direct()\n\tvar receiver_result := other.direct()\n\tvar static_result := Exact.static_direct()\n\tvar declared_result := declared()\n\tvar callback_result := callback()\n\tvar alias = direct\n\tvar alias_result := alias()\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("exact.gd")
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "direct_result", want: Builtin("int")},
		{name: "receiver_result", want: Builtin("int")},
		{name: "static_result", want: Builtin("int")},
		{name: "declared_result", want: Builtin("String")},
		{name: "alias_result", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			require.True(t, got.Equal(testCase.want))
		})
	}
	got := analyzer.TypeOf(reducerVariableValue(t, file, "callback_result"))
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
}

// TestFunctionReturnsGodot47Oracle is an opt-in Godot 4.7.2 producer receipt.
// --check-only establishes parser/type-check acceptance only; the parser-backed
// tests above remain the authority for semantic return inference.
func TestFunctionReturnsGodot47Oracle(t *testing.T) {
	godot := strings.TrimSpace(os.Getenv("GODOT_BIN"))
	if godot == "" {
		t.Skip("GODOT_BIN is required for the Godot 4.7.2 producer oracle")
	}
	version, err := exec.Command(godot, "--version").CombinedOutput()
	require.NoError(t, err, "Godot version probe failed")
	require.True(t, strings.HasPrefix(strings.TrimSpace(string(version)), "4.7.2"))
	writeProject := func(name, source string) string {
		t.Helper()
		root := t.TempDir()
		err := os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\nconfig/name=\"Function Returns Oracle\"\n"), 0o644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(root, name), []byte(source), 0o644)
		require.NoError(t, err)
		return root
	}
	checkOnly := func(root string) ([]byte, error) {
		t.Helper()
		return exec.Command(godot, "--headless", "--path", root, "--script", "res://oracle.gd", "--check-only").CombinedOutput()
	}

	accepted := writeProject("oracle.gd", "extends Node\n\nfunc partial(flag: bool):\n\tif flag:\n\t\treturn 1\n\nfunc lambda_owner():\n\tvar callback = func(): return 1\n\nfunc loop_owner(flag: bool):\n\twhile flag:\n\t\treturn 1\n\nfunc consume() -> void:\n\tvar partial_value = partial(false)\n\tvar loop_value = loop_owner(false)\n")
	_, err = checkOnly(accepted)
	require.NoError(t, err, "Godot 4.7.2 --check-only rejected the fixture")

	annotated := writeProject("oracle.gd", "extends Node\n\nfunc partial(flag: bool) -> int:\n\tif flag:\n\t\treturn 1\n")
	_, err = checkOnly(annotated)
	require.Error(t, err, "Godot 4.7.2 --check-only accepted an annotated partial return")
}
