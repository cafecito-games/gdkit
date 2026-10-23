package semantic

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBindingKindsHaveStableVocabulary(t *testing.T) {
	cases := []struct {
		kind BindingKind
		want string
	}{
		{BindingLocal, "local"},
		{BindingParameter, "parameter"},
		{BindingFor, "for"},
		{BindingMatch, "match"},
		{BindingSetter, "setter"},
		{BindingMember, "member"},
		{BindingEnclosingConstant, "enclosing-constant"},
		{BindingEnclosingType, "enclosing-type"},
		{BindingProjectClass, "project-class"},
		{BindingAutoload, "autoload"},
		{BindingEngineType, "engine-type"},
		{BindingEngineSingleton, "engine-singleton"},
		{BindingEngineUtility, "engine-utility"},
		{BindingLanguageSpecial, "language-special"},
		{BindingSelf, "self"},
		{BindingSuper, "super"},
	}
	seen := map[string]bool{}
	for _, testCase := range cases {
		got := testCase.kind.String()
		assert.Equal(t, testCase.want, got)
		assert.False(t, seen[testCase.want])
		seen[testCase.want] = true
	}
	got, want := BindingKind(255).String(), fmt.Sprintf("BindingKind(%d)", 255)
	require.Equal(t, want, got)
}

func TestScopesRespectDeclarationPointsAndBlockLifetimes(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run(value: int):\n\tvar before = before\n\tvar local = value\n\tif true:\n\t\tvar branch = local\n\t\tbranch\n\tbranch\n\tlocal\n",
	})
	interfaces := BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t))
	scopes := BuildScopes(interfaces)
	file := source.File("player.gd")

	before := scopeIdentifiersNamed(t, file, "before")
	require.Len(t, before, 1)
	scopeRequireUnknown(t, scopes, before[0], "before declaration")

	value := scopeIdentifiersNamed(t, file, "value")
	require.Len(t, value, 1)
	parameter := scopeRequireBinding(t, scopes, value[0], BindingParameter)
	require.Equal(t, KindBuiltin, parameter.Type().Kind())
	require.Equal(t, "int", parameter.Type().Name())

	branch := scopeIdentifiersNamed(t, file, "branch")
	require.Len(t, branch, 2)
	scopeRequireBinding(t, scopes, branch[0], BindingLocal)
	scopeRequireUnknown(t, scopes, branch[1], "branch expired")

	local := scopeIdentifiersNamed(t, file, "local")
	require.Len(t, local, 2)
	first := scopeRequireBinding(t, scopes, local[0], BindingLocal)
	second := scopeRequireBinding(t, scopes, local[1], BindingLocal)
	require.Equal(t, second.ID(), first.ID())
}

func TestScopesHandleParametersLambdasAndAccessors(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nvar property: int:\n\tget:\n\t\tproperty\n\tset(next):\n\t\tnext\nfunc defaults(first: int, second = first, third = later, later: int = 3, self_default = self_default):\n\tpass\nfunc captures():\n\tvar captured = 1\n\tvar callable = func named(captured):\n\t\tcaptured\n\t\tnamed\n\tcaptured\n",
	})
	interfaces := BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t))
	scopes := BuildScopes(interfaces)
	file := source.File("player.gd")

	member := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "property", 4), BindingMember)
	require.Equal(t, KindBuiltin, member.Type().Kind())
	require.Equal(t, "int", member.Type().Name())
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "next", 6), BindingSetter)

	first := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "first", 7), BindingParameter)
	require.Equal(t, 0, first.Slot())
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "later", 7), "later parameter default")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "self_default", 7), "self parameter default")

	lambdaParameter := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "captured", 12), BindingParameter)
	outerLocal := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "captured", 14), BindingLocal)
	require.False(t, lambdaParameter.ID() == outerLocal.ID(), "lambda parameter did not shadow the captured outer local")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "named", 13), "lambda debug name")
}

func TestScopesLetDefaultLambdasCaptureEarlierParameters(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc configure(first: int, callback = func(): return first):\n\tpass\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	file := source.File("player.gd")
	captured := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "first", 2), BindingParameter)
	require.Equal(t, 0, captured.Slot())
}

func TestScopesKeepStaticPropertyAccessorsStatic(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nstatic var property: int:\n\tget:\n\t\tself\n\t\tproperty\n\tset(next):\n\t\tself\n\t\tnext\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	file := source.File("player.gd")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "self", 4), "static getter self")
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "property", 5), BindingMember)
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "self", 7), "static setter self")
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "next", 8), BindingSetter)
}

func TestScopesKeepLambdasInTheirCreationStaticContext(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nstatic func build():\n\tvar callback = func():\n\t\tself\n\tcallback\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	file := source.File("player.gd")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "self", 4), "static lambda self")
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "callback", 5), BindingLocal)
}

func TestScopesIsolateEveryControlFlowBodyAndMatchCase(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run(input):\n\tif input:\n\t\tvar in_if = 1\n\t\tin_if\n\telif input:\n\t\tvar in_elif = 2\n\t\tin_elif\n\telse:\n\t\tvar in_else = 3\n\t\tin_else\n\tin_if\n\twhile input:\n\t\tvar in_while = 4\n\t\tin_while\n\tin_while\n\tfor item: int in []:\n\t\titem\n\titem\n\tmatch input:\n\t\t[[var left], {\"key\": [var right]}]:\n\t\t\tleft\n\t\t\tright\n\t\tvar lone:\n\t\t\tlone\n\tleft\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	file := source.File("player.gd")

	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "in_if", line: 5},
		{name: "in_elif", line: 8},
		{name: "in_else", line: 11},
		{name: "in_while", line: 15},
	} {
		scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), BindingLocal)
	}
	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "in_if", line: 12},
		{name: "in_while", line: 16},
		{name: "item", line: 19},
		{name: "left", line: 26},
	} {
		scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), "expired control-flow binding")
	}
	item := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "item", 18), BindingFor)
	require.Equal(t, KindBuiltin, item.Type().Kind())
	require.Equal(t, "int", item.Type().Name())
	left := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "left", 22), BindingMatch)
	right := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "right", 23), BindingMatch)
	lone := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "lone", 25), BindingMatch)
	require.False(t, left.ID() == right.ID())
	require.False(t, left.ID() == lone.ID())
	require.False(t, right.ID() == lone.ID())
}

