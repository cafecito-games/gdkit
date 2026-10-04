package lint

import (
	"strings"
	"testing"
)

// typingConfig enables every typing rule and nothing else. The rules ship
// inert, so a test that does not enable them asserts nothing.
//
// It opts in by name rather than with EnableNewRules, which would also enable
// no-engine-logging and make a fixture holding a print() report a rule this
// group is not about. Deriving the names from PendingRuleNames keeps the helper
// correct as each task registers another rule.
func typingConfig() Config {
	config := DefaultConfig()
	for _, name := range PendingRuleNames() {
		if strings.HasPrefix(name, "require-") {
			config.Enable = append(config.Enable, name)
		}
	}
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

func TestRequireArgumentTypeChecksALambdaParameter(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
func build() -> void:
	var double := func(value): return value * 2
	double.call(1)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Message != `Argument "value" of lambda has no type` {
		t.Errorf("message = %q", found[0].Message)
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
