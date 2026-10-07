package semantic

import (
	"sync"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
)

func TestCollectionLifetimeFoldsClosedMutationVocabulary(t *testing.T) {
	source := sources(t, map[string]string{
		"collections.gd": `class_name Collections
func run(flag: bool) -> void:
	var array := [1]
	array.append(2)
	array.push_back(3)
	array.push_front(4)
	array.insert(0, 5)
	array.set(0, 6)
	array.fill(7)
	array.append_array([8.5])
	array.assign([9])
	array.clear()
	array.erase(9)
	array.pop_at(0)
	array.pop_back()
	array.pop_front()
	array.remove_at(0)
	array.reverse()
	array.shuffle()
	array.sort()
	array.sort_custom(func(left: Variant, right: Variant) -> bool: return left < right)
	array.make_read_only()
	var dictionary := {"one": 1}
	dictionary["two"] = 2.5
	dictionary.set("three", 3)
	dictionary.get_or_add("four", 4)
	dictionary.merge({"five": 5}, true)
	dictionary.assign({"six": 6})
	dictionary.clear()
	dictionary.erase("six")
	dictionary.sort()
	dictionary.make_read_only()
	var conditional := [1]
	if flag:
		conditional.append(2.5)
	var branch_only := [1]
	if flag:
		branch_only.append("two")
	match flag:
		true:
			conditional.push_back(3)
		_:
			pass
	while flag:
		conditional.fill(4)
		break
	for value: float in [5.5]:
		conditional.append(value)
	var indexed := [1]
	indexed[0] = "two"
	var callback := func() -> void:
		var lambda_values := [1]
		lambda_values.append(2.5)
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, collectionTestEngine(t))
	file := source.File("collections.gd")
	floatType := Builtin("float")
	stringType := Builtin("String")
	want := map[string]Type{
		"array":         Array(&floatType),
		"dictionary":    Dictionary(&stringType, &floatType),
		"conditional":   Array(&floatType),
		"branch_only":   Array(collectionTypePointer(Variant())),
		"indexed":       Array(collectionTypePointer(Variant())),
		"lambda_values": Array(&floatType),
	}
	for name, expected := range want {
		t.Run(name, func(t *testing.T) {
			declaration := collectionDeclaration(t, file, name)
			if got := analyzer.CollectionLifetime(declaration); !got.Equal(expected) {
				t.Fatalf("CollectionLifetime(%s) = %s (%q), want %s", name, got, got.Reason(), expected)
			}
		})
	}
}

func TestCollectionLifetimeUsesExactBindingAndFailsClosedAfterEscapes(t *testing.T) {
	source := sources(t, map[string]string{
		"lifetime.gd": `class_name Lifetime
var stale_member_source := [1]
func consume(value: Variant) -> void:
	pass
func parameter_source(parameter := [1]) -> void:
	parameter.append("two")
	var parameter_target := [1]
	parameter_target.append(parameter[1])
func stale_member_element():
	return stale_member_source[1]
func stale_member_collection():
	return stale_member_source
func run() -> void:
	var untouched := [1]
	var shadowed := [1]
	shadowed.append(2)
	var callback := func(shadowed: Array):
		shadowed.append("not ours")
	var passed := [1]
	consume(passed)
	var captured := [1]
	var capture := func(): return captured
	var aliased := [1]
	var alias := aliased
	var stale_element_source := [1]
	stale_element_source.append("two")
	var stale_element_target := [1]
	stale_element_target.append(stale_element_source[1])
	var stale_bulk_source := [1]
	stale_bulk_source.append("two")
	var stale_bulk_target := [1]
	stale_bulk_target.append_array(stale_bulk_source)
	var stale_indirect_source := [1]
	stale_indirect_source.append("two")
	var stale_element := stale_indirect_source[1]
	var stale_indirect_target := [1]
	stale_indirect_target.append(stale_element)
	stale_member_source.append("two")
	var stale_member_target := [1]
	stale_member_target.append(stale_member_source[1])
	var stale_call_element_target := [1]
	stale_call_element_target.append(stale_member_element())
	var stale_call_bulk_target := [1]
	stale_call_bulk_target.append_array(stale_member_collection())
	var match_bound := [1]
	match match_bound:
		var whole:
			whole.append("two")
	var returned := [1]
	if true:
		return returned
	var rebound := [1]
	rebound = [2]
	var compound := [1]
	compound[0] += 2
	var dynamic := [1]
	dynamic.resize(2)
	var unknown_write := [1]
	unknown_write.append(missing)
	var unknown_source := [1]
	unknown_source.append_array([])
	var stored := [1]
	var holder := [stored]
	var stored_as_key := [1]
	var dictionary := {}
	dictionary[stored_as_key] = 1
	var self_index := [1]
	self_index[self_index] = 2
	var ternary_receiver := [1]
	var other := [2]
	(ternary_receiver if true else other).append("two")
	var cast_receiver := [1]
	(cast_receiver as Array).append("two")
	var subscript_aliased := [1]
	var alias_from_subscript := [subscript_aliased][0]
	alias_from_subscript.append("two")
	var subscript_passed := [1]
	consume([subscript_passed][0])
	var subscript_returned := [1]
	if true:
		return [subscript_returned][0]
	var subscript_iterated := [1]
	for item in [subscript_iterated]:
		item.append("two")
	var self_passed := [1]
	self_passed.append(self_passed)
	var optional := {"one": 1}
	optional.get_or_add("two")
	var wrong_arity := [1]
	wrong_arity.append()
	callback.call([])
	capture.call()
	print(alias)
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, collectionTestEngine(t))
	file := source.File("lifetime.gd")
	intType := Builtin("int")
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, file, "untouched")); !got.Equal(Array(&intType)) {
		t.Fatalf("untouched = %s (%q), want Array[int]", got, got.Reason())
	}
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, file, "shadowed")); !got.Equal(Array(&intType)) {
		t.Fatalf("same-spelled lambda shadow changed outer result to %s (%q)", got, got.Reason())
	}
	for _, name := range []string{"passed", "captured", "aliased", "parameter_target", "stale_element_target", "stale_bulk_target", "stale_indirect_target", "stale_member_target", "stale_call_element_target", "stale_call_bulk_target", "match_bound", "returned", "rebound", "compound", "dynamic", "unknown_write", "unknown_source", "stored", "stored_as_key", "self_index", "ternary_receiver", "cast_receiver", "subscript_aliased", "subscript_passed", "subscript_returned", "subscript_iterated", "self_passed", "wrong_arity"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.CollectionLifetime(collectionDeclaration(t, file, name))
			if !got.Equal(Array(nil)) {
				t.Fatalf("CollectionLifetime(%s) = %s (%q), want generic Array", name, got, got.Reason())
			}
		})
	}
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, file, "optional")); !got.Equal(Dictionary(nil, nil)) {
		t.Fatalf("omitted get_or_add default = %s (%q), want generic Dictionary", got, got.Reason())
	}
}

