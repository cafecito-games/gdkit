package lint

import "testing"

// inconsistentReturnConfig enables the rule, which ships inert.
func inconsistentReturnConfig() Config {
	config := DefaultConfig()
	config.Enable = []string{"inconsistent-return-statements"}
	return config
}

func TestInconsistentReturnStatements(t *testing.T) {
	cases := []struct {
		name   string
		source string
		lines  []int
	}{
		{"falls off the end", "func a(x):\n\tif x:\n\t\treturn 1\n", []int{1}},
		{"inline branch falls off the end", "func a(flag):\n\tif flag: return 1\n", []int{1}},
		{"annotated return type still falls off the end", "func a(x) -> int:\n\tif x:\n\t\treturn 1\n", []int{1}},
		{"if and else both return", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\treturn 0\n", nil},
		{"elif chain with else", "func a(x):\n\tif x == 1:\n\t\treturn 1\n\telif x == 2:\n\t\treturn 2\n\telse:\n\t\treturn 0\n", nil},
		{"if without else but a trailing return", "func a(x):\n\tif x:\n\t\treturn 1\n\treturn 0\n", nil},
		{"match with a wildcard", "func a(x):\n\tmatch x:\n\t\t1:\n\t\t\treturn 1\n\t\t_:\n\t\t\treturn 0\n", nil},
		{"match without a wildcard", "func a(x):\n\tmatch x:\n\t\t1:\n\t\t\treturn 1\n\t\t2:\n\t\t\treturn 2\n", []int{1}},
		{"trailing bare return terminates", "func a(x):\n\tif x:\n\t\treturn 1\n\treturn\n", nil},
		{"only bare returns", "func a(x):\n\tif x:\n\t\treturn\n\tprint(1)\n", nil},
		{"no return at all", "func a(x):\n\tprint(x)\n", nil},
		{"void function", "func _ready():\n\tprint(1)\n", nil},
		{"abstract function", "@abstract\nclass_name A\n@abstract\nfunc a(x):\n\tpass\n", nil},
		{"return inside a for loop", "func a(x):\n\tfor i in x:\n\t\treturn i\n", []int{1}},
		{"return inside a conditional while", "func a(x):\n\twhile x:\n\t\treturn 1\n", []int{1}},
		{"return inside an endless while", "func a(x):\n\twhile true:\n\t\tif x:\n\t\t\treturn 1\n", nil},
		{"endless while with a break", "func a(x):\n\twhile true:\n\t\tif x:\n\t\t\treturn 1\n\t\tbreak\n", []int{1}},
		{"break in a loop nested in an endless while", "func a(x):\n\twhile true:\n\t\tfor i in x:\n\t\t\tbreak\n\t\tif x:\n\t\t\treturn 1\n", nil},
		{"endless while after a returning if", "func a(x):\n\tif x:\n\t\treturn 1\n\twhile true:\n\t\tif x:\n\t\t\treturn 2\n", nil},
		{"second function reported at its own keyword", "func a(x):\n\treturn x\n\n\nfunc b(x):\n\tif x:\n\t\treturn 1\n", []int{5}},
		{"one diagnostic per function", "func a(x):\n\tif x:\n\t\treturn 1\n\tif x:\n\t\treturn 2\n", []int{1}},
		{"lambda falls off the end", "func a(x):\n\tvar f = func(y):\n\t\tif y:\n\t\t\treturn 1\n\treturn f\n", []int{2}},
		{"a lambda's return is not the enclosing function's", "func a(x):\n\tvar f = func(y):\n\t\treturn y\n\tprint(f)\n", nil},
		{"lambda that always returns", "func a(x):\n\tvar f = func(y):\n\t\tif y:\n\t\t\treturn 1\n\t\treturn 0\n\treturn f\n", nil},
		{"static function", "static func a(x):\n\tif x:\n\t\treturn 1\n", []int{1}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRuleWithConfig(t, inconsistentReturnConfig(), "inconsistent-return-statements", testCase.source, testCase.lines...)
		})
	}
}

func TestInconsistentReturnStatementsStaysInertByDefault(t *testing.T) {
	assertNoRule(t, "inconsistent-return-statements", "func a(x):\n\tif x:\n\t\treturn 1\n")
}

func TestInconsistentReturnStatementsMessage(t *testing.T) {
	found := lintSourceWithConfig(t, inconsistentReturnConfig(), "inconsistent-return-statements", "func a(x):\n\tif x:\n\t\treturn 1\n")
	if len(found) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(found), found)
	}
	if want := "Not all code paths return a value"; found[0].Message != want {
		t.Fatalf("message = %q, want %q", found[0].Message, want)
	}
	if found[0].Column != 1 {
		t.Fatalf("column = %d, want 1", found[0].Column)
	}
}