func TestScopesKeepSiblingBranchesIsolated(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run(input):\n\tif input:\n\t\tvar only_first = 1\n\telif input:\n\t\tonly_first\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, source.File("player.gd"), "only_first", 6), "sibling branch binding")
}

func TestScopesInstallMatchBindingsBeforeTheirGuard(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run(input):\n\tmatch input:\n\t\tvar captured when captured:\n\t\t\tcaptured\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	file := source.File("player.gd")
	guard := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "captured", 4), BindingMatch)
	body := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "captured", 5), BindingMatch)
	require.Equal(t, body.ID(), guard.ID())
}

func TestScopesFailClosedForMalformedDuplicateBindings(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run(value):\n\tpass\n",
	})
	function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
	duplicateUse := &ast.Identifier{Name: "value"}
	function.Body = []ast.Statement{
		&ast.VariableDeclaration{Name: "value", Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}},
		&ast.ExpressionStatement{Expression: duplicateUse},
	}
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, scopes, duplicateUse, "duplicate local over parameter")

	matchSource := sources(t, map[string]string{
		"match.gd": "class_name Match\nfunc run(input):\n\tmatch input:\n\t\t_:\n\t\t\tpass\n",
	})
	matchFunction := matchSource.File("match.gd").Statements[1].(*ast.FunctionDeclaration)
	match := matchFunction.Body[0].(*ast.MatchStatement)
	duplicatedUse := &ast.Identifier{Name: "duplicated"}
	match.Cases[0].Patterns = []ast.Expression{&ast.ArrayLiteral{Elements: []ast.Expression{
		&ast.BindingPattern{Name: "duplicated"},
		&ast.BindingPattern{Name: "duplicated"},
	}}}
	match.Cases[0].Body = []ast.Statement{&ast.ExpressionStatement{Expression: duplicatedUse}}
	matchScopes := BuildScopes(BuildInterfaces(BuildIndex(matchSource), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, matchScopes, duplicatedUse, "duplicate match binding")

	incompatibleSource := sources(t, map[string]string{
		"match.gd": "class_name Match\nfunc run(input):\n\tmatch input:\n\t\t_:\n\t\t\tpass\n",
	})
	incompatibleFunction := incompatibleSource.File("match.gd").Statements[1].(*ast.FunctionDeclaration)
	incompatibleMatch := incompatibleFunction.Body[0].(*ast.MatchStatement)
	firstUse := &ast.Identifier{Name: "first"}
	secondUse := &ast.Identifier{Name: "second"}
	incompatibleMatch.Cases[0].Patterns = []ast.Expression{
		&ast.BindingPattern{Name: "first"},
		&ast.BindingPattern{Name: "second"},
	}
	incompatibleMatch.Cases[0].Body = []ast.Statement{
		&ast.ExpressionStatement{Expression: firstUse},
		&ast.ExpressionStatement{Expression: secondUse},
	}
	incompatibleScopes := BuildScopes(BuildInterfaces(BuildIndex(incompatibleSource), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, incompatibleScopes, firstUse, "incompatible match alternatives")
	scopeRequireUnknown(t, incompatibleScopes, secondUse, "incompatible match alternatives")

	multiPatternSource := sources(t, map[string]string{
		"match.gd": "class_name Match\nfunc run(input):\n\tmatch input:\n\t\t_:\n\t\t\tpass\n",
	})
	multiPatternFunction := multiPatternSource.File("match.gd").Statements[1].(*ast.FunctionDeclaration)
	multiPatternMatch := multiPatternFunction.Body[0].(*ast.MatchStatement)
	multiPatternUse := &ast.Identifier{Name: "bound"}
	multiPatternMatch.Cases[0].Patterns = []ast.Expression{
		&ast.BindingPattern{Name: "bound"},
		&ast.BindingPattern{Name: "bound"},
	}
	multiPatternMatch.Cases[0].Body = []ast.Statement{&ast.ExpressionStatement{Expression: multiPatternUse}}
	multiPatternScopes := BuildScopes(BuildInterfaces(BuildIndex(multiPatternSource), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, multiPatternScopes, multiPatternUse, "variable binding across multiple match patterns")
}

func TestScopesFailClosedForUnsupportedStatementBoundaries(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nvar illegal: int\nvar shorthand: int\nvar static_illegal: int\nfunc run():\n\tpass\n",
	})
	function := source.File("player.gd").Statements[4].(*ast.FunctionDeclaration)
	unsupportedUse := &ast.Identifier{Name: "Native"}
	accessorUse := &ast.Identifier{Name: "Native"}
	accessorSibling := &ast.Identifier{Name: "illegal"}
	shorthandSibling := &ast.Identifier{Name: "shorthand"}
	staticInitializer := &ast.Identifier{Name: "Native"}
	staticSibling := &ast.Identifier{Name: "static_illegal"}
	functionSibling := &ast.Identifier{Name: "utility"}
	classSibling := &ast.Identifier{Name: "Native"}
	signalSibling := &ast.Identifier{Name: "Single"}
	enumSibling := &ast.Identifier{Name: "Global"}
	function.Body = []ast.Statement{
		&ast.EnumDeclaration{Members: []ast.EnumMember{{Name: "VALUE", Value: unsupportedUse}}},
		&ast.VariableDeclaration{Name: "illegal", Getter: []ast.Statement{&ast.ExpressionStatement{Expression: accessorUse}}},
		&ast.ExpressionStatement{Expression: accessorSibling},
		&ast.VariableDeclaration{Name: "shorthand", GetterName: "getter"},
		&ast.ExpressionStatement{Expression: shorthandSibling},
		&ast.VariableDeclaration{Name: "static_illegal", Static: true, Value: staticInitializer},
		&ast.ExpressionStatement{Expression: staticSibling},
		&ast.FunctionDeclaration{Name: "utility"},
		&ast.ExpressionStatement{Expression: functionSibling},
		&ast.ClassDeclaration{Name: "Native"},
		&ast.ExpressionStatement{Expression: classSibling},
		&ast.SignalDeclaration{Name: "Single"},
		&ast.ExpressionStatement{Expression: signalSibling},
		&ast.EnumDeclaration{Name: "Global"},
		&ast.ExpressionStatement{Expression: enumSibling},
	}
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeNamespaceEngine(t)))
	unsupportedStatement := function.Body[0]
	unsupportedScope, ok := scopes.ScopeAt(unsupportedStatement)
	require.True(t, ok, "unsupported statement was not indexed")
	const unsupportedReason = "unsupported nested *ast.EnumDeclaration has no established lexical scope boundary"
	require.Equal(t, unsupportedReason, unsupportedScope.blocked)
	scopeRequireUnknown(t, scopes, unsupportedUse, "unsupported nested enum")
	scopeRequireUnknown(t, scopes, accessorUse, "unsupported local accessor")
	for _, testCase := range []struct {
		name string
		node *ast.Identifier
	}{
		{name: "unsupported accessor sibling", node: accessorSibling},
		{name: "unsupported shorthand accessor sibling", node: shorthandSibling},
		{name: "unsupported static initializer", node: staticInitializer},
		{name: "unsupported static sibling", node: staticSibling},
		{name: "unsupported function sibling", node: functionSibling},
		{name: "unsupported class sibling", node: classSibling},
		{name: "unsupported signal sibling", node: signalSibling},
		{name: "unsupported enum sibling", node: enumSibling},
	} {
		scopeRequireUnknown(t, scopes, testCase.node, testCase.name)
	}
}

