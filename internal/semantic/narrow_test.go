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

func narrowTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "float", "String", "StringName", "NodePath", "bool", "Array", "Dictionary", "Callable", "Signal", "Variant"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "Sprite2D", parent: "Node"},
		{name: "Resource", parent: "RefCounted"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddProperty("Node", "node_only", "int"))
	require.NoError(t, builder.AddProperty("Sprite2D", "sprite_only", "int"))
	require.NoError(t, builder.AddProperty("Resource", "resource_only", "int"))
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}

func TestNarrowingIfBodyUsesExactBindingFact(t *testing.T) {
	source := sources(t, map[string]string{
		"narrow.gd": "class_name Narrow\nfunc run(value: Variant):\n\tif value is Node:\n\t\tvar inside := value\n\tvar after := value\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("narrow.gd")
	var guarded *ast.IfStatement
	ast.Inspect(file, func(node ast.Node) bool {
		if statement, ok := node.(*ast.IfStatement); ok {
			guarded = statement
		}
		return true
	})
	require.NotNil(t, guarded)
	require.Len(t, guarded.Branches, 1)
	condition, ok := guarded.Branches[0].Condition.(*ast.BinaryExpression)
	require.True(t, ok)
	require.Equal(t, "is", condition.Operator)
	_, ok = condition.Left.(*ast.Identifier)
	require.True(t, ok)
	target, ok := condition.Right.(*ast.TypeExpression)
	require.True(t, ok)
	require.Equal(t, "Node", target.Name)

	analyzer := NewAnalyzer(source, reducerTestEngine(t))
	got := analyzer.TypeOf(reducerVariableValue(t, file, "inside"))
	require.True(t, got.Equal(reducerTestEngine(t).Class("Node")))
	got = analyzer.TypeOf(reducerVariableValue(t, file, "after"))
	require.True(t, got.Equal(Variant()))
}

func TestNarrowingDirectAssignmentInstallsTombstoneAfterTargetAndValue(t *testing.T) {
	source := sources(t, map[string]string{
		"assignment.gd": "class_name Assignment\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar before := value.sprite_only\n\t\tvalue = null\n\t\tvar after := value.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("assignment.gd")
	var assignment *ast.Assignment
	ast.Inspect(file, func(node ast.Node) bool {
		if parsed, ok := node.(*ast.Assignment); ok {
			assignment = parsed
		}
		return true
	})
	require.NotNil(t, assignment)
	require.Equal(t, "=", assignment.Operator)
	_, ok := assignment.Target.(*ast.Identifier)
	require.True(t, ok)
	literal, ok := assignment.Value.(*ast.Literal)
	require.True(t, ok)
	require.Equal(t, ast.NullLiteral, literal.Kind)

	engine := narrowTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	got := analyzer.TypeOf(reducerVariableValue(t, file, "before"))
	require.True(t, got.Equal(Builtin("int")))
	got = analyzer.TypeOf(assignment.Target)
	require.True(t, got.Equal(narrowTestEngine(t).Class("Sprite2D")))
	got = analyzer.TypeOf(reducerVariableValue(t, file, "after"))
	require.True(t, got.Equal(Variant()))
}

func TestNarrowingPreservesMoreSpecificBaseBindings(t *testing.T) {
	source := sources(t, map[string]string{
		"specific.gd": "class_name Specific\nfunc parameter(sprite: Sprite2D):\n\tif sprite is Node:\n\t\tvar parameter_member := sprite.sprite_only\nfunc array_local():\n\tvar nodes: Array[Node] = []\n\tif nodes is Array:\n\t\tvar array_inside := nodes\nfunc inferred_local():\n\tvar inferred := Sprite2D.new()\n\tif inferred is Node:\n\t\tvar inferred_member := inferred.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("specific.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if binary, ok := node.(*ast.BinaryExpression); ok && binary.Operator == "is" {
			guards = append(guards, binary)
		}
		return true
	})
	require.Len(t, guards, 3)
	for _, guard := range guards {
		_, ok := guard.Left.(*ast.Identifier)
		require.True(t, ok)
		_, ok = guard.Right.(*ast.TypeExpression)
		require.True(t, ok)
	}

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	got := analyzer.TypeOf(reducerVariableValue(t, file, "parameter_member"))
	require.True(t, got.Equal(Builtin("int")))
	got = analyzer.TypeOf(reducerVariableValue(t, file, "inferred_member"))
	require.True(t, got.Equal(Builtin("int")))
	node := narrowTestEngine(t).Class("Node")
	wantArray := Array(&node)
	got = analyzer.TypeOf(reducerVariableValue(t, file, "array_inside"))
	require.True(t, got.Equal(wantArray))
}

func TestNarrowingConditionsArePositiveAndScoped(t *testing.T) {
	source := sources(t, map[string]string{
		"conditions.gd": "class_name Conditions\nfunc check() -> bool:\n\treturn true\nfunc run(value: Variant, other: Variant):\n\tif value is Sprite2D && value is Node:\n\t\tvar positive := value.sprite_only\n\telif other is Node:\n\t\tvar elif_own := other.node_only\n\t\tvar elif_original := value.sprite_only\n\telse:\n\t\tvar otherwise := value.sprite_only\n\tif value is Sprite2D or other is Node:\n\t\tvar disjunction := value.sprite_only\n\tif !(value is Sprite2D):\n\t\tvar negated := value.sprite_only\n\tif value is not Sprite2D:\n\t\tvar negative := value.sprite_only\n\tif value == null:\n\t\tvar nil_checked := value.sprite_only\n\tif value:\n\t\tvar truthy := value.sprite_only\n\tif check() and value is Sprite2D:\n\t\tvar call_guard := value.sprite_only\n\tvar after := value.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
		require.NotZero(t, operators[operator])
	}
	require.NotNil(t, unaryNot)

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
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestNarrowingMatchBindingGuardStaysInItsCase(t *testing.T) {
	source := sources(t, map[string]string{
		"match.gd": "class_name MatchGuard\nfunc run(subject: Variant):\n\tmatch subject:\n\t\tvar captured when captured is Sprite2D:\n\t\t\tvar in_case := captured.sprite_only\n\t\tvar captured when captured is Node:\n\t\t\tvar sibling_case := captured.sprite_only\n\t\t_:\n\t\t\tvar fallback := subject\n\tvar after := subject\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("match.gd")
	var match *ast.MatchStatement
	ast.Inspect(file, func(node ast.Node) bool {
		if statement, ok := node.(*ast.MatchStatement); ok {
			match = statement
		}
		return true
	})
	require.NotNil(t, match)
	require.Len(t, match.Cases, 3)
	require.Len(t, match.Cases[0].Patterns, 1)
	patternBinding, ok := match.Cases[0].Patterns[0].(*ast.BindingPattern)
	require.True(t, ok)
	require.Equal(t, "captured", patternBinding.Name)
	guard, ok := match.Cases[0].Guard.(*ast.BinaryExpression)
	require.True(t, ok)
	require.Equal(t, "is", guard.Operator)

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	guarded, ok := guard.Left.(*ast.Identifier)
	require.True(t, ok)
	resolved := analyzer.scopes.Resolve(guarded)
	binding, found := resolved.Binding()
	require.Equal(t, LookupFound, resolved.State())
	require.True(t, found)
	require.Equal(t, BindingMatch, binding.Kind())
	got := analyzer.TypeOf(reducerVariableValue(t, file, "in_case"))
	require.True(t, got.Equal(Builtin("int")))
	got = analyzer.TypeOf(reducerVariableValue(t, file, "sibling_case"))
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	for _, name := range []string{"fallback", "after"} {
		got = analyzer.TypeOf(reducerVariableValue(t, file, name))
		require.True(t, got.Equal(Variant()))
	}
}

func TestNarrowingLambdaCaptureShadowAndMutationAreBindingScoped(t *testing.T) {
	source := sources(t, map[string]string{
		"lambda.gd": "class_name LambdaGuard\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar capture_lambda := func():\n\t\t\tvar captured := value.sprite_only\n\t\tvar shadow_lambda := func(value: Variant):\n\t\t\tvar shadowed := value.sprite_only\n\t\tvar mutation_lambda := func():\n\t\t\tvalue = null\n\t\t\tvar mutated := value.sprite_only\n\t\tvar outer := value.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
	require.Len(t, lambdas, 3)
	require.NotNil(t, lambdas["capture_lambda"])
	require.NotNil(t, lambdas["mutation_lambda"])
	shadow := lambdas["shadow_lambda"]
	require.NotNil(t, shadow)
	require.Len(t, shadow.Parameters, 1)
	require.Equal(t, "value", shadow.Parameters[0].Name)
	require.NotNil(t, assignment)
	require.Equal(t, "=", assignment.Operator)

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
			got := analyzer.TypeOf(reducerVariableValue(t, file, testCase.name))
			require.True(t, got.Equal(testCase.want))
		})
	}
}

func TestNarrowingRequiresAnEligibleFoundBinding(t *testing.T) {
	source := sources(t, map[string]string{
		"eligible.gd": "class_name Eligible\nvar field: Variant\nfunc run(value: Variant):\n\tvar shallow_unknown: Missing\n\tif shallow_unknown is Sprite2D:\n\t\tvar unknown_local := shallow_unknown.sprite_only\n\tif missing is Sprite2D:\n\t\tvar unresolved := missing\n\tif self.field is Sprite2D:\n\t\tvar member := self.field.sprite_only\n\tfor item in []:\n\t\tif item is Sprite2D:\n\t\t\tvar loop_item := item.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("eligible.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if binary, ok := node.(*ast.BinaryExpression); ok && binary.Operator == "is" {
			guards = append(guards, binary)
		}
		return true
	})
	require.Len(t, guards, 4)
	_, ok := guards[2].Left.(*ast.MemberExpression)
	require.True(t, ok)

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	got := analyzer.TypeOf(reducerVariableValue(t, file, "unknown_local"))
	require.True(t, got.Equal(Builtin("int")))
	for _, name := range []string{"unresolved", "loop_item"} {
		got = analyzer.TypeOf(reducerVariableValue(t, file, name))
		require.Equal(t, KindUnknown, got.Kind())
		require.NotEmpty(t, got.Reason())
	}
	got = analyzer.TypeOf(reducerVariableValue(t, file, "member"))
	require.True(t, got.Equal(Variant()))
}

func TestNarrowingCacheViewsAreImmutableAndSnapshotBound(t *testing.T) {
	source := sources(t, map[string]string{
		"cache.gd": "class_name NarrowCache\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar narrowed := value\n\t\tvalue = null\n\t\tvar invalidated := value\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("cache.gd")
	before, err := json.Marshal(file)
	require.NoError(t, err)
	engine := narrowTestEngine(t)
	analyzer := NewAnalyzer(source, engine)
	afterBuild, err := json.Marshal(file)
	require.NoError(t, err)
	require.Equal(t, string(before), string(afterBuild), "NewAnalyzer mutated the parsed AST while publishing narrowing views")

	narrowed := reducerVariableValue(t, file, "narrowed")
	invalidated := reducerVariableValue(t, file, "invalidated")
	scope, ok := analyzer.scopes.ScopeAt(narrowed)
	require.True(t, ok, "narrowed expression has no recorded scope")
	narrowedToken := analyzer.narrow.tokenAt(narrowed)
	invalidatedToken := analyzer.narrow.tokenAt(invalidated)
	require.NotNil(t, narrowedToken.overlay)
	require.NotNil(t, invalidatedToken.overlay)
	require.False(t, narrowedToken == invalidatedToken, "positive and tombstone views reused one token")
	got := analyzer.typeOfIn(narrowed, scope, reductionContextToken{}).typeValue
	require.True(t, got.Equal(Variant()))
	got = analyzer.TypeOf(narrowed)
	require.True(t, got.Equal(engine.Class("Sprite2D")))
	got = analyzer.TypeOf(invalidated)
	require.True(t, got.Equal(Variant()))
	resolved := analyzer.scopes.Resolve(narrowed.(*ast.Identifier))
	binding, found := resolved.Binding()
	require.Equal(t, LookupFound, resolved.State())
	require.True(t, found)
	sibling := reductionContextToken{overlay: &reductionOverlay{
		owner:     analyzer.narrow,
		binding:   binding.ID(),
		typeValue: engine.Class("Node"),
		state:     narrowPositive,
		ordinal:   1,
	}}
	require.False(t, sibling == narrowedToken)
	require.False(t, sibling == invalidatedToken)
	got = analyzer.typeOfIn(narrowed, scope, sibling).typeValue
	require.True(t, got.Equal(engine.Class("Node")))
	for _, key := range []reductionKey{
		{expression: narrowed, scope: scope.ID(), token: reductionContextToken{}},
		{expression: narrowed, scope: scope.ID(), token: narrowedToken},
		{expression: narrowed, scope: scope.ID(), token: sibling},
	} {
		_, cached := analyzer.cache[key]
		require.True(t, cached)
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
		assert.Fail(t, problem)
	}
	afterQueries, err := json.Marshal(file)
	require.NoError(t, err)
	require.Equal(t, string(before), string(afterQueries), "TypeOf mutated the parsed AST while reading narrowing views")

	foreignSource := sources(t, map[string]string{
		"cache.gd": "class_name NarrowCache\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar narrowed := value\n",
	})
	foreign := reducerVariableValue(t, foreignSource.File("cache.gd"), "narrowed")
	got = analyzer.TypeOf(foreign)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
}

func TestNarrowingOnlyInvalidatesDirectExactAssignments(t *testing.T) {
	source := sources(t, map[string]string{
		"aliases.gd": "class_name Aliases\nfunc run(value: Variant, replacement: Variant):\n\tif value is Sprite2D:\n\t\tvar alias := value\n\t\talias = replacement\n\t\tvar after_alias := value.sprite_only\n\t\tvalue.sprite_only = 1\n\t\tvar after_member := value.sprite_only\n\t\tvalue[0] = 1\n\t\tvar after_subscript := value.sprite_only\n\t\tvalue += 1\n\t\tvar after_compound := value.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("aliases.gd")
	var assignments []*ast.Assignment
	ast.Inspect(file, func(node ast.Node) bool {
		if assignment, ok := node.(*ast.Assignment); ok {
			assignments = append(assignments, assignment)
		}
		return true
	})
	require.Len(t, assignments, 4)
	require.Equal(t, "+=", assignments[3].Operator)
	_, ok := assignments[0].Target.(*ast.Identifier)
	require.True(t, ok)
	_, ok = assignments[1].Target.(*ast.MemberExpression)
	require.True(t, ok)
	_, ok = assignments[2].Target.(*ast.SubscriptExpression)
	require.True(t, ok)

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, name := range []string{"after_alias", "after_member", "after_subscript"} {
		got := analyzer.TypeOf(reducerVariableValue(t, file, name))
		require.True(t, got.Equal(Builtin("int")))
	}
	got := analyzer.TypeOf(reducerVariableValue(t, file, "after_compound"))
	require.True(t, got.Equal(Variant()))
}

func TestNarrowingPreservesSpecificFactsAndAppliesUnrelatedInnerFacts(t *testing.T) {
	source := sources(t, map[string]string{
		"nested.gd": "class_name Nested\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tif value is Node:\n\t\t\tvar preserved := value.sprite_only\n\t\tif value is Resource:\n\t\t\tvar unrelated := value.resource_only\n\t\tvar restored := value.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("nested.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if guard, ok := node.(*ast.BinaryExpression); ok && guard.Operator == "is" {
			guards = append(guards, guard)
		}
		return true
	})
	require.Len(t, guards, 3)

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, name := range []string{"preserved", "unrelated", "restored"} {
		got := analyzer.TypeOf(reducerVariableValue(t, file, name))
		require.True(t, got.Equal(Builtin("int")))
	}
}

func TestNarrowingFailsClosedForASharedGuardNode(t *testing.T) {
	source := sources(t, map[string]string{
		"shared.gd": "class_name SharedGuard\nfunc run(value: Variant, other: Variant):\n\tif value is Sprite2D:\n\t\tvar first := value.sprite_only\n\tif other is Sprite2D:\n\t\tvar second := other.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("shared.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if guard, ok := node.(*ast.BinaryExpression); ok && guard.Operator == "is" {
			guards = append(guards, guard)
		}
		return true
	})
	require.Len(t, guards, 2)
	shared, ok := guards[0].Left.(*ast.Identifier)
	require.True(t, ok)
	// Reuse one parser-produced node in another lexical position before
	// building the analyzer. ScopeIndex must mark that malformed identity
	// shared, and narrow must not attach a live flow view to it.
	guards[1].Left = shared
	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	token := analyzer.narrow.tokenAt(shared)
	require.Nil(t, token.overlay, "shared guard node received a live narrowing token")
	got := analyzer.TypeOf(shared)
	require.Equal(t, KindUnknown, got.Kind())
	require.NotEmpty(t, got.Reason())
	for _, name := range []string{"first", "second"} {
		got = analyzer.TypeOf(reducerVariableValue(t, file, name))
		require.True(t, got.Equal(Variant()))
	}
}

func TestNarrowingRejectsASharedGuardTarget(t *testing.T) {
	source := sources(t, map[string]string{
		"shared_target.gd": "class_name SharedTarget\nfunc run(value: Variant, other: Variant):\n\tif value is Sprite2D:\n\t\tvar first := value.sprite_only\n\tif other is Node:\n\t\tvar second := other.node_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
	file := source.File("shared_target.gd")
	var guards []*ast.BinaryExpression
	ast.Inspect(file, func(node ast.Node) bool {
		if guard, ok := node.(*ast.BinaryExpression); ok && guard.Operator == "is" {
			guards = append(guards, guard)
		}
		return true
	})
	require.Len(t, guards, 2)
	target, ok := guards[0].Right.(*ast.TypeExpression)
	require.True(t, ok)
	require.Equal(t, "Sprite2D", target.Name)
	// Reusing this parser-produced target makes its source position ambiguous.
	// A valid left identifier alone must not let the guard manufacture a fact.
	guards[1].Right = target
	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	scope, found := analyzer.scopes.ScopeAt(target)
	require.True(t, found)
	require.NotEmpty(t, scope.blocked)
	for _, name := range []string{"first", "second"} {
		got := analyzer.TypeOf(reducerVariableValue(t, file, name))
		require.True(t, got.Equal(Variant()))
	}
}

func TestNarrowingFactsNeverCrossEquivalentSnapshotIdentities(t *testing.T) {
	files := map[string]string{
		"identity.gd": "class_name Identity\nfunc run(value: Variant):\n\tif value is Sprite2D:\n\t\tvar narrowed := value\n",
	}
	firstSource := sources(t, files)
	secondSource := sources(t, files)
	require.Empty(t, append(firstSource.ParseFailures(), secondSource.ParseFailures()...), "real parser fixtures failed")
	first := NewAnalyzer(firstSource, narrowTestEngine(t))
	second := NewAnalyzer(secondSource, narrowTestEngine(t))
	firstExpression := reducerVariableValue(t, firstSource.File("identity.gd"), "narrowed")
	secondExpression := reducerVariableValue(t, secondSource.File("identity.gd"), "narrowed")
	firstToken := first.narrow.tokenAt(firstExpression)
	require.NotNil(t, firstToken.overlay, "first snapshot did not publish a guarded token")
	firstBinding, firstFound := first.scopes.Resolve(firstExpression.(*ast.Identifier)).Binding()
	secondBinding, secondFound := second.scopes.Resolve(secondExpression.(*ast.Identifier)).Binding()
	require.True(t, firstFound)
	require.True(t, secondFound)
	require.Equal(t, secondBinding.ID().String(), firstBinding.ID().String())
	require.False(t, firstBinding.ID() == secondBinding.ID(), "fixture needs distinct full identities")
	secondScope, ok := second.scopes.ScopeAt(secondExpression)
	require.True(t, ok, "second snapshot expression has no scope")
	got := second.typeOfIn(secondExpression, secondScope, firstToken).typeValue
	require.True(t, got.Equal(Variant()))
}

func TestNarrowingNestedControlAssignmentsTombstoneOuterFacts(t *testing.T) {
	source := sources(t, map[string]string{
		"nested_assignment.gd": "class_name NestedAssignment\nfunc nested_if(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tif condition:\n\t\t\tvalue = null\n\t\tvar after_nested_if := value.sprite_only\nfunc nested_else(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tif condition:\n\t\t\tpass\n\t\telse:\n\t\t\tvalue = null\n\t\tvar after_nested_else := value.sprite_only\nfunc nested_lambda(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tif condition:\n\t\t\tvar deferred_assignment := func():\n\t\t\t\tvalue = null\n\t\tvar after_nested_lambda := value.sprite_only\nfunc while_body(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\twhile value.sprite_only:\n\t\t\tvar before_while_assignment := value.sprite_only\n\t\t\tvalue = null\n\t\t\tvar after_while_assignment := value.sprite_only\nfunc for_body(value: Variant, values: Array):\n\tif value is Sprite2D:\n\t\tfor item in value.sprite_only:\n\t\t\tvar before_for_assignment := value.sprite_only\n\t\t\tvalue = null\n\t\t\tvar after_for_assignment := value.sprite_only\nfunc match_case(value: Variant, condition: bool):\n\tif value is Sprite2D:\n\t\tmatch condition:\n\t\t\t_:\n\t\t\t\tvalue = null\n\t\tvar after_match_assignment := value.sprite_only\n",
	})
	require.Empty(t, source.ParseFailures(), "real parser fixture failed")
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
		require.NotZero(t, counts[kind])
	}
	require.NotNil(t, deferredAssignment)
	require.Len(t, deferredAssignment.Body, 1)
	_, ok := deferredAssignment.Body[0].(*ast.Assignment)
	require.True(t, ok)

	analyzer := NewAnalyzer(source, narrowTestEngine(t))
	for _, name := range []string{
		"after_nested_if", "after_nested_else", "before_while_assignment", "after_while_assignment",
		"before_for_assignment", "after_for_assignment", "after_match_assignment",
	} {
		got := analyzer.TypeOf(reducerVariableValue(t, file, name))
		require.True(t, got.Equal(Variant()))
	}
	got := analyzer.TypeOf(reducerVariableValue(t, file, "after_nested_lambda"))
	require.True(t, got.Equal(Builtin("int")))
	require.NotNil(t, whileStatement)
	require.NotNil(t, forStatement)
	for _, expression := range map[string]ast.Expression{
		"while condition": whileStatement.Condition,
		"for iterable":    forStatement.Iterable,
	} {
		got = analyzer.TypeOf(expression)
		require.True(t, got.Equal(Variant()))
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
	require.NoError(t, err, "Godot version probe failed")
	require.True(t, strings.HasPrefix(strings.TrimSpace(string(version)), "4.7.2"))
	writeProject := func(name, source string) string {
		t.Helper()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\nconfig/name=\"Narrowing Oracle\"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(source), 0o644))
		return root
	}
	positive := writeProject("oracle.gd", "extends Node\n\nfunc require_sprite(sprite: Sprite2D) -> void:\n\tpass\n\nfunc verify(value: Node) -> void:\n\tif value is Sprite2D:\n\t\trequire_sprite(value)\n\t\tvar capture := func() -> void:\n\t\t\trequire_sprite(value)\n\tmatch value:\n\t\tvar matched when matched is Sprite2D:\n\t\t\trequire_sprite(matched)\n")
	_, err = exec.Command(godot, "--headless", "--path", positive, "--script", "res://oracle.gd", "--check-only").CombinedOutput()
	require.NoError(t, err, "Godot 4.7.2 --check-only rejected local/parameter, lambda-capture, or match guard fixture")
}
