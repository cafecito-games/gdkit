package semantic

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
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
		if got := testCase.kind.String(); got != testCase.want {
			t.Errorf("%d.String() = %q, want %q", testCase.kind, got, testCase.want)
		}
		if seen[testCase.want] {
			t.Errorf("binding vocabulary repeats %q", testCase.want)
		}
		seen[testCase.want] = true
	}
	if got, want := BindingKind(255).String(), fmt.Sprintf("BindingKind(%d)", 255); got != want {
		t.Fatalf("unknown binding kind = %q, want %q", got, want)
	}
}

func TestScopesRespectDeclarationPointsAndBlockLifetimes(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc run(value: int):\n\tvar before = before\n\tvar local = value\n\tif true:\n\t\tvar branch = local\n\t\tbranch\n\tbranch\n\tlocal\n",
	})
	interfaces := BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t))
	scopes := BuildScopes(interfaces)
	file := source.File("player.gd")

	before := scopeIdentifiersNamed(t, file, "before")
	if len(before) != 1 {
		t.Fatalf("before identifiers = %d, want 1", len(before))
	}
	scopeRequireUnknown(t, scopes, before[0], "before declaration")

	value := scopeIdentifiersNamed(t, file, "value")
	if len(value) != 1 {
		t.Fatalf("value identifiers = %d, want 1", len(value))
	}
	parameter := scopeRequireBinding(t, scopes, value[0], BindingParameter)
	if parameter.Type().Kind() != KindBuiltin || parameter.Type().Name() != "int" {
		t.Fatalf("parameter type = %v %q (%q)", parameter.Type().Kind(), parameter.Type().Name(), parameter.Type().Reason())
	}

	branch := scopeIdentifiersNamed(t, file, "branch")
	if len(branch) != 2 {
		t.Fatalf("branch identifiers = %d, want 2", len(branch))
	}
	scopeRequireBinding(t, scopes, branch[0], BindingLocal)
	scopeRequireUnknown(t, scopes, branch[1], "branch expired")

	local := scopeIdentifiersNamed(t, file, "local")
	if len(local) != 2 {
		t.Fatalf("local identifiers = %d, want 2", len(local))
	}
	first := scopeRequireBinding(t, scopes, local[0], BindingLocal)
	second := scopeRequireBinding(t, scopes, local[1], BindingLocal)
	if first.ID() != second.ID() {
		t.Fatalf("local binding identity changed: %v / %v", first.ID(), second.ID())
	}
}

func TestScopesHandleParametersLambdasAndAccessors(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nvar property: int:\n\tget:\n\t\tproperty\n\tset(next):\n\t\tnext\nfunc defaults(first: int, second = first, third = later, later: int = 3, self_default = self_default):\n\tpass\nfunc captures():\n\tvar captured = 1\n\tvar callable = func named(captured):\n\t\tcaptured\n\t\tnamed\n\tcaptured\n",
	})
	interfaces := BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t))
	scopes := BuildScopes(interfaces)
	file := source.File("player.gd")

	member := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "property", 4), BindingMember)
	if member.Type().Kind() != KindBuiltin || member.Type().Name() != "int" {
		t.Fatalf("getter member type = %v %q (%q)", member.Type().Kind(), member.Type().Name(), member.Type().Reason())
	}
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "next", 6), BindingSetter)

	first := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "first", 7), BindingParameter)
	if first.Slot() != 0 {
		t.Fatalf("first parameter slot = %d, want 0", first.Slot())
	}
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "later", 7), "later parameter default")
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "self_default", 7), "self parameter default")

	lambdaParameter := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "captured", 12), BindingParameter)
	outerLocal := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "captured", 14), BindingLocal)
	if lambdaParameter.ID() == outerLocal.ID() {
		t.Fatal("lambda parameter did not shadow the captured outer local")
	}
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, file, "named", 13), "lambda debug name")
}

func TestScopesLetDefaultLambdasCaptureEarlierParameters(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nfunc configure(first: int, callback = func(): return first):\n\tpass\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), richInterfaceTestEngine(t)))
	file := source.File("player.gd")
	captured := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "first", 2), BindingParameter)
	if captured.Slot() != 0 {
		t.Fatalf("captured parameter slot = %d, want 0", captured.Slot())
	}
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
	if item.Type().Kind() != KindBuiltin || item.Type().Name() != "int" {
		t.Fatalf("for binding type = %v %q (%q)", item.Type().Kind(), item.Type().Name(), item.Type().Reason())
	}
	left := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "left", 22), BindingMatch)
	right := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "right", 23), BindingMatch)
	lone := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "lone", 25), BindingMatch)
	if left.ID() == right.ID() || left.ID() == lone.ID() || right.ID() == lone.ID() {
		t.Fatalf("match binding identities collided: left=%v right=%v lone=%v", left.ID(), right.ID(), lone.ID())
	}
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
	if guard.ID() != body.ID() {
		t.Fatalf("match binding changed between guard and body: %v / %v", guard.ID(), body.ID())
	}
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