func TestScopesFailClosedAfterUnmodelledStatementBoundary(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tpass\n",
	})
	function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
	use := &ast.Identifier{Name: "Node"}
	// A directive cannot occur in a function in parsed GDScript, but it is a
	// concrete ast.Statement that ScopeIndex has no lexical model for. It pins
	// the generic unsupported-statement continuation used for future parser
	// statement kinds with unknown declaration behavior.
	function.Body = []ast.Statement{
		&ast.Directive{Name: "unexpected"},
		&ast.ExpressionStatement{Expression: use},
	}

	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, scopes, use, "unmodelled statement continuation")
}

func TestScopesFailClosedAfterSharedUnmodelledStatementBoundary(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc first():\n\tpass\nfunc second():\n\tpass\n",
	})
	file := source.File("player.gd")
	first := file.Statements[1].(*ast.FunctionDeclaration)
	second := file.Statements[2].(*ast.FunctionDeclaration)
	shared := &ast.Directive{Name: "unexpected"}
	firstUse := &ast.Identifier{Name: "Node"}
	secondUse := &ast.Identifier{Name: "Node"}
	first.Body = []ast.Statement{
		shared,
		&ast.ExpressionStatement{Expression: firstUse},
	}
	second.Body = []ast.Statement{
		shared,
		&ast.ExpressionStatement{Expression: secondUse},
	}

	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, scopes, firstUse, "first shared unmodelled statement continuation")
	scopeRequireUnknown(t, scopes, secondUse, "second shared unmodelled statement continuation")
}

func TestScopesUseStaticContextForConstantInitializers(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nvar field: int\nconst SELF_VALUE = self\nconst FIELD_VALUE = field\nfunc run():\n\tconst LOCAL_SELF = self\n\tconst LOCAL_FIELD = field\nfunc values(parameter):\n\tvar mutable = 1\n\tconst FROM_PARAMETER = parameter\n\tconst FROM_MUTABLE = mutable\n\tconst FIRST = 1\n\tconst FROM_CONST = FIRST\n",
	})
	require.Empty(t, source.ParseFailures(), "constant fixture did not parse")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	file := source.File("player.gd")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "self", 3), "constant self")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "field", 4), "constant instance member")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "self", 6), "local constant self")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "field", 7), "local constant instance member")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "parameter", 10), "local constant parameter")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "mutable", 11), "local constant mutable local")
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "FIRST", 13), BindingLocal)
}

func TestScopesDoNotResolveSuperInConstantInitializers(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":  "class_name Base\nstatic func inherited():\n\tpass\n",
		"child.gd": "class_name Child extends Base\nstatic func inherited():\n\tconst VALUE = super()\n",
	})
	require.Empty(t, source.ParseFailures(), "constant super fixture did not parse")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, source.File("child.gd"), "super", 3), "constant super")
}

func TestScopesDoNotResolveExplicitSuperMembersInConstantInitializers(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":  "class_name Base\nstatic func build():\n\treturn 1\n",
		"child.gd": "class_name Child extends Base\nconst CLASS_VALUE = super.build()\nenum { ENUM_VALUE = super.build() }\nstatic func run():\n\tconst LOCAL_VALUE = super.build()\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	file := source.File("child.gd")
	for _, line := range []int{2, 3, 5} {
		scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "super", line), "explicit super in constant initializer")
	}
}

func TestScopesFreezeConstantBindingFacts(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tconst FIRST = 1\n\tconst SECOND = FIRST\n",
	})
	function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
	first := function.Body[0].(*ast.VariableDeclaration)
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	first.Constant = false
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, source.File("player.gd"), "FIRST", 4), BindingLocal)
}

func TestScopesRestrictNonConstantNamespacesInConstantExpressions(t *testing.T) {
	source := sources(t, map[string]string{
		"autoload.gd": "class_name Auto\n",
		"player.gd":   "class_name Player\nstatic var static_field: int\nstatic func static_method():\n\tpass\nconst TOP_STATIC_FIELD = static_field\nconst TOP_STATIC_METHOD = static_method\nconst TOP_AUTOLOAD = AutoLoad\nconst TOP_SINGLE = Single\nconst TOP_PRELOAD = preload(\"res://thing.gd\")\nconst TOP_LOAD = load(\"res://thing.gd\")\nconst TOP_UTILITY = utility()\nenum { BAD = static_field }\nfunc run():\n\tconst LOCAL_STATIC_FIELD = static_field\n\tconst LOCAL_STATIC_METHOD = static_method\n\tconst LOCAL_AUTOLOAD = AutoLoad\n\tconst LOCAL_SINGLE = Single\n\tconst LOCAL_UTILITY = utility()\n",
	})
	source.autoloads["AutoLoad"] = "autoload.gd"
	require.Empty(t, source.ParseFailures(), "constant namespace fixture did not parse")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeNamespaceEngine(t)))
	file := source.File("player.gd")
	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "static_field", line: 5},
		{name: "static_method", line: 6},
		{name: "AutoLoad", line: 7},
		{name: "Single", line: 8},
		{name: "load", line: 10},
		{name: "utility", line: 11},
		{name: "static_field", line: 12},
		{name: "static_field", line: 14},
		{name: "static_method", line: 15},
		{name: "AutoLoad", line: 16},
		{name: "Single", line: 17},
		{name: "utility", line: 18},
	} {
		scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), "non-constant namespace")
	}
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "preload", 9), BindingLanguageSpecial)
}

