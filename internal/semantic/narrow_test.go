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
)

func narrowTestEngine(t *testing.T) *Engine {
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
		{name: "Sprite2D", parent: "Node"},
		{name: "Resource", parent: "RefCounted"},
	} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddProperty("Node", "node_only", "int"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddProperty("Sprite2D", "sprite_only", "int"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddProperty("Resource", "resource_only", "int"); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestNarrowingIfBodyUsesExactBindingFact(t *testing.T) {
	source := sources(t, map[string]string{
		"narrow.gd": "class_name Narrow\nfunc run(value: Variant):\n\tif value is Node:\n\t\tvar inside := value\n\tvar after := value\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("narrow.gd")
	var guarded *ast.IfStatement
	ast.Inspect(file, func(node ast.Node) bool {
		if statement, ok := node.(*ast.IfStatement); ok {
			guarded = statement
		}
		return true
	})
	if guarded == nil || len(guarded.Branches) != 1 {
		t.Fatalf("real parser fixture if statement = %#v, want one branch", guarded)
	}
	condition, ok := guarded.Branches[0].Condition.(*ast.BinaryExpression)
	if !ok || condition.Operator != "is" {
		t.Fatalf("real parser fixture condition = %#v, want positive is binary", guarded.Branches[0].Condition)
	}
	if _, ok := condition.Left.(*ast.Identifier); !ok {
		t.Fatalf("real parser fixture guard left = %T, want identifier", condition.Left)
	}
	if target, ok := condition.Right.(*ast.TypeExpression); !ok || target.Name != "Node" {
		t.Fatalf("real parser fixture guard right = %#v, want Node type expression", condition.Right)
	}

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "inside")); !got.Equal(reducerTestEngine(t).Class("Node")) {
		t.Fatalf("guarded binding = %s (%q), want Node", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "after")); !got.Equal(Variant()) {
		t.Fatalf("post-branch binding = %s (%q), want Variant", got, got.Reason())
	}
}

func TestNarrowingDirectAssignmentInstallsTombstoneAfterTargetAndValue(t *testing.T) {
	source := sources(t, map[string]string{
		"assignment.gd": "class_name Assignment\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar before := value.sprite_only\n\t\tvalue = null\n\t\tvar after := value.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("assignment.gd")
	var assignment *ast.Assignment
	ast.Inspect(file, func(node ast.Node) bool {
		if parsed, ok := node.(*ast.Assignment); ok {
			assignment = parsed
		}
		return true
	})
	if assignment == nil || assignment.Operator != "=" {
		t.Fatalf("real parser fixture assignment = %#v, want direct assignment", assignment)
	}
	if _, ok := assignment.Target.(*ast.Identifier); !ok {
		t.Fatalf("real parser fixture target = %T, want identifier", assignment.Target)
	}
	if literal, ok := assignment.Value.(*ast.Literal); !ok || literal.Kind != ast.NullLiteral {
		t.Fatalf("real parser fixture value = %#v, want null literal", assignment.Value)
	}

	engine := narrowTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "before")); !got.Equal(Builtin("int")) {
		t.Fatalf("pre-assignment use = %s (%q), want int", got, got.Reason())
	}
	if got := analyzer.TypeOf(assignment.Target); !got.Equal(narrowTestEngine(t).Class("Sprite2D")) {
		t.Fatalf("assignment target = %s (%q), want pre-assignment Sprite2D", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "after")); !got.Equal(Variant()) {
		t.Fatalf("post-assignment use = %s (%q), want restored Variant", got, got.Reason())
	}
}

func TestNarrowingPreservesMoreSpecificBaseBindings(t *testing.T) {
	source := sources(t, map[string]string{
		"specific.gd": "class_name Specific\nfunc parameter(sprite: Sprite2D):\n\tif sprite is Node:\n\t\tvar parameter_member := sprite.sprite_only\nfunc array_local():\n\tvar nodes: Array[Node] = []\n\tif nodes is Array:\n\t\tvar array_inside := nodes\nfunc inferred_local():\n\tvar inferred := Sprite2D.new()\n\tif inferred is Node:\n\t\tvar inferred_member := inferred.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("specific.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if binary, ok := node.(*ast.BinaryExpression); ok && binary.Operator == "is" {
			guards = append(guards, binary)
		}
		return true
	})
	if len(guards) != 3 {
		t.Fatalf("real parser fixture is guards = %d, want 3", len(guards))
	}
	for _, guard := range guards {
		if _, ok := guard.Left.(*ast.Identifier); !ok {
			t.Fatalf("real parser fixture guard left = %T, want identifier", guard.Left)
		}
		if _, ok := guard.Right.(*ast.TypeExpression); !ok {
			t.Fatalf("real parser fixture guard right = %T, want type expression", guard.Right)
		}
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "parameter_member")); !got.Equal(Builtin("int")) {
		t.Fatalf("wider parameter guard lost Sprite2D member = %s (%q), want int", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "inferred_member")); !got.Equal(Builtin("int")) {
		t.Fatalf("wider inferred guard lost Sprite2D member = %s (%q), want int", got, got.Reason())
	}
	node := narrowTestEngine(t).Class("Node")
	wantArray := Array(&node)
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "array_inside")); !got.Equal(wantArray) {
		t.Fatalf("wider Array guard lost element type = %s (%q), want %s", got, got.Reason(), wantArray)
	}
}

