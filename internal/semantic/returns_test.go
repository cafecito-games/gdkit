package semantic

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
)

func TestAnalyzerInfersOmittedDirectMethodReturn(t *testing.T) {
	source := sources(t, map[string]string{
		"returns.gd": "class_name Returns\nfunc answer():\n\treturn 42\nfunc run():\n\tvar result := answer()\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	got := analyzer.TypeOf(reducerVariableValue(t, source.File("returns.gd"), "result"))
	if !got.Equal(Builtin("int")) {
		t.Fatalf("omitted direct-method return = %s (%q), want int", got, got.Reason())
	}
}

func TestAnalyzerClassifiesOmittedMethodReturns(t *testing.T) {
	source := sources(t, map[string]string{
		"returns.gd": "class_name Returns\nfunc no_value():\n\tpass\nfunc bare_only():\n\treturn\nfunc same(flag: bool):\n\tif flag:\n\t\treturn 1\n\telse:\n\t\treturn 2\nfunc numeric(flag: bool):\n\tif flag:\n\t\treturn 1\n\telse:\n\t\treturn 2.0\nfunc falls_through(flag: bool):\n\tif flag:\n\t\treturn 1\nfunc run():\n\tvar no_value_result := no_value()\n\tvar bare_result := bare_only()\n\tvar same_result := same(true)\n\tvar numeric_result := numeric(true)\n\tvar falls_through_result := falls_through(true)\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("returns.gd")
	if file == nil || len(file.Statements) < 6 {
		t.Fatalf("real parser fixture did not retain functions: %#v", file)
	}
	same, ok := file.Statements[3].(*ast.FunctionDeclaration)
	if !ok || len(same.Body) != 1 {
		t.Fatalf("real parser fixture did not retain same body: %#v", file.Statements[3])
	}
	ifStatement, ok := same.Body[0].(*ast.IfStatement)
	if !ok || len(ifStatement.Branches) != 1 || len(ifStatement.Branches[0].Body) != 1 || len(ifStatement.Else) != 1 {
		t.Fatalf("real parser fixture did not retain if/else return shape: %#v", same.Body[0])
	}
	if _, ok := ifStatement.Branches[0].Body[0].(*ast.ReturnStatement); !ok {
		t.Fatalf("real parser fixture branch return = %T, want ReturnStatement", ifStatement.Branches[0].Body[0])
	}

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
			if !got.Equal(testCase.want) {
				t.Fatalf("omitted method return = %s (%q), want %s", got, got.Reason(), testCase.want)
			}
		})
	}
}

func TestAnalyzerInfersControlFlowReturnsConservatively(t *testing.T) {
	source := sources(t, map[string]string{
		"control.gd": "class_name Control\nfunc elif_total(first: bool, second: bool):\n\tif first:\n\t\treturn 1\n\telif second:\n\t\treturn 2\n\telse:\n\t\treturn 3\nfunc in_while(flag: bool):\n\twhile flag:\n\t\treturn 1\nfunc in_for(values: Array):\n\tfor value in values:\n\t\treturn 1\nfunc matched(value):\n\tmatch value:\n\t\t1:\n\t\t\treturn 1\n\t\t_:\n\t\t\treturn 2\nfunc guarded_match(value):\n\tmatch value:\n\t\t1:\n\t\t\treturn 1\n\t\t_ when true:\n\t\t\treturn 2\nfunc run():\n\tvar elif_result := elif_total(true, false)\n\tvar while_result := in_while(true)\n\tvar for_result := in_for([])\n\tvar matched_result := matched(1)\n\tvar guarded_result := guarded_match(1)\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("control.gd")
	if file == nil || len(file.Statements) < 7 {
		t.Fatalf("real parser fixture did not retain control functions: %#v", file)
	}
	matched, ok := file.Statements[4].(*ast.FunctionDeclaration)
	if !ok || len(matched.Body) != 1 {
		t.Fatalf("real parser fixture did not retain match function: %#v", file.Statements[4])
	}
	match, ok := matched.Body[0].(*ast.MatchStatement)
	if !ok || len(match.Cases) != 2 || len(match.Cases[1].Patterns) != 1 {
		t.Fatalf("real parser fixture match shape = %#v", matched.Body[0])
	}
	if wildcard, ok := match.Cases[1].Patterns[0].(*ast.WildcardPattern); !ok || wildcard == nil || match.Cases[1].Guard != nil {
		t.Fatalf("real parser fixture fallback = %#v, want unguarded wildcard", match.Cases[1])
	}
	guarded, ok := file.Statements[5].(*ast.FunctionDeclaration)
	if !ok || len(guarded.Body) != 1 {
		t.Fatalf("real parser fixture did not retain guarded-match function: %#v", file.Statements[5])
	}
	guardedMatch, ok := guarded.Body[0].(*ast.MatchStatement)
	if !ok || len(guardedMatch.Cases) != 2 || guardedMatch.Cases[1].Guard == nil {
		t.Fatalf("real parser fixture guarded match shape = %#v", guarded.Body[0])
	}

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
			if !got.Equal(testCase.want) {
				t.Fatalf("control-flow return = %s (%q), want %s", got, got.Reason(), testCase.want)
			}
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
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("returns.gd")
	if file == nil || len(file.Statements) < 10 {
		t.Fatalf("real parser fixture did not retain return cases: %#v", file)
	}
	arrayFunction, ok := file.Statements[3].(*ast.FunctionDeclaration)
	if !ok || len(arrayFunction.Body) != 1 {
		t.Fatalf("real parser fixture did not retain array function: %#v", file.Statements[3])
	}
	arrayIf, ok := arrayFunction.Body[0].(*ast.IfStatement)
	if !ok || len(arrayIf.Branches) != 1 || len(arrayIf.Else) != 1 {
		t.Fatalf("real parser fixture array branches = %#v", arrayFunction.Body[0])
	}
	if _, ok := arrayIf.Branches[0].Body[0].(*ast.ReturnStatement); !ok {
		t.Fatalf("real parser fixture array branch statement = %T, want ReturnStatement", arrayIf.Branches[0].Body[0])
	}

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	base, ok := analyzer.interfaces.Class("base.gd")
	if !ok {
		t.Fatal("Base interface is unavailable")
	}
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
			if !got.Equal(testCase.want) {
				t.Fatalf("unified return = %s (%q), want %s", got, got.Reason(), testCase.want)
			}
		})
	}
	for _, name := range []string{"incomplete_result", "unknown_result"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(reducerVariableValue(t, file, name))
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("unified return = %s (%q), want reasoned Unknown", got, got.Reason())
			}
		})
	}
}

