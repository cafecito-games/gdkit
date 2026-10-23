package semantic

import (
	"strings"
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterfacesResolveImplicitEngineChainAndDirectMembers(t *testing.T) {
	engine := interfaceTestEngine(t)
	index := BuildIndex(sources(t, map[string]string{
		"player.gd": "class_name Player\nvar health: int\n",
	}))

	interfaces := BuildInterfaces(index, engine)
	player, ok := interfaces.Class("player.gd")
	require.True(t, ok, "Player interface was not published")
	require.True(t, player.Complete())

	chain := player.Type()
	for _, want := range []string{"player.gd", "RefCounted", "Object"} {
		require.Equal(t, KindClass, chain.Kind())
		require.Equal(t, want, chain.Name())
		base, hasBase := chain.Base()
		if want == "Object" {
			require.False(t, hasBase, "Object unexpectedly has base %q", base.Name())
			break
		}
		require.True(t, hasBase, "%s has no base", want)
		chain = base
	}

	result := player.Lookup("health")
	require.Equal(t, LookupFound, result.State())
	member, found := result.Member()
	require.True(t, found)
	require.Equal(t, MemberVariable, member.Kind())
	require.Equal(t, KindBuiltin, member.Type().Kind())
	require.Equal(t, "int", member.Type().Name())
	require.Equal(t, "player.gd", member.DeclaringClassID())
	require.Equal(t, 2, member.Line())
	require.Equal(t, 5, member.Column())
	missing := player.Lookup("missing")
	require.Equal(t, LookupAbsent, missing.State())
}

func interfaceTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	require.NoError(t, builder.AddBuiltin("int"))
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}

func TestInterfacesResolveDeclarationKindsAndSignatures(t *testing.T) {
	engine := richInterfaceTestEngine(t)
	index := BuildIndex(sources(t, map[string]string{
		"friend.gd": "class_name Friend\nclass Inner:\n\tpass\n",
		"player.gd": "class_name Player extends CharacterBody2D\nvar explicit: float\nvar inferred := 10\nvar dynamic\nvar unresolved = fetch()\nvar float_literal := 1.5\nvar string_literal := \"message\"\nvar string_name_literal := &\"label\"\nvar node_path_literal := ^\"child\"\nvar boolean_literal := true\nvar null_literal := null\nvar collection_literal := []\nvar operator_literal := 1 + 2\nvar identifier_literal = LIMIT\nstatic var static_field: int\nconst LIMIT := 10\nconst TYPED: String = \"typed\"\nconst BAD = fetch()\nvar array: Array[Dictionary[String, int]]\nvar friend: Friend\nvar nested: Friend.Inner\nvar inner: Inner\nvar mode: Mode\nvar native: Node\nclass Inner:\n\tpass\nenum Mode { ONE }\nenum { FLAG }\nsignal changed(typed: int, dynamic_parameter)\n@abstract\nfunc abstracted(typed: int, dynamic_parameter, inferred_parameter := 3, ...rest: Array) -> void\nfunc typed_signature(friend_value: Friend, nested_value: Friend.Inner, local_value: Inner, mode_value: Mode, node_value: Node, table: Dictionary[String, Array[int]]) -> Friend:\n\tpass\nstatic func static_value() -> String:\n\tpass\nfunc no_return():\n\tpass\nfunc untyped_rest(...rest):\n\tpass\nfunc invalid_rest(...rest: String):\n\tpass\nfunc invalid_typed_array_rest(...rest: Array[String]):\n\tpass\n",
	}))
	require.Empty(t, index.ParseFailures, "fixture did not parse")

	interfaces := BuildInterfaces(index, engine)
	player, ok := interfaces.Class("player.gd")
	require.True(t, ok, "Player interface was not published")
	assertClassChain(t, player.Type(), "player.gd", "CharacterBody2D", "Node2D", "CanvasItem", "Node", "Object")
	for name, want := range map[string]struct {
		kind     MemberKind
		typeKind Kind
		typeName string
	}{
		"explicit":            {kind: MemberVariable, typeKind: KindBuiltin, typeName: "float"},
		"inferred":            {kind: MemberVariable, typeKind: KindBuiltin, typeName: "int"},
		"dynamic":             {kind: MemberVariable, typeKind: KindVariant},
		"float_literal":       {kind: MemberVariable, typeKind: KindBuiltin, typeName: "float"},
		"string_literal":      {kind: MemberVariable, typeKind: KindBuiltin, typeName: "String"},
		"string_name_literal": {kind: MemberVariable, typeKind: KindBuiltin, typeName: "StringName"},
		"node_path_literal":   {kind: MemberVariable, typeKind: KindBuiltin, typeName: "NodePath"},
		"boolean_literal":     {kind: MemberVariable, typeKind: KindBuiltin, typeName: "bool"},
		"static_field":        {kind: MemberVariable, typeKind: KindBuiltin, typeName: "int"},
		"LIMIT":               {kind: MemberConstant, typeKind: KindBuiltin, typeName: "int"},
		"TYPED":               {kind: MemberConstant, typeKind: KindBuiltin, typeName: "String"},
		"friend":              {kind: MemberVariable, typeKind: KindClass, typeName: "friend.gd"},
		"nested":              {kind: MemberVariable, typeKind: KindClass, typeName: "friend.gd#Inner"},
		"inner":               {kind: MemberVariable, typeKind: KindClass, typeName: "player.gd#Inner"},
		"mode":                {kind: MemberVariable, typeKind: KindEnum, typeName: "player.gd.Mode"},
		"native":              {kind: MemberVariable, typeKind: KindClass, typeName: "Node"},
		"Inner":               {kind: MemberClass, typeKind: KindClass, typeName: "player.gd#Inner"},
		"Mode":                {kind: MemberEnum, typeKind: KindEnum, typeName: "player.gd.Mode"},
		"FLAG":                {kind: MemberEnumMember, typeKind: KindBuiltin, typeName: "int"},
		"changed":             {kind: MemberSignal, typeKind: KindSignal},
		"abstracted":          {kind: MemberMethod, typeKind: KindCallable},
		"typed_signature":     {kind: MemberMethod, typeKind: KindCallable},
	} {
		member := interfaceMember(t, player, name)
		assert.Equal(t, want.kind, member.Kind())
		assert.Equal(t, want.typeKind, member.Type().Kind())
		assert.Equal(t, want.typeName, member.Type().Name())
	}

	assert.True(t, interfaceMember(t, player, "LIMIT").Const())
	assert.True(t, interfaceMember(t, player, "FLAG").Const())
	assert.True(t, interfaceMember(t, player, "TYPED").Const())
	assert.True(t, interfaceMember(t, player, "Inner").Type().Meta())
	for _, name := range []string{"unresolved", "null_literal", "collection_literal", "operator_literal", "identifier_literal", "BAD"} {
		got := interfaceMember(t, player, name).Type()
		assert.Equal(t, KindUnknown, got.Kind())
		assert.NotEmpty(t, got.Reason())
	}

	array := interfaceMember(t, player, "array").Type()
	element, hasElement := array.Element()
	key, hasKey := element.Key()
	value, hasValue := element.Value()
	require.Equal(t, KindArray, array.Kind())
	require.True(t, hasElement)
	require.Equal(t, KindDictionary, element.Kind())
	require.True(t, hasKey)
	require.True(t, hasValue)
	require.Equal(t, "String", key.Name())
	require.Equal(t, "int", value.Name())

	abstracted := interfaceMember(t, player, "abstracted")
	require.True(t, abstracted.Abstract())
	require.False(t, abstracted.Static())
	require.True(t, abstracted.Variadic())
	returnType, hasReturn := abstracted.ReturnType()
	require.True(t, hasReturn)
	require.Equal(t, KindVoid, returnType.Kind())
	parameters := abstracted.Parameters()
	require.Len(t, parameters, 4)
	require.Equal(t, "int", parameters[0].Type().Name())
	require.Equal(t, KindVariant, parameters[1].Type().Kind())
	require.Equal(t, "int", parameters[2].Type().Name())
	require.True(t, parameters[2].HasDefault())
	require.True(t, parameters[3].Variadic())
	require.Equal(t, KindArray, parameters[3].Type().Kind())
	_, typedRest := parameters[3].Type().Element()
	require.False(t, typedRest, "typed rest parameter was not retained as an untyped Array")
	staticMethod := interfaceMember(t, player, "static_value")
	require.True(t, staticMethod.Static())
	require.False(t, staticMethod.Abstract())
	staticField := interfaceMember(t, player, "static_field")
	require.True(t, staticField.Static(), "static field lost static metadata")
	signature := interfaceMember(t, player, "typed_signature")
	returned, hasReturned := signature.ReturnType()
	require.True(t, hasReturned)
	require.Equal(t, KindClass, returned.Kind())
	require.Equal(t, "friend.gd", returned.Name())
	for at, want := range []struct {
		kind Kind
		name string
	}{
		{kind: KindClass, name: "friend.gd"},
		{kind: KindClass, name: "friend.gd#Inner"},
		{kind: KindClass, name: "player.gd#Inner"},
		{kind: KindEnum, name: "player.gd.Mode"},
		{kind: KindClass, name: "Node"},
		{kind: KindDictionary},
	} {
		parameter := signature.Parameters()[at].Type()
		assert.Equal(t, want.kind, parameter.Kind())
		assert.Equal(t, want.name, parameter.Name())
	}
	table := signature.Parameters()[5].Type()
	tableKey, tableKeyOK := table.Key()
	tableValue, tableValueOK := table.Value()
	arrayElement, arrayElementOK := tableValue.Element()
	require.True(t, tableKeyOK)
	require.True(t, tableValueOK)
	require.True(t, arrayElementOK)
	require.Equal(t, "String", tableKey.Name())
	require.Equal(t, KindArray, tableValue.Kind())
	require.Equal(t, "int", arrayElement.Name())
	noReturn, hasReturn := interfaceMember(t, player, "no_return").ReturnType()
	require.True(t, hasReturn)
	require.Equal(t, KindUnknown, noReturn.Kind())
	require.NotEmpty(t, noReturn.Reason())
	changed := interfaceMember(t, player, "changed")
	changedParameters := changed.Parameters()
	require.Len(t, changedParameters, 2)
	require.Equal(t, "int", changedParameters[0].Type().Name())
	require.Equal(t, KindVariant, changedParameters[1].Type().Kind())
	untypedRest := interfaceMember(t, player, "untyped_rest")
	require.True(t, untypedRest.Variadic(), "untyped rest method lost variadic metadata")
	untypedParameters := untypedRest.Parameters()
	require.Len(t, untypedParameters, 1)
	require.True(t, untypedParameters[0].Variadic())
	require.Equal(t, KindArray, untypedParameters[0].Type().Kind())
	_, typed := untypedParameters[0].Type().Element()
	require.False(t, typed, "untyped rest parameter became Array[Variant]")
	for _, name := range []string{"invalid_rest", "invalid_typed_array_rest"} {
		invalidRest := interfaceMember(t, player, name).Parameters()
		assert.Len(t, invalidRest, 1)
		assert.Equal(t, KindUnknown, invalidRest[0].Type().Kind())
		assert.Contains(t, invalidRest[0].Type().Reason(), "Array")
	}
}

