package semantic

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestInterfacesResolveImplicitEngineChainAndDirectMembers(t *testing.T) {
	engine := interfaceTestEngine(t)
	index := BuildIndex(sources(t, map[string]string{
		"player.gd": "class_name Player\nvar health: int\n",
	}))

	interfaces := BuildInterfaces(index, engine)
	player, ok := interfaces.Class("player.gd")
	if !ok {
		t.Fatal("Player interface was not published")
	}
	if !player.Complete() {
		t.Fatalf("Player interface is incomplete: %s", player.Cause())
	}

	chain := player.Type()
	for _, want := range []string{"player.gd", "RefCounted", "Object"} {
		if chain.Kind() != KindClass || chain.Name() != want {
			t.Fatalf("chain at %q = %v %q (%q)", want, chain.Kind(), chain.Name(), chain.Reason())
		}
		base, hasBase := chain.Base()
		if want == "Object" {
			if hasBase {
				t.Fatalf("Object unexpectedly has base %q", base.Name())
			}
			break
		}
		if !hasBase {
			t.Fatalf("%s has no base", want)
		}
		chain = base
	}

	result := player.Lookup("health")
	if result.State() != LookupFound {
		t.Fatalf("health lookup state = %s (%s)", result.State(), result.Reason())
	}
	member, found := result.Member()
	if !found || member.Kind() != MemberVariable || member.Type().Kind() != KindBuiltin || member.Type().Name() != "int" || member.DeclaringClassID() != "player.gd" || member.Line() != 2 || member.Column() != 5 {
		t.Fatalf("health lookup = %#v, found %t", member, found)
	}
	if missing := player.Lookup("missing"); missing.State() != LookupAbsent {
		t.Fatalf("missing lookup state = %s (%s)", missing.State(), missing.Reason())
	}
}