func TestAnalyzerExcludesLambdaBodiesAndPreservesDeclaredReturns(t *testing.T) {
	source := sources(t, map[string]string{
		"ownership.gd": "class_name Ownership\nfunc lambda_only():\n\tvar callback = func(): return 1\nfunc lambda_in_expression():\n\tvar callbacks = [func(): return 1]\nfunc lambda_value():\n\treturn func(): return 1\nfunc default_lambda(callback = func(): return 1):\n\tpass\nfunc declared() -> String:\n\treturn 1\nfunc run():\n\tvar lambda_only_result := lambda_only()\n\tvar lambda_expression_result := lambda_in_expression()\n\tvar lambda_value_result := lambda_value()\n\tvar default_lambda_result := default_lambda()\n\tvar declared_result := declared()\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("ownership.gd")
	if file == nil || len(file.Statements) < 6 {
		t.Fatalf("real parser fixture did not retain ownership functions: %#v", file)
	}
	lambdaOnly, ok := file.Statements[1].(*ast.FunctionDeclaration)
	if !ok || len(lambdaOnly.Body) != 1 {
		t.Fatalf("real parser fixture lambda-only function = %#v", file.Statements[1])
	}
	local, ok := lambdaOnly.Body[0].(*ast.VariableDeclaration)
	if !ok {
		t.Fatalf("real parser fixture lambda local = %T, want VariableDeclaration", lambdaOnly.Body[0])
	}
	lambda, ok := local.Value.(*ast.LambdaExpression)
	if !ok || len(lambda.Body) != 1 {
		t.Fatalf("real parser fixture lambda local value = %#v", local.Value)
	}
	if _, ok := lambda.Body[0].(*ast.ReturnStatement); !ok {
		t.Fatalf("real parser fixture lambda body = %T, want ReturnStatement", lambda.Body[0])
	}
	defaultLambda, ok := file.Statements[4].(*ast.FunctionDeclaration)
	if !ok || len(defaultLambda.Parameters) != 1 {
		t.Fatalf("real parser fixture default-lambda function = %#v", file.Statements[4])
	}
	if _, ok := defaultLambda.Parameters[0].Default.(*ast.LambdaExpression); !ok {
		t.Fatalf("real parser fixture default = %T, want LambdaExpression", defaultLambda.Parameters[0].Default)
	}

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
			if !got.Equal(testCase.want) {
				t.Fatalf("ownership result = %s (%q), want %s", got, got.Reason(), testCase.want)
			}
		})
	}
}

