package format

import "testing"

func TestFormatRefusesOneLineClassBodyWithSeveralMembers(t *testing.T) {
	cases := map[string]struct {
		source       string
		line, column int
	}{
		"variable":            {"class A: var v = 1; var u = 2\n", 1, 1},
		"function":            {"class A: var v = 1; func f(): pass\n", 1, 1},
		"constant":            {"class A: var v = 1; const C = 2\n", 1, 1},
		"signal":              {"class A: var v = 1; signal changed\n", 1, 1},
		"enum":                {"class A: var v = 1; enum E { ONE }\n", 1, 1},
		"class":               {"class A: var v = 1; class B: pass\n", 1, 1},
		"after pass":          {"class A extends RefCounted: pass; var u = 2\n", 1, 1},
		"trailing comment":    {"class A: var v = 1; var u = 2  # why\n", 1, 1},
		"annotated":           {"extends Node\n\n@abstract class A: pass; var u = 2\n", 3, 11},
		"inside a block":      {"class Outer:\n\tvar w = 0\n\tclass A: var v = 1; var u = 2\n", 3, 2},
		"inside a one-liner":  {"class Outer: class A: var v = 1; var u = 2\n", 1, 14},
		"after wide text":     {"class Outer:\n\tvar é = 0; class A: var v = 1; var u = 2\n", 2, 13},
		"first member wraps":  {"class A: var v = [\n\t1]; var u = 2\n", 1, 1},
		"continued header":    {"class A: \\\n\tvar v = 1; var u = 2\n", 1, 1},
		"continued before it": {"class A \\\n\t: var v = 1; var u = 2\n", 1, 1},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			report, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": testCase.source})
			assertRefused(t, report, Diagnostic{
				Rule:    ruleUnsafe,
				Message: ambiguousClassBody,
				Path:    "a.gd",
				Line:    testCase.line,
				Column:  testCase.column,
			})
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