func TestCollectionLifetimeFailsClosedWhenNestedContentsLeaveDirectly(t *testing.T) {
	source := sources(t, map[string]string{
		"nested.gd": `class_name Nested
func run() -> void:
	var subscript := [[1]]
	var row = subscript[0]
	row.append("two")
	var iterated := [[1]]
	for item in iterated:
		item.append("two")
	var popped := [[1]]
	var popped_row = popped.pop_back()
	popped_row.append("two")
	var mapping := {"one": [1]}
	var value = mapping["one"]
	value.append("two")
	var added := {"one": [1]}
	var added_value = added.get_or_add("two", [2])
	added_value.append("two")
	var sorted := [[1]]
	var comparator := func(left: Array, right: Array) -> bool:
		left.append("two")
		return true
	sorted.sort_custom(comparator)
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, collectionTestEngine(t))
	file := source.File("nested.gd")
	for _, name := range []string{"subscript", "iterated", "popped", "mapping", "added", "sorted"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.CollectionLifetime(collectionDeclaration(t, file, name))
			if got.Kind() == KindArray && got.Equal(Array(nil)) || got.Kind() == KindDictionary && got.Equal(Dictionary(nil, nil)) {
				return
			}
			t.Fatalf("CollectionLifetime(%s) = %s (%q), want generic collection", name, got, got.Reason())
		})
	}
}

func TestCollectionLifetimeFailsClosedForForeignInitialLiteralContents(t *testing.T) {
	source := sources(t, map[string]string{
		"initial.gd": `class_name Initial
var member_source := [1]
func stale_member_element():
	return member_source[1]
func run() -> void:
	var fresh := [[1]]
	var row := [1]
	var array_value := [row]
	var dictionary_value := {"row": row}
	var dictionary_key := {row: 1}
	var member_value := [member_source]
	var scalar_read := [row.size()]
	var scalar_dictionary := {"size": row.size()}
	row.append("two")
	var source := [1]
	source.append("two")
	var direct_subscript := [source[1]]
	var stale_element := source[1]
	var indirect_subscript := [stale_element]
	member_source.append("two")
	var call_subscript := [stale_member_element()]
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, collectionTestEngine(t))
	file := source.File("initial.gd")
	intType := Builtin("int")
	inner := Array(&intType)
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, file, "fresh")); !got.Equal(Array(&inner)) {
		t.Fatalf("fresh = %s (%q), want Array[Array[int]]", got, got.Reason())
	}
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, file, "scalar_read")); !got.Equal(Array(&intType)) {
		t.Fatalf("scalar_read = %s (%q), want Array[int]", got, got.Reason())
	}
	stringType := Builtin("String")
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, file, "scalar_dictionary")); !got.Equal(Dictionary(&stringType, &intType)) {
		t.Fatalf("scalar_dictionary = %s (%q), want Dictionary[String, int]", got, got.Reason())
	}
	for _, name := range []string{"array_value", "member_value", "direct_subscript", "indirect_subscript", "call_subscript"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.CollectionLifetime(collectionDeclaration(t, file, name))
			if !got.Equal(Array(nil)) {
				t.Fatalf("CollectionLifetime(%s) = %s (%q), want generic Array", name, got, got.Reason())
			}
		})
	}
	for _, name := range []string{"dictionary_value", "dictionary_key"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.CollectionLifetime(collectionDeclaration(t, file, name))
			if !got.Equal(Dictionary(nil, nil)) {
				t.Fatalf("CollectionLifetime(%s) = %s (%q), want generic Dictionary", name, got, got.Reason())
			}
		})
	}
}