func TestAnalyzerFailsClosedForMalformedFunctionReturnOwnership(t *testing.T) {
	source := sources(t, map[string]string{
		"malformed.gd": "class_name Malformed\nfunc nested_function():\n\tpass\nfunc nested_class():\n\tpass\nfunc accessor():\n\tpass\nfunc shared():\n\tpass\nfunc absent_body():\n\tpass\nfunc abstracted():\n\tpass\nfunc later_cycle():\n\tpass\nfunc typed_nil():\n\tpass\nfunc run():\n\tvar nested_function_result := nested_function()\n\tvar nested_class_result := nested_class()\n\tvar accessor_result := accessor()\n\tvar shared_result := shared()\n\tvar absent_body_result := absent_body()\n\tvar abstracted_result := abstracted()\n\tvar later_cycle_result := later_cycle()\n\tvar typed_nil_result := typed_nil()\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("malformed.gd")
	if file == nil {
		t.Fatal("real parser fixture did not retain malformed.gd")
	}
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
	if analyzer.scopes.nodeShared(sharedReturn) == false {
		t.Fatal("hand-built shared return was not detected before semantic analysis")
	}
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
			if got.Kind() != KindUnknown || got.Reason() == "" {
				t.Fatalf("malformed ownership result = %s (%q), want reasoned Unknown", got, got.Reason())
			}
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
	t.Fatalf("real parser fixture has no direct function %q", name)
	return nil
}

func TestAnalyzerFunctionReturnCacheIsSnapshotBoundAndRecursionSafe(t *testing.T) {
	files := map[string]string{
		"functions.gd": "class_name Functions\nfunc stable():\n\treturn 1\nfunc self_recursive():\n\treturn self_recursive()\nfunc first_recursive():\n\treturn second_recursive()\nfunc second_recursive():\n\treturn first_recursive()\nfunc run():\n\tvar stable_result := stable()\n\tvar self_result := self_recursive()\n\tvar mutual_result := first_recursive()\n",
	}
	source := sources(t, files)
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("functions.gd")
	before, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	stableResult := reducerVariableValue(t, file, "stable_result")
	selfResult := reducerVariableValue(t, file, "self_result")
	mutualResult := reducerVariableValue(t, file, "mutual_result")
	stable := returnTestFunction(t, file, "stable")
	self := returnTestFunction(t, file, "self_recursive")
	first := returnTestFunction(t, file, "first_recursive")
	second := returnTestFunction(t, file, "second_recursive")

	if got := analyzer.TypeOf(stableResult); !got.Equal(Builtin("int")) {
		t.Fatalf("stable result = %s (%q), want int", got, got.Reason())
	}
	if _, ok := analyzer.cachedFunctionReturn(stable); !ok {
		t.Fatal("completed exact function result was not published")
	}
	for _, testCase := range []struct {
		name       string
		expression ast.Expression
	}{
		{name: "self", expression: selfResult},
		{name: "mutual", expression: mutualResult},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.TypeOf(testCase.expression)
			if got.Kind() != KindUnknown || got.Reason() != "function return inference cycle" {
				t.Fatalf("recursive result = %s (%q), want cycle Unknown", got, got.Reason())
			}
		})
	}
	for _, function := range []*ast.FunctionDeclaration{self, first, second} {
		if _, ok := analyzer.cachedFunctionReturn(function); ok {
			t.Fatalf("recursive function %q published a partial result", function.Name)
		}
	}
	if repeated := analyzer.TypeOf(stableResult); !repeated.Equal(Builtin("int")) || repeated.Reason() != "" {
		t.Fatalf("repeated stable result = %s (%q), want immutable int", repeated, repeated.Reason())
	}
	after, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("function return inference mutated the parsed AST")
	}

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
		t.Errorf("concurrent function return = %s, want int", problem)
	}
	if _, ok := cold.cachedFunctionReturn(stable); !ok {
		t.Fatal("concurrent completed function result was not published")
	}

	foreignSource := sources(t, files)
	if failures := foreignSource.ParseFailures(); len(failures) != 0 {
		t.Fatalf("foreign real parser fixture failed: %v", failures)
	}
	foreignFile := foreignSource.File("functions.gd")
	foreignStable := returnTestFunction(t, foreignFile, "stable")
	if got := analyzer.TypeOf(reducerVariableValue(t, foreignFile, "stable_result")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("foreign call result = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	if _, ok := analyzer.cachedFunctionReturn(foreignStable); ok {
		t.Fatal("foreign exact function pointer reused this analyzer's completed result")
	}
}

