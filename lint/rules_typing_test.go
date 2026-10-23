package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typingConfig enables every typing rule and nothing else. The rules ship
// inert, so a test that does not enable them asserts nothing.
//
// It opts in by name rather than with EnableNewRules, which would also enable
// no-engine-logging and make a fixture holding a print() report a rule this
// group is not about. The names come from typingRuleNames, the same list the
// rules register from, so the helper enables exactly the typing rules and
// nothing a later inert rule happens to be named like.
func typingConfig() Config {
	config := DefaultConfig()
	config.Enable = append(config.Enable, typingRuleNames...)
	return config
}

// collectionFindingsWithoutSemanticSetup exercises the pre-existing syntax
// floors directly. An enabled semantic consumer is intentionally rejected at
// an unsupported engine minor before Lint starts, so this helper isolates the
// per-site floor contract that both empty literals and written annotations
// retain.
func collectionFindingsWithoutSemanticSetup(t *testing.T, config Config, source string) []Diagnostic {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.gd"), []byte(source), 0o644))
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude})
	require.NoError(t, err)
	compiled, err := config.validate()
	require.NoError(t, err)
	return (typingRule{rule: ruleRequireTypedCollection}).Check(&Context{Config: config, compiled: compiled}, snapshot.Scripts["a.gd"])
}

func TestRequireReturnTypeReportsAnUnannotatedFunction(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-return-type", `
func move():
	pass
`)
	require.Len(t, found, 1)
	assert.Equal(t, 2, found[0].Line)
	assert.Equal(t, 6, found[0].Column)
	assert.Equal(t, `Function "move" has no return type`, found[0].Message)
}

// The satisfied cases are asserted as explicitly as the violating one: a rule
// that fires where it should not reaches users as noise.
func TestRequireReturnTypeAcceptsAnAnnotatedFunction(t *testing.T) {
	sources := map[string]string{
		"void":    "func move() -> void:\n\tpass\n",
		"a type":  "func move() -> int:\n\treturn 1\n",
		"Variant": "func move() -> Variant:\n\treturn 1\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			found := lintSourceWithConfig(t, typingConfig(), "require-return-type", source)
			require.Empty(t, found)
		})
	}
}

// A lambda's parameters are a contract its caller satisfies; its return value is
// consumed where the lambda is written, so the return type is not checked.
func TestRequireReturnTypeIgnoresALambda(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-return-type", `
func build() -> void:
	var double := func(value: int): return value * 2
	double.call(1)
`)
	require.Empty(t, found)
}

func TestRequireArgumentTypeReportsAnUntypedParameter(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
func apply(data, amount: int) -> void:
	print(data, amount)
`)
	require.Len(t, found, 1)
	assert.Equal(t, 2, found[0].Line)
	assert.Equal(t, 12, found[0].Column)
	assert.Equal(t, `Argument "data" of function "apply" has no type`, found[0].Message)
}

func TestRequireArgumentTypeAcceptsTypedAndInferredAndVariadic(t *testing.T) {
	sources := map[string]string{
		"annotated": "func apply(data: int) -> void:\n\tprint(data)\n",
		"inferred":  "func apply(data := 0) -> void:\n\tprint(data)\n",
		"Variant":   "func apply(data: Variant) -> void:\n\tprint(data)\n",
		"variadic":  "func apply(...rest) -> void:\n\tprint(rest)\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", source)
			require.Empty(t, found)
		})
	}
}

// A named lambda is named in the diagnostic, because that is the name Godot
// reports in a stack trace and the one a reader will recognize.
func TestRequireArgumentTypeChecksALambdaParameter(t *testing.T) {
	cases := map[string]struct {
		source  string
		message string
	}{
		"anonymous": {
			source:  "func build() -> void:\n\tvar double := func(value): return value * 2\n\tdouble.call(1)\n",
			message: `Argument "value" of lambda has no type`,
		},
		"named": {
			source:  "func build() -> void:\n\tvar named := func helper(value): return value\n\tnamed.call(1)\n",
			message: `Argument "value" of lambda "helper" has no type`,
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", test.source)
			require.Len(t, found, 1)
			assert.Equal(t, test.message, found[0].Message)
		})
	}
}