func interfaceTestEngine(t *testing.T) *Engine {
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
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestInterfacesResolveDeclarationKindsAndSignatures(t *testing.T) {
	engine := richInterfaceTestEngine(t)
	index := BuildIndex(sources(t, map[string]string{
		"friend.gd": "class_name Friend\nclass Inner:\n\tpass\n",
		"player.gd": "class_name Player extends CharacterBody2D\nvar explicit: float\nvar inferred := 10\nvar dynamic\nvar unresolved = fetch()\nvar float_literal := 1.5\nvar string_literal := \"message\"\nvar string_name_literal := &\"label\"\nvar node_path_literal := ^\"child\"\nvar boolean_literal := true\nvar null_literal := null\nvar collection_literal := []\nvar operator_literal := 1 + 2\nvar identifier_literal = LIMIT\nstatic var static_field: int\nconst LIMIT := 10\nconst TYPED: String = \"typed\"\nconst BAD = fetch()\nvar array: Array[Dictionary[String, int]]\nvar friend: Friend\nvar nested: Friend.Inner\nvar inner: Inner\nvar mode: Mode\nvar native: Node\nclass Inner:\n\tpass\nenum Mode { ONE }\nenum { FLAG }\nsignal changed(typed: int, dynamic_parameter)\n@abstract\nfunc abstracted(typed: int, dynamic_parameter, inferred_parameter := 3, ...rest: Array) -> void\nfunc typed_signature(friend_value: Friend, nested_value: Friend.Inner, local_value: Inner, mode_value: Mode, node_value: Node, table: Dictionary[String, Array[int]]) -> Friend:\n\tpass\nstatic func static_value() -> String:\n\tpass\nfunc no_return():\n\tpass\nfunc untyped_rest(...rest):\n\tpass\nfunc invalid_rest(...rest: String):\n\tpass\nfunc invalid_typed_array_rest(...rest: Array[String]):\n\tpass\n",
	}))
	if len(index.ParseFailures) != 0 {
		t.Fatalf("fixture did not parse: %v", index.ParseFailures)
	}

	interfaces := BuildInterfaces(index, engine)
	player, ok := interfaces.Class("player.gd")
	if !ok {
		t.Fatal("Player interface was not published")
	}
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
		if member.Kind() != want.kind || member.Type().Kind() != want.typeKind || member.Type().Name() != want.typeName {
			t.Errorf("%s = kind %s type %v %q, want %s %v %q", name, member.Kind(), member.Type().Kind(), member.Type().Name(), want.kind, want.typeKind, want.typeName)
		}
	}

	if interfaceMember(t, player, "LIMIT").Const() != true || interfaceMember(t, player, "FLAG").Const() != true {
		t.Error("constants lost their metadata")
	}
	if !interfaceMember(t, player, "TYPED").Const() {
		t.Error("typed constant lost const metadata")
	}
	if interfaceMember(t, player, "Inner").Type().Meta() != true {
		t.Error("inner class declaration did not expose a meta-class")
	}
	for _, name := range []string{"unresolved", "null_literal", "collection_literal", "operator_literal", "identifier_literal", "BAD"} {
		if got := interfaceMember(t, player, name).Type(); got.Kind() != KindUnknown || got.Reason() == "" {
			t.Errorf("%s = kind %v reason %q, want reasoned Unknown", name, got.Kind(), got.Reason())
		}
	}

	array := interfaceMember(t, player, "array").Type()
	element, hasElement := array.Element()
	key, hasKey := element.Key()
	value, hasValue := element.Value()
	if array.Kind() != KindArray || !hasElement || element.Kind() != KindDictionary || !hasKey || !hasValue || key.Name() != "String" || value.Name() != "int" {
		t.Fatalf("nested container = %s; element=%s/%t key=%s/%t value=%s/%t", array, element, hasElement, key, hasKey, value, hasValue)
	}

	abstracted := interfaceMember(t, player, "abstracted")
	if !abstracted.Abstract() || abstracted.Static() || !abstracted.Variadic() {
		t.Fatalf("abstracted metadata = abstract %t static %t variadic %t", abstracted.Abstract(), abstracted.Static(), abstracted.Variadic())
	}
	returnType, hasReturn := abstracted.ReturnType()
	if !hasReturn || returnType.Kind() != KindVoid {
		t.Fatalf("abstracted return = %v %t", returnType, hasReturn)
	}
	parameters := abstracted.Parameters()
	if len(parameters) != 4 || parameters[0].Type().Name() != "int" || parameters[1].Type().Kind() != KindVariant || parameters[2].Type().Name() != "int" || !parameters[2].HasDefault() || !parameters[3].Variadic() {
		t.Fatalf("abstracted parameters = %#v", parameters)
	}
	if parameters[3].Type().Kind() != KindArray {
		t.Fatalf("rest parameter type = %s", parameters[3].Type())
	}
	if _, typedRest := parameters[3].Type().Element(); typedRest {
		t.Fatal("typed rest parameter was not retained as an untyped Array")
	}
	if static := interfaceMember(t, player, "static_value"); !static.Static() || static.Abstract() {
		t.Fatalf("static method metadata = static %t abstract %t", static.Static(), static.Abstract())
	}
	if static := interfaceMember(t, player, "static_field"); !static.Static() {
		t.Fatal("static field lost static metadata")
	}
	signature := interfaceMember(t, player, "typed_signature")
	returned, hasReturned := signature.ReturnType()
	if !hasReturned || returned.Kind() != KindClass || returned.Name() != "friend.gd" {
		t.Fatalf("typed signature return = %s/%t", returned, hasReturned)
	}
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
		if parameter.Kind() != want.kind || parameter.Name() != want.name {
			t.Errorf("typed signature parameter %d = %v %q, want %v %q", at, parameter.Kind(), parameter.Name(), want.kind, want.name)
		}
	}
	table := signature.Parameters()[5].Type()
	tableKey, tableKeyOK := table.Key()
	tableValue, tableValueOK := table.Value()
	arrayElement, arrayElementOK := tableValue.Element()
	if !tableKeyOK || !tableValueOK || !arrayElementOK || tableKey.Name() != "String" || tableValue.Kind() != KindArray || arrayElement.Name() != "int" {
		t.Fatalf("typed signature nested parameter = %s", table)
	}
	noReturn, hasReturn := interfaceMember(t, player, "no_return").ReturnType()
	if !hasReturn || noReturn.Kind() != KindUnknown || noReturn.Reason() == "" {
		t.Fatalf("omitted method return = %v %t", noReturn, hasReturn)
	}
	changed := interfaceMember(t, player, "changed")
	if parameters := changed.Parameters(); len(parameters) != 2 || parameters[0].Type().Name() != "int" || parameters[1].Type().Kind() != KindVariant {
		t.Fatalf("signal parameters = %#v", parameters)
	}
	untypedRest := interfaceMember(t, player, "untyped_rest")
	if !untypedRest.Variadic() {
		t.Fatal("untyped rest method lost variadic metadata")
	}
	untypedParameters := untypedRest.Parameters()
	if len(untypedParameters) != 1 || !untypedParameters[0].Variadic() || untypedParameters[0].Type().Kind() != KindArray {
		t.Fatalf("untyped rest parameter = %#v", untypedParameters)
	}
	if _, typed := untypedParameters[0].Type().Element(); typed {
		t.Fatal("untyped rest parameter became Array[Variant]")
	}
	for _, name := range []string{"invalid_rest", "invalid_typed_array_rest"} {
		invalidRest := interfaceMember(t, player, name).Parameters()
		if len(invalidRest) != 1 || invalidRest[0].Type().Kind() != KindUnknown || !strings.Contains(invalidRest[0].Type().Reason(), "Array") {
			t.Errorf("%s parameter = %#v, want reasoned Unknown", name, invalidRest)
		}
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
	if len(index.ParseFailures) != 0 {
		t.Fatalf("fixture did not parse: %v", index.ParseFailures)
	}
	interfaces := BuildInterfaces(index, engine)
	holder, ok := interfaces.Class("holder.gd")
	if !ok || !holder.Complete() {
		t.Fatalf("holder interface = %#v, present %t", holder, ok)
	}
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
		if got.Kind() != want.kind || got.Name() != want.name {
			t.Errorf("%s = %v %q (%q), want %v %q", name, got.Kind(), got.Name(), got.Reason(), want.kind, want.name)
		}
	}
	if _, typed := interfaceMember(t, holder, "plain_array").Type().Element(); typed {
		t.Error("bare Array annotation became typed")
	}
	if _, typed := interfaceMember(t, holder, "plain_dictionary").Type().Key(); typed {
		t.Error("bare Dictionary annotation became typed")
	}
	for _, name := range []string{"ambiguous", "unknown"} {
		got := interfaceMember(t, holder, name).Type()
		if got.Kind() != KindUnknown || !strings.Contains(got.Reason(), map[string]string{"ambiguous": "Duplicate", "unknown": "MissingType"}[name]) {
			t.Errorf("%s = %v (%q), want original unresolved spelling", name, got.Kind(), got.Reason())
		}
	}

	nested := interfaces.ResolveType("holder.gd", "Array[Dictionary[String, int]]")
	element, hasElement := nested.Element()
	if nested.Kind() != KindArray || !hasElement || element.Kind() != KindDictionary {
		t.Fatalf("nested annotation = %s", nested)
	}
	// The Engine boundary deliberately carries no engine enum declarations. A
	// qualified engine spelling therefore cannot be proven here, even where it
	// happens to name a real Godot enum; user-declared named enums above cover
	// successful named-enum resolution.
	for _, spelling := range []string{"Array[int", "Callable[int]", "MissingType", "Node.ProcessMode", "Node.ProcesMode", "int.Whatever"} {
		got := interfaces.ResolveType("holder.gd", spelling)
		if got.Kind() != KindUnknown || !strings.Contains(got.Reason(), spelling) {
			t.Errorf("ResolveType(%q) = %v (%q), want reasoned Unknown retaining spelling", spelling, got.Kind(), got.Reason())
		}
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
			if len(parsed.ParseFailures()) != 1 || parsed.ParseFailures()[0] != "voids.gd" {
				t.Fatalf("ParseFailures = %v, want voids.gd", parsed.ParseFailures())
			}
		})
	}

	index := BuildIndex(sources(t, map[string]string{
		"voids.gd": "class_name Voids\nfunc returns_void() -> void:\n\tpass\n",
	}))
	if len(index.ParseFailures) != 0 {
		t.Fatalf("return fixture did not parse: %v", index.ParseFailures)
	}
	interfaces := BuildInterfaces(index, richInterfaceTestEngine(t))
	class := requireInterface(t, interfaces, "voids.gd")
	returned, hasReturned := interfaceMember(t, class, "returns_void").ReturnType()
	if !hasReturned || returned.Kind() != KindVoid {
		t.Fatalf("explicit void return = %v %q (%q), present %t", returned.Kind(), returned.Name(), returned.Reason(), hasReturned)
	}
	for _, spelling := range []string{"void", "Array[void]", "Dictionary[void, int]", "Dictionary[int, void]", "Array[Dictionary[String, Array[void]]]"} {
		if got := interfaces.ResolveType("voids.gd", spelling); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "void") {
			t.Errorf("ResolveType(%q) = %v %q (%q), want void-specific Unknown", spelling, got.Kind(), got.Name(), got.Reason())
		}
	}
}