func TestScopesDoNotLetMalformedLexicalBindingsShadowReservedNames(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tpass\n",
	})
	function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
	selfUse := &ast.Identifier{Name: "self"}
	function.Body = []ast.Statement{
		&ast.VariableDeclaration{Name: "self", Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}},
		&ast.ExpressionStatement{Expression: selfUse},
	}
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, scopes, selfUse, "malformed lexical self")
}

func TestScopesFailClosedForSharedAndTypedNilASTNodes(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tpass\n",
	})
	function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
	shared := &ast.Identifier{Name: "local"}
	var nilCall *ast.CallExpression
	var nilVariable *ast.VariableDeclaration
	function.Body = []ast.Statement{
		&ast.ExpressionStatement{Expression: shared},
		nilVariable,
		&ast.VariableDeclaration{Name: "local", Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}},
		&ast.ExpressionStatement{Expression: shared},
		&ast.ExpressionStatement{Expression: nilCall},
	}
	interfaces := BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t))
	scopes := BuildScopes(interfaces)
	scopeRequireUnknown(t, scopes, shared, "shared AST node")
	_, ok := scopes.ScopeAt(nilCall)
	assert.False(t, ok)
	_, ok = scopes.ScopeAt(nilVariable)
	assert.False(t, ok)
	// The published interface snapshot remains immutable. Exercise the direct
	// scope-builder boundary with a detached malformed class record instead of
	// mutating the snapshot that InterfaceSet has already validated.
	malformedClass := *interfaces.index.Classes["player.gd"]
	malformedClass.Declarations = append(append([]Declaration(nil), malformedClass.Declarations...), Declaration{Node: nilVariable})
	malformedScopes := &ScopeIndex{interfaces: interfaces, scopes: map[ast.Node]*Scope{}}
	malformedScopes.buildClass(&malformedClass)
	_, ok = malformedScopes.ScopeAt(nilVariable)
	assert.False(t, ok)
}

func TestScopesFailClosedForSharedDeclarationNodes(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc first():\n\tpass\nfunc second():\n\tpass\n",
	})
	file := source.File("player.gd")
	first := file.Statements[1].(*ast.FunctionDeclaration)
	second := file.Statements[2].(*ast.FunctionDeclaration)
	shared := &ast.VariableDeclaration{Name: "value", Value: &ast.Literal{Kind: ast.IntegerLiteral, Raw: "1"}}
	firstUse := &ast.Identifier{Name: "value"}
	secondUse := &ast.Identifier{Name: "value"}
	first.Body = []ast.Statement{shared, &ast.ExpressionStatement{Expression: firstUse}}
	second.Body = []ast.Statement{shared, &ast.ExpressionStatement{Expression: secondUse}}

	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	scopeRequireUnknown(t, scopes, firstUse, "first shared declaration")
	scopeRequireUnknown(t, scopes, secondUse, "second shared declaration")
	sharedScope, ok := scopes.ScopeAt(shared)
	require.True(t, ok, "shared declaration was not indexed")
	result := sharedScope.Lookup("value")
	require.Equal(t, LookupUnknown, result.State())
	require.Equal(t, "AST node is shared between lexical positions", result.Reason())
}

func TestScopesFailClosedForSharedAndCyclicExpressionGraphs(t *testing.T) {
	t.Run("diamond", func(t *testing.T) {
		source := sources(t, map[string]string{
			"player.gd": "class_name Player\nfunc run():\n\tpass\n",
		})
		function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
		leaf := &ast.Identifier{Name: "Node"}
		var expression ast.Expression = leaf
		for range 24 {
			expression = &ast.BinaryExpression{Left: expression, Operator: "+", Right: expression}
		}
		function.Body = []ast.Statement{&ast.ExpressionStatement{Expression: expression}}

		scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
		scopeRequireUnknown(t, scopes, leaf, "shared expression DAG")
	})

	t.Run("cycle", func(t *testing.T) {
		source := sources(t, map[string]string{
			"player.gd": "class_name Player\nfunc run():\n\tpass\n",
		})
		function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
		cycle := &ast.BinaryExpression{Operator: "+"}
		cycleUse := &ast.Identifier{Name: "Node"}
		cycle.Left = cycle
		cycle.Right = cycleUse
		function.Body = []ast.Statement{&ast.ExpressionStatement{Expression: cycle}}

		scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
		scopeRequireUnknown(t, scopes, cycleUse, "cyclic expression")
	})

	t.Run("cycle inside unsupported statement", func(t *testing.T) {
		source := sources(t, map[string]string{
			"player.gd": "class_name Player\nfunc run():\n\tpass\n",
		})
		function := source.File("player.gd").Statements[1].(*ast.FunctionDeclaration)
		nested := &ast.FunctionDeclaration{Name: "nested"}
		cycle := &ast.BinaryExpression{Operator: "+"}
		cycleUse := &ast.Identifier{Name: "Node"}
		cycle.Left = cycle
		cycle.Right = cycleUse
		nested.Body = []ast.Statement{&ast.ExpressionStatement{Expression: cycle}}
		function.Body = []ast.Statement{nested}

		scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
		scopeRequireUnknown(t, scopes, cycleUse, "cyclic unsupported statement")
	})
}