func TestTypingRulesAreInertByDefault(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{"a.gd": "func move():\n\tpass\n"})
	assert.Empty(t, report.Diagnostics, "an inert typing rule fired")
}

func TestRequireReturnTypeHonorsAnExemptPattern(t *testing.T) {
	config := typingConfig()
	config.RequireReturnType = []string{"_ready"}
	found := lintSourceWithConfig(t, config, "require-return-type", `
func _ready():
	pass

func move():
	pass
`)
	require.Len(t, found, 1)
	assert.Equal(t, 5, found[0].Line)
}

func TestRequireVariableTypeHonorsAnExemptPattern(t *testing.T) {
	config := typingConfig()
	config.RequireVariableType = []string{"_process"}
	found := lintSourceWithConfig(t, config, "require-variable-type", `
func _process(delta: float) -> void:
	var count = delta
	print(count)

func tally() -> void:
	var count = 1
	print(count)
`)
	require.Len(t, found, 1)
	assert.Equal(t, 7, found[0].Line)
}

// A bare collection can be written in three places inside one function — a
// local, the return type, and a loop variable's annotation — and the exempt
// pattern has to reach all three. ["_process"] to quiet a hot loop is the
// motivating case, and each place is carried by its own call site.
func TestRequireTypedCollectionHonorsAnExemptPatternEverywhereInAFunction(t *testing.T) {
	config := typingConfig()
	config.RequireTypedCollection = []string{"_process"}
	found := lintSourceWithConfig(t, config, "require-typed-collection", `
func _process(delta: float) -> Array:
	var bag: Array = [delta]
	for row: Array in bag:
		print(row)
	return bag

func tally() -> Array:
	var bag: Array = []
	for row: Array in bag:
		print(row)
	return bag
`)
	var lines []int
	for _, diagnostic := range found {
		lines = append(lines, diagnostic.Line)
	}
	slices.Sort(lines)
	// The return type, the local, and the loop annotation, all in "tally".
	require.Equal(t, []int{8, 9, 10}, lines)
}

func TestRequireArgumentTypeReachesLambdasOutsideFunctionBodies(t *testing.T) {
	sources := map[string]string{
		"class variable":    "var handler = func(event): return event\n",
		"class constant":    "const HANDLER = func(event): return event\n",
		"inner class":       "class Inner:\n\tvar handler = func(event): return event\n",
		"getter":            "var total: int:\n\tget:\n\t\tvar read := func(event): return event\n\t\treturn read.call(1)\n",
		"setter":            "var total: int:\n\tset(value):\n\t\tvar write := func(event): return event\n\t\ttotal = write.call(value)\n",
		"enum member":       "enum Kind { A = (func(event): return event).call(1) }\n",
		"parameter default": "func apply(callback: Callable = func(event): return event) -> void:\n\tpass\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", source)
			require.Len(t, found, 1)
			require.Equal(t, `Argument "event" of lambda has no type`, found[0].Message)
		})
	}
}

// An accessor body belongs to its property, so that is the name an exempt
// pattern matches; a function's parameter default belongs to the function.
func TestRequireArgumentTypeNamesTheEnclosingDeclarationForExemption(t *testing.T) {
	sources := map[string]struct{ source, exempt string }{
		"getter":            {"var total: int:\n\tget:\n\t\tvar read := func(event): return event\n\t\treturn read.call(1)\n", "total"},
		"setter":            {"var total: int:\n\tset(value):\n\t\tvar write := func(event): return event\n\t\ttotal = write.call(value)\n", "total"},
		"parameter default": {"func apply(callback: Callable = func(event): return event) -> void:\n\tpass\n", "apply"},
	}
	for name, test := range sources {
		t.Run(name, func(t *testing.T) {
			config := typingConfig()
			config.RequireArgumentType = []string{test.exempt}
			found := lintSourceWithConfig(t, config, "require-argument-type", test.source)
			require.Empty(t, found)
		})
	}
}

