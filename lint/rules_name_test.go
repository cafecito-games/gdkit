package lint

import "testing"

// Every expectation below was confirmed against gdlint from
// godot-gdscript-toolkit.
func TestNameRulesFireOnBadNames(t *testing.T) {
	tests := []struct {
		rule    string
		source  string
		message string
		lines   []int
	}{
		{"function-name", "func doThing():\n\tpass\n", `Function name "doThing" is not valid`, []int{1}},
		{"class-name", "class_name my_class\n", `Class name "my_class" is not valid`, []int{1}},
		{"sub-class-name", "class my_inner:\n\tpass\n", `Class name "my_inner" is not valid`, []int{1}},
		{"signal-name", "signal Bad_Signal(Arg_X)\n", `Signal name "Bad_Signal" is not valid`, []int{1}},
		{"class-variable-name", "var BadVar = 1\n", `Class-scope variable name "BadVar" is not valid`, []int{1}},
		{"class-load-variable-name", "var bad_Load = load(\"res://a.gd\")\n", `Class-scope load/preload variable name "bad_Load" is not valid`, []int{1}},
		{"function-variable-name", "func f():\n\tvar Bad = 1\n", `Function-scope variable name "Bad" is not valid`, []int{2}},
		{"function-preload-variable-name", "func f():\n\tvar bad_pre = preload(\"res://a.gd\")\n", `Function-scope preload variable name "bad_pre" is not valid`, []int{2}},
		{"function-argument-name", "func f(Bad):\n\tpass\n", `Function argument name "Bad" is not valid`, []int{1}},
		{"loop-variable-name", "func f():\n\tfor I in range(3):\n\t\tpass\n", `Loop variable name "I" is not valid`, []int{2}},
		{"enum-name", "enum bad_enum { A }\n", `Enum name "bad_enum" is not valid`, []int{1}},
		{"enum-element-name", "enum E { a }\n", `Enum element name "a" is not valid`, []int{1}},
		{"constant-name", "const bad = 1\n", `Constant name "bad" is not valid`, []int{1}},
		{"load-constant-name", "const loadBad = preload(\"res://a.gd\")\n", `Constant (load/preload) name "loadBad" is not valid`, []int{1}},
	}
	for _, test := range tests {
		t.Run(test.rule, func(t *testing.T) {
			assertRule(t, test.rule, test.source, test.lines...)
			found := lintSource(t, test.rule, test.source)
			if found[0].Message != test.message {
				t.Fatalf("message = %q, want %q", found[0].Message, test.message)
			}
		})
	}
}

func TestNameRulesIgnoreGoodNames(t *testing.T) {
	source := `class_name PlayerController
extends Node

signal health_changed(old_value, new_value: int)

enum Direction { UP, DOWN_LEFT = 2 }
enum { ANONYMOUS_MEMBER }

const MAX_HEALTH = 100
const _PRIVATE_LIMIT := 5
const Scene = preload("res://scene.tscn")
const LOADED_ALSO = load("res://other.tres")

var health = 3
var _hidden: int = 2
var Loaded = load("res://a.gd")
var loaded_snake = preload("res://a.gd")
var wrapped = load("res://a.gd") as Resource

var watched: int:
	set(NewValue):
		watched = NewValue
	get:
		var cached = watched
		return cached

class _InnerHelper:
	var inner_value = 1
	func inner_method(inner_argument):
		var inner_local = inner_argument
		return inner_local

func _on_Button_pressed():
	pass

func _on_Button_pressed_now():
	pass

func do_thing(first_argument, second_argument: int = 2):
	var local_value = 1
	var Preloaded = preload("res://a.gd")
	var Skipped = load("res://b.gd")
	var skipped_too = load("res://b.gd")
	const LOCAL_LIMIT = 3
	for index in range(local_value):
		pass
	for typed_index: int in range(3):
		pass
	var callback = func(lambda_argument): return lambda_argument
	return callback

@abstract func AbstractName(abstract_argument)
`
	for rule := range nameMessages {
		assertNoRule(t, rule, source)
	}
}

func TestNameRulesFollowGdlintScopeQuirks(t *testing.T) {
	t.Run("a local load variable is governed by no rule", func(t *testing.T) {
		source := "func f():\n\tvar Bad_Name = load(\"res://a.gd\")\n"
		assertNoRule(t, "function-variable-name", source)
		assertNoRule(t, "function-preload-variable-name", source)
	})
	t.Run("a local variable named by preload uses the preload pattern", func(t *testing.T) {
		assertRule(t, "function-preload-variable-name", "func f():\n\tvar bad_pre := preload(\"res://a.gd\")\n", 2)
	})
	t.Run("a cast hides the load from the load-aware rules", func(t *testing.T) {
		source := "var Good_Cast = load(\"res://a.gd\") as Node\nvar BadChain = load(\"res://a.gd\").new()\n"
		assertRule(t, "class-variable-name", source, 1, 2)
		assertNoRule(t, "class-load-variable-name", source)
	})
	t.Run("parentheses and await hide the load", func(t *testing.T) {
		source := "var Pa = (preload(\"res://a.gd\"))\nvar Aw = await load(\"res://a.gd\")\n"
		assertRule(t, "class-variable-name", source, 1, 2)
	})
	t.Run("a method named like load is not a load", func(t *testing.T) {
		assertRule(t, "class-variable-name", "var Bad = loader.load(\"res://a.gd\")\n", 1)
	})
	t.Run("class_name combined with extends is not checked", func(t *testing.T) {
		assertNoRule(t, "class-name", "class_name my_class extends Node\n")
	})
	t.Run("class_name on its own line is checked even with extends after", func(t *testing.T) {
		assertRule(t, "class-name", "class_name my_class\nextends Node\n", 1)
	})
	t.Run("an abstract function name is not checked, its arguments are", func(t *testing.T) {
		source := "@abstract func Bad(Bad_Arg)\n"
		assertNoRule(t, "function-name", source)
		assertRule(t, "function-argument-name", source, 1)
	})
	t.Run("signal parameters are not checked", func(t *testing.T) {
		assertNoRule(t, "function-argument-name", "signal changed(Bad_Arg)\n")
	})
	t.Run("setter parameters are not checked", func(t *testing.T) {
		assertNoRule(t, "function-argument-name", "var x: int:\n\tset(Value):\n\t\tpass\n")
	})
}