func TestCollectionLifetimeFailsClosedForSharedDeclarationOwnership(t *testing.T) {
	source := sources(t, map[string]string{
		"shared.gd": `class_name Shared
func first() -> void:
	var values := [1]
func second() -> void:
	pass
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("shared.gd")
	declaration := collectionDeclaration(t, file, "values")
	var second *ast.FunctionDeclaration
	ast.Inspect(file, func(node ast.Node) bool {
		if function, ok := node.(*ast.FunctionDeclaration); ok && function.Name == "second" {
			second = function
		}
		return true
	})
	if second == nil {
		t.Fatal("fixture has no second function")
	}
	second.Body = append([]ast.Statement{declaration}, second.Body...)
	analyzer := NewAnalyzer(source, collectionTestEngine(t))
	if got := analyzer.CollectionLifetime(declaration); !got.Equal(Array(nil)) {
		t.Fatalf("shared declaration = %s (%q), want generic Array", got, got.Reason())
	}
}

func TestCollectionLifetimeDistinguishesInitialUnknownFromIncompleteLifetime(t *testing.T) {
	source := sources(t, map[string]string{
		"closed.gd": `class_name Closed
var member := [1]
func run() -> void:
	var unknown := [missing]
	var local := [1]
`,
		"foreign.gd": `class_name Foreign
func run() -> void:
	var local := [1]
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	analyzer := NewAnalyzer(source, collectionTestEngine(t))
	closed := source.File("closed.gd")
	unknown := analyzer.CollectionLifetime(collectionDeclaration(t, closed, "unknown"))
	if unknown.Kind() != KindUnknown || unknown.Reason() == "" {
		t.Fatalf("initial Unknown = %s (%q), want reasoned Unknown", unknown, unknown.Reason())
	}
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, closed, "member")); !got.Equal(Array(nil)) {
		t.Fatalf("member = %s (%q), want generic Array", got, got.Reason())
	}
	foreign := sources(t, map[string]string{"foreign.gd": `class_name Foreign
func run() -> void:
	var local := [1]
`})
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, foreign.File("foreign.gd"), "local")); !got.Equal(Array(nil)) {
		t.Fatalf("foreign declaration = %s (%q), want generic Array", got, got.Reason())
	}
	local := collectionDeclaration(t, closed, "local")
	want := analyzer.CollectionLifetime(local)
	const readers = 16
	var wait sync.WaitGroup
	results := make(chan Type, readers)
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- analyzer.CollectionLifetime(local)
		}()
	}
	wait.Wait()
	close(results)
	for got := range results {
		if !got.Equal(want) {
			t.Fatalf("concurrent result = %s (%q), want %s", got, got.Reason(), want)
		}
	}
}