func TestInterfacesResolveScopedAndFailClosedAnnotations(t *testing.T) {
	engine := richInterfaceTestEngine(t)
	source := sources(t, map[string]string{
		"loaded.gd": "class_name Loaded\nclass Inner:\n\tpass\n",
		"holder.gd": "class_name Holder\nconst LoadedAlias = preload(\"res://loaded.gd\")\nclass Local:\n\tclass Leaf:\n\t\tpass\nenum Mode { ONE }\nvar via_alias: LoadedAlias\nvar via_alias_inner: LoadedAlias.Inner\nvar local: Local\nvar leaf: Local.Leaf\nvar mode: Mode\nvar ambiguous: Duplicate\nvar unknown: MissingType\nvar plain_array: Array\nvar plain_dictionary: Dictionary\n",
		"first.gd":  "class_name Duplicate\n",
		"second.gd": "class_name Duplicate\n",
	})
	source.resolved["holder.gd\x00res://loaded.gd"] = "loaded.gd"
	index := BuildIndex(source)
	require.Empty(t, index.ParseFailures, "fixture did not parse")
	interfaces := BuildInterfaces(index, engine)
	holder, ok := interfaces.Class("holder.gd")
	require.True(t, ok)
	require.True(t, holder.Complete())
	for name, want := range map[string]struct {
		kind Kind
		name string
	}{
		"via_alias":        {kind: KindClass, name: "loaded.gd"},
		"via_alias_inner":  {kind: KindClass, name: "loaded.gd#Inner"},
		"local":            {kind: KindClass, name: "holder.gd#Local"},
		"leaf":             {kind: KindClass, name: "holder.gd#Local#Leaf"},
		"mode":             {kind: KindEnum, name: "holder.gd.Mode"},
		"plain_array":      {kind: KindArray},
		"plain_dictionary": {kind: KindDictionary},
	} {
		got := interfaceMember(t, holder, name).Type()
		assert.Equal(t, want.kind, got.Kind())
		assert.Equal(t, want.name, got.Name())
	}
	_, typed := interfaceMember(t, holder, "plain_array").Type().Element()
	assert.False(t, typed)
	_, typed = interfaceMember(t, holder, "plain_dictionary").Type().Key()
	assert.False(t, typed)
	for _, name := range []string{"ambiguous", "unknown"} {
		got := interfaceMember(t, holder, name).Type()
		assert.Equal(t, KindUnknown, got.Kind())
		assert.Contains(t, got.Reason(), map[string]string{"ambiguous": "Duplicate", "unknown": "MissingType"}[name])
	}

	nested := interfaces.ResolveType("holder.gd", "Array[Dictionary[String, int]]")
	element, hasElement := nested.Element()
	require.Equal(t, KindArray, nested.Kind())
	require.True(t, hasElement)
	require.Equal(t, KindDictionary, element.Kind())
	// The Engine boundary deliberately carries no engine enum declarations. A
	// qualified engine spelling therefore cannot be proven here, even where it
	// happens to name a real Godot enum; user-declared named enums above cover
	// successful named-enum resolution.
	for _, spelling := range []string{"Array[int", "Callable[int]", "MissingType", "Node.ProcessMode", "Node.ProcesMode", "int.Whatever"} {
		got := interfaces.ResolveType("holder.gd", spelling)
		assert.Equal(t, KindUnknown, got.Kind())
		assert.Contains(t, got.Reason(), spelling)
	}
}

