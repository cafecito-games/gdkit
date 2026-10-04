package lint

import (
	"fmt"
	"slices"
	"testing"
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

func TestRequireReturnTypeReportsAnUnannotatedFunction(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-return-type", `
func move():
	pass
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 || found[0].Column != 6 {
		t.Errorf("reported at %d:%d, want 2:6 (the function's name)", found[0].Line, found[0].Column)
	}
	if found[0].Message != `Function "move" has no return type` {
		t.Errorf("message = %q", found[0].Message)
	}
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
			if found := lintSourceWithConfig(t, typingConfig(), "require-return-type", source); len(found) != 0 {
				t.Fatalf("got %v, want none", found)
			}
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
	if len(found) != 0 {
		t.Fatalf("got %v, want none", found)
	}
}

func TestRequireArgumentTypeReportsAnUntypedParameter(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
func apply(data, amount: int) -> void:
	print(data, amount)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 || found[0].Column != 12 {
		t.Errorf("reported at %d:%d, want 2:12 (the parameter's name)", found[0].Line, found[0].Column)
	}
	if found[0].Message != `Argument "data" of function "apply" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
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
			if found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", source); len(found) != 0 {
				t.Fatalf("got %v, want none", found)
			}
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
			if len(found) != 1 {
				t.Fatalf("got %v, want one diagnostic", found)
			}
			if found[0].Message != test.message {
				t.Errorf("message = %q, want %q", found[0].Message, test.message)
			}
		})
	}
}

func TestTypingRulesAreInertByDefault(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{"a.gd": "func move():\n\tpass\n"})
	for _, diagnostic := range report.Diagnostics {
		t.Errorf("an inert typing rule fired: %v", diagnostic)
	}
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
	if len(found) != 1 {
		t.Fatalf("got %v, want only the unexempted function", found)
	}
	if found[0].Line != 5 {
		t.Errorf("reported line %d, want 5", found[0].Line)
	}
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
	if len(found) != 1 {
		t.Fatalf("got %v, want only the variable in the unexempted function", found)
	}
	if found[0].Line != 7 {
		t.Errorf("reported line %d, want 7", found[0].Line)
	}
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
	if !slices.Equal(lines, []int{8, 9, 10}) {
		t.Fatalf("reported lines %v, want [8 9 10] (only the unexempted function)", lines)
	}
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
			if len(found) != 1 || found[0].Message != `Argument "event" of lambda has no type` {
				t.Fatalf("got %v, want one lambda diagnostic", found)
			}
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
			if found := lintSourceWithConfig(t, config, "require-argument-type", test.source); len(found) != 0 {
				t.Fatalf("got %v, want none after exempting %q", found, test.exempt)
			}
		})
	}
}

func TestRequireArgumentTypeReportsALocalLambdaOnce(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
func build() -> void:
	var outer := func(first): return func(second): return second
	outer.call(1)
`)
	if len(found) != 2 {
		t.Fatalf("got %v, want one diagnostic per lambda parameter", found)
	}
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
	if len(found) != 2 {
		t.Fatalf("got %v, want the two untyped variables", found)
	}
	if found[0].Line != 2 || found[1].Line != 7 {
		t.Errorf("reported lines %d and %d, want 2 and 7", found[0].Line, found[1].Line)
	}
	if found[0].Message != `Variable "health" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

// An @export is still a Variant; the editor infers the exported type from the
// assigned value, which is not the same as the variable carrying one.
func TestRequireVariableTypeReportsAnUntypedExport(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
@export var speed = 1.0
@export var typed: float = 1.0
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 {
		t.Errorf("reported line %d, want 2", found[0].Line)
	}
}

// GDScript types a const from its value, so it is already statically typed.
func TestRequireVariableTypeIgnoresAConstant(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
const LIMIT = 10

func move() -> void:
	const LOCAL = 2
	print(LIMIT, LOCAL)
`)
	if len(found) != 0 {
		t.Fatalf("got %v, want none", found)
	}
}