func TestNameRulesCoverNestedScopes(t *testing.T) {
	t.Run("inner classes use class and function rules", func(t *testing.T) {
		source := "class Inner:\n\tvar Bad = 1\n\tfunc Bad_Fn():\n\t\tvar Local = 1\n"
		assertRule(t, "class-variable-name", source, 2)
		assertRule(t, "function-name", source, 3)
		assertRule(t, "function-variable-name", source, 4)
	})
	t.Run("classes nested in classes are visited", func(t *testing.T) {
		source := "class Outer:\n\tclass bad_inner:\n\t\tpass\n"
		assertRule(t, "sub-class-name", source, 2)
	})
	t.Run("lambda parameters and locals are checked", func(t *testing.T) {
		source := "func f():\n\tvar callback = func(Lam, Lam2: int):\n\t\tvar Inside = 1\n\t\treturn Inside\n"
		assertRule(t, "function-argument-name", source, 2, 2)
		assertRule(t, "function-variable-name", source, 3)
	})
	t.Run("a class-level lambda has function-scope locals", func(t *testing.T) {
		source := "var callback = func(Lam):\n\tvar Inside = 1\n\treturn Inside\n"
		assertRule(t, "function-argument-name", source, 1)
		assertRule(t, "function-variable-name", source, 2)
		assertRule(t, "class-variable-name", "var callback = func():\n\tvar Inside = 1\n")
	})
	t.Run("property accessor bodies are function scope", func(t *testing.T) {
		source := "var x: int:\n\tget:\n\t\tvar Bad = 1\n\t\treturn Bad\n"
		assertRule(t, "function-variable-name", source, 3)
	})
	t.Run("local constants use the constant rules", func(t *testing.T) {
		source := "func f():\n\tconst Bad = 1\n\tconst loadBad = load(\"res://a.gd\")\n"
		assertRule(t, "constant-name", source, 2)
		assertRule(t, "load-constant-name", source, 3)
	})
	t.Run("every typed and inferred form is checked", func(t *testing.T) {
		source := "func f(Bad, Bad2: int = 1, Bad3 := 2):\n\tvar Bad4: int\n\tvar Bad5 := 2\n\tfor J: int in range(3):\n\t\tpass\n"
		assertRule(t, "function-argument-name", source, 1, 1, 1)
		assertRule(t, "function-variable-name", source, 2, 3)
		assertRule(t, "loop-variable-name", source, 4)
	})
	t.Run("static declarations are checked", func(t *testing.T) {
		source := "static var Bad = 1\nstatic func Bad2(): pass\n"
		assertRule(t, "class-variable-name", source, 1)
		assertRule(t, "function-name", source, 2)
	})
}

func TestNameDiagnosticsPointAtTheIdentifier(t *testing.T) {
	tests := []struct {
		rule   string
		source string
		column int
	}{
		{"function-name", "func doThing():\n\tpass\n", 6},
		{"class-variable-name", "static var BadVar = 1\n", 12},
		{"constant-name", "const bad = 1\n", 7},
		{"sub-class-name", "class my_inner:\n\tpass\n", 7},
		{"class-name", "class_name my_class\n", 12},
		{"function-argument-name", "func f(first, Second):\n\tpass\n", 15},
		{"loop-variable-name", "func f():\n\tfor I in range(3):\n\t\tpass\n", 6},
		{"enum-element-name", "enum E { A, b }\n", 13},
		{"function-variable-name", "func f():\n\tvar x := 1; var Bad = 2\n", 18},
	}
	for _, test := range tests {
		t.Run(test.rule, func(t *testing.T) {
			found := lintSource(t, test.rule, test.source)
			if len(found) != 1 {
				t.Fatalf("got %d diagnostics, want 1", len(found))
			}
			if found[0].Column != test.column {
				t.Fatalf("column = %d, want %d", found[0].Column, test.column)
			}
			if found[0].EndColumn <= found[0].Column {
				t.Fatalf("end column %d is not after column %d", found[0].EndColumn, found[0].Column)
			}
		})
	}
}

func TestNameColumnCountsRunes(t *testing.T) {
	found := lintSource(t, "function-variable-name", "func f():\n\tvar text = \"éé\"; var Bad = 2\n")
	if len(found) != 1 || found[0].Column != 23 {
		t.Fatalf("got %+v, want one diagnostic at rune column 23", found)
	}
}

func TestNameRulesHonorConfiguredPatterns(t *testing.T) {
	config := DefaultConfig()
	config.FunctionName = `[a-z]+`
	assertRuleWithConfig(t, config, "function-name", "func two_words():\n\tpass\n", 1)
	assertRuleWithConfig(t, config, "function-name", "func words():\n\tpass\n")
}