func TestScopesInheritSharedPatternBlocksIntoNestedFrames(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":   "class_name Base\nfunc run(input):\n\tpass\n",
		"player.gd": "class_name Player extends Base\nfunc run(input):\n\tmatch input:\n\t\t_:\n\t\t\tif true:\n\t\t\t\tNode\n\t\t\tvar callback = func():\n\t\t\t\tNode\n\t\t\tsuper()\n",
	})
	file := source.File("player.gd")
	function := file.Statements[1].(*ast.FunctionDeclaration)
	match := function.Body[0].(*ast.MatchStatement)
	binding := &ast.BindingPattern{Name: "Node"}
	match.Cases[0].Patterns = []ast.Expression{&ast.ArrayLiteral{Elements: []ast.Expression{binding, binding}}}

	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "Node", line: 6},
		{name: "Node", line: 8},
		{name: "super", line: 9},
	} {
		identifier := scopeIdentifierAt(t, file, testCase.name, testCase.line)
		scope, ok := scopes.ScopeAt(identifier)
		require.True(t, ok)
		result := scope.Lookup(testCase.name)
		require.Equal(t, LookupUnknown, result.State())
		require.Equal(t, sharedASTNodeReason, result.Reason())
	}
}

func TestScopesComposeLocalMemberProjectAndEngineNamespaces(t *testing.T) {
	source := sources(t, map[string]string{
		"global.gd":   "class_name Global\n",
		"autoload.gd": "class_name Auto\n",
		"player.gd":   "class_name Player\nvar member: int\nvar unresolved = dynamic_value()\nfunc run():\n\tvar member = 1\n\tmember\n\tunresolved\n\tGlobal\n\tAutoLoad\n\tNative\n\tSingle\n\tutility\n\tpreload(\"res://thing.gd\")\n\tload(\"res://thing.gd\")\n",
	})
	source.autoloads["AutoLoad"] = "autoload.gd"
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeNamespaceEngine(t)))
	file := source.File("player.gd")

	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "member", 6), BindingLocal)
	unresolved := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "unresolved", 7), BindingMember)
	require.Equal(t, KindUnknown, unresolved.Type().Kind())
	require.NotEmpty(t, unresolved.Type().Reason())
	for _, testCase := range []struct {
		name    string
		line    int
		kind    BindingKind
		classID string
	}{
		{name: "Global", line: 8, kind: BindingProjectClass, classID: "global.gd"},
		{name: "AutoLoad", line: 9, kind: BindingAutoload, classID: "autoload.gd"},
		{name: "Native", line: 10, kind: BindingEngineType},
		{name: "Single", line: 11, kind: BindingEngineSingleton},
		{name: "utility", line: 12, kind: BindingEngineUtility},
		{name: "preload", line: 13, kind: BindingLanguageSpecial},
		{name: "load", line: 14, kind: BindingLanguageSpecial},
	} {
		binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), testCase.kind)
		if testCase.classID != "" {
			assert.Equal(t, testCase.classID, binding.ClassID())
		}
	}
	for _, testCase := range []struct {
		name string
		line int
		kind BindingKind
		meta bool
	}{
		{name: "Global", line: 8, kind: BindingProjectClass, meta: true},
		{name: "AutoLoad", line: 9, kind: BindingAutoload, meta: false},
		{name: "Native", line: 10, kind: BindingEngineType, meta: true},
	} {
		binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), testCase.kind)
		assert.Equal(t, KindClass, binding.Type().Kind())
		assert.Equal(t, testCase.meta, binding.Type().Meta())
	}
}

func TestScopesResolveSameNamedEngineSingletonAsInstance(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nconst DEFAULT_INPUT = Input\nfunc run():\n\tInput\n",
	})
	file := source.File("player.gd")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeNamespaceEngine(t)))

	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "Input", 2), "non-constant singleton")
	binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "Input", 4), BindingEngineSingleton)
	require.Equal(t, KindClass, binding.Type().Kind())
	require.Equal(t, "Input", binding.Type().Name())
	require.False(t, binding.Type().Meta())
}

func TestScopesKeepSameNamedSingletonTypeCollisionsUnknown(t *testing.T) {
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddBuiltin("BuiltinOnly"))
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "Foo", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddSingleton("BuiltinOnly", "Object"))
	require.NoError(t, builder.AddSingleton("Foo", "Object"))
	engine, err := builder.Build()
	require.NoError(t, err)
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tBuiltinOnly\n\tFoo\n",
	})
	file := source.File("player.gd")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), engine))

	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "BuiltinOnly", 3), "builtin/singleton collision")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "Foo", 4), "different-type singleton collision")
}

func TestScopesKeepSameNamedSingletonUtilityCollisionUnknown(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "Input", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddSingleton("Input", "Input"))
	require.NoError(t, builder.AddUtility("Input", "int", nil, false))
	engine, err := builder.Build()
	require.NoError(t, err)
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tInput\n",
	})
	file := source.File("player.gd")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), engine))

	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "Input", 3), "singleton/utility collision")
}

func TestScopesKeepBuiltinTypeObjectsFoundButUnknown(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tint\n\tString\n\tCallable\n",
	})
	require.Empty(t, source.ParseFailures(), "builtin type fixture did not parse")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeNamespaceEngine(t)))
	file := source.File("player.gd")
	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "int", line: 3},
		{name: "String", line: 4},
		{name: "Callable", line: 5},
	} {
		binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), BindingEngineType)
		assert.Equal(t, KindUnknown, binding.Type().Kind())
		assert.NotEmpty(t, binding.Type().Reason())
	}
}