func TestAnalyzerUsesEachReturnExpressionsSourceBoundNarrowingToken(t *testing.T) {
	source := sources(t, map[string]string{
		"tokens.gd": "class_name Tokens\nfunc refined(value: Variant):\n\tif value is Sprite2D:\n\t\treturn value.sprite_only\n\telse:\n\t\treturn 1\nfunc deferred(value: Variant):\n\tif value is Sprite2D:\n\t\tvar captured := value.sprite_only\n\t\treturn captured\n\telse:\n\t\treturn 1\nfunc run():\n\tvar refined_result := refined(null)\n\tvar deferred_result := deferred(null)\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("tokens.gd")
	refined := returnTestFunction(t, file, "refined")
	if len(refined.Body) != 1 {
		t.Fatalf("real parser fixture refined body = %#v", refined.Body)
	}
	branch, ok := refined.Body[0].(*ast.IfStatement)
	if !ok || len(branch.Branches) != 1 || len(branch.Branches[0].Body) != 1 {
		t.Fatalf("real parser fixture refined if = %#v", refined.Body[0])
	}
	returned, ok := branch.Branches[0].Body[0].(*ast.ReturnStatement)
	if !ok || returned.Value == nil {
		t.Fatalf("real parser fixture refined return = %#v", branch.Branches[0].Body[0])
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	if token := analyzer.narrow.tokenAt(returned.Value); token.overlay == nil {
		t.Fatal("final #50 source-bound token is absent from the guarded return expression")
	}
	refinedResult := reducerVariableValue(t, file, "refined_result")
	scope, ok := analyzer.scopes.ScopeAt(refinedResult)
	if !ok {
		t.Fatal("refined call has no recorded scope")
	}
	foreignCallerToken := reductionContextToken{overlay: &reductionOverlay{}}
	if got := analyzer.typeOfIn(refinedResult, scope, foreignCallerToken).typeValue; !got.Equal(Builtin("int")) {
		t.Fatalf("foreign caller overlay affected return inference: %s (%q), want int", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "deferred_result")); !got.Equal(Variant()) {
		t.Fatalf("deferred initializer inherited return overlay = %s (%q), want Variant base-view result", got, got.Reason())
	}
}

func TestAnalyzerInfersOnlyExactOmittedUserMethodCalls(t *testing.T) {
	source := sources(t, map[string]string{
		"exact.gd": "class_name Exact\nfunc direct():\n\treturn 1\nstatic func static_direct():\n\treturn 2\nfunc declared() -> String:\n\treturn \"declared\"\nfunc run(other: Exact, callback: Callable):\n\tvar direct_result := direct()\n\tvar receiver_result := other.direct()\n\tvar static_result := Exact.static_direct()\n\tvar declared_result := declared()\n\tvar callback_result := callback()\n\tvar alias = direct\n\tvar alias_result := alias()\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
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
			if !got.Equal(testCase.want) {
				t.Fatalf("exact user-call result = %s (%q), want %s", got, got.Reason(), testCase.want)
			}
		})
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "callback_result")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("Callable result = %s (%q), want existing reasoned Unknown", got, got.Reason())
	}
}
