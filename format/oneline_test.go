package format

import "testing"

// A one-line class body holds one member: in "class A: var v = 1; var u = 2"
// the class holds v, and what a semicolon separates from it belongs to the
// enclosing scope. Formatting writes the body as a block, so the member that
// is not the class's own has to land outside it; a tree that read both into
// the class would move u into A for Godot too, and neither the tree nor the
// token check could see the move, since semicolons are layout.
func TestFormatOneLineClassBodyKeepsTheScopeOfEveryMember(t *testing.T) {
	cases := map[string]struct{ source, want string }{
		"variable":            {"class A: var v = 1; var u = 2\n", "class A:\n\tvar v = 1\n\n\nvar u = 2\n"},
		"function":            {"class A: var v = 1; func f(): pass\n", "class A:\n\tvar v = 1\n\n\nfunc f():\n\tpass\n"},
		"constant":            {"class A: var v = 1; const C = 2\n", "class A:\n\tvar v = 1\n\n\nconst C = 2\n"},
		"signal":              {"class A: var v = 1; signal changed\n", "class A:\n\tvar v = 1\n\n\nsignal changed\n"},
		"enum":                {"class A: var v = 1; enum E { ONE }\n", "class A:\n\tvar v = 1\n\n\nenum E { ONE }\n"},
		"class":               {"class A: var v = 1; class B: pass\n", "class A:\n\tvar v = 1\n\n\nclass B:\n\tpass\n"},
		"three members":       {"class A: var v = 1; var u = 2; var w = 3\n", "class A:\n\tvar v = 1\n\n\nvar u = 2\nvar w = 3\n"},
		"after pass":          {"class A extends RefCounted: pass; var u = 2\n", "class A extends RefCounted:\n\tpass\n\n\nvar u = 2\n"},
		"trailing comment":    {"class A: var v = 1; var u = 2  # why\n", "class A:\n\tvar v = 1\n\n\nvar u = 2  # why\n"},
		"annotated":           {"extends Node\n\n@abstract class A: pass; var u = 2\n", "extends Node\n\n\n@abstract class A:\n\tpass\n\n\nvar u = 2\n"},
		"inside a block":      {"class Outer:\n\tvar w = 0\n\tclass A: var v = 1; var u = 2\n", "class Outer:\n\tvar w = 0\n\n\n\tclass A:\n\t\tvar v = 1\n\n\n\tvar u = 2\n"},
		"after wide text":     {"class Outer:\n\tvar é = 0; class A: var v = 1; var u = 2\n", "class Outer:\n\tvar é = 0\n\n\n\tclass A:\n\t\tvar v = 1\n\n\n\tvar u = 2\n"},
		"first member wraps":  {"class A: var v = [\n\t1]; var u = 2\n", "class A:\n\tvar v = [1]\n\n\nvar u = 2\n"},
		"continued header":    {"class A: \\\n\tvar v = 1; var u = 2\n", "class A:\n\tvar v = 1\n\n\nvar u = 2\n"},
		"continued before it": {"class A \\\n\t: var v = 1; var u = 2\n", "class A:\n\tvar v = 1\n\n\nvar u = 2\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, DefaultConfig(), testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestFormatAcceptsOneMemberAndBlockClassBodies(t *testing.T) {
	cases := map[string]struct{ source, want string }{
		"one statement":       {"class A: pass\n", "class A:\n\tpass\n"},
		"one member":          {"class A: var v = 1\n", "class A:\n\tvar v = 1\n"},
		"one member, comment": {"class A: var v = 1  # why\n", "class A:  # why\n\tvar v = 1\n"},
		"member then sibling": {"class A: var v = 1\nvar u = 2\n", "class A:\n\tvar v = 1\n\n\nvar u = 2\n"},
		"block":               {"class A:\n\tvar v = 1\n\tvar u = 2\n", "class A:\n\tvar v = 1\n\tvar u = 2\n"},
		"block, semicolon":    {"class A:\n\tvar v = 1; var u = 2\n", "class A:\n\tvar v = 1\n\tvar u = 2\n"},
		"block, comment":      {"class A:  # why\n\tvar v = 1\n\tvar u = 2\n", "class A:  # why\n\tvar v = 1\n\tvar u = 2\n"},
		"block, backslash":    {"class A:  # a \\\n\tvar v = 1\n\tvar u = 2\n", "class A:  # a \\\n\tvar v = 1\n\tvar u = 2\n"},
		"block, continued":    {"class A \\\n\textends RefCounted:\n\tvar v = 1\n\tvar u = 2\n", "class A extends RefCounted:\n\tvar v = 1\n\tvar u = 2\n"},
		"nested block":        {"class A:\n\tclass B:\n\t\tvar v = 1\n\t\tvar u = 2\n", "class A:\n\tclass B:\n\t\tvar v = 1\n\t\tvar u = 2\n"},
		"one-line branch":     {"func f(x):\n\tif x: a(); b()\n", "func f(x):\n\tif x:\n\t\ta()\n\t\tb()\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, DefaultConfig(), testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}

// A lambda whose one-line body opens a block, and an annotation sharing its
// line with extends or class_name, were both refused as format.unsafe: the
// output parsed back to a tree whose inline and own_line flags differed from
// the source's. Those flags are compared like any other field, so this is
// what says the formatter puts the layout back where it found it.
func TestFormatOneLineLambdaBodiesAndSameLineAnnotations(t *testing.T) {
	cases := map[string]struct{ source, want string }{
		"lambda body opens a block":  {"var f = func(): if a: return 1\n", "var f = func():\n\tif a:\n\t\treturn 1\n"},
		"lambda inside a function":   {"func f():\n\tvar g = func(): if a: return 1\n", "func f():\n\tvar g = func():\n\t\tif a:\n\t\t\treturn 1\n"},
		"lambda body on one line":    {"var f = func(): return 1\n", "var f = func(): return 1\n"},
		"tool before extends":        {"@tool extends Node\n", "@tool extends Node\n"},
		"abstract before class_name": {"@abstract class_name X extends Node\n", "@abstract class_name X extends Node\n"},
		"icon before class_name":     {"@icon(\"res://icon.svg\") class_name X extends Node\n", "@icon(\"res://icon.svg\") class_name X extends Node\n"},
		"two annotations":            {"@tool @icon(\"res://icon.svg\") extends Node\n", "@tool @icon(\"res://icon.svg\") extends Node\n"},
		"static_unload":              {"@static_unload extends Node\n", "@static_unload extends Node\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, DefaultConfig(), testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}