func TestInterfacesGlobalTypeResolutionFailsClosedOnIncompleteIndex(t *testing.T) {
	t.Run("parse failure blocks global class", func(t *testing.T) {
		index := BuildIndex(sources(t, map[string]string{
			"global.gd": "class_name Global\n",
			"broken.gd": "func (\n",
		}))
		interfaces := BuildInterfaces(index, interfaceTestEngine(t))
		if got := interfaces.ResolveType("", "Global"); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "Global") || !strings.Contains(got.Reason(), "broken.gd") {
			t.Fatalf("global class across parse failure = %v %q (%q), want reasoned Unknown", got.Kind(), got.Name(), got.Reason())
		}
		if got := interfaces.ResolveType("", "int"); got.Kind() != KindBuiltin || got.Name() != "int" {
			t.Fatalf("grounded builtin across parse failure = %v %q (%q)", got.Kind(), got.Name(), got.Reason())
		}
	})

	t.Run("duplicate global class remains ambiguous", func(t *testing.T) {
		interfaces := BuildInterfaces(BuildIndex(sources(t, map[string]string{
			"first.gd":  "class_name Duplicate\n",
			"second.gd": "class_name Duplicate\n",
		})), interfaceTestEngine(t))
		if got := interfaces.ResolveType("", "Duplicate"); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "Duplicate") {
			t.Fatalf("duplicate global class = %v %q (%q), want reasoned Unknown", got.Kind(), got.Name(), got.Reason())
		}
	})
}