func TestRequireArgumentTypeReportsALocalLambdaOnce(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
func build() -> void:
	var outer := func(first): return func(second): return second
	outer.call(1)
`)
	require.Len(t, found, 2)
}

func TestRequireVariableTypeReportsAtBothScopes(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
var health = 100
var typed: int = 100
const LIMIT = 10

func move() -> void:
	var speed = 1.0
	var inferred := 1.0
	print(health, typed, LIMIT, speed, inferred)
`)
	require.Len(t, found, 2)
	assert.Equal(t, 2, found[0].Line)
	assert.Equal(t, 7, found[1].Line)
	assert.Equal(t, `Variable "health" has no type`, found[0].Message)
}

// An @export is still a Variant; the editor infers the exported type from the
// assigned value, which is not the same as the variable carrying one.
func TestRequireVariableTypeReportsAnUntypedExport(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
@export var speed = 1.0
@export var typed: float = 1.0
`)
	require.Len(t, found, 1)
	assert.Equal(t, 2, found[0].Line)
}

// GDScript types a const from its value, so it is already statically typed.
func TestRequireVariableTypeIgnoresAConstant(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
const LIMIT = 10

func move() -> void:
	const LOCAL = 2
	print(LIMIT, LOCAL)
`)
	require.Empty(t, found)
}

func TestRequireTypedCollectionReportsABareAnnotation(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var items: Array = []
var typed: Array[int] = []
`)
	require.Len(t, found, 1)
	assert.Equal(t, 2, found[0].Line)
	assert.Equal(t, 12, found[0].Column)
	assert.Equal(t, "Array has no element type; write Array[T]", found[0].Message)
}

// An empty literal with no written type infers the same untyped collection a
// bare annotation declares, at both scopes, and the finding underlines the
// literal because that is what the reader has to change.
func TestRequireTypedCollectionReportsAnEmptyLiteral(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var pending := []
var cache := {}
var typed: Array[int] = []

func collect() -> void:
	var seen := {}
	print(pending, cache, typed, seen)
`)
	want := []struct {
		line    int
		column  int
		message string
	}{
		{line: 2, column: 16, message: "Array has no element type; write Array[T]"},
		{line: 3, column: 14, message: "Dictionary has no element type; write Dictionary[K, V]"},
		{line: 7, column: 14, message: "Dictionary has no element type; write Dictionary[K, V]"},
	}
	require.Len(t, found, len(want))
	for index, expected := range want {
		assert.Equal(t, expected.line, found[index].Line)
		assert.Equal(t, expected.column, found[index].Column)
		assert.Equal(t, expected.message, found[index].Message)
	}
}

// A written annotation is the whole story when there is one: the declaration is
// one bare collection, so it is reported once, from the annotation.
func TestRequireTypedCollectionReportsAnAnnotatedEmptyLiteralOnce(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var items: Array = []
var lookup: Dictionary = {}
`)
	require.Len(t, found, 2)
	assert.Equal(t, 2, found[0].Line)
	assert.Equal(t, 12, found[0].Column)
	assert.Equal(t, 3, found[1].Line)
	assert.Equal(t, 13, found[1].Column)
}

// Direct populated literals have one source expression the rule can ask the
// analyzer about. Calls and references remain out of scope: their values are
// not a literal the author can annotate directly.
func TestRequireTypedCollectionLeavesNonLiteralInitializersQuiet(t *testing.T) {
	sources := map[string]string{
		"call":          "var items := build()\n\nfunc build() -> Array[int]:\n\treturn []\n",
		"reference":     "var source: Array[int] = [1]\nvar items := source\n",
		"other literal": "var count := 0\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", source)
			require.Empty(t, found)
		})
	}
}

func TestRequireTypedCollectionUsesGenericFallbackForKnownIncompleteLiterals(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var mixed := [1, "two"]
var lookup := {"one": 1, "two": "two"}
var rows := [[]]
var matrix := [[1], [2]]
`)
	want := []struct {
		line    int
		message string
	}{
		{line: 2, message: "Array has no element type; write Array[T]"},
		{line: 3, message: "Dictionary has no element type; write Dictionary[K, V]"},
		{line: 4, message: "Array has no element type; write Array[T]"},
		{line: 5, message: "Array has no element type; write Array[T]"},
	}
	require.Len(t, found, len(want))
	for index, expected := range want {
		assert.Equal(t, expected.line, found[index].Line)
		assert.Equal(t, expected.message, found[index].Message)
		assert.NotContains(t, found[index].Message, "Variant")
		assert.NotContains(t, found[index].Message, "unknown:")
	}
}