func TestRequireTypedCollectionReportsABareAnnotation(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var items: Array = []
var typed: Array[int] = []
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 || found[0].Column != 12 {
		t.Errorf("reported at %d:%d, want 2:12 (the annotation)", found[0].Line, found[0].Column)
	}
	if found[0].Message != "Array has no element type; write Array[T]" {
		t.Errorf("message = %q", found[0].Message)
	}
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
	if len(found) != len(want) {
		t.Fatalf("got %v, want %d diagnostics", found, len(want))
	}
	for index, expected := range want {
		if found[index].Line != expected.line || found[index].Column != expected.column {
			t.Errorf("diagnostic %d reported at %d:%d, want %d:%d (the literal)",
				index, found[index].Line, found[index].Column, expected.line, expected.column)
		}
		if found[index].Message != expected.message {
			t.Errorf("diagnostic %d message = %q, want %q", index, found[index].Message, expected.message)
		}
	}
}

// A written annotation is the whole story when there is one: the declaration is
// one bare collection, so it is reported once, from the annotation.
func TestRequireTypedCollectionReportsAnAnnotatedEmptyLiteralOnce(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var items: Array = []
var lookup: Dictionary = {}
`)
	if len(found) != 2 {
		t.Fatalf("got %v, want one diagnostic per declaration", found)
	}
	if found[0].Line != 2 || found[0].Column != 12 {
		t.Errorf("reported at %d:%d, want 2:12 (the annotation, not the literal)",
			found[0].Line, found[0].Column)
	}
	if found[1].Line != 3 || found[1].Column != 13 {
		t.Errorf("reported at %d:%d, want 3:13 (the annotation, not the literal)",
			found[1].Line, found[1].Column)
	}
}

// Nothing but an empty literal is inferred. A populated literal is an untyped
// collection in Godot too, but choosing its element type is the expression
// inference this package does not have, and a call or a reference says even
// less.
func TestRequireTypedCollectionIgnoresAnythingButAnEmptyLiteral(t *testing.T) {
	sources := map[string]string{
		"populated array":      "var items := [1, 2, 3]\n",
		"populated dictionary": "var lookup := {\"a\": 1}\n",
		"nested empty array":   "var rows := [[]]\n",
		"call":                 "var items := build()\n\nfunc build() -> Array[int]:\n\treturn []\n",
		"reference":            "var source := [1]\nvar items := source\n",
		"other literal":        "var count := 0\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			if found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", source); len(found) != 0 {
				t.Fatalf("got %v, want none", found)
			}
		})
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
		found := lintSourceWithConfig(t, config, "require-typed-collection", source)
		if len(found) != len(test.want) {
			t.Fatalf("godot_version %q: got %v, want %d diagnostics", test.version, found, len(test.want))
		}
		for index, line := range test.want {
			if found[index].Line != line {
				t.Errorf("godot_version %q: diagnostic %d on line %d, want %d", test.version, index, found[index].Line, line)
			}
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
	if len(found) != 2 {
		t.Fatalf("got %v, want both constants", found)
	}
	if found[0].Line != 2 || found[1].Line != 3 {
		t.Errorf("reported lines %d and %d, want 2 and 3", found[0].Line, found[1].Line)
	}
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
	if reported["require-variable-type"] != 1 || reported["require-typed-collection"] != 1 {
		t.Fatalf("got %v, want one diagnostic from each rule", reported)
	}
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
		found := lintSourceWithConfig(t, config, "require-typed-collection", source)
		if len(found) != len(test.want) {
			t.Fatalf("godot_version %q: got %v, want %d diagnostics", test.version, found, len(test.want))
		}
		for index, line := range test.want {
			if found[index].Line != line {
				t.Errorf("godot_version %q: diagnostic %d on line %d, want %d", test.version, index, found[index].Line, line)
			}
		}
	}
}

func TestRequireTypedCollectionChecksSignaturesToo(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func pick(from: Array) -> Array:
	return from
`)
	if len(found) != 2 {
		t.Fatalf("got %v, want the parameter and the return type", found)
	}
}