func TestInterfacesLookupIsNearestAndTriState(t *testing.T) {
	if zero := (LookupResult{}); zero.State() != LookupUnknown || zero.Reason() != "" {
		t.Fatalf("zero lookup result = %s (%q), want fail-closed unknown", zero.State(), zero.Reason())
	}
	engine := lookupInterfaceTestEngine(t)
	index := BuildIndex(sources(t, map[string]string{
		"base.gd":    "class_name Base extends Node\nvar inherited: int\nvar shadowed: int\n",
		"derived.gd": "class_name Derived extends Base\nfunc shadowed() -> void:\n\tpass\nvar duplicate: int\n",
		"broken.gd":  "class_name Broken extends \"res://missing.gd\"\nvar direct: int\n",
	}))
	if len(index.ParseFailures) != 0 {
		t.Fatalf("fixture did not parse: %v", index.ParseFailures)
	}
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
	if !ok || !derived.Complete() {
		t.Fatalf("Derived interface = %#v, %t", derived, ok)
	}

	inherited := interfaceMember(t, derived, "inherited")
	if inherited.Kind() != MemberVariable || inherited.DeclaringClassID() != "base.gd" {
		t.Fatalf("inherited = %#v", inherited)
	}
	shadowed := interfaceMember(t, derived, "shadowed")
	if shadowed.Kind() != MemberMethod || shadowed.DeclaringClassID() != "derived.gd" {
		t.Fatalf("nearest incompatible shadow = %#v", shadowed)
	}
	engineMethod := interfaceMember(t, derived, "engine_only")
	engineMethodReturn, hasEngineMethodReturn := engineMethod.ReturnType()
	if engineMethod.Kind() != MemberEngineMethod || engineMethod.EngineOwner() != "Object" || !hasEngineMethodReturn || engineMethodReturn.Name() != "int" || !engineMethod.Static() || !engineMethod.Variadic() || engineMethod.Line() != 0 || engineMethod.Column() != 0 {
		t.Fatalf("engine method = %#v", engineMethod)
	}
	if parameters := engineMethod.Parameters(); len(parameters) != 1 || parameters[0].Name() != "" || parameters[0].Type().Name() != "int" || !parameters[0].HasDefault() {
		t.Fatalf("engine method parameters = %#v", parameters)
	}
	if engineProperty := interfaceMember(t, derived, "engine_property"); engineProperty.Kind() != MemberEngineProperty || engineProperty.EngineOwner() != "Object" || engineProperty.Type().Name() != "int" {
		t.Fatalf("engine property = %#v", engineProperty)
	}
	if result := derived.Lookup("duplicate"); result.State() != LookupUnknown || result.Reason() == "" {
		t.Fatalf("duplicate direct declaration = %s (%q)", result.State(), result.Reason())
	}
	if result := derived.Lookup("conflict"); result.State() != LookupUnknown || result.Reason() == "" {
		t.Fatalf("conflicting engine declaration = %s (%q)", result.State(), result.Reason())
	}
	if result := derived.Lookup("missing"); result.State() != LookupAbsent {
		t.Fatalf("complete-chain miss = %s (%q)", result.State(), result.Reason())
	}

	broken, ok := interfaces.Class("broken.gd")
	if !ok || broken.Complete() || broken.Cause() == "" {
		t.Fatalf("Broken interface = %#v, %t", broken, ok)
	}
	if direct := interfaceMember(t, broken, "direct"); direct.Kind() != MemberVariable || direct.DeclaringClassID() != "broken.gd" || direct.Type().Name() != "int" {
		t.Fatalf("direct member on incomplete class = %#v", direct)
	}
	if result := broken.Lookup("missing"); result.State() != LookupUnknown || result.Reason() == "" {
		t.Fatalf("incomplete-chain miss = %s (%q)", result.State(), result.Reason())
	}
}