func TestRequireTypedCollectionFallsBackToGenericForUnreducibleLiterals(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func build() -> void:
	var items := [not_declared]
	var lookup := {"key": also_not_declared}
	var nested := [[still_not_declared]]
`)
	want := []struct {
		line    int
		message string
	}{
		{line: 3, message: "Array has no element type; write Array[T]"},
		{line: 4, message: "Dictionary has no element type; write Dictionary[K, V]"},
		{line: 5, message: "Array has no element type; write Array[T]"},
	}
	require.Len(t, found, len(want))
	for index, expected := range want {
		assert.Equal(t, expected.line, found[index].Line)
		assert.Equal(t, expected.message, found[index].Message)
	}
}

// An inferred populated literal whose element type has no writable spelling
// still reports the generic wording, exactly as the annotated form does: the
// annotation only decides whether an exact spelling can be proposed, never
// whether the collection is untyped.
func TestRequireTypedCollectionFallsBackForEnumKeyedAndBuiltinValuedLiterals(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
enum Mode { ALPHA, BETA }

const ENUM_KEYED := {
	Mode.ALPHA: "alpha",
	Mode.BETA: "beta",
}

const BUILTIN_VALUE := { "origin": Vector2i(0, 0) }
`)
	want := []struct {
		line    int
		message string
	}{
		{line: 4, message: "Dictionary has no element type; write Dictionary[K, V]"},
		{line: 9, message: "Dictionary has no element type; write Dictionary[K, V]"},
	}
	require.Len(t, found, len(want))
	for index, expected := range want {
		assert.Equal(t, expected.line, found[index].Line)
		assert.Equal(t, expected.message, found[index].Message)
	}
}