func TestScopesUseStaticContextForConstantInitializers(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nvar field: int\nconst SELF_VALUE = self\nconst FIELD_VALUE = field\nfunc run():\n\tconst LOCAL_SELF = self\n\tconst LOCAL_FIELD = field\nfunc values(parameter):\n\tvar mutable = 1\n\tconst FROM_PARAMETER = parameter\n\tconst FROM_MUTABLE = mutable\n\tconst FIRST = 1\n\tconst FROM_CONST = FIRST\n",
	})
	if got := source.ParseFailures(); len(got) != 0 {
		t.Fatalf("constant fixture did not parse: %v", got)
	}
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
	if got := source.ParseFailures(); len(got) != 0 {
		t.Fatalf("constant super fixture did not parse: %v", got)
	}
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	scopeRequireUnknown(t, scopes, scopeIdentifierAt(t, source.File("child.gd"), "super", 3), "constant super")
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
	if got := source.ParseFailures(); len(got) != 0 {
		t.Fatalf("constant namespace fixture did not parse: %v", got)
	}
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
	if unresolved.Type().Kind() != KindUnknown || unresolved.Type().Reason() == "" {
		t.Fatalf("unknown member became %v %q (%q)", unresolved.Type().Kind(), unresolved.Type().Name(), unresolved.Type().Reason())
	}
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
		if testCase.classID != "" && binding.ClassID() != testCase.classID {
			t.Errorf("%s binding class = %q, want %q", testCase.name, binding.ClassID(), testCase.classID)
		}
	}
}