func TestInterfacesRejectVoidOutsideMethodReturns(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		source string
	}{
		{name: "field", source: "var field: void\n"},
		{name: "constant", source: "const CONSTANT: void = 1\n"},
		{name: "parameter", source: "func takes(value: void):\n\tpass\n"},
		{name: "signal", source: "signal changed(value: void)\n"},
		{name: "array element", source: "var array: Array[void]\n"},
		{name: "dictionary key", source: "var table: Dictionary[void, int]\n"},
		{name: "dictionary value", source: "var table: Dictionary[int, void]\n"},
		{name: "nested container", source: "var nested: Array[Dictionary[String, Array[void]]]\n"},
	} {
		t.Run("parser rejects "+testCase.name, func(t *testing.T) {
			parsed := sources(t, map[string]string{"voids.gd": testCase.source})
			require.Len(t, parsed.ParseFailures(), 1)
			require.Equal(t, "voids.gd", parsed.ParseFailures()[0])
		})
	}

	index := BuildIndex(sources(t, map[string]string{
		"voids.gd": "class_name Voids\nfunc returns_void() -> void:\n\tpass\n",
	}))
	require.Empty(t, index.ParseFailures, "return fixture did not parse")
	interfaces := BuildInterfaces(index, richInterfaceTestEngine(t))
	class := requireInterface(t, interfaces, "voids.gd")
	returned, hasReturned := interfaceMember(t, class, "returns_void").ReturnType()
	require.True(t, hasReturned)
	require.Equal(t, KindVoid, returned.Kind())
	for _, spelling := range []string{"void", "Array[void]", "Dictionary[void, int]", "Dictionary[int, void]", "Array[Dictionary[String, Array[void]]]"} {
		got := interfaces.ResolveType("voids.gd", spelling)
		assert.Equal(t, KindUnknown, got.Kind())
		assert.Contains(t, got.Reason(), "void")
	}
}

func TestInterfacesGlobalTypeResolutionFailsClosedOnIncompleteIndex(t *testing.T) {
	t.Run("parse failure blocks global class", func(t *testing.T) {
		index := BuildIndex(sources(t, map[string]string{
			"global.gd": "class_name Global\n",
			"broken.gd": "func (\n",
		}))
		interfaces := BuildInterfaces(index, interfaceTestEngine(t))
		got := interfaces.ResolveType("", "Global")
		require.Equal(t, KindUnknown, got.Kind())
		require.Contains(t, got.Reason(), "Global")
		require.Contains(t, got.Reason(), "broken.gd")
		got = interfaces.ResolveType("", "int")
		require.Equal(t, KindBuiltin, got.Kind())
		require.Equal(t, "int", got.Name())
	})

	t.Run("duplicate global class remains ambiguous", func(t *testing.T) {
		interfaces := BuildInterfaces(BuildIndex(sources(t, map[string]string{
			"first.gd":  "class_name Duplicate\n",
			"second.gd": "class_name Duplicate\n",
		})), interfaceTestEngine(t))
		got := interfaces.ResolveType("", "Duplicate")
		require.Equal(t, KindUnknown, got.Kind())
		require.Contains(t, got.Reason(), "Duplicate")
	})
}

func TestInterfacesLookupIsNearestAndTriState(t *testing.T) {
	zero := LookupResult{}
	require.Equal(t, LookupUnknown, zero.State())
	require.Empty(t, zero.Reason())
	engine := lookupInterfaceTestEngine(t)
	index := BuildIndex(sources(t, map[string]string{
		"base.gd":    "class_name Base extends Node\nvar inherited: int\nvar shadowed: int\n",
		"derived.gd": "class_name Derived extends Base\nfunc shadowed() -> void:\n\tpass\nvar duplicate: int\n",
		"broken.gd":  "class_name Broken extends \"res://missing.gd\"\nvar direct: int\n",
	}))
	require.Empty(t, index.ParseFailures, "fixture did not parse")
	// gdparser rejects duplicate declarations in source before BuildIndex can
	// represent them. The interface boundary still receives a public Index, so
	// preserve the fail-closed contract for a malformed or externally built
	// index by modelling the same-scope duplicate here.
	derivedDecl := index.Classes["derived.gd"]
	for _, declaration := range derivedDecl.Declarations {
		if declaration.Name == "duplicate" {
			derivedDecl.Declarations = append(derivedDecl.Declarations, declaration)
			break
		}
	}
	interfaces := BuildInterfaces(index, engine)
	derived, ok := interfaces.Class("derived.gd")
	require.True(t, ok)
	require.True(t, derived.Complete())

	inherited := interfaceMember(t, derived, "inherited")
	require.Equal(t, MemberVariable, inherited.Kind())
	require.Equal(t, "base.gd", inherited.DeclaringClassID())
	shadowed := interfaceMember(t, derived, "shadowed")
	require.Equal(t, MemberMethod, shadowed.Kind())
	require.Equal(t, "derived.gd", shadowed.DeclaringClassID())
	engineMethod := interfaceMember(t, derived, "engine_only")
	engineMethodReturn, hasEngineMethodReturn := engineMethod.ReturnType()
	require.Equal(t, MemberEngineMethod, engineMethod.Kind())
	require.Equal(t, "Object", engineMethod.EngineOwner())
	require.True(t, hasEngineMethodReturn)
	require.Equal(t, "int", engineMethodReturn.Name())
	require.True(t, engineMethod.Static())
	require.True(t, engineMethod.Variadic())
	require.Equal(t, 0, engineMethod.Line())
	require.Equal(t, 0, engineMethod.Column())
	engineMethodParameters := engineMethod.Parameters()
	require.Len(t, engineMethodParameters, 1)
	require.Empty(t, engineMethodParameters[0].Name())
	require.Equal(t, "int", engineMethodParameters[0].Type().Name())
	require.True(t, engineMethodParameters[0].HasDefault())
	engineProperty := interfaceMember(t, derived, "engine_property")
	require.Equal(t, MemberEngineProperty, engineProperty.Kind())
	require.Equal(t, "Object", engineProperty.EngineOwner())
	require.Equal(t, "int", engineProperty.Type().Name())
	duplicateResult := derived.Lookup("duplicate")
	require.Equal(t, LookupUnknown, duplicateResult.State())
	require.NotEmpty(t, duplicateResult.Reason())
	conflictResult := derived.Lookup("conflict")
	require.Equal(t, LookupUnknown, conflictResult.State())
	require.NotEmpty(t, conflictResult.Reason())
	missingResult := derived.Lookup("missing")
	require.Equal(t, LookupAbsent, missingResult.State())

	broken, ok := interfaces.Class("broken.gd")
	require.True(t, ok)
	require.False(t, broken.Complete())
	require.NotEmpty(t, broken.Cause())
	direct := interfaceMember(t, broken, "direct")
	require.Equal(t, MemberVariable, direct.Kind())
	require.Equal(t, "broken.gd", direct.DeclaringClassID())
	require.Equal(t, "int", direct.Type().Name())
	brokenMissing := broken.Lookup("missing")
	require.Equal(t, LookupUnknown, brokenMissing.State())
	require.NotEmpty(t, brokenMissing.Reason())
}