func TestRequireTypedCollectionDoesNotDuplicateWrittenPopulatedAnnotations(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var items: Array = [1, 2, 3]
var lookup: Dictionary = {"a": 1}
`)
	require.Len(t, found, 2)
	for index, wantColumn := range []int{12, 13} {
		assert.Equal(t, wantColumn, found[index].Column)
	}
}

func TestCollectionTypeMessageFailsClosedAndNeverLeaksUnsafeNames(t *testing.T) {
	engine := selectedCollectionEngine(t)
	intType := semantic.Builtin("int")
	unknown := semantic.Unknown("unresolved value")
	localClass := semantic.Class("LocalInventory", nil, false)
	enum := semantic.Enum("InventoryKind")

	tests := []struct {
		name      string
		typeValue semantic.Type
		message   string
		reported  bool
	}{
		{
			name:      "nested array is not source writable",
			typeValue: semantic.Array(typePointer(semantic.Array(&intType))),
			message:   "Array has no element type; write Array[T]",
			reported:  true,
		},
		{
			name:      "untyped heterogeneous array",
			typeValue: semantic.Array(nil),
			message:   "Array has no element type; write Array[T]",
			reported:  true,
		},
		{
			name:      "unknown component",
			typeValue: semantic.Array(&unknown),
			reported:  false,
		},
		{
			name:      "unknown nested component",
			typeValue: semantic.Array(typePointer(semantic.Array(&unknown))),
			reported:  false,
		},
		{
			name:      "user class component",
			typeValue: semantic.Array(&localClass),
			message:   "Array has no element type; write Array[T]",
			reported:  true,
		},
		{
			name:      "enum dictionary component",
			typeValue: semantic.Dictionary(&enum, &intType),
			message:   "Dictionary has no element type; write Dictionary[K, V]",
			reported:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message, reported := collectionTypeMessage(engine, test.typeValue)
			require.Equal(t, test.reported, reported)
			require.Equal(t, test.message, message)
			assert.NotContains(t, message, "LocalInventory")
			assert.NotContains(t, message, "InventoryKind")
		})
	}
}

func TestSourceWritableTypeAcceptsOnlySelectedVocabulary(t *testing.T) {
	engine := selectedCollectionEngine(t)
	intType := semantic.Builtin("int")
	node := engine.Class("Node")
	unknown := semantic.Unknown("unresolved value")
	localClass := semantic.Class("LocalInventory", nil, false)
	metaNode := semantic.Class("Node", nil, true)
	enum := semantic.Enum("InventoryKind")

	tests := []struct {
		name      string
		typeValue semantic.Type
		spelling  string
		status    sourceTypeStatus
	}{
		{name: "selected builtin", typeValue: intType, spelling: "int", status: sourceTypeWritable},
		{name: "Variant", typeValue: semantic.Variant(), spelling: "Variant", status: sourceTypeWritable},
		{name: "Callable", typeValue: semantic.Callable(), spelling: "Callable", status: sourceTypeWritable},
		{name: "Signal", typeValue: semantic.Signal(), spelling: "Signal", status: sourceTypeWritable},
		{name: "selected engine class", typeValue: node, spelling: "Node", status: sourceTypeWritable},
		{name: "nested typed collection", typeValue: semantic.Dictionary(&intType, typePointer(semantic.Array(&node))), status: sourceTypeUnwritable},
		{name: "unknown", typeValue: unknown, status: sourceTypeUnknown},
		{name: "unknown nested component", typeValue: semantic.Array(&unknown), status: sourceTypeUnknown},
		{name: "untyped nested container", typeValue: semantic.Array(typePointer(semantic.Array(nil))), status: sourceTypeUnwritable},
		{name: "user class", typeValue: localClass, status: sourceTypeUnwritable},
		{name: "meta class", typeValue: metaNode, status: sourceTypeUnwritable},
		{name: "enum", typeValue: enum, status: sourceTypeUnwritable},
		{name: "void", typeValue: semantic.Void(), status: sourceTypeUnwritable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spelling, status := sourceWritableType(engine, test.typeValue)
			require.Equal(t, test.spelling, spelling)
			require.Equal(t, test.status, status)
		})
	}
}

func TestCollectionTypeMessageDoesNotSuggestNestedDictionary(t *testing.T) {
	engine := selectedCollectionEngine(t)
	key, value := semantic.Builtin("String"), semantic.Builtin("int")
	dictionary := semantic.Dictionary(&key, &value)
	collection := semantic.Array(&dictionary)
	message, reported := collectionTypeMessage(engine, collection)
	require.True(t, reported)
	require.Equal(t, "Array has no element type; write Array[T]", message)
}

func selectedCollectionEngine(t *testing.T) *semantic.Engine {
	t.Helper()
	config := DefaultConfig()
	config.Enable = []string{ruleRequireTypedCollection}
	linter, err := newLinter(config, []Rule{typingRule{rule: ruleRequireTypedCollection}})
	require.NoError(t, err)
	engine := linter.context.Engine()
	require.NotNil(t, engine)
	return engine
}

func typePointer(value semantic.Type) *semantic.Type { return &value }

func TestRequireTypedCollectionUsesGenericPromptsForClassMembers(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var items := [1, 2, 3]
var lookup := {"a": 1}
var variants := [null, null]
`)
	want := []struct {
		line    int
		column  int
		message string
	}{
		{line: 2, column: 14, message: "Array has no element type; write Array[T]"},
		{line: 3, column: 15, message: "Dictionary has no element type; write Dictionary[K, V]"},
		{line: 4, column: 17, message: "Array has no element type; write Array[T]"},
	}
	require.Len(t, found, len(want))
	for index, expected := range want {
		assert.Equal(t, expected.line, found[index].Line)
		assert.Equal(t, expected.column, found[index].Column)
		assert.Equal(t, expected.line, found[index].EndLine)
		assert.False(t, found[index].EndColumn <= expected.column)
		assert.Equal(t, expected.message, found[index].Message)
	}
}