func TestNarrowingConditionsArePositiveAndScoped(t *testing.T) {
	source := sources(t, map[string]string{
		"conditions.gd": "class_name Conditions\nfunc check() -> bool:\n\treturn true\nfunc run(value: Variant, other: Variant):\n\tif value is Sprite2D && value is Node:\n\t\tvar positive := value.sprite_only\n\telif other is Node:\n\t\tvar elif_own := other.node_only\n\t\tvar elif_original := value.sprite_only\n\telse:\n\t\tvar otherwise := value.sprite_only\n\tif value is Sprite2D or other is Node:\n\t\tvar disjunction := value.sprite_only\n\tif !(value is Sprite2D):\n\t\tvar negated := value.sprite_only\n\tif value is not Sprite2D:\n\t\tvar negative := value.sprite_only\n\tif value == null:\n\t\tvar nil_checked := value.sprite_only\n\tif value:\n\t\tvar truthy := value.sprite_only\n\tif check() and value is Sprite2D:\n\t\tvar call_guard := value.sprite_only\n\tvar after := value.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("conditions.gd")
	operators := map[string]int{}
	var unaryNot *ast.UnaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if binary, ok := node.(*ast.BinaryExpression); ok {
			operators[binary.Operator]++
		}
		if unary, ok := node.(*ast.UnaryExpression); ok && unary.Operator == "!" {
			unaryNot = unary
		}
		return true
	})
	for _, operator := range []string{"is", "&&", "or", "is not", "==", "and"} {
		if operators[operator] == 0 {
			t.Fatalf("real parser fixture did not retain operator %q: %v", operator, operators)
		}
	}
	if unaryNot == nil {
		t.Fatal("real parser fixture did not retain unary !")
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "positive", want: Builtin("int")},
		{name: "elif_own", want: Builtin("int")},
		{name: "elif_original", want: Variant()},
		{name: "otherwise", want: Variant()},
		{name: "disjunction", want: Variant()},
		{name: "negated", want: Variant()},
		{name: "negative", want: Variant()},
		{name: "nil_checked", want: Variant()},
		{name: "truthy", want: Variant()},
		{name: "call_guard", want: Variant()},
		{name: "after", want: Variant()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name)); !got.Equal(testCase.want) {
				t.Fatalf("%s = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
}

func TestNarrowingMatchBindingGuardStaysInItsCase(t *testing.T) {
	source := sources(t, map[string]string{
		"match.gd": "class_name MatchGuard\nfunc run(subject: Variant):\n\tmatch subject:\n\t\tvar captured when captured is Sprite2D:\n\t\t\tvar in_case := captured.sprite_only\n\t\tvar captured when captured is Node:\n\t\t\tvar sibling_case := captured.sprite_only\n\t\t_:\n\t\t\tvar fallback := subject\n\tvar after := subject\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("match.gd")
	var match *ast.MatchStatement
	ast.Inspect(file, func(node ast.Node) bool {
		if statement, ok := node.(*ast.MatchStatement); ok {
			match = statement
		}
		return true
	})
	if match == nil || len(match.Cases) != 3 {
		t.Fatalf("real parser fixture match = %#v, want three cases", match)
	}
	if len(match.Cases[0].Patterns) != 1 {
		t.Fatalf("real parser fixture first patterns = %#v, want one binding", match.Cases[0].Patterns)
	}
	if binding, ok := match.Cases[0].Patterns[0].(*ast.BindingPattern); !ok || binding.Name != "captured" {
		t.Fatalf("real parser fixture first pattern = %#v, want captured binding", match.Cases[0].Patterns[0])
	}
	guard, ok := match.Cases[0].Guard.(*ast.BinaryExpression)
	if !ok || guard.Operator != "is" {
		t.Fatalf("real parser fixture case guard = %#v, want positive is", match.Cases[0].Guard)
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	guarded, ok := guard.Left.(*ast.Identifier)
	if !ok {
		t.Fatalf("real parser fixture guard left = %T, want identifier", guard.Left)
	}
	resolved := analyzer.scopes.Resolve(guarded)
	binding, found := resolved.Binding()
	if resolved.State() != LookupFound || !found || binding.Kind() != BindingMatch {
		t.Fatalf("guard binding = %#v / %#v, want found BindingMatch", resolved.State(), binding)
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "in_case")); !got.Equal(Builtin("int")) {
		t.Fatalf("guarded match binding = %s (%q), want int", got, got.Reason())
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "sibling_case")); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("same-spelled sibling match binding = %s (%q), want no leaked Sprite2D fact", got, got.Reason())
	}
	for _, name := range []string{"fallback", "after"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); !got.Equal(Variant()) {
			t.Fatalf("%s = %s (%q), want Variant", name, got, got.Reason())
		}
	}
}