func TestInterfacesLookupBaseKeepsUnifiedPrecedenceAndFailureState(t *testing.T) {
	interfaces := BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"base.gd":   "class_name Base\nfunc instance() -> int:\n\tpass\nstatic func shared() -> int:\n\tpass\n",
		"child.gd":  "class_name Child extends Base\nfunc instance() -> int:\n\tpass\nstatic func shared() -> int:\n\tpass\n",
		"broken.gd": "class_name Broken extends MissingBase\nfunc current():\n\tpass\n",
	})), richInterfaceTestEngine(t))

	for _, name := range []string{"instance", "shared"} {
		result := interfaces.LookupBase("child.gd", name)
		require.Equal(t, LookupFound, result.State())
		member, ok := result.Member()
		require.True(t, ok)
		require.Equal(t, "base.gd", member.DeclaringClassID())
		require.Equal(t, name, member.Name())
	}
	completeMiss := interfaces.LookupBase("child.gd", "missing")
	require.Equal(t, LookupAbsent, completeMiss.State())
	incompleteMiss := interfaces.LookupBase("broken.gd", "missing")
	require.Equal(t, LookupUnknown, incompleteMiss.State())
	require.NotEmpty(t, incompleteMiss.Reason())
}

func TestInterfacesFailClosedForIncompleteChains(t *testing.T) {
	t.Run("ambiguous project base is never reinterpreted as engine", func(t *testing.T) {
		engine := buildInterfaceEngine(t, []string{"int"}, []interfaceEngineClass{
			{name: "Object"},
			{name: "RefCounted", parent: "Object"},
			{name: "Duplicate", parent: "Object"},
		})
		index := BuildIndex(sources(t, map[string]string{
			"first.gd":     "class_name Duplicate\n",
			"second.gd":    "class_name Duplicate\n",
			"ambiguous.gd": "class_name Ambiguous extends Duplicate\nvar direct: int\n",
		}))
		interfaces := BuildInterfaces(index, engine)
		class := requireInterface(t, interfaces, "ambiguous.gd")
		require.False(t, class.Complete())
		require.Contains(t, class.Cause(), "Duplicate")
		owners := class.Owners()
		require.Len(t, owners, 1)
		require.Equal(t, "ambiguous.gd", owners[0].ClassID())
		base, hasBase := class.Type().Base()
		require.True(t, hasBase)
		require.Equal(t, KindUnknown, base.Kind())
		require.Contains(t, base.Reason(), "Duplicate")
		direct := interfaceMember(t, class, "direct")
		require.Equal(t, "int", direct.Type().Name())
		ambiguousMiss := class.Lookup("missing")
		require.Equal(t, LookupUnknown, ambiguousMiss.State())
		require.Contains(t, ambiguousMiss.Reason(), "Duplicate")
		got := interfaces.ResolveType("ambiguous.gd", "Duplicate")
		require.Equal(t, KindUnknown, got.Kind())
		require.Contains(t, got.Reason(), "Duplicate")
	})

	t.Run("project cycle and parse failure terminate", func(t *testing.T) {
		engine := interfaceTestEngine(t)
		cycleIndex := BuildIndex(sources(t, map[string]string{
			"a.gd": "class_name A extends B\n",
			"b.gd": "class_name B extends A\n",
		}))
		cyclic := requireInterface(t, BuildInterfaces(cycleIndex, engine), "a.gd")
		require.False(t, cyclic.Complete())
		require.Contains(t, cyclic.Cause(), "cycle")
		cycleBase, hasCycleBase := cyclic.Type().Base()
		require.True(t, hasCycleBase)
		require.Equal(t, KindUnknown, cycleBase.Kind())
		require.Contains(t, cycleBase.Reason(), "cycle")
		for attempt := 0; attempt < 3; attempt++ {
			cyclicMiss := cyclic.Lookup("missing")
			require.Equal(t, LookupUnknown, cyclicMiss.State())
			require.Contains(t, cyclicMiss.Reason(), "cycle")
		}

		source := sources(t, map[string]string{"good.gd": "class_name Good\nvar direct: int\n"})
		source.paths = append(source.paths, "broken.gd")
		source.failures = append(source.failures, "broken.gd")
		parseFailed := requireInterface(t, BuildInterfaces(BuildIndex(source), lookupInterfaceTestEngine(t)), "good.gd")
		require.False(t, parseFailed.Complete())
		require.Contains(t, parseFailed.Cause(), "broken.gd")
		parseBase, hasParseBase := parseFailed.Type().Base()
		require.True(t, hasParseBase)
		require.Equal(t, KindUnknown, parseBase.Kind())
		require.Contains(t, parseBase.Reason(), "broken.gd")
		parseDirect := interfaceMember(t, parseFailed, "direct")
		require.Equal(t, "int", parseDirect.Type().Name())
		parseMissing := parseFailed.Lookup("missing")
		require.Equal(t, LookupUnknown, parseMissing.State())
		require.Contains(t, parseMissing.Reason(), "broken.gd")
		parseEngineOnly := parseFailed.Lookup("engine_only")
		require.Equal(t, LookupUnknown, parseEngineOnly.State())
		require.Contains(t, parseEngineOnly.Reason(), "broken.gd")
	})

	t.Run("unavailable and malformed engine chains stay incomplete", func(t *testing.T) {
		index := BuildIndex(sources(t, map[string]string{"plain.gd": "class_name Plain\nvar literal := 3\n"}))
		for name, testCase := range map[string]struct {
			engine    *Engine
			wantCause string
		}{
			"unavailable": {engine: nil, wantCause: "unavailable"},
			"missing class": {
				engine:    buildInterfaceEngine(t, []string{"int"}, []interfaceEngineClass{{name: "Object"}}),
				wantCause: "RefCounted",
			},
			"unknown parent": {
				engine: buildInterfaceEngine(t, []string{"int"}, []interfaceEngineClass{
					{name: "Object"},
					{name: "RefCounted", parent: "MissingParent"},
				}),
				wantCause: "MissingParent",
			},
			"wrong root": {
				engine:    buildInterfaceEngine(t, []string{"int"}, []interfaceEngineClass{{name: "RefCounted"}}),
				wantCause: "instead of Object",
			},
		} {
			t.Run(name, func(t *testing.T) {
				class := requireInterface(t, BuildInterfaces(index, testCase.engine), "plain.gd")
				require.False(t, class.Complete())
				require.Contains(t, class.Cause(), testCase.wantCause)
				base, hasBase := class.Type().Base()
				require.True(t, hasBase)
				require.Equal(t, KindUnknown, base.Kind())
				require.Contains(t, base.Reason(), testCase.wantCause)
				literal := interfaceMember(t, class, "literal")
				require.Equal(t, "int", literal.Type().Name())
				incompleteEngineMiss := class.Lookup("missing")
				require.Equal(t, LookupUnknown, incompleteEngineMiss.State())
				require.Contains(t, incompleteEngineMiss.Reason(), testCase.wantCause)
			})
		}

		external := BuildIndex(sources(t, map[string]string{
			"external.gd": "class_name External extends MissingNative\nvar literal := 3\n",
		}))
		externalClass := requireInterface(t, BuildInterfaces(external, interfaceTestEngine(t)), "external.gd")
		require.False(t, externalClass.Complete())
		require.Contains(t, externalClass.Cause(), "MissingNative")
		externalBase, hasExternalBase := externalClass.Type().Base()
		require.True(t, hasExternalBase)
		require.Equal(t, KindUnknown, externalBase.Kind())
		require.Contains(t, externalBase.Reason(), "MissingNative")
	})
}

