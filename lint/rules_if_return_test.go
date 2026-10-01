package lint

import "testing"

func TestNoElseReturn(t *testing.T) {
	cases := []struct {
		name   string
		source string
		lines  []int
	}{
		{"simple", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\tprint(1)\n", []int{4}},
		{"inline branches", "func a(x):\n\tif x: return 1\n\telse: print(3)\n", []int{3}},
		{"branch does not return", "func a(x):\n\tif x:\n\t\tprint(1)\n\telse:\n\t\treturn 1\n", nil},
		{"elif branch does not return", "func a(x):\n\tif x:\n\t\treturn 1\n\telif x > 1:\n\t\tprint(1)\n\telse:\n\t\tprint(2)\n", nil},
		{"all branches return", "func a(x):\n\tif x:\n\t\treturn 1\n\telif x > 1:\n\t\treturn 2\n\telse:\n\t\tprint(2)\n", []int{6}},
		{"nested if with else", "func a(x, y):\n\tif x:\n\t\tif y:\n\t\t\treturn 1\n\t\telse:\n\t\t\treturn 2\n\telse:\n\t\tprint(1)\n", []int{5, 7}},
		{"nested if without else", "func a(x, y):\n\tif x:\n\t\tif y:\n\t\t\treturn 1\n\telse:\n\t\tprint(1)\n", nil},
		{"nested if with a branch that does not return", "func a(x, y):\n\tif x:\n\t\tif y:\n\t\t\treturn 1\n\t\telif x:\n\t\t\tprint(1)\n\t\telse:\n\t\t\treturn 2\n\telse:\n\t\tprint(1)\n", nil},
		{"match with wildcard", "func a(x, y):\n\tif x:\n\t\tmatch y:\n\t\t\t1:\n\t\t\t\treturn 1\n\t\t\t_:\n\t\t\t\treturn 2\n\telse:\n\t\tprint(1)\n", []int{8}},
		{"match with guarded wildcard", "func a(x, y):\n\tif x:\n\t\tmatch y:\n\t\t\t1:\n\t\t\t\treturn 1\n\t\t\t_ when x:\n\t\t\t\treturn 2\n\telse:\n\t\tprint(1)\n", []int{8}},
		{"match without wildcard", "func a(x, y):\n\tif x:\n\t\tmatch y:\n\t\t\t1:\n\t\t\t\treturn 1\n\t\t\t2:\n\t\t\t\treturn 2\n\telse:\n\t\tprint(1)\n", nil},
		{"match with a wildcard inside a pattern list", "func a(x, y):\n\tif x:\n\t\tmatch y:\n\t\t\t1:\n\t\t\t\treturn 1\n\t\t\t_, 2:\n\t\t\t\treturn 2\n\telse:\n\t\tprint(1)\n", nil},
		{"match case does not return", "func a(x, y):\n\tif x:\n\t\tmatch y:\n\t\t\t1:\n\t\t\t\treturn 1\n\t\t\t_:\n\t\t\t\tprint(2)\n\telse:\n\t\tprint(1)\n", nil},
		{"return inside a loop does not count", "func a(x, y):\n\tif x:\n\t\tfor i in y:\n\t\t\treturn 1\n\telse:\n\t\tprint(1)\n", nil},
		{"enclosing variable declared after", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\tvar v = 2\n\t\tprint(v)\n\tvar v = 3\n", nil},
		{"unrelated variable in else", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\tvar w = 2\n\t\tprint(w)\n\tvar v = 3\n", []int{4}},
		{"enclosing constant does not collide", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\tvar v = 2\n\tconst v = 1\n", []int{4}},
		{"else constant does not collide", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\tconst v = 2\n\tvar v = 1\n", []int{4}},
		{"variable nested deeper in else does not collide", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\tif x:\n\t\t\tvar v = 2\n\tvar v = 1\n", []int{4}},
		{"variable scoped to an outer function does not suppress a lambda", "func a(x):\n\tvar f = func():\n\t\tif x:\n\t\t\treturn 1\n\t\telse:\n\t\t\tvar v = 2\n\tvar v = 1\n", []int{5}},
		{"variable in the enclosing branch suppresses", "func a(x):\n\tif x:\n\t\tif x:\n\t\t\treturn 1\n\t\telse:\n\t\t\tvar v = 2\n\t\tvar v = 1\n\tpass\n", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRule(t, "no-else-return", testCase.source, testCase.lines...)
		})
	}
}

func TestNoElifReturn(t *testing.T) {
	cases := []struct {
		name   string
		source string
		lines  []int
	}{
		{"simple", "func a(x):\n\tif x:\n\t\treturn 1\n\telif x > 1:\n\t\tprint(1)\n", []int{4}},
		{"branch does not return", "func a(x):\n\tif x:\n\t\tprint(1)\n\telif x > 1:\n\t\treturn 1\n", nil},
		{"else only", "func a(x):\n\tif x:\n\t\treturn 1\n\telse:\n\t\tprint(1)\n", nil},
		{"stops at the first branch that does not return", "func a(x):\n\tif x:\n\t\treturn 1\n\telif x > 1:\n\t\treturn 2\n\telif x > 2:\n\t\tprint(1)\n\telif x > 3:\n\t\treturn 4\n\telse:\n\t\tprint(2)\n", []int{4, 6}},
		{"reported at the elif keyword", "func a(x):\n\tif x:\n\t\treturn 1\n\telif (\n\t\tx > 1\n\t):\n\t\treturn 2\n\telse:\n\t\tprint(2)\n", []int{4}},
		{"nested if with else", "func a(x, y):\n\tif x:\n\t\tif y:\n\t\t\treturn 1\n\t\telse:\n\t\t\treturn 2\n\telif y:\n\t\tprint(1)\n", []int{7}},
		{"nested if without else", "func a(x, y):\n\tif x:\n\t\tif y:\n\t\t\treturn 1\n\telif y:\n\t\tprint(1)\n", nil},
		{"match with wildcard", "func a(x, y):\n\tif x:\n\t\tmatch y:\n\t\t\t1:\n\t\t\t\treturn 1\n\t\t\t_:\n\t\t\t\treturn 2\n\telif y:\n\t\tprint(1)\n", []int{8}},
		{"match without wildcard", "func a(x, y):\n\tif x:\n\t\tmatch y:\n\t\t\t1:\n\t\t\t\treturn 1\n\t\t\t2:\n\t\t\t\treturn 2\n\telif y:\n\t\tprint(1)\n", nil},
		{"elif inside a nested if", "func a(x, y):\n\tif x:\n\t\tif y:\n\t\t\treturn 1\n\t\telif x:\n\t\t\tprint(1)\n\t\telse:\n\t\t\treturn 2\n\telse:\n\t\tprint(1)\n", []int{5}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRule(t, "no-elif-return", testCase.source, testCase.lines...)
		})
	}
}