func TestNarrowingLambdaCaptureShadowAndMutationAreBindingScoped(t *testing.T) {
	source := sources(t, map[string]string{
		"lambda.gd": "class_name LambdaGuard\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar capture_lambda := func():\n\t\t\tvar captured := value.sprite_only\n\t\tvar shadow_lambda := func(value: Variant):\n\t\t\tvar shadowed := value.sprite_only\n\t\tvar mutation_lambda := func():\n\t\t\tvalue = null\n\t\t\tvar mutated := value.sprite_only\n\t\tvar outer := value.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("lambda.gd")
	lambdas := map[string]*ast.LambdaExpression{}
	var assignment *ast.Assignment
	ast.Inspect(file, func(node ast.Node) bool {
		if declaration, ok := node.(*ast.VariableDeclaration); ok {
			if lambda, ok := declaration.Value.(*ast.LambdaExpression); ok {
				lambdas[declaration.Name] = lambda
			}
		}
		if parsed, ok := node.(*ast.Assignment); ok {
			assignment = parsed
		}
		return true
	})
	if len(lambdas) != 3 || lambdas["capture_lambda"] == nil || lambdas["mutation_lambda"] == nil {
		t.Fatalf("real parser fixture lambdas = %#v, want capture/shadow/mutation lambdas", lambdas)
	}
	if shadow := lambdas["shadow_lambda"]; shadow == nil || len(shadow.Parameters) != 1 || shadow.Parameters[0].Name != "value" {
		t.Fatalf("real parser fixture shadow lambda = %#v, want one value parameter", shadow)
	}
	if assignment == nil || assignment.Operator != "=" {
		t.Fatalf("real parser fixture lambda assignment = %#v, want direct assignment", assignment)
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, testCase := range []struct {
		name string
		want Type
	}{
		{name: "captured", want: Builtin("int")},
		{name: "shadowed", want: Variant()},
		{name: "mutated", want: Variant()},
		{name: "outer", want: Builtin("int")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name)); !got.Equal(testCase.want) {
				t.Fatalf("%s = %s (%q), want %s", testCase.name, got, got.Reason(), testCase.want)
			}
		})
	}
}