func TestInterfacesBlockGlobalTypeFallbackAcrossIncompleteAncestry(t *testing.T) {
	type testCase struct {
		name      string
		engine    func(*testing.T) *Engine
		scoped    string
		extra     map[string]string
		wantCause string
	}
	cases := []testCase{
		{
			name:      "unresolved project path base",
			engine:    interfaceTestEngine,
			scoped:    "class_name Scoped extends \"res://missing.gd\"\nclass Local:\n\tpass\n",
			wantCause: "res://missing.gd",
		},
		{
			name:   "ambiguous project base",
			engine: interfaceTestEngine,
			scoped: "class_name Scoped extends Parent\nclass Local:\n\tpass\n",
			extra: map[string]string{
				"first.gd":  "class_name Parent\n",
				"second.gd": "class_name Parent\n",
			},
			wantCause: "Parent",
		},
		{
			name:   "project parse failure",
			engine: interfaceTestEngine,
			scoped: "class_name Scoped\nclass Local:\n\tpass\n",
			extra: map[string]string{
				"broken.gd": "func (\n",
			},
			wantCause: "broken.gd",
		},
		{
			name:      "unavailable engine",
			engine:    func(*testing.T) *Engine { return nil },
			scoped:    "class_name Scoped\nclass Local:\n\tpass\n",
			wantCause: "unavailable",
		},
		{
			name: "missing implicit engine base",
			engine: func(t *testing.T) *Engine {
				return buildInterfaceEngine(t, nil, []interfaceEngineClass{{name: "Object"}})
			},
			scoped:    "class_name Scoped\nclass Local:\n\tpass\n",
			wantCause: "RefCounted",
		},
		{
			name: "unknown engine parent",
			engine: func(t *testing.T) *Engine {
				return buildInterfaceEngine(t, nil, []interfaceEngineClass{
					{name: "Object"},
					{name: "RefCounted", parent: "MissingParent"},
				})
			},
			scoped:    "class_name Scoped\nclass Local:\n\tpass\n",
			wantCause: "MissingParent",
		},
		{
			name: "wrong engine root",
			engine: func(t *testing.T) *Engine {
				return buildInterfaceEngine(t, nil, []interfaceEngineClass{{name: "RefCounted"}})
			},
			scoped:    "class_name Scoped\nclass Local:\n\tpass\n",
			wantCause: "instead of Object",
		},
		{
			name:      "absent external engine base",
			engine:    interfaceTestEngine,
			scoped:    "class_name Scoped extends MissingNative\nclass Local:\n\tpass\n",
			wantCause: "MissingNative",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			files := map[string]string{
				"global.gd": "class_name Global\n",
				"scoped.gd": testCase.scoped,
			}
			for path, source := range testCase.extra {
				files[path] = source
			}
			interfaces := BuildInterfaces(BuildIndex(sources(t, files)), testCase.engine(t))
			scoped := requireInterface(t, interfaces, "scoped.gd")
			require.False(t, scoped.Complete())
			require.Contains(t, scoped.Cause(), testCase.wantCause)

			// A direct lexical declaration remains usable before the incomplete
			// ancestry segment; only a miss must not fall through to Global.
			local := interfaces.ResolveType("scoped.gd", "Local")
			require.Equal(t, KindClass, local.Kind())
			require.Equal(t, "scoped.gd#Local", local.Name())
			global := interfaces.ResolveType("scoped.gd", "Global")
			require.Equal(t, KindUnknown, global.Kind())
			require.Contains(t, global.Reason(), "Global")
			require.Contains(t, global.Reason(), testCase.wantCause)
		})
	}
}

func TestInterfacesDoNotWalkFunctionAccessorOrLambdaBodies(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"body.gd": "class_name Body\nvar exposed: int:\n\tget:\n\t\tvar getter_hidden := 1\n\t\treturn getter_hidden\n\tset(value):\n\t\tvar setter_hidden := 2\n\t\texposed = value\nfunc outer():\n\tvar function_hidden := 3\n\tvar closure = func():\n\t\tvar lambda_hidden := 4\n\t\treturn lambda_hidden\n",
	}))
	require.Empty(t, index.ParseFailures, "fixture did not parse")
	class := requireInterface(t, BuildInterfaces(index, interfaceTestEngine(t)), "body.gd")
	direct := class.DirectMembers()
	require.Len(t, direct, 2)
	require.Equal(t, "exposed", direct[0].Name())
	require.Equal(t, "outer", direct[1].Name())
	exposed := interfaceMember(t, class, "exposed")
	require.Equal(t, "int", exposed.Type().Name())
	for _, hidden := range []string{"getter_hidden", "setter_hidden", "function_hidden", "closure", "lambda_hidden"} {
		result := class.Lookup(hidden)
		assert.Equal(t, LookupAbsent, result.State())
	}
}