func TestScopesKeepBuiltinConstructorCapabilityPrivateAndSnapshotBound(t *testing.T) {
	builder := NewEngineBuilder()
	for _, name := range []string{"Vector2", "Dormant", "Collision", "int"} {
		require.NoError(t, builder.AddBuiltin(name))
	}
	require.NoError(t, builder.AddClass("Object", ""))
	require.NoError(t, builder.AddClass("RefCounted", "Object"))
	for _, name := range []string{"Vector2", "Collision"} {
		require.NoError(t, builder.AddBuiltinConstructor(name))
	}
	require.NoError(t, builder.AddUtility("Collision", "int", nil, false))
	engine, err := builder.Build()
	require.NoError(t, err)
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tVector2\n\tDormant\n\tCollision\n",
	})
	file := source.File("player.gd")
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), engine))

	binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "Vector2", 3), BindingEngineType)
	require.Equal(t, KindUnknown, binding.Type().Kind())
	require.NotEmpty(t, binding.Type().Reason())
	capability, ok := binding.builtinConstructorCapability()
	require.True(t, ok)
	require.Same(t, engine, capability.engine)
	require.Same(t, scopes, capability.index)
	require.Equal(t, "Vector2", capability.name)
	require.True(t, capability.result.Equal(engine.ResolveType("Vector2")))
	clone := cloneBinding(binding)
	clonedCapability, ok := clone.builtinConstructorCapability()
	require.True(t, ok)
	require.False(t, clonedCapability == capability)
	require.Same(t, capability.engine, clonedCapability.engine)
	require.Same(t, capability.index, clonedCapability.index)
	require.True(t, clonedCapability.result.Equal(capability.result))

	dormant := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "Dormant", 4), BindingEngineType)
	dormantCapability, ok := dormant.builtinConstructorCapability()
	require.False(t, ok)
	require.Nil(t, dormantCapability)
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "Collision", 5), "builtin/utility collision")

	second := BuildScopes(BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tVector2\n",
	})), engine))
	require.False(t, capability.index == second, "separately parsed snapshot reused constructor capability identity")
}

func TestScopesGiveOneMemberDeclarationOneBindingIdentity(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":  "class_name Base\nconst LIMIT = 1\nfunc direct():\n\tLIMIT\n",
		"child.gd": "class_name Child extends Base\nfunc inherited():\n\tLIMIT\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	direct := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, source.File("base.gd"), "LIMIT", 4), BindingMember)
	inherited := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, source.File("child.gd"), "LIMIT", 3), BindingMember)
	require.Equal(t, inherited.ID(), direct.ID())
}

func TestScopesKeepFoundUnknownMembersAheadOfRetainedGlobals(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nvar instance_field = dynamic_value()\nfunc run():\n\tinstance_field\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, source.File("player.gd"), "instance_field", 4), BindingMember)
	require.Equal(t, KindUnknown, binding.Type().Kind())
	require.NotEmpty(t, binding.Type().Reason())
}

func TestScopesRetainUserMemberDeclarationProvenance(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nconst LIMIT = 1\nfunc run():\n\tLIMIT\n",
	})
	file := source.File("player.gd")
	declaration := file.Statements[1]
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "LIMIT", 4), BindingMember)
	require.Same(t, declaration, binding.Declaration())
}

func TestScopesFailClosedForIncompleteAndAmbiguousGlobalEvidence(t *testing.T) {
	broken := sources(t, map[string]string{
		"global.gd": "class_name Global\n",
		"broken.gd": "class_name Broken extends MissingBase\nfunc run():\n\tGlobal\n",
	})
	brokenScopes := BuildScopes(BuildInterfaces(BuildIndex(broken), scopeNamespaceEngine(t)))
	scopeRequireUnknown(t, brokenScopes, scopeIdentifierAt(t, broken.File("broken.gd"), "Global", 3), "incomplete member chain")

	ambiguous := sources(t, map[string]string{
		"first.gd":  "class_name Duplicate\n",
		"second.gd": "class_name Duplicate\n",
		"player.gd": "class_name Player\nfunc run():\n\tDuplicate\n\tMissingConstant\n\tClash\n",
	})
	ambiguousScopes := BuildScopes(BuildInterfaces(BuildIndex(ambiguous), scopeNamespaceEngine(t)))
	file := ambiguous.File("player.gd")
	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "Duplicate", line: 3},
		{name: "MissingConstant", line: 4},
		{name: "Clash", line: 5},
	} {
		scopeRequireUnknown(t, ambiguousScopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), "ambiguous or unretained global")
	}
}

func TestScopesFailClosedForAutoloadCollisionsAndParseFailures(t *testing.T) {
	source := sources(t, map[string]string{
		"class.gd":  "class_name Shared\n",
		"auto.gd":   "class_name Auto\n",
		"player.gd": "class_name Player\nfunc run():\n\tShared\n\tMissingAuto\n",
	})
	source.autoloads["Shared"] = "auto.gd"
	source.autoloads["MissingAuto"] = "missing.gd"
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeNamespaceEngine(t)))
	file := source.File("player.gd")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "Shared", 3), "class-name/autoload collision")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "MissingAuto", 4), "unindexed autoload")

	failed := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tNative\n",
	})
	failed.paths = append(failed.paths, "broken.gd")
	failed.failures = append(failed.failures, "broken.gd")
	failedScopes := BuildScopes(BuildInterfaces(BuildIndex(failed), scopeNamespaceEngine(t)))
	scopeRequireUnknown(t, failedScopes, scopeIdentifierAt(t, failed.File("player.gd"), "Native", 3), "parse-incomplete project globals")
}

func TestScopesFailClosedWithoutAnEngineSchema(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run():\n\tNative\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), nil))
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, source.File("player.gd"), "Native", 3), "unavailable engine schema")
}

func TestScopesRestrictInnerClassesToEnclosingConstantsAndTypes(t *testing.T) {
	source := sources(t, map[string]string{
		"outer.gd": "class_name Outer\nconst LIMIT: int = 1\nenum Mode { ONE }\nenum { FLAG }\nvar instance: int\nstatic var static_field: int\nfunc method():\n\tpass\nclass Child:\n\tfunc run():\n\t\tLIMIT\n\t\tMode\n\t\tFLAG\n\t\tSibling\n\t\tinstance\n\t\tstatic_field\n\t\tmethod\n\t\tself\nclass Sibling:\n\tpass\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	file := source.File("outer.gd")
	for _, testCase := range []struct {
		name string
		line int
		kind BindingKind
	}{
		{name: "LIMIT", line: 11, kind: BindingEnclosingConstant},
		{name: "Mode", line: 12, kind: BindingEnclosingType},
		{name: "FLAG", line: 13, kind: BindingEnclosingConstant},
		{name: "Sibling", line: 14, kind: BindingEnclosingType},
	} {
		binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), testCase.kind)
		assert.Equal(t, "outer.gd", binding.ClassID())
	}
	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "instance", line: 15},
		{name: "static_field", line: 16},
		{name: "method", line: 17},
	} {
		scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), "inaccessible enclosing member")
	}
	self := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "self", 18), BindingSelf)
	require.Equal(t, KindClass, self.Type().Kind())
	require.Equal(t, "outer.gd#Child", self.Type().Name())
}