func TestNarrowingRequiresAnEligibleFoundBinding(t *testing.T) {
	source := sources(t, map[string]string{
		"eligible.gd": "class_name Eligible\nvar field: Variant\nfunc run(value: Variant):\n\tvar shallow_unknown: Missing\n\tif shallow_unknown is Sprite2D:\n\t\tvar unknown_local := shallow_unknown.sprite_only\n\tif missing is Sprite2D:\n\t\tvar unresolved := missing\n\tif self.field is Sprite2D:\n\t\tvar member := self.field.sprite_only\n\tfor item in []:\n\t\tif item is Sprite2D:\n\t\t\tvar loop_item := item.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("eligible.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if binary, ok := node.(*ast.BinaryExpression); ok && binary.Operator == "is" {
			guards = append(guards, binary)
		}
		return true
	})
	if len(guards) != 4 {
		t.Fatalf("real parser fixture is guards = %d, want 4", len(guards))
	}
	if _, ok := guards[2].Left.(*ast.MemberExpression); !ok {
		t.Fatalf("real parser fixture member guard left = %T, want member expression", guards[2].Left)
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "unknown_local")); !got.Equal(Builtin("int")) {
		t.Fatalf("shallow Unknown local guard = %s (%q), want int", got, got.Reason())
	}
	for _, name := range []string{"unresolved", "loop_item"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); got.Kind() != KindUnknown || got.Reason() == "" {
			t.Fatalf("%s = %s (%q), want reasoned Unknown without a fact", name, got, got.Reason())
		}
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "member")); !got.Equal(Variant()) {
		t.Fatalf("member guard = %s (%q), want Variant without member fact", got, got.Reason())
	}
}