func TestInterfacesLookupBaseKeepsUnifiedPrecedenceAndFailureState(t *testing.T) {
	interfaces := BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"base.gd":   "class_name Base\nfunc instance() -> int:\n\tpass\nstatic func shared() -> int:\n\tpass\n",
		"child.gd":  "class_name Child extends Base\nfunc instance() -> int:\n\tpass\nstatic func shared() -> int:\n\tpass\n",
		"broken.gd": "class_name Broken extends MissingBase\nfunc current():\n\tpass\n",
	})), richInterfaceTestEngine(t))

	for _, name := range []string{"instance", "shared"} {
		result := interfaces.LookupBase("child.gd", name)
		if result.State() != LookupFound {
			t.Fatalf("LookupBase(%q) = %s (%s), want found", name, result.State(), result.Reason())
		}
		member, ok := result.Member()
		if !ok || member.DeclaringClassID() != "base.gd" || member.Name() != name {
			t.Fatalf("LookupBase(%q) = %#v, found %t", name, member, ok)
		}
	}
	if result := interfaces.LookupBase("child.gd", "missing"); result.State() != LookupAbsent {
		t.Fatalf("complete missing base member = %s (%s), want absent", result.State(), result.Reason())
	}
	if result := interfaces.LookupBase("broken.gd", "missing"); result.State() != LookupUnknown || result.Reason() == "" {
		t.Fatalf("incomplete base member = %s (%q), want reasoned unknown", result.State(), result.Reason())
	}
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
		if class.Complete() || !strings.Contains(class.Cause(), "Duplicate") {
			t.Fatalf("ambiguous class completeness = %t (%q)", class.Complete(), class.Cause())
		}
		owners := class.Owners()
		if len(owners) != 1 || owners[0].ClassID() != "ambiguous.gd" {
			t.Fatalf("ambiguous owners = %#v; engine Duplicate must not be used", owners)
		}
		base, hasBase := class.Type().Base()
		if !hasBase || base.Kind() != KindUnknown || !strings.Contains(base.Reason(), "Duplicate") {
			t.Fatalf("ambiguous base = %s/%t (%q)", base, hasBase, base.Reason())
		}
		if direct := interfaceMember(t, class, "direct"); direct.Type().Name() != "int" {
			t.Fatalf("direct declaration was not independently resolved: %#v", direct)
		}
		if result := class.Lookup("missing"); result.State() != LookupUnknown || !strings.Contains(result.Reason(), "Duplicate") {
			t.Fatalf("ambiguous base miss = %s (%q)", result.State(), result.Reason())
		}
		if got := interfaces.ResolveType("ambiguous.gd", "Duplicate"); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "Duplicate") {
			t.Fatalf("ambiguous annotation became engine type: %v (%q)", got.Kind(), got.Reason())
		}
	})

	t.Run("project cycle and parse failure terminate", func(t *testing.T) {
		engine := interfaceTestEngine(t)
		cycleIndex := BuildIndex(sources(t, map[string]string{
			"a.gd": "class_name A extends B\n",
			"b.gd": "class_name B extends A\n",
		}))
		cyclic := requireInterface(t, BuildInterfaces(cycleIndex, engine), "a.gd")
		if cyclic.Complete() || !strings.Contains(cyclic.Cause(), "cycle") {
			t.Fatalf("cyclic class completeness = %t (%q)", cyclic.Complete(), cyclic.Cause())
		}
		cycleBase, hasCycleBase := cyclic.Type().Base()
		if !hasCycleBase || cycleBase.Kind() != KindUnknown || !strings.Contains(cycleBase.Reason(), "cycle") {
			t.Fatalf("cyclic type base = %s/%t (%q)", cycleBase, hasCycleBase, cycleBase.Reason())
		}
		for attempt := 0; attempt < 3; attempt++ {
			if result := cyclic.Lookup("missing"); result.State() != LookupUnknown || !strings.Contains(result.Reason(), "cycle") {
				t.Fatalf("cyclic miss attempt %d = %s (%q)", attempt, result.State(), result.Reason())
			}
		}

		source := sources(t, map[string]string{"good.gd": "class_name Good\nvar direct: int\n"})
		source.paths = append(source.paths, "broken.gd")
		source.failures = append(source.failures, "broken.gd")
		parseFailed := requireInterface(t, BuildInterfaces(BuildIndex(source), lookupInterfaceTestEngine(t)), "good.gd")
		if parseFailed.Complete() || !strings.Contains(parseFailed.Cause(), "broken.gd") {
			t.Fatalf("parse-failed class completeness = %t (%q)", parseFailed.Complete(), parseFailed.Cause())
		}
		parseBase, hasParseBase := parseFailed.Type().Base()
		if !hasParseBase || parseBase.Kind() != KindUnknown || !strings.Contains(parseBase.Reason(), "broken.gd") {
			t.Fatalf("parse-failed type base = %s/%t (%q)", parseBase, hasParseBase, parseBase.Reason())
		}
		if direct := interfaceMember(t, parseFailed, "direct"); direct.Type().Name() != "int" {
			t.Fatalf("parse failure prevented known intrinsic type: %#v", direct)
		}
		if result := parseFailed.Lookup("missing"); result.State() != LookupUnknown || !strings.Contains(result.Reason(), "broken.gd") {
			t.Fatalf("parse-failed miss = %s (%q)", result.State(), result.Reason())
		}
		if result := parseFailed.Lookup("engine_only"); result.State() != LookupUnknown || !strings.Contains(result.Reason(), "broken.gd") {
			t.Fatalf("parse failure exposed an unproven engine member: %s (%q)", result.State(), result.Reason())
		}
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
				if class.Complete() || !strings.Contains(class.Cause(), testCase.wantCause) {
					t.Fatalf("completeness = %t (%q), want cause containing %q", class.Complete(), class.Cause(), testCase.wantCause)
				}
				base, hasBase := class.Type().Base()
				if !hasBase || base.Kind() != KindUnknown || !strings.Contains(base.Reason(), testCase.wantCause) {
					t.Fatalf("type base = %s/%t (%q), want unknown containing %q", base, hasBase, base.Reason(), testCase.wantCause)
				}
				if literal := interfaceMember(t, class, "literal"); literal.Type().Name() != "int" {
					t.Fatalf("literal direct declaration = %#v", literal)
				}
				if result := class.Lookup("missing"); result.State() != LookupUnknown || !strings.Contains(result.Reason(), testCase.wantCause) {
					t.Fatalf("incomplete engine miss = %s (%q)", result.State(), result.Reason())
				}
			})
		}

		external := BuildIndex(sources(t, map[string]string{
			"external.gd": "class_name External extends MissingNative\nvar literal := 3\n",
		}))
		externalClass := requireInterface(t, BuildInterfaces(external, interfaceTestEngine(t)), "external.gd")
		if externalClass.Complete() || !strings.Contains(externalClass.Cause(), "MissingNative") {
			t.Fatalf("absent external engine class completeness = %t (%q)", externalClass.Complete(), externalClass.Cause())
		}
		externalBase, hasExternalBase := externalClass.Type().Base()
		if !hasExternalBase || externalBase.Kind() != KindUnknown || !strings.Contains(externalBase.Reason(), "MissingNative") {
			t.Fatalf("absent external engine type base = %s/%t (%q)", externalBase, hasExternalBase, externalBase.Reason())
		}
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
			if scoped.Complete() || !strings.Contains(scoped.Cause(), testCase.wantCause) {
				t.Fatalf("scoped interface completeness = %t (%q), want incomplete cause containing %q", scoped.Complete(), scoped.Cause(), testCase.wantCause)
			}

			// A direct lexical declaration remains usable before the incomplete
			// ancestry segment; only a miss must not fall through to Global.
			if got := interfaces.ResolveType("scoped.gd", "Local"); got.Kind() != KindClass || got.Name() != "scoped.gd#Local" {
				t.Fatalf("direct Local = %v %q (%q)", got.Kind(), got.Name(), got.Reason())
			}
			if got := interfaces.ResolveType("scoped.gd", "Global"); got.Kind() != KindUnknown || !strings.Contains(got.Reason(), "Global") || !strings.Contains(got.Reason(), testCase.wantCause) {
				t.Fatalf("Global fell through incomplete ancestry = %v %q (%q), want reasoned Unknown containing %q", got.Kind(), got.Name(), got.Reason(), testCase.wantCause)
			}
		})
	}
}