func TestScopesSearchBaseInnerClassEnclosuresBeforeOwnEnclosure(t *testing.T) {
	source := sources(t, map[string]string{
		"other.gd":  "class_name Other\nconst SPEED = 3\nclass Inner:\n\tpass\n",
		"player.gd": "class_name Player\nconst SPEED = \"wrong\"\nclass Child extends Other.Inner:\n\tfunc run():\n\t\tSPEED\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, source.File("player.gd"), "SPEED", 5), BindingEnclosingConstant)
	require.Equal(t, "other.gd", binding.ClassID())
}

func TestScopesDoNotFallThroughInaccessibleEnclosingMembers(t *testing.T) {
	source := sources(t, map[string]string{
		"outer.gd": "class_name Outer\nvar instance_field: int\nfunc instance_method():\n\tpass\nclass Child:\n\tfunc run():\n\t\tinstance_field\n\t\tinstance_method()\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	file := source.File("outer.gd")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "instance_field", 7), "inaccessible enclosing variable must block singleton fallback")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "instance_method", 8), "inaccessible enclosing method must block utility fallback")
}

func TestScopesDoNotTreatHashInTopLevelPathsAsInnerClassSyntax(t *testing.T) {
	source := sources(t, map[string]string{
		"global.gd":             "class_name Global\n",
		"levels/#boss/enemy.gd": "class_name Enemy\nfunc run():\n\tGlobal\n\tNative\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeNamespaceEngine(t)))
	file := source.File("levels/#boss/enemy.gd")
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "Global", 3), BindingProjectClass)
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "Native", 4), BindingEngineType)
}

func TestScopesEnforceStaticContextAndResolveCompatibleSuper(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":  "class_name Base\nvar instance_field: int\nstatic var static_field: int\nfunc instance_method():\n\tpass\nstatic func static_method():\n\tpass\n",
		"child.gd": "class_name Child extends Base\nstatic func static_method():\n\tself\n\tinstance_field\n\tinstance_method()\n\tstatic_field\n\tstatic_method()\n\tsuper()\nfunc instance_method():\n\tself\n\tinstance_field\n\tstatic_field\n\tsuper()\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	file := source.File("child.gd")

	for _, testCase := range []struct {
		name string
		line int
	}{
		{name: "self", line: 3},
		{name: "instance_field", line: 4},
		{name: "instance_method", line: 5},
	} {
		scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, testCase.name, testCase.line), "static context")
	}
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "static_field", 6), BindingMember)
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "static_method", 7), BindingMember)
	staticSuper := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "super", 8), BindingSuper)
	staticBase, ok := staticSuper.SuperMember()
	require.True(t, ok)
	require.True(t, staticBase.Static())
	require.Equal(t, "base.gd", staticBase.DeclaringClassID())

	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "self", 10), BindingSelf)
	instanceField := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "instance_field", 11), BindingMember)
	assert.Equal(t, "base.gd", instanceField.ClassID())
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "static_field", 12), BindingMember)
	instanceSuper := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "super", 13), BindingSuper)
	instanceBase, ok := instanceSuper.SuperMember()
	require.True(t, ok)
	require.False(t, instanceBase.Static())
	require.Equal(t, "base.gd", instanceBase.DeclaringClassID())
}

func TestScopesResolveExplicitSuperMembersByTheirOwnName(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":  "class_name Base\nfunc alternate():\n\tpass\nstatic func static_alternate():\n\tpass\n",
		"child.gd": "class_name Child extends Base\nfunc caller():\n\tsuper.alternate()\nstatic func static_caller():\n\tsuper.static_alternate()\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	for _, testCase := range []struct {
		line   int
		method string
		static bool
	}{
		{line: 3, method: "alternate", static: false},
		{line: 5, method: "static_alternate", static: true},
	} {
		binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, source.File("child.gd"), "super", testCase.line), BindingSuper)
		member, ok := binding.SuperMember()
		require.True(t, ok)
		require.Equal(t, testCase.method, member.Name())
		require.Equal(t, testCase.static, member.Static())
		require.Equal(t, "base.gd", member.DeclaringClassID())
	}
}

func TestScopesFailClosedForInvalidSuperContexts(t *testing.T) {
	source := sources(t, map[string]string{
		"base.gd":   "class_name Base\nfunc shared():\n\tpass\nfunc inherited():\n\tpass\n",
		"child.gd":  "class_name Child extends Base\nstatic func shared():\n\tsuper()\nstatic func fresh():\n\tsuper()\nfunc inherited():\n\tvar callback = func(): return super()\n",
		"broken.gd": "class_name Broken extends MissingBase\nstatic func run():\n\tsuper()\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	for _, testCase := range []struct {
		file string
		line int
		name string
	}{
		{file: "child.gd", line: 3, name: "static/instance mismatch"},
		{file: "child.gd", line: 5, name: "missing base method"},
		{file: "child.gd", line: 7, name: "lambda super"},
		{file: "broken.gd", line: 3, name: "incomplete base chain"},
	} {
		scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, source.File(testCase.file), "super", testCase.line), testCase.name)
	}
}