func TestNarrowingCacheViewsAreImmutableAndSnapshotBound(t *testing.T) {
	source := sources(t, map[string]string{
		"cache.gd": "class_name NarrowCache\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar narrowed := value\n\t\tvalue = null\n\t\tvar invalidated := value\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("cache.gd")
	before, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	engine := narrowTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	afterBuild, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(afterBuild) {
		t.Fatal("NewAnalyzer mutated the parsed AST while publishing narrowing views")
	}

	narrowed := reducerVariableValue(t, file, "narrowed")
	invalidated := reducerVariableValue(t, file, "invalidated")
	scope, ok := analyzer.scopes.ScopeAt(narrowed)
	if !ok {
		t.Fatal("narrowed expression has no recorded scope")
	}
	narrowedToken := analyzer.narrow.tokenAt(narrowed)
	invalidatedToken := analyzer.narrow.tokenAt(invalidated)
	if narrowedToken.overlay == nil || invalidatedToken.overlay == nil {
		t.Fatal("guarded and invalidated expressions need distinct source-bound overlays")
	}
	if narrowedToken == invalidatedToken {
		t.Fatal("positive and tombstone views reused one token")
	}
	if got := analyzer.typeOfIn(narrowed, scope, reductionContextToken{}).typeValue; !got.Equal(Variant()) {
		t.Fatalf("base query = %s (%q), want Variant", got, got.Reason())
	}
	if got := analyzer.TypeOf(narrowed); !got.Equal(engine.Class("Sprite2D")) {
		t.Fatalf("source-bound query = %s (%q), want Sprite2D", got, got.Reason())
	}
	if got := analyzer.TypeOf(invalidated); !got.Equal(Variant()) {
		t.Fatalf("tombstone query = %s (%q), want Variant", got, got.Reason())
	}
	resolved := analyzer.scopes.Resolve(narrowed.(*ast.Identifier))
	binding, found := resolved.Binding()
	if resolved.State() != LookupFound || !found {
		t.Fatalf("narrowed identifier did not resolve: %#v", resolved.State())
	}
	sibling := reductionContextToken{overlay: &reductionOverlay{
		owner:     analyzer.narrow,
		binding:   binding.ID(),
		typeValue: engine.Class("Node"),
		state:     narrowPositive,
		ordinal:   1,
	}}
	if sibling == narrowedToken || sibling == invalidatedToken {
		t.Fatal("independent persistent overlay reused a source-bound cache token")
	}
	if got := analyzer.typeOfIn(narrowed, scope, sibling).typeValue; !got.Equal(engine.Class("Node")) {
		t.Fatalf("sibling overlay query = %s (%q), want Node", got, got.Reason())
	}
	for _, key := range []reductionKey{
		{expression: narrowed, scope: scope.ID(), token: reductionContextToken{}},
		{expression: narrowed, scope: scope.ID(), token: narrowedToken},
		{expression: narrowed, scope: scope.ID(), token: sibling},
	} {
		if _, cached := analyzer.cache[key]; !cached {
			t.Fatalf("completed result missing cache key %#v", key)
		}
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
				if got := analyzer.TypeOf(narrowed); !got.Equal(engine.Class("Sprite2D")) {
					problems <- "narrowed: " + got.String()
					return
				}
				if got := analyzer.TypeOf(invalidated); !got.Equal(Variant()) {
					problems <- "invalidated: " + got.String()
					return
				}
			}
		}()
	}
	done.Wait()
	close(problems)
	for problem := range problems {
		t.Error(problem)
	}
	afterQueries, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(afterQueries) {
		t.Fatal("TypeOf mutated the parsed AST while reading narrowing views")
	}

	foreignSource := sources(t, map[string]string{
		"cache.gd": "class_name NarrowCache\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar narrowed := value\n",
	})
	foreign := reducerVariableValue(t, foreignSource.File("cache.gd"), "narrowed")
	if got := analyzer.TypeOf(foreign); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("foreign snapshot query = %s (%q), want reasoned Unknown", got, got.Reason())
	}
}