func TestInterfacesAreDeterministicImmutableAndSafeForConcurrentReads(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"alpha.gd": "class_name Alpha extends Node\nvar field: int\nfunc call(value: int, fallback := 3) -> void:\n\tpass\n",
	}))
	first := BuildInterfaces(index, richInterfaceTestEngine(t))
	second := BuildInterfaces(index, richInterfaceTestEngine(t))
	require.Equal(t, second.ClassIDs(), first.ClassIDs())
	for _, id := range first.ClassIDs() {
		left, right := requireInterface(t, first, id), requireInterface(t, second, id)
		require.True(t, left.Type().Equal(right.Type()))
		require.Equal(t, right.Complete(), left.Complete())
		require.Equal(t, right.Cause(), left.Cause())
		require.Equal(t, right.Owners(), left.Owners())
		require.Equal(t, right.DirectMembers(), left.DirectMembers())
	}

	alpha := requireInterface(t, first, "alpha.gd")
	members := alpha.DirectMembers()
	owners := alpha.Owners()
	ids := first.ClassIDs()
	call := interfaceMember(t, alpha, "call")
	parameters := call.Parameters()
	members[0] = Member{}
	owners[0] = InterfaceOwner{}
	ids[0] = "mutated"
	parameters[0] = Parameter{}
	index.Classes["alpha.gd"].Declarations[0].Name = "mutated-index"
	index.Classes["alpha.gd"].ParentID = "missing.gd"
	index.Classes["alpha.gd"].ID = "mutated-index"

	againField := interfaceMember(t, alpha, "field")
	require.Equal(t, "field", againField.Name())
	require.Equal(t, "int", againField.Type().Name())
	againCall := interfaceMember(t, alpha, "call")
	require.Len(t, againCall.Parameters(), 2)
	require.Equal(t, "value", againCall.Parameters()[0].Name())
	cachedOwners := alpha.Owners()
	require.NotEmpty(t, cachedOwners)
	require.Equal(t, "alpha.gd", cachedOwners[0].ClassID())
	cachedIDs := first.ClassIDs()
	require.Len(t, cachedIDs, 1)
	require.Equal(t, "alpha.gd", cachedIDs[0])
	mutationLookup := first.Lookup("alpha.gd", "missing")
	require.Equal(t, LookupAbsent, mutationLookup.State())
	resolved := first.ResolveType("alpha.gd", "Alpha")
	require.Equal(t, KindClass, resolved.Kind())
	require.Equal(t, "alpha.gd", resolved.Name())

	const readers = 24
	const readsPerReader = 100
	problems := make(chan string, readers)
	var readersDone sync.WaitGroup
	readersDone.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer readersDone.Done()
			for attempt := 0; attempt < readsPerReader; attempt++ {
				result := first.Lookup("alpha.gd", "call")
				member, found := result.Member()
				if result.State() != LookupFound || !found || member.Kind() != MemberMethod || len(member.Parameters()) != 2 {
					problems <- "concurrent read observed an incomplete method"
					return
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

func TestInterfacesLookupMemberBridgesReceiverKindsAndTriState(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "Array", "Dictionary", "Callable", "Signal"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "Broken", parent: "MissingParent"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	for _, method := range []struct {
		owner, name string
	}{
		{owner: "Object", name: "inherited"},
		{owner: "Broken", name: "direct"},
		{owner: "int", name: "to_string"},
		{owner: "Array", name: "append"},
		{owner: "Dictionary", name: "get"},
		{owner: "Callable", name: "call"},
		{owner: "Signal", name: "connect"},
		{owner: "Object", name: "conflict"},
	} {
		require.NoError(t, builder.AddMethod(method.owner, method.name, "int", nil, false, false))
	}
	require.NoError(t, builder.AddProperty("Object", "conflict", "int"))
	engine, err := builder.Build()
	require.NoError(t, err)
	interfaces := BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"player.gd": "class_name Player\nvar field: int\n",
	})), engine)
	player := requireInterface(t, interfaces, "player.gd")
	malformedArrayNode := &typeNode{kind: KindArray, hasElement: true}
	malformedArray := Type{node: malformedArrayNode}
	malformedArrayNode.element = malformedArray

	for _, testCase := range []struct {
		name     string
		receiver Type
		member   string
		state    LookupState
		kind     MemberKind
	}{
		{name: "user", receiver: player.Type(), member: "field", state: LookupFound, kind: MemberVariable},
		{name: "engine inherited", receiver: engine.Class("Node"), member: "inherited", state: LookupFound, kind: MemberEngineMethod},
		{name: "builtin scalar", receiver: Builtin("int"), member: "to_string", state: LookupFound, kind: MemberEngineMethod},
		{name: "array", receiver: Array(nil), member: "append", state: LookupFound, kind: MemberEngineMethod},
		{name: "dictionary", receiver: Dictionary(nil, nil), member: "get", state: LookupFound, kind: MemberEngineMethod},
		{name: "callable", receiver: Callable(), member: "call", state: LookupFound, kind: MemberEngineMethod},
		{name: "signal", receiver: Signal(), member: "connect", state: LookupFound, kind: MemberEngineMethod},
		{name: "engine absent", receiver: engine.Class("Node"), member: "missing", state: LookupAbsent},
		{name: "builtin absent", receiver: Dictionary(nil, nil), member: "missing", state: LookupAbsent},
		{name: "incomplete chain keeps direct", receiver: engine.Class("Broken"), member: "direct", state: LookupFound, kind: MemberEngineMethod},
		{name: "incomplete chain blocks absence", receiver: engine.Class("Broken"), member: "missing", state: LookupUnknown},
		{name: "engine direct collision", receiver: engine.Class("Node"), member: "conflict", state: LookupUnknown},
		{name: "meta", receiver: Class("Node", nil, true), member: "inherited", state: LookupUnknown},
		{name: "enum", receiver: Enum("Node.Mode"), member: "missing", state: LookupUnknown},
		{name: "variant", receiver: Variant(), member: "missing", state: LookupUnknown},
		{name: "malformed container", receiver: malformedArray, member: "append", state: LookupUnknown},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := interfaces.LookupMember(testCase.receiver, testCase.member)
			require.Equal(t, testCase.state, result.State())
			if testCase.state == LookupFound {
				member, ok := result.Member()
				require.True(t, ok)
				require.Equal(t, testCase.kind, member.Kind())
			}
		})
	}
	playerConflict := player.Lookup("conflict")
	require.Equal(t, LookupUnknown, playerConflict.State())

	withoutEngine := BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"plain.gd": "class_name Plain\n",
	})), nil)
	unavailableResult := withoutEngine.LookupMember(Array(nil), "append")
	require.Equal(t, LookupUnknown, unavailableResult.State())
	require.Contains(t, unavailableResult.Reason(), "unavailable")
}