// A variadic parameter needs no annotation, but it may carry one, and a bare
// Array there is the same defect as anywhere else.
func TestRequireTypedCollectionChecksAVariadicAnnotation(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func gather(...rest: Array) -> void:
	print(rest)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want the variadic annotation", found)
	}
}

func TestRequireSignalArgumentTypeReportsAnUntypedPayload(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-signal-argument-type", `
signal damaged(amount)
signal healed(amount: int)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 || found[0].Column != 16 {
		t.Errorf("reported at %d:%d, want 2:16 (the parameter's name)", found[0].Line, found[0].Column)
	}
	if found[0].Message != `Argument "amount" of signal "damaged" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

// A signal has no enclosing function, so its exempt list matches its own name.
func TestRequireSignalArgumentTypeExemptsBySignalName(t *testing.T) {
	config := typingConfig()
	config.RequireSignalArgumentType = []string{"damaged"}
	found := lintSourceWithConfig(t, config, "require-signal-argument-type", `
signal damaged(amount)
signal healed(amount)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want only the unexempted signal", found)
	}
	if found[0].Line != 3 {
		t.Errorf("reported line %d, want 3", found[0].Line)
	}
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
	if len(found) != 1 {
		t.Fatalf("got %v, want only the unexempted signal", found)
	}
	if found[0].Line != 3 {
		t.Errorf("reported line %d, want 3", found[0].Line)
	}
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
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 4 || found[0].Column != 6 {
		t.Errorf("reported at %d:%d, want 4:6 (the loop variable)", found[0].Line, found[0].Column)
	}
	if found[0].Message != `Loop variable "value" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
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
		config := typingConfig()
		config.GodotVersion = version
		found := lintSourceWithConfig(t, config, "require-typed-loop-variable", source)
		if len(found) != want {
			t.Errorf("godot_version %q: got %v, want %d diagnostics", version, found, want)
		}
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
	if len(found) != 1 {
		t.Fatalf("got %v, want the loop annotation", found)
	}
	if found[0].Line != 3 {
		t.Errorf("reported line %d, want 3", found[0].Line)
	}
}

// A signal parameter's default is code like any other, and gdparser parses one,
// so the collector walks it the way it walks a function's.
func TestRequireArgumentTypeReachesALambdaInASignalDefault(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
signal damaged(handler: Callable = func(event): return event)
`)
	if len(found) != 1 || found[0].Message != `Argument "event" of lambda has no type` {
		t.Fatalf("got %v, want one lambda diagnostic", found)
	}
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
	if len(found) != 1 {
		t.Fatalf("got %v, want only the loop in the unexempted function", found)
	}
	if found[0].Line != 7 {
		t.Errorf("reported line %d, want 7", found[0].Line)
	}
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
	if found := lintSourceWithConfig(t, config, "require-typed-loop-variable", source); len(found) != 1 {
		t.Fatalf("got %v, want the loop no pattern can name", found)
	}
	suppressed := lintSourceWithConfig(t, typingConfig(), "require-typed-loop-variable", `
var setup = func():
	# gdkit:ignore = require-typed-loop-variable
	for value in [1]:
		print(value)
`)
	if len(suppressed) != 0 {
		t.Fatalf("got %v, want none after an ignore comment", suppressed)
	}
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
				"require-typed-collection:4",
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
		report := lintProject(t, config, map[string]string{"a.gd": source})
		// Report.sort() orders by path, line, column and rule. No two findings
		// in this source share a line, so column and rule never tiebreak and
		// the findings arrive in the order the table lists. A second finding on
		// one line would make that order depend on the column.
		var got []string
		for _, diagnostic := range report.Diagnostics {
			got = append(got, fmt.Sprintf("%s:%d", diagnostic.Rule, diagnostic.Line))
		}
		if !slices.Equal(got, test.want) {
			t.Errorf("godot_version %q: unexpected %v, missing %v (got %v, want %v)",
				test.version, absentFrom(got, test.want), absentFrom(test.want, got), got, test.want)
		}
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