func TestNarrowingOnlyInvalidatesDirectExactAssignments(t *testing.T) {
	source := sources(t, map[string]string{
		"aliases.gd": "class_name Aliases\nfunc run(value: Variant, replacement: Variant):\n\tif value is Sprite2D:\n\t\tvar alias := value\n\t\talias = replacement\n\t\tvar after_alias := value.sprite_only\n\t\tvalue.sprite_only = 1\n\t\tvar after_member := value.sprite_only\n\t\tvalue[0] = 1\n\t\tvar after_subscript := value.sprite_only\n\t\tvalue += 1\n\t\tvar after_compound := value.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("aliases.gd")
	var assignments []*ast.Assignment
	ast.Inspect(file, func(node ast.Node) bool {
		if assignment, ok := node.(*ast.Assignment); ok {
			assignments = append(assignments, assignment)
		}
		return true
	})
	if len(assignments) != 4 || assignments[3].Operator != "+=" {
		t.Fatalf("real parser fixture assignments = %#v, want alias/member/subscript/compound", assignments)
	}
	if _, ok := assignments[0].Target.(*ast.Identifier); !ok {
		t.Fatalf("alias target = %T, want identifier", assignments[0].Target)
	}
	if _, ok := assignments[1].Target.(*ast.MemberExpression); !ok {
		t.Fatalf("member target = %T, want member expression", assignments[1].Target)
	}
	if _, ok := assignments[2].Target.(*ast.SubscriptExpression); !ok {
		t.Fatalf("subscript target = %T, want subscript expression", assignments[2].Target)
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, name := range []string{"after_alias", "after_member", "after_subscript"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); !got.Equal(Builtin("int")) {
			t.Fatalf("%s = %s (%q), want retained Sprite2D fact", name, got, got.Reason())
		}
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "after_compound")); !got.Equal(Variant()) {
		t.Fatalf("compound direct assignment = %s (%q), want restored Variant", got, got.Reason())
	}
}

func TestNarrowingPreservesSpecificFactsAndAppliesUnrelatedInnerFacts(t *testing.T) {
	source := sources(t, map[string]string{
		"nested.gd": "class_name Nested\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tif value is Node:\n\t\t\tvar preserved := value.sprite_only\n\t\tif value is Resource:\n\t\t\tvar unrelated := value.resource_only\n\t\tvar restored := value.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("nested.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if guard, ok := node.(*ast.BinaryExpression); ok && guard.Operator == "is" {
			guards = append(guards, guard)
		}
		return true
	})
	if len(guards) != 3 {
		t.Fatalf("real parser fixture nested guards = %d, want 3", len(guards))
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, name := range []string{"preserved", "unrelated", "restored"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); !got.Equal(Builtin("int")) {
			t.Fatalf("%s = %s (%q), want guarded property int", name, got, got.Reason())
		}
	}
}

func TestNarrowingFailsClosedForASharedGuardNode(t *testing.T) {
	source := sources(t, map[string]string{
		"shared.gd": "class_name SharedGuard\nfunc run(value: Variant, other: Variant):\n\tif value is Sprite2D:\n\t\tvar first := value.sprite_only\n\tif other is Sprite2D:\n\t\tvar second := other.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("shared.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if guard, ok := node.(*ast.BinaryExpression); ok && guard.Operator == "is" {
			guards = append(guards, guard)
		}
		return true
	})
	if len(guards) != 2 {
		t.Fatalf("real parser fixture guards = %d, want 2", len(guards))
	}
	shared, ok := guards[0].Left.(*ast.Identifier)
	if !ok {
		t.Fatalf("real parser fixture first guard left = %T, want identifier", guards[0].Left)
	}
	// Reuse one parser-produced node in another lexical position before
	// building the analyzer. ScopeIndex must mark that malformed identity
	// shared, and narrow must not attach a live flow view to it.
	guards[1].Left = shared
	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	if token := analyzer.narrow.tokenAt(shared); token.overlay != nil {
		t.Fatal("shared guard node received a live narrowing token")
	}
	if got := analyzer.TypeOf(shared); got.Kind() != KindUnknown || got.Reason() == "" {
		t.Fatalf("shared guard identifier = %s (%q), want reasoned Unknown", got, got.Reason())
	}
	for _, name := range []string{"first", "second"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); !got.Equal(Variant()) {
			t.Fatalf("%s under shared guard = %s (%q), want base Variant", name, got, got.Reason())
		}
	}
}

func TestNarrowingRejectsASharedGuardTarget(t *testing.T) {
	source := sources(t, map[string]string{
		"shared_target.gd": "class_name SharedTarget\nfunc run(value: Variant, other: Variant):\n\tif value is Sprite2D:\n\t\tvar first := value.sprite_only\n\tif other is Node:\n\t\tvar second := other.node_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("shared_target.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if guard, ok := node.(*ast.BinaryExpression); ok && guard.Operator == "is" {
			guards = append(guards, guard)
		}
		return true
	})
	if len(guards) != 2 {
		t.Fatalf("real parser fixture guards = %d, want 2", len(guards))
	}
	target, ok := guards[0].Right.(*ast.TypeExpression)
	if !ok || target.Name != "Sprite2D" {
		t.Fatalf("real parser fixture target = %#v, want Sprite2D type expression", guards[0].Right)
	}
	// Reusing this parser-produced target makes its source position ambiguous.
	// A valid left identifier alone must not let the guard manufacture a fact.
	guards[1].Right = target
	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	if scope, found := analyzer.scopes.ScopeAt(target); !found || scope.blocked == "" {
		t.Fatalf("shared target scope = %#v / %v, want blocked", scope, found)
	}
	for _, name := range []string{"first", "second"} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); !got.Equal(Variant()) {
			t.Fatalf("%s under shared target = %s (%q), want base Variant", name, got, got.Reason())
		}
	}
}