func TestInterfacesLookupMetaMemberFiltersStaticFactsAndPreservesOwnerChains(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "String"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "Broken", parent: "Missing"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	for _, method := range []struct {
		owner, name string
		static      bool
	}{
		{owner: "Object", name: "inherited_static", static: true},
		{owner: "Node", name: "static_engine", static: true},
		{owner: "Node", name: "instance_engine"},
		{owner: "Broken", name: "direct_static", static: true},
	} {
		require.NoError(t, builder.AddMethod(method.owner, method.name, "int", nil, method.static, false))
	}
	require.NoError(t, builder.AddProperty("Node", "engine_property", "int"))
	engine, err := builder.Build()
	require.NoError(t, err)
	interfaces := BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"base.gd":  "class_name Base\nconst ANSWER = 42\nstatic var static_field: int\nvar instance_field: int\nstatic func static_method() -> int:\n\tpass\nfunc instance_method() -> int:\n\tpass\nsignal changed\nenum Named { VALUE }\nenum { ANON }\nclass Inner:\n\tpass\n",
		"child.gd": "class_name Child extends Base\nvar static_field: int\n",
	})), engine)

	assertResult := func(t *testing.T, receiver Type, name string, want LookupState, wantKind MemberKind) {
		t.Helper()
		got := interfaces.LookupMetaMember(receiver, name)
		require.Equal(t, want, got.State())
		if want == LookupFound {
			member, ok := got.Member()
			require.True(t, ok)
			require.Equal(t, wantKind, member.Kind())
		}
	}

	base := Class("base.gd", nil, true)
	for _, testCase := range []struct {
		name string
		kind MemberKind
	}{
		{name: "ANSWER", kind: MemberConstant},
		{name: "static_field", kind: MemberVariable},
		{name: "static_method", kind: MemberMethod},
		{name: "Named", kind: MemberEnum},
		{name: "ANON", kind: MemberEnumMember},
		{name: "Inner", kind: MemberClass},
	} {
		t.Run("user "+testCase.name, func(t *testing.T) {
			assertResult(t, base, testCase.name, LookupFound, testCase.kind)
		})
	}
	for _, name := range []string{"instance_field", "instance_method", "changed"} {
		t.Run("user instance "+name, func(t *testing.T) {
			assertResult(t, base, name, LookupUnknown, MemberVariable)
		})
	}
	assertResult(t, base, "missing", LookupUnknown, MemberVariable)
	assertResult(t, Class("child.gd", nil, true), "ANSWER", LookupFound, MemberConstant)
	assertResult(t, Class("child.gd", nil, true), "static_field", LookupUnknown, MemberVariable)

	node := Class("Node", nil, true)
	assertResult(t, node, "static_engine", LookupFound, MemberEngineMethod)
	assertResult(t, node, "inherited_static", LookupFound, MemberEngineMethod)
	assertResult(t, node, "instance_engine", LookupUnknown, MemberEngineMethod)
	assertResult(t, node, "engine_property", LookupUnknown, MemberEngineProperty)
	assertResult(t, node, "missing", LookupUnknown, MemberEngineMethod)
	assertResult(t, Class("Broken", nil, true), "direct_static", LookupFound, MemberEngineMethod)
	assertResult(t, Class("Broken", nil, true), "missing", LookupUnknown, MemberEngineMethod)
	assertResult(t, Class("Node", nil, false), "static_engine", LookupUnknown, MemberEngineMethod)
}