func TestInterfacesDoNotWalkFunctionAccessorOrLambdaBodies(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"body.gd": "class_name Body\nvar exposed: int:\n\tget:\n\t\tvar getter_hidden := 1\n\t\treturn getter_hidden\n\tset(value):\n\t\tvar setter_hidden := 2\n\t\texposed = value\nfunc outer():\n\tvar function_hidden := 3\n\tvar closure = func():\n\t\tvar lambda_hidden := 4\n\t\treturn lambda_hidden\n",
	}))
	if len(index.ParseFailures) != 0 {
		t.Fatalf("fixture did not parse: %v", index.ParseFailures)
	}
	class := requireInterface(t, BuildInterfaces(index, interfaceTestEngine(t)), "body.gd")
	if direct := class.DirectMembers(); len(direct) != 2 || direct[0].Name() != "exposed" || direct[1].Name() != "outer" {
		t.Fatalf("direct members = %#v", direct)
	}
	if exposed := interfaceMember(t, class, "exposed"); exposed.Type().Name() != "int" {
		t.Fatalf("header type was not retained: %#v", exposed)
	}
	for _, hidden := range []string{"getter_hidden", "setter_hidden", "function_hidden", "closure", "lambda_hidden"} {
		if result := class.Lookup(hidden); result.State() != LookupAbsent {
			t.Errorf("body-local %q changed shallow lookup to %s (%q)", hidden, result.State(), result.Reason())
		}
	}
}