func TestNarrowingFactsNeverCrossEquivalentSnapshotIdentities(t *testing.T) {
	files := map[string]string{
		"identity.gd": "class_name Identity\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar narrowed := value\n",
	}
	firstSource := sources(t, files)
	secondSource := sources(t, files)
	if failures := append(firstSource.ParseFailures(), secondSource.ParseFailures()...); len(failures) != 0 {
		t.Fatalf("real parser fixtures failed: %v", failures)
	}
	first := NewAnalyzer(firstSource, narrowTestEngine(t))
	second := NewAnalyzer(secondSource, narrowTestEngine(t))
	firstExpression := reducerVariableValue(t, firstSource.File("identity.gd"), "narrowed")
	secondExpression := reducerVariableValue(t, secondSource.File("identity.gd"), "narrowed")
	firstToken := first.narrow.tokenAt(firstExpression)
	if firstToken.overlay == nil {
		t.Fatal("first snapshot did not publish a guarded token")
	}
	firstBinding, firstFound := first.scopes.Resolve(firstExpression.(*ast.Identifier)).Binding()
	secondBinding, secondFound := second.scopes.Resolve(secondExpression.(*ast.Identifier)).Binding()
	if !firstFound || !secondFound || firstBinding.ID().String() != secondBinding.ID().String() || firstBinding.ID() == secondBinding.ID() {
		t.Fatalf("fixture needs equal display IDs with distinct full identities: %#v / %#v", firstBinding, secondBinding)
	}
	secondScope, ok := second.scopes.ScopeAt(secondExpression)
	if !ok {
		t.Fatal("second snapshot expression has no scope")
	}
	if got := second.typeOfIn(secondExpression, secondScope, firstToken).typeValue; !got.Equal(Variant()) {
		t.Fatalf("foreign overlay token crossed equivalent snapshot = %s (%q), want Variant", got, got.Reason())
	}
}