func TestCollectionLifetimeRejectsWrongEngineSignatures(t *testing.T) {
	source := sources(t, map[string]string{
		"signature.gd": `class_name Signature
func run() -> void:
	var values := [1]
	values.append(2)
`,
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "float", "String", "bool", "Array", "Dictionary", "Callable", "Variant"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.AddMethod("Array", "append", "Variant", []EngineArgumentSpec{{Type: "String"}}, false, false); err != nil {
		t.Fatal(err)
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	analyzer := NewAnalyzer(source, engine)
	if got := analyzer.CollectionLifetime(collectionDeclaration(t, source.File("signature.gd"), "values")); !got.Equal(Array(nil)) {
		t.Fatalf("wrong append signature = %s (%q), want generic Array", got, got.Reason())
	}
}

func collectionDeclaration(t *testing.T, file *ast.File, name string) *ast.VariableDeclaration {
	t.Helper()
	var found *ast.VariableDeclaration
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.VariableDeclaration)
		if ok && declaration.Name == name {
			if found != nil {
				t.Fatalf("fixture has more than one declaration named %q", name)
			}
			found = declaration
		}
		return true
	})
	if found == nil {
		t.Fatalf("fixture has no declaration named %q", name)
	}
	return found
}

func collectionTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"int", "float", "String", "StringName", "NodePath", "bool", "Array", "Dictionary", "Callable", "Signal", "Variant"} {
		if err := builder.AddBuiltin(builtin); err != nil {
			t.Fatal(err)
		}
	}
	methods := []struct {
		owner, name, result string
		arguments           []EngineArgumentSpec
	}{
		{owner: "Array", name: "append", arguments: collectionArguments("Variant")},
		{owner: "Array", name: "push_back", arguments: collectionArguments("Variant")},
		{owner: "Array", name: "push_front", arguments: collectionArguments("Variant")},
		{owner: "Array", name: "insert", arguments: collectionArguments("int", "Variant")},
		{owner: "Array", name: "set", arguments: collectionArguments("int", "Variant")},
		{owner: "Array", name: "fill", arguments: collectionArguments("Variant")},
		{owner: "Array", name: "append_array", arguments: collectionArguments("Array")},
		{owner: "Array", name: "assign", arguments: collectionArguments("Array")},
		{owner: "Array", name: "clear"},
		{owner: "Array", name: "erase", arguments: collectionArguments("Variant")},
		{owner: "Array", name: "pop_at", arguments: collectionArguments("int")},
		{owner: "Array", name: "pop_back"},
		{owner: "Array", name: "pop_front"},
		{owner: "Array", name: "remove_at", arguments: collectionArguments("int")},
		{owner: "Array", name: "reverse"},
		{owner: "Array", name: "shuffle"},
		{owner: "Array", name: "sort"},
		{owner: "Array", name: "sort_custom", arguments: collectionArguments("Callable")},
		{owner: "Array", name: "size", result: "int"},
		{owner: "Array", name: "make_read_only"},
		{owner: "Dictionary", name: "set", arguments: collectionArguments("Variant", "Variant")},
		{owner: "Dictionary", name: "get_or_add", arguments: []EngineArgumentSpec{{Type: "Variant"}, {Type: "Variant", HasDefault: true}}},
		{owner: "Dictionary", name: "merge", arguments: []EngineArgumentSpec{{Type: "Dictionary"}, {Type: "bool", HasDefault: true}}},
		{owner: "Dictionary", name: "assign", arguments: collectionArguments("Dictionary")},
		{owner: "Dictionary", name: "clear"},
		{owner: "Dictionary", name: "erase", arguments: collectionArguments("Variant")},
		{owner: "Dictionary", name: "sort"},
		{owner: "Dictionary", name: "make_read_only"},
	}
	for _, method := range methods {
		result := method.result
		if result == "" {
			result = "Variant"
		}
		if err := builder.AddMethod(method.owner, method.name, result, method.arguments, false, false); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func collectionArguments(types ...string) []EngineArgumentSpec {
	arguments := make([]EngineArgumentSpec, len(types))
	for index, typeName := range types {
		arguments[index] = EngineArgumentSpec{Type: typeName}
	}
	return arguments
}

func collectionTypePointer(value Type) *Type { return &value }