func TestScopesKeepFoundUnknownMembersAheadOfRetainedGlobals(t *testing.T) {
	source := sources(t, map[string]string{
		"player.gd": "class_name Player\nvar instance_field = dynamic_value()\nfunc run():\n\tinstance_field\n",
	})
	scopes := BuildScopes(BuildInterfaces(BuildIndex(source), scopeStaticEngine(t)))
	binding := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, source.File("player.gd"), "instance_field", 4), BindingMember)
	if binding.Type().Kind() != KindUnknown || binding.Type().Reason() == "" {
		t.Fatalf("found member type = %v %q (%q), want its reasoned unknown declaration type", binding.Type().Kind(), binding.Type().Name(), binding.Type().Reason())
	}
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
		if binding.ClassID() != "outer.gd" {
			t.Errorf("%s enclosing binding class = %q, want outer.gd", testCase.name, binding.ClassID())
		}
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
	if self.Type().Kind() != KindClass || self.Type().Name() != "outer.gd#Child" {
		t.Fatalf("inner class self type = %v %q (%q), want Child instance", self.Type().Kind(), self.Type().Name(), self.Type().Reason())
	}
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
	if !ok || !staticBase.Static() || staticBase.DeclaringClassID() != "base.gd" {
		t.Fatalf("static super base = %#v, present %t", staticBase, ok)
	}

	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "self", 10), BindingSelf)
	instanceField := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "instance_field", 11), BindingMember)
	if instanceField.ClassID() != "base.gd" {
		t.Errorf("inherited member class = %q, want base.gd", instanceField.ClassID())
	}
	scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "static_field", 12), BindingMember)
	instanceSuper := scopeRequireBinding(t, scopes, scopeIdentifierAt(t, file, "super", 13), BindingSuper)
	instanceBase, ok := instanceSuper.SuperMember()
	if !ok || instanceBase.Static() || instanceBase.DeclaringClassID() != "base.gd" {
		t.Fatalf("instance super base = %#v, present %t", instanceBase, ok)
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
		"player.gd": "class_name Player\nfunc run(value: int):\n\tvar local = value\n\tlocal\n",
	}
	firstSource := sources(t, files)
	firstIndex := BuildIndex(firstSource)
	interfaces := BuildInterfaces(firstIndex, richInterfaceTestEngine(t))
	before, err := json.Marshal(firstSource.File("player.gd"))
	if err != nil {
		t.Fatal(err)
	}
	firstScopes := BuildScopes(interfaces)
	after, err := json.Marshal(firstSource.File("player.gd"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("building scopes mutated the parsed AST")
	}
	secondScopes := BuildScopes(interfaces)
	firstUse := scopeIdentifierAt(t, firstSource.File("player.gd"), "local", 4)
	firstBinding := scopeRequireBinding(t, firstScopes, firstUse, BindingLocal)
	secondBinding := scopeRequireBinding(t, secondScopes, firstUse, BindingLocal)
	if firstBinding.ID() == secondBinding.ID() {
		t.Fatal("separate scope builds reused a binding identity")
	}
	if firstBinding.Name() != secondBinding.Name() || firstBinding.Kind() != secondBinding.Kind() || firstBinding.ClassID() != secondBinding.ClassID() || firstBinding.Slot() != secondBinding.Slot() {
		t.Fatalf("repeated scope build changed binding topology: %#v / %#v", firstBinding, secondBinding)
	}

	firstIndex.Classes["player.gd"].Declarations[0].Name = "mutated"
	if again := scopeRequireBinding(t, firstScopes, firstUse, BindingLocal); again.Name() != "local" {
		t.Fatalf("caller-owned index mutation changed published local binding to %q", again.Name())
	}

	secondSource := sources(t, files)
	secondInterfaces := BuildInterfaces(BuildIndex(secondSource), richInterfaceTestEngine(t))
	secondSnapshot := BuildScopes(secondInterfaces)
	secondUse := scopeIdentifierAt(t, secondSource.File("player.gd"), "local", 4)
	if _, found := firstScopes.ScopeAt(secondUse); found {
		t.Fatal("first scope index accepted a node from a separately parsed snapshot")
	}
	if firstBinding.ID() == scopeRequireBinding(t, secondSnapshot, secondUse, BindingLocal).ID() {
		t.Fatal("separately parsed snapshot reused a binding identity")
	}

	const readers = 24
	const readsPerReader = 100
	problems := make(chan string, readers)
	var readersDone sync.WaitGroup
	readersDone.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer readersDone.Done()
			for attempt := 0; attempt < readsPerReader; attempt++ {
				result := firstScopes.Resolve(firstUse)
				binding, found := result.Binding()
				if result.State() != LookupFound || !found || binding.Kind() != BindingLocal || binding.Name() != "local" {
					problems <- "concurrent read observed an incomplete lexical binding"
					return
				}
			}
		}()
	}
	readersDone.Wait()
	close(problems)
	for problem := range problems {
		t.Error(problem)
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
	if len(found) != 1 {
		t.Fatalf("identifiers named %q at line %d = %d, want 1", name, line, len(found))
	}
	return found[0]
}

func scopeRequireBinding(t *testing.T, scopes *ScopeIndex, identifier *ast.Identifier, want BindingKind) Binding {
	t.Helper()
	scope, ok := scopes.ScopeAt(identifier)
	if !ok {
		t.Fatalf("scope for %q was not indexed", identifier.Name)
	}
	result := scope.Lookup(identifier.Name)
	if result.State() != LookupFound {
		t.Fatalf("lookup %q = %s (%s), want found", identifier.Name, result.State(), result.Reason())
	}
	binding, ok := result.Binding()
	if !ok {
		t.Fatalf("lookup %q returned no binding", identifier.Name)
	}
	if binding.Kind() != want {
		t.Fatalf("binding %q kind = %s, want %s", identifier.Name, binding.Kind(), want)
	}
	return binding
}

func scopeRequireUnknown(t *testing.T, scopes *ScopeIndex, identifier *ast.Identifier, label string) {
	t.Helper()
	scope, ok := scopes.ScopeAt(identifier)
	if !ok {
		t.Fatalf("scope for %q was not indexed", identifier.Name)
	}
	result := scope.Lookup(identifier.Name)
	if result.State() != LookupUnknown || result.Reason() == "" {
		t.Fatalf("%s lookup %q = %s (%q), want reasoned unknown", label, identifier.Name, result.State(), result.Reason())
	}
}

func scopeNamespaceEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "String", "Callable", "Clash"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Native", parent: "Object"},
		{name: "Global", parent: "Object"},
	} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddSingleton("Single", "Native"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddSingleton("Clash", "Object"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddUtility("utility", "int", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddUtility("Clash", "int", nil, false); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func scopeStaticEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	if err := builder.AddBuiltin("int"); err != nil {
		t.Fatal(err)
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
	} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddSingleton("instance_field", "Object"); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddUtility("instance_method", "int", nil, false); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