func TestScopesAreDeterministicImmutableConcurrentAndSnapshotBound(t *testing.T) {
	files := map[string]string{
		"global.gd": "class_name Global\n",
		"player.gd": "class_name Player\nvar field: int\nfunc run(value: int):\n\tvar local = value\n\tlocal\n\tfield\n\tGlobal\n\tNode\n\tself\n",
	}
	firstSource := sources(t, files)
	firstIndex := BuildIndex(firstSource)
	interfaces := BuildInterfaces(firstIndex, richInterfaceTestEngine(t))
	before, err := json.Marshal(firstSource.File("player.gd"))
	require.NoError(t, err)
	firstScopes := BuildScopes(interfaces)
	after, err := json.Marshal(firstSource.File("player.gd"))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "building scopes mutated the parsed AST")
	secondScopes := BuildScopes(interfaces)
	firstFile := firstSource.File("player.gd")
	firstUse := scopeIdentifierAt(t, firstFile, "local", 5)
	memberUse := scopeIdentifierAt(t, firstFile, "field", 6)
	projectUse := scopeIdentifierAt(t, firstFile, "Global", 7)
	engineUse := scopeIdentifierAt(t, firstFile, "Node", 8)
	selfUse := scopeIdentifierAt(t, firstFile, "self", 9)
	firstBinding := scopeRequireBinding(t, firstScopes, firstUse, BindingLocal)
	secondBinding := scopeRequireBinding(t, secondScopes, firstUse, BindingLocal)
	require.False(t, firstBinding.ID() == secondBinding.ID(), "separate scope builds reused a binding identity")
	require.Equal(t, secondBinding.Name(), firstBinding.Name())
	require.Equal(t, secondBinding.Kind(), firstBinding.Kind())
	require.Equal(t, secondBinding.ClassID(), firstBinding.ClassID())
	require.Equal(t, secondBinding.Slot(), firstBinding.Slot())

	firstIndex.Classes["player.gd"].Declarations[0].Name = "mutated-member"
	firstIndex.byClassName["Global"] = firstIndex.Classes["player.gd"]
	again := scopeRequireBinding(t, firstScopes, firstUse, BindingLocal)
	require.Equal(t, "local", again.Name())
	again = scopeRequireBinding(t, firstScopes, memberUse, BindingMember)
	require.Equal(t, "field", again.Name())
	require.Equal(t, "player.gd", again.ClassID())
	again = scopeRequireBinding(t, firstScopes, projectUse, BindingProjectClass)
	require.Equal(t, "global.gd", again.ClassID())

	secondSource := sources(t, files)
	secondInterfaces := BuildInterfaces(BuildIndex(secondSource), richInterfaceTestEngine(t))
	secondSnapshot := BuildScopes(secondInterfaces)
	secondUse := scopeIdentifierAt(t, secondSource.File("player.gd"), "local", 5)
	_, found := firstScopes.ScopeAt(secondUse)
	require.False(t, found, "first scope index accepted a node from a separately parsed snapshot")
	require.False(t, firstBinding.ID() == scopeRequireBinding(t, secondSnapshot, secondUse, BindingLocal).ID(), "separately parsed snapshot reused a binding identity")

	const readers = 24
	const readsPerReader = 100
	problems := make(chan string, readers)
	lookups := []struct {
		name       string
		identifier *ast.Identifier
		kind       BindingKind
		classID    string
	}{
		{name: "local", identifier: firstUse, kind: BindingLocal, classID: "player.gd"},
		{name: "member", identifier: memberUse, kind: BindingMember, classID: "player.gd"},
		{name: "project", identifier: projectUse, kind: BindingProjectClass, classID: "global.gd"},
		{name: "engine", identifier: engineUse, kind: BindingEngineType},
		{name: "self", identifier: selfUse, kind: BindingSelf, classID: "player.gd"},
	}
	var readersDone sync.WaitGroup
	readersDone.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer readersDone.Done()
			for attempt := 0; attempt < readsPerReader; attempt++ {
				for _, lookup := range lookups {
					result := firstScopes.Resolve(lookup.identifier)
					binding, found := result.Binding()
					if result.State() != LookupFound || !found || binding.Kind() != lookup.kind || binding.Name() != lookup.identifier.Name || (lookup.classID != "" && binding.ClassID() != lookup.classID) {
						problems <- fmt.Sprintf("concurrent %s lookup observed an incomplete binding", lookup.name)
						return
					}
				}
			}
		}()
	}
	readersDone.Wait()
	close(problems)
	for problem := range problems {
		assert.Fail(t, problem)
	}
}

func scopeIdentifiersNamed(t *testing.T, file *ast.File, name string) []*ast.Identifier {
	t.Helper()
	var identifiers []*ast.Identifier
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Identifier)
		if ok && identifier.Name == name {
			identifiers = append(identifiers, identifier)
		}
		return true
	})
	return identifiers
}

func scopeIdentifierAt(t *testing.T, file *ast.File, name string, line int) *ast.Identifier {
	t.Helper()
	var found []*ast.Identifier
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Identifier)
		if ok && identifier.Name == name && identifier.Span().Start.Line == line {
			found = append(found, identifier)
		}
		return true
	})
	require.Len(t, found, 1)
	return found[0]
}

func scopeRequireBinding(t *testing.T, scopes *ScopeIndex, identifier *ast.Identifier, want BindingKind) Binding {
	t.Helper()
	scope, ok := scopes.ScopeAt(identifier)
	require.True(t, ok)
	result := scope.Lookup(identifier.Name)
	require.Equal(t, LookupFound, result.State())
	binding, ok := result.Binding()
	require.True(t, ok)
	require.Equal(t, want, binding.Kind())
	return binding
}

func scopeRequireUnknown(t *testing.T, scopes *ScopeIndex, identifier *ast.Identifier, label string) {
	t.Helper()
	scope, ok := scopes.ScopeAt(identifier)
	require.True(t, ok)
	result := scope.Lookup(identifier.Name)
	require.Equal(t, LookupUnknown, result.State())
	require.NotEmpty(t, result.Reason())
}

func scopeNamespaceEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "String", "Callable", "Clash"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Native", parent: "Object"},
		{name: "Global", parent: "Object"},
		{name: "Input", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddSingleton("Single", "Native"))
	require.NoError(t, builder.AddSingleton("Input", "Input"))
	require.NoError(t, builder.AddSingleton("Clash", "Object"))
	require.NoError(t, builder.AddUtility("utility", "int", nil, false))
	require.NoError(t, builder.AddUtility("Clash", "int", nil, false))
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}

func scopeStaticEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddBuiltin("int"))
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddSingleton("instance_field", "Object"))
	require.NoError(t, builder.AddUtility("instance_method", "int", nil, false))
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}