func TestRequireTypedCollectionAccountsForCompleteLocalLifetime(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func build(flag: bool) -> void:
	var untouched := [1, 2, 3]
	var widened := {"game_server_id": "server", "name": "character"}
	if flag:
		widened["appearance"] = {"avatar_id": "one"}
	var escaped := [1]
	consume(escaped)
`)
	want := []struct {
		line    int
		message string
	}{
		{line: 3, message: "Array has no element type; write Array[int]"},
		{line: 4, message: "Dictionary has no element type; write Dictionary[String, Variant]"},
		{line: 7, message: "Array has no element type; write Array[T]"},
	}
	require.Len(t, found, len(want))
	for index, expected := range want {
		assert.Equal(t, expected.line, found[index].Line)
		assert.Equal(t, expected.message, found[index].Message)
	}
}

// Exercising every retained mutation against the selected embedded 4.7 engine
// pins the semantic table to the real method owner, arity, defaults, static,
// and vararg metadata rather than to a test-only reconstruction.
func TestRequireTypedCollectionValidatesClosedVocabularyAgainstSelectedEngine(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func build() -> void:
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
	dictionary.set("two", 2.5)
	dictionary.get_or_add("three", 3)
	dictionary.merge({"four": 4})
	dictionary.assign({"five": 5})
	dictionary.clear()
	dictionary.erase("five")
	dictionary.sort()
	dictionary.make_read_only()
`)
	want := []string{
		"Array has no element type; write Array[float]",
		"Dictionary has no element type; write Dictionary[String, float]",
	}
	require.Len(t, found, len(want))
	for index, message := range want {
		assert.Equal(t, message, found[index].Message)
	}
}

// An inferred empty dictionary inherits the same version gate a written bare
// Dictionary has: below 4.4 there is no Dictionary[K, V] to suggest, so the
// finding is dropped silently.
func TestRequireTypedCollectionGatesAnEmptyDictionaryLiteral(t *testing.T) {
	source := `
var pending := []
var cache := {}
`
	tests := []struct {
		version string
		want    []int
	}{
		{version: "4.3", want: []int{2}},
		{version: "4.4", want: []int{2, 3}},
	}
	for _, test := range tests {
		config := typingConfig()
		config.GodotVersion = test.version
		found := collectionFindingsWithoutSemanticSetup(t, config, source)
		require.Len(t, found, len(test.want), "godot_version %q", test.version)
		for index, line := range test.want {
			assert.Equal(t, line, found[index].Line, "godot_version %q", test.version)
		}
	}
}

// A constant's collection is as untyped as a variable's, and a written "const
// ITEMS: Array" is already reported, so the inferred spelling is too. The
// element type is what is missing, which typing the const from its value does
// not supply.
func TestRequireTypedCollectionReportsAConstantsEmptyLiteral(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
const WRITTEN: Array = []
const INFERRED := []
`)
	require.Len(t, found, 2)
	assert.Equal(t, 2, found[0].Line)
	assert.Equal(t, 3, found[1].Line)
}

// A declaration with neither a type nor ":=" is missing both a type and an
// element type, and the two rules report that independently: the fix for the
// first, "var items: Array = []", still leaves the collection untyped.
func TestRequireTypedCollectionAndVariableTypeBothReportABareAssignment(t *testing.T) {
	config := typingConfig()
	reported := map[string]int{}
	for _, rule := range []string{"require-variable-type", "require-typed-collection"} {
		reported[rule] = len(lintSourceWithConfig(t, config, rule, "var items = []\n"))
	}
	require.Equal(t, 1, reported["require-variable-type"])
	require.Equal(t, 1, reported["require-typed-collection"])
}

// This is the only rule in gdkit whose applicability varies per finding:
// Array[T] is Godot 4.0 and Dictionary[K, V] is 4.4, so one engine version
// accepts the fix for one and not the other.
func TestRequireTypedCollectionGatesDictionarySeparately(t *testing.T) {
	source := `
var items: Array = []
var lookup: Dictionary = {}
`
	tests := []struct {
		version string
		want    []int
	}{
		{version: "4.3", want: []int{2}},
		{version: "4.4", want: []int{2, 3}},
	}
	for _, test := range tests {
		config := typingConfig()
		config.GodotVersion = test.version
		found := collectionFindingsWithoutSemanticSetup(t, config, source)
		require.Len(t, found, len(test.want), "godot_version %q", test.version)
		for index, line := range test.want {
			assert.Equal(t, line, found[index].Line, "godot_version %q", test.version)
		}
	}
}

func TestRequireTypedCollectionChecksSignaturesToo(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func pick(from: Array) -> Array:
	return from
`)
	require.Len(t, found, 2)
}