func TestInterfacesLookupNamedEnumMembersRequiresExactProvenance(t *testing.T) {
	build := func(t *testing.T, files map[string]string, receiver Type, enumName string) (*InterfaceSet, Member, *namedEnumObjectProvenance) {
		t.Helper()
		source := sources(t, files)
		require.Empty(t, source.ParseFailures(), "real parser fixture failed")
		interfaces := BuildInterfaces(BuildIndex(source), reducerTestEngine(t))
		selected := interfaces.LookupMetaMember(receiver, enumName)
		require.Equal(t, LookupFound, selected.State())
		member, ok := selected.Member()
		require.True(t, ok)
		require.Equal(t, MemberEnum, member.Kind())
		return interfaces, member, interfaces.namedEnumObject(receiver, member)
	}
	assertUnknown := func(t *testing.T, got namedEnumMemberLookup) {
		t.Helper()
		require.Equal(t, LookupUnknown, got.state)
		require.NotEmpty(t, strings.TrimSpace(got.reason))
	}

	files := map[string]string{
		"base.gd":  "class_name Base\nenum Mode { IDLE, RUNNING }\n",
		"child.gd": "class_name Child extends Base\n",
	}
	receiver := Class("child.gd", nil, true)
	interfaces, _, provenance := build(t, files, receiver, "Mode")
	found := interfaces.lookupNamedEnumMember(provenance, "IDLE")
	require.Equal(t, LookupFound, found.state)
	require.True(t, found.typeValue.Equal(Enum("base.gd.Mode")))
	absent := interfaces.lookupNamedEnumMember(provenance, "MISSING")
	require.Equal(t, LookupAbsent, absent.state)

	t.Run("foreign snapshot", func(t *testing.T) {
		foreign, _, _ := build(t, files, receiver, "Mode")
		assertUnknown(t, foreign.lookupNamedEnumMember(provenance, "IDLE"))
	})
	t.Run("missing payload", func(t *testing.T) {
		assertUnknown(t, interfaces.lookupNamedEnumMember(nil, "IDLE"))
	})
	t.Run("unavailable interface set", func(t *testing.T) {
		var unavailable *InterfaceSet
		assertUnknown(t, unavailable.lookupNamedEnumMember(provenance, "IDLE"))
	})
	t.Run("incomplete owner", func(t *testing.T) {
		incomplete, _, incompleteProvenance := build(t, map[string]string{
			"broken.gd": "class_name Broken extends Missing\nenum Mode { IDLE }\n",
		}, Class("broken.gd", nil, true), "Mode")
		assertUnknown(t, incomplete.lookupNamedEnumMember(incompleteProvenance, "IDLE"))
	})
	t.Run("duplicate enum declarations", func(t *testing.T) {
		source := sources(t, map[string]string{
			"duplicate.gd": "class_name Duplicate\nenum Mode { IDLE }\n",
		})
		require.Empty(t, source.ParseFailures(), "real parser fixture failed")
		index := BuildIndex(source)
		index.Classes["duplicate.gd"].Declarations = append(index.Classes["duplicate.gd"].Declarations, index.Classes["duplicate.gd"].Declarations[0])
		duplicate := BuildInterfaces(index, reducerTestEngine(t))
		got := duplicate.LookupMetaMember(Class("duplicate.gd", nil, true), "Mode")
		require.Equal(t, LookupUnknown, got.State())
		require.NotEmpty(t, strings.TrimSpace(got.Reason()))
	})
	t.Run("duplicate enum member names", func(t *testing.T) {
		duplicate, _, duplicateProvenance := build(t, map[string]string{
			"duplicate.gd": "class_name Duplicate\nenum Mode { IDLE, IDLE }\n",
		}, Class("duplicate.gd", nil, true), "Mode")
		assertUnknown(t, duplicate.lookupNamedEnumMember(duplicateProvenance, "IDLE"))
	})
	t.Run("wrong retained node", func(t *testing.T) {
		wrong, _, wrongProvenance := build(t, files, receiver, "Mode")
		class := wrong.index.Classes[wrongProvenance.declaringClassID]
		for at := range class.Declarations {
			if class.Declarations[at].Name == "Mode" {
				class.Declarations[at].Node = &ast.VariableDeclaration{Name: "Mode"}
			}
		}
		assertUnknown(t, wrong.lookupNamedEnumMember(wrongProvenance, "IDLE"))
	})
	t.Run("multiple exact declarations", func(t *testing.T) {
		multiple, _, multipleProvenance := build(t, files, receiver, "Mode")
		class := multiple.index.Classes[multipleProvenance.declaringClassID]
		for _, declaration := range class.Declarations {
			if declaration.Name == "Mode" {
				class.Declarations = append(class.Declarations, declaration)
				break
			}
		}
		assertUnknown(t, multiple.lookupNamedEnumMember(multipleProvenance, "IDLE"))
	})
	t.Run("shared retained node", func(t *testing.T) {
		source := sources(t, map[string]string{
			"base.gd":  "class_name Base\nenum Mode { IDLE }\n",
			"other.gd": "class_name Other\n",
		})
		require.Empty(t, source.ParseFailures(), "real parser fixture failed")
		index := BuildIndex(source)
		declaration := index.Classes["base.gd"].Declarations[0]
		index.Classes["other.gd"].Declarations = append(index.Classes["other.gd"].Declarations, Declaration{
			Name: "OtherMode", Kind: DeclarationEnum, Line: 2, Column: 1, Node: declaration.Node,
		})
		shared := BuildInterfaces(index, reducerTestEngine(t))
		selected := shared.LookupMetaMember(Class("base.gd", nil, true), "Mode")
		member, ok := selected.Member()
		require.Equal(t, LookupFound, selected.State())
		require.True(t, ok)
		sharedProvenance := shared.namedEnumObject(Class("base.gd", nil, true), member)
		assertUnknown(t, shared.lookupNamedEnumMember(sharedProvenance, "IDLE"))
	})
	t.Run("unavailable ownership inventory", func(t *testing.T) {
		copy := *interfaces
		copy.enumDeclarationOwners = nil
		copyProvenance := *provenance
		copyProvenance.set = &copy
		assertUnknown(t, copy.lookupNamedEnumMember(&copyProvenance, "IDLE"))
	})
	t.Run("nil class entry", func(t *testing.T) {
		source := sources(t, map[string]string{"valid.gd": "class_name Valid\n"})
		index := BuildIndex(source)
		index.Classes["missing.gd"] = nil
		withNilClass := BuildInterfaces(index, reducerTestEngine(t))
		got := withNilClass.LookupMetaMember(Class("missing.gd", nil, true), "Mode")
		require.Equal(t, LookupUnknown, got.State())
		require.NotEmpty(t, strings.TrimSpace(got.Reason()))
	})
	t.Run("malformed retained node", func(t *testing.T) {
		malformed, _, malformedProvenance := build(t, files, receiver, "Mode")
		malformedProvenance.declaration.Name = "Other"
		assertUnknown(t, malformed.lookupNamedEnumMember(malformedProvenance, "IDLE"))
	})
	t.Run("identity disagreement", func(t *testing.T) {
		for _, mutate := range []func(*namedEnumObjectProvenance){
			func(value *namedEnumObjectProvenance) { value.set = nil },
			func(value *namedEnumObjectProvenance) { value.receiver = Class("other.gd", nil, true) },
			func(value *namedEnumObjectProvenance) { value.declaringClassID = "other.gd" },
			func(value *namedEnumObjectProvenance) { value.enumName = "Other" },
			func(value *namedEnumObjectProvenance) { value.line++ },
			func(value *namedEnumObjectProvenance) { value.column++ },
			func(value *namedEnumObjectProvenance) { value.typeValue = Enum("other.gd.Mode") },
			func(value *namedEnumObjectProvenance) { value.declaration = nil },
		} {
			copy := *provenance
			mutate(&copy)
			assertUnknown(t, interfaces.lookupNamedEnumMember(&copy, "IDLE"))
		}
	})
}

type interfaceEngineClass struct {
	name   string
	parent string
}

func buildInterfaceEngine(t *testing.T, builtins []string, classes []interfaceEngineClass) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range builtins {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range classes {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}

func requireInterface(t *testing.T, interfaces *InterfaceSet, id string) *ClassInterface {
	t.Helper()
	class, ok := interfaces.Class(id)
	require.True(t, ok, "interface %q was not published", id)
	return class
}

func assertClassChain(t *testing.T, resolved Type, want ...string) {
	t.Helper()
	for index, name := range want {
		require.Equal(t, KindClass, resolved.Kind())
		require.Equal(t, name, resolved.Name())
		base, hasBase := resolved.Base()
		if index == len(want)-1 {
			require.False(t, hasBase, "chain root %q unexpectedly has base %q", name, base.Name())
			return
		}
		require.True(t, hasBase, "chain class %q has no base", name)
		resolved = base
	}
}

func interfaceMember(t *testing.T, class *ClassInterface, name string) Member {
	t.Helper()
	result := class.Lookup(name)
	require.Equal(t, LookupFound, result.State())
	member, ok := result.Member()
	require.True(t, ok, "Lookup(%q) returned no member", name)
	return member
}

func richInterfaceTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "float", "String", "StringName", "NodePath", "bool", "Array", "Dictionary", "Callable", "Signal"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "CanvasItem", parent: "Node"},
		{name: "Node2D", parent: "CanvasItem"},
		{name: "CharacterBody2D", parent: "Node2D"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}

func lookupInterfaceTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "Array", "Dictionary"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddMethod("Object", "engine_only", "int", []EngineArgumentSpec{{Type: "int", HasDefault: true}}, true, true))
	require.NoError(t, builder.AddMethod("Object", "conflict", "int", nil, false, false))
	for _, property := range []string{"engine_property", "conflict"} {
		require.NoError(t, builder.AddProperty("Object", property, "int"))
	}
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}