func TestInterfacesAreDeterministicImmutableAndSafeForConcurrentReads(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"alpha.gd": "class_name Alpha extends Node\nvar field: int\nfunc call(value: int, fallback := 3) -> void:\n\tpass\n",
	}))
	first := BuildInterfaces(index, richInterfaceTestEngine(t))
	second := BuildInterfaces(index, richInterfaceTestEngine(t))
	if !reflect.DeepEqual(first.ClassIDs(), second.ClassIDs()) {
		t.Fatalf("class IDs differ: %v / %v", first.ClassIDs(), second.ClassIDs())
	}
	for _, id := range first.ClassIDs() {
		left, right := requireInterface(t, first, id), requireInterface(t, second, id)
		if !left.Type().Equal(right.Type()) || left.Complete() != right.Complete() || left.Cause() != right.Cause() ||
			!reflect.DeepEqual(left.Owners(), right.Owners()) || !reflect.DeepEqual(left.DirectMembers(), right.DirectMembers()) {
			t.Fatalf("repeated interface resolution for %q differed", id)
		}
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

	if again := interfaceMember(t, alpha, "field"); again.Name() != "field" || again.Type().Name() != "int" {
		t.Fatalf("caller mutation changed cached field: %#v", again)
	}
	if again := interfaceMember(t, alpha, "call"); len(again.Parameters()) != 2 || again.Parameters()[0].Name() != "value" {
		t.Fatalf("caller mutation changed cached parameters: %#v", again)
	}
	if owners := alpha.Owners(); len(owners) == 0 || owners[0].ClassID() != "alpha.gd" {
		t.Fatalf("caller mutation changed cached owners: %#v", owners)
	}
	if ids := first.ClassIDs(); len(ids) != 1 || ids[0] != "alpha.gd" {
		t.Fatalf("caller mutation changed cached IDs: %#v", ids)
	}
	if result := first.Lookup("alpha.gd", "missing"); result.State() != LookupAbsent {
		t.Fatalf("post-publication input mutation changed lookup: %s (%q)", result.State(), result.Reason())
	}
	if resolved := first.ResolveType("alpha.gd", "Alpha"); resolved.Kind() != KindClass || resolved.Name() != "alpha.gd" {
		t.Fatalf("post-publication input mutation changed type resolution: %v %q (%q)", resolved.Kind(), resolved.Name(), resolved.Reason())
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
		t.Error(problem)
	}
}

func TestInterfacesLookupMemberBridgesReceiverKindsAndTriState(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "Array", "Dictionary", "Callable", "Signal"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "Broken", parent: "MissingParent"},
	} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
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
		if err := builder.AddMethod(method.owner, method.name, "int", nil, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddProperty("Object", "conflict", "int"); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
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
			if result.State() != testCase.state {
				t.Fatalf("LookupMember(%s, %q) = %s (%q), want %s", testCase.receiver, testCase.member, result.State(), result.Reason(), testCase.state)
			}
			if testCase.state == LookupFound {
				member, ok := result.Member()
				if !ok || member.Kind() != testCase.kind {
					t.Fatalf("LookupMember(%s, %q) member = %#v, %t; want %s", testCase.receiver, testCase.member, member, ok, testCase.kind)
				}
			}
		})
	}
	if result := player.Lookup("conflict"); result.State() != LookupUnknown {
		t.Fatalf("user lookup inherited engine direct collision = %s (%q), want unknown", result.State(), result.Reason())
	}

	withoutEngine := BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"plain.gd": "class_name Plain\n",
	})), nil)
	if result := withoutEngine.LookupMember(Array(nil), "append"); result.State() != LookupUnknown || !strings.Contains(result.Reason(), "unavailable") {
		t.Fatalf("unavailable builtin engine = %s (%q), want reasoned unknown", result.State(), result.Reason())
	}
}