// A variadic parameter needs no annotation, but it may carry one, and a bare
// Array there is the same defect as anywhere else.
func TestRequireTypedCollectionChecksAVariadicAnnotation(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func gather(...rest: Array) -> void:
	print(rest)
`)
	require.Len(t, found, 1)
}

func TestRequireSignalArgumentTypeReportsAnUntypedPayload(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-signal-argument-type", `
signal damaged(amount)
signal healed(amount: int)
`)
	require.Len(t, found, 1)
	assert.Equal(t, 2, found[0].Line)
	assert.Equal(t, 16, found[0].Column)
	assert.Equal(t, `Argument "amount" of signal "damaged" has no type`, found[0].Message)
}

// A signal has no enclosing function, so its exempt list matches its own name.
func TestRequireSignalArgumentTypeExemptsBySignalName(t *testing.T) {
	config := typingConfig()
	config.RequireSignalArgumentType = []string{"damaged"}
	found := lintSourceWithConfig(t, config, "require-signal-argument-type", `
signal damaged(amount)
signal healed(amount)
`)
	require.Len(t, found, 1)
	assert.Equal(t, 3, found[0].Line)
}

// A bare collection in a signal's payload is named for the signal too, because
// the payload shares the parameter walk with a function's arguments.
func TestRequireTypedCollectionNamesTheSignalForAPayload(t *testing.T) {
	config := typingConfig()
	config.RequireTypedCollection = []string{"damaged"}
	found := lintSourceWithConfig(t, config, "require-typed-collection", `
signal damaged(amounts: Array)
signal healed(amounts: Array)
`)
	require.Len(t, found, 1)
	assert.Equal(t, 3, found[0].Line)
}

func TestRequireTypedLoopVariableReportsAnUntypedHeader(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-loop-variable", `
func tally(items: Array[int]) -> int:
	var total := 0
	for value in items:
		total += value
	for typed: int in items:
		total += typed
	return total
`)
	require.Len(t, found, 1)
	assert.Equal(t, 4, found[0].Line)
	assert.Equal(t, 6, found[0].Column)
	assert.Equal(t, `Loop variable "value" has no type`, found[0].Message)
}

// A typed loop variable does not exist before Godot 4.2, so on an older engine
// there is no fix to recommend and the rule says nothing.
func TestRequireTypedLoopVariableIsGatedAt42(t *testing.T) {
	source := `
func tally(items: Array[int]) -> int:
	var total := 0
	for value in items:
		total += value
	return total
`
	for version, want := range map[string]int{"4.1": 0, "4.2": 1} {
		config := DefaultConfig()
		config.Enable = []string{ruleRequireTypedLoopVariable}
		config.GodotVersion = version
		found := lintSourceWithConfig(t, config, "require-typed-loop-variable", source)
		assert.Len(t, found, want, "godot_version %q", version)
	}
}

// The loop variable's own annotation is checked for a bare collection, like any
// other annotation the walk reaches.
func TestRequireTypedCollectionChecksALoopAnnotation(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func tally(rows: Array[Array]) -> void:
	for row: Array in rows:
		print(row)
`)
	require.Len(t, found, 1)
	assert.Equal(t, 3, found[0].Line)
}