func TestNarrowingNestedControlAssignmentsTombstoneOuterFacts(t *testing.T) {
	source := sources(t, map[string]string{
		"nested_assignment.gd": "class_name NestedAssignment\nfunc nested_if(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tif condition:\n\t\t\tvalue = null\n\t\tvar after_nested_if := value.sprite_only\nfunc nested_else(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tif condition:\n\t\t\tpass\n\t\telse:\n\t\t\tvalue = null\n\t\tvar after_nested_else := value.sprite_only\nfunc nested_lambda(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tif condition:\n\t\t\tvar deferred_assignment := func():\n\t\t\t\tvalue = null\n\t\tvar after_nested_lambda := value.sprite_only\nfunc while_body(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\twhile value.sprite_only:\n\t\t\tvar before_while_assignment := value.sprite_only\n\t\t\tvalue = null\n\t\t\tvar after_while_assignment := value.sprite_only\nfunc for_body(value: Variant, values: Array):\n\tif value is Sprite2D:\n\t\tfor item in value.sprite_only:\n\t\t\tvar before_for_assignment := value.sprite_only\n\t\t\tvalue = null\n\t\t\tvar after_for_assignment := value.sprite_only\nfunc match_case(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tmatch condition:\n\t\t\t_:\n\t\t\t\tvalue = null\n\t\tvar after_match_assignment := value.sprite_only\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("nested_assignment.gd")
	counts := map[string]int{}
	var whileStatement *ast.WhileStatement
	var forStatement *ast.ForStatement
	var deferredAssignment *ast.LambdaExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if declaration, ok := node.(*ast.VariableDeclaration); ok && declaration.Name == "deferred_assignment" {
			deferredAssignment, _ = declaration.Value.(*ast.LambdaExpression)
		}
		switch parsed := node.(type) {
		case *ast.IfStatement:
			counts["if"]++
		case *ast.WhileStatement:
			counts["while"]++
			whileStatement = parsed
		case *ast.ForStatement:
			counts["for"]++
			forStatement = parsed
		case *ast.MatchStatement:
			counts["match"]++
		case *ast.Assignment:
			counts["assignment"]++
		}
		return true
	})
	for _, kind := range []string{"if", "while", "for", "match", "assignment"} {
		if counts[kind] == 0 {
			t.Fatalf("real parser fixture did not retain %s: %v", kind, counts)
		}
	}
	if deferredAssignment == nil || len(deferredAssignment.Body) != 1 {
		t.Fatalf("real parser fixture deferred lambda = %#v, want one-body lambda", deferredAssignment)
	}
	if _, ok := deferredAssignment.Body[0].(*ast.Assignment); !ok {
		t.Fatalf("real parser fixture deferred lambda body = %T, want assignment", deferredAssignment.Body[0])
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, name := range []string{
		"after_nested_if", "after_nested_else", "before_while_assignment", "after_while_assignment",
		"before_for_assignment", "after_for_assignment", "after_match_assignment",
	} {
		if got := analyzer.TypeOf(reducerVariableValue(t, file, name)); !got.Equal(Variant()) {
			t.Fatalf("%s = %s (%q), want tombstoned base Variant", name, got, got.Reason())
		}
	}
	if got := analyzer.TypeOf(reducerVariableValue(t, file, "after_nested_lambda")); !got.Equal(Builtin("int")) {
		t.Fatalf("after_nested_lambda = %s (%q), want live Sprite2D fact", got, got.Reason())
	}
	if whileStatement == nil || forStatement == nil {
		t.Fatalf("real parser fixture loop statements = while:%#v for:%#v", whileStatement, forStatement)
	}
	for name, expression := range map[string]ast.Expression{
		"while condition": whileStatement.Condition,
		"for iterable":    forStatement.Iterable,
	} {
		if got := analyzer.TypeOf(expression); !got.Equal(Variant()) {
			t.Fatalf("%s = %s (%q), want pre-tombstoned Variant", name, got, got.Reason())
		}
	}
}

// TestNarrowingGodot47Oracle keeps the producer receipt opt-in: CI does not
// ship Godot, while the issue's verified 4.7.2 binary accepts the exact
// local/parameter, lambda capture, and match-binding source forms before
// release. --check-only is parser-only, so the parser-backed semantic tests
// above, rather than this producer receipt, assert narrowing precision.
func TestNarrowingGodot47Oracle(t *testing.T) {
	godot := strings.TrimSpace(os.Getenv("GODOT_BIN"))
	if godot == "" {
		t.Skip("GODOT_BIN is required for the Godot 4.7.2 producer oracle")
	}
	version, err := exec.Command(godot, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("Godot version probe failed: %v\n%s", err, version)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(version)), "4.7.2") {
		t.Fatalf("Godot producer is %q, want verified 4.7.2", strings.TrimSpace(string(version)))
	}
	writeProject := func(name, source string) string {
		t.Helper()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\nconfig/name=\"Narrowing Oracle\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	positive := writeProject("oracle.gd", "extends Node\n\nfunc require_sprite(sprite: Sprite2D) -> void:\n\tpass\n\nfunc verify(value: Node) -> void:\n\tif value is Sprite2D:\n\t\trequire_sprite(value)\n\t\tvar capture := func() -> void:\n\t\t\trequire_sprite(value)\n\tmatch value:\n\t\tvar matched when matched is Sprite2D:\n\t\t\trequire_sprite(matched)\n")
	output, err := exec.Command(godot, "--headless", "--path", positive, "--script", "res://oracle.gd", "--check-only").CombinedOutput()
	if err != nil {
		t.Fatalf("Godot 4.7.2 --check-only rejected local/parameter, lambda-capture, or match guard fixture: %v\n%s", err, output)
	}
}