func TestInterfacesLookupMetaMemberFiltersStaticFactsAndPreservesOwnerChains(t *testing.T) {
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "String"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "Broken", parent: "Missing"},
	} {
		if err := builder.AddClass(class.name, class.parent); err != nil {
			t.Fatal(err)
		}
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
		if err := builder.AddMethod(method.owner, method.name, "int", nil, method.static, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddProperty("Node", "engine_property", "int"); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	interfaces := BuildInterfaces(BuildIndex(sources(t, map[string]string{
		"base.gd":  "class_name Base\nconst ANSWER = 42\nstatic var static_field: int\nvar instance_field: int\nstatic func static_method() -> int:\n\tpass\nfunc instance_method() -> int:\n\tpass\nsignal changed\nenum Named { VALUE }\nenum { ANON }\nclass Inner:\n\tpass\n",
		"child.gd": "class_name Child extends Base\nvar static_field: int\n",
	})), engine)

	assertResult := func(t *testing.T, receiver Type, name string, want LookupState, wantKind MemberKind) {
		t.Helper()
		got := interfaces.LookupMetaMember(receiver, name)
		if got.State() != want {
			t.Fatalf("LookupMetaMember(%s, %q) = %s (%q), want %s", receiver, name, got.State(), got.Reason(), want)
		}
		if want == LookupFound {
			member, ok := got.Member()
			if !ok || member.Kind() != wantKind {
				t.Fatalf("LookupMetaMember(%s, %q) member = %#v, %t; want %s", receiver, name, member, ok, wantKind)
			}
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

type interfaceEngineClass struct {
	name   string
	parent string
}

func buildInterfaceEngine(t *testing.T, builtins []string, classes []interfaceEngineClass) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range builtins {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	for _, class := range classes {
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

func requireInterface(t *testing.T, interfaces *InterfaceSet, id string) *ClassInterface {
	t.Helper()
	class, ok := interfaces.Class(id)
	if !ok {
		t.Fatalf("interface %q was not published", id)
	}
	return class
}

func assertClassChain(t *testing.T, resolved Type, want ...string) {
	t.Helper()
	for index, name := range want {
		if resolved.Kind() != KindClass || resolved.Name() != name {
			t.Fatalf("chain at %d = %v %q (%q), want class %q", index, resolved.Kind(), resolved.Name(), resolved.Reason(), name)
		}
		base, hasBase := resolved.Base()
		if index == len(want)-1 {
			if hasBase {
				t.Fatalf("chain root %q unexpectedly has base %q", name, base.Name())
			}
			return
		}
		if !hasBase {
			t.Fatalf("chain class %q has no base", name)
		}
		resolved = base
	}
}

func interfaceMember(t *testing.T, class *ClassInterface, name string) Member {
	t.Helper()
	result := class.Lookup(name)
	if result.State() != LookupFound {
		t.Fatalf("Lookup(%q) = %s (%s)", name, result.State(), result.Reason())
	}
	member, ok := result.Member()
	if !ok {
		t.Fatalf("Lookup(%q) returned no member", name)
	}
	return member
}

func richInterfaceTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "float", "String", "StringName", "NodePath", "bool", "Array", "Dictionary", "Callable", "Signal"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Node", parent: "Object"},
		{name: "CanvasItem", parent: "Node"},
		{name: "Node2D", parent: "CanvasItem"},
		{name: "CharacterBody2D", parent: "Node2D"},
	} {
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

func lookupInterfaceTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "Array", "Dictionary"} {
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
	if err := builder.AddMethod("Object", "engine_only", "int", []EngineArgumentSpec{{Type: "int", HasDefault: true}}, true, true); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddMethod("Object", "conflict", "int", nil, false, false); err != nil {
		t.Fatal(err)
	}
	for _, property := range []string{"engine_property", "conflict"} {
		if err := builder.AddProperty("Object", property, "int"); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