// A signal parameter's default is code like any other, and gdparser parses one,
// so the collector walks it the way it walks a function's.
func TestRequireArgumentTypeReachesALambdaInASignalDefault(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
signal damaged(handler: Callable = func(event): return event)
`)
	require.Len(t, found, 1)
	require.Equal(t, `Argument "event" of lambda has no type`, found[0].Message)
}

// A loop inside a function is named for that function, which is what makes
// ["_process"] quiet a hot loop's locals without quieting the file.
func TestRequireTypedLoopVariableHonorsAnExemptPattern(t *testing.T) {
	config := typingConfig()
	config.RequireTypedLoopVariable = []string{"_process"}
	found := lintSourceWithConfig(t, config, "require-typed-loop-variable", `
func _process(delta: float) -> void:
	for value in [delta]:
		print(value)

func tally() -> void:
	for value in [1]:
		print(value)
`)
	require.Len(t, found, 1)
	assert.Equal(t, 7, found[0].Line)
}

// A loop inside a class-scope initializer has no enclosing function, so no
// pattern can exempt it and a "# gdkit:ignore" comment is the only way to
// silence it.
func TestRequireTypedLoopVariableInAClassInitializerIsNotExemptible(t *testing.T) {
	source := `
var setup = func():
	for value in [1]:
		print(value)
`
	config := typingConfig()
	config.RequireTypedLoopVariable = []string{"*"}
	found := lintSourceWithConfig(t, config, "require-typed-loop-variable", source)
	require.Len(t, found, 1)
	suppressed := lintSourceWithConfig(t, typingConfig(), "require-typed-loop-variable", `
var setup = func():
	# gdkit:ignore = require-typed-loop-variable
	for value in [1]:
		print(value)
`)
	require.Empty(t, suppressed)
}

// The version gate's job is to make a rule report nothing, which is exactly
// what a rule whose traversal is broken does. Asserting both sides of the gate
// on one source is what tells the two apart: every rule must report something
// on the newest engine, and only the ungated ones on the oldest.
//
// require-typed-loop-variable is the only rule gated as a whole, so it is the
// only rule missing from the older engine's findings. A typed signal parameter
// is Godot 4.0 syntax like any other annotation, so require-signal-argument-type
// fires on both. The bare Dictionary is the third difference: it is a site of a
// rule that reports on both engines, which is why the findings are compared by
// rule and line rather than by rule alone.
//
// Comparing by line pins that the Dictionary is gated and the Array is not. It
// does not pin where the Dictionary's boundary sits: moving it to 4.2 leaves
// this test green, because both versions in the table fall on the same side of
// it. TestRequireTypedCollectionGatesDictionarySeparately is what holds it at
// 4.4.
//
// The report is not filtered to the typing rules. DefaultConfig() reports
// nothing on this source, so the whole report is the typing rules' findings,
// and an unrelated rule that starts firing here is worth a failure rather than
// being hidden.
func TestTypingVersionGateSilencesOnlyTheGatedRules(t *testing.T) {
	source := `
signal damaged(amount)

var items: Array = []
var lookup: Dictionary = {}

func reset():
	items.clear()

func tally(start) -> int:
	var total = start
	for value in items:
		total += value
	return total + lookup.size()
`
	tests := []struct {
		version string
		want    []string
	}{
		{
			version: "4.0",
			want: []string{
				"require-signal-argument-type:2",
				"require-return-type:7",
				"require-argument-type:10",
				"require-variable-type:11",
			},
		},
		{
			version: "4.7",
			want: []string{
				"require-signal-argument-type:2",
				"require-typed-collection:4",
				"require-typed-collection:5",
				"require-return-type:7",
				"require-argument-type:10",
				"require-variable-type:11",
				"require-typed-loop-variable:12",
			},
		},
	}
	for _, test := range tests {
		config := typingConfig()
		config.GodotVersion = test.version
		if test.version == "4.0" {
			// The populated-literal consumer needs a selected semantic schema,
			// which gdkit deliberately does not borrow for 4.0. Disabling it
			// leaves this test focused on the older syntactic rules.
			config.Disable = append(config.Disable, ruleRequireTypedCollection)
		}
		report := lintProject(t, config, map[string]string{"a.gd": source})
		// Report.sort() orders by path, line, column and rule. No two findings
		// in this source share a line, so column and rule never tiebreak and
		// the findings arrive in the order the table lists. A second finding on
		// one line would make that order depend on the column.
		var got []string
		for _, diagnostic := range report.Diagnostics {
			got = append(got, fmt.Sprintf("%s:%d", diagnostic.Rule, diagnostic.Line))
		}
		assert.Equal(t, test.want, got, "godot_version %q: unexpected %v, missing %v",
			test.version, absentFrom(got, test.want), absentFrom(test.want, got))
	}
}

// absentFrom returns the entries of first that second does not hold, so a
// failing gate names the finding it gained or lost instead of leaving the
// reader to diff two lists.
func absentFrom(first, second []string) []string {
	var found []string
	for _, entry := range first {
		if !slices.Contains(second, entry) {
			found = append(found, entry)
		}
	}
	return found
}
