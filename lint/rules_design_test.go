package lint

import (
	"strings"
	"testing"
)

func designConfig(maxReturns, maxPublicMethods, arguments int) Config {
	config := DefaultConfig()
	config.MaxReturns = maxReturns
	config.MaxPublicMethods = maxPublicMethods
	config.FunctionArgumentsNumber = arguments
	return config
}

func repeatedFunctions(prefix string, count int, indent string) string {
	var source strings.Builder
	for index := 0; index < count; index++ {
		source.WriteString(indent + "func " + prefix + string(rune('a'+index)) + "(): pass\n")
	}
	return source.String()
}

func TestMaxReturnsBoundaryAndLocation(t *testing.T) {
	atLimit := "func a(x):\n\tif x:\n\t\treturn 1\n\treturn 2\n"
	assertRuleWithConfig(t, designConfig(2, 20, 10), "max-returns", atLimit)
	overLimit := "func a(x):\n\tif x:\n\t\treturn 1\n\treturn 2\n\treturn 3\n"
	assertRuleWithConfig(t, designConfig(2, 20, 10), "max-returns", overLimit, 5)
	found := lintSourceWithConfig(t, designConfig(2, 20, 10), "max-returns", overLimit)
	if found[0].Message != `Function "a" has more than 2 return statements` || found[0].Column != 2 {
		t.Fatalf("unexpected diagnostic %+v", found[0])
	}
}

func TestMaxReturnsDefaultIsSix(t *testing.T) {
	body := strings.Repeat("\treturn 1\n", 6)
	assertNoRule(t, "max-returns", "func a():\n"+body)
	assertRule(t, "max-returns", "func a():\n"+body+"\treturn 1\n", 8)
}

func TestMaxReturnsCountsNestedBlocksButNotLambdasOrStatics(t *testing.T) {
	config := designConfig(1, 20, 10)
	source := `func a(x):
	for i in 3:
		return 1
	while x:
		return 2
	match x:
		1:
			return 3
	return 4

func b():
	var f = func():
		return 1
		return 2
	return f

static func s():
	return 1
	return 2
`
	assertRuleWithConfig(t, config, "max-returns", source, 9)
}

func TestMaxReturnsCountsInnerClassMethodsSeparately(t *testing.T) {
	source := `func outer():
	return 1

class Inner:
	func j():
		return 1
		return 2
		return 3
`
	assertRuleWithConfig(t, designConfig(2, 20, 10), "max-returns", source, 8)
}

func TestMaxReturnsIgnoresPropertyAccessors(t *testing.T) {
	source := "var v:\n\tget:\n\t\treturn 1\n\t\treturn 2\n"
	assertRuleWithConfig(t, designConfig(1, 20, 10), "max-returns", source)
}

func TestMaxPublicMethodsBoundary(t *testing.T) {
	config := designConfig(6, 2, 10)
	assertRuleWithConfig(t, config, "max-public-methods", repeatedFunctions("f", 2, "")+"func _p(): pass\n")
	assertRuleWithConfig(t, config, "max-public-methods", repeatedFunctions("f", 3, ""), 1)
	found := lintSourceWithConfig(t, config, "max-public-methods", repeatedFunctions("f", 3, ""))
	if found[0].Message != `"Class global scope" has more than 2 public methods (functions)` || found[0].Column != 1 {
		t.Fatalf("unexpected diagnostic %+v", found[0])
	}
}

func TestMaxPublicMethodsDefaultIsTwenty(t *testing.T) {
	assertNoRule(t, "max-public-methods", repeatedFunctions("f", 20, ""))
	source := "extends Node\n" + repeatedFunctions("f", 21, "")
	assertRule(t, "max-public-methods", source, 1)
}

func TestMaxPublicMethodsReportsGlobalScopeAtLineOne(t *testing.T) {
	source := "\n\n# lead\n@tool\nextends Node\n" + repeatedFunctions("f", 3, "")
	assertRuleWithConfig(t, designConfig(6, 2, 10), "max-public-methods", source, 1)
}

func TestMaxPublicMethodsPerClassAndIgnoresStatics(t *testing.T) {
	source := `func a(): pass
class Inner:
	func i1(): pass
	func i2(): pass
	func i3(): pass
	class Deep:
		func x1(): pass
		func x2(): pass
		func x3(): pass
	@abstract class Ab:
		func z(): pass
		func y(): pass
		func w(): pass

class Statics:
	static func a1(): pass
	static func a2(): pass
	static func a3(): pass
`
	found := lintSourceWithConfig(t, designConfig(6, 2, 10), "max-public-methods", source)
	var got []string
	for _, diagnostic := range found {
		got = append(got, diagnostic.Message)
	}
	want := []string{
		`"Class Inner" has more than 2 public methods (functions)`,
		`"Class Deep" has more than 2 public methods (functions)`,
		`"Class Ab" has more than 2 public methods (functions)`,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
	lines := []int{found[0].Line, found[1].Line, found[2].Line}
	if lines[0] != 2 || lines[1] != 6 || lines[2] != 10 {
		t.Fatalf("lines %v", lines)
	}
	if found[2].Column != 12 {
		t.Fatalf("abstract class column %d, want the class keyword", found[2].Column)
	}
}

func TestFunctionArgumentsNumberBoundaryAndLocation(t *testing.T) {
	config := designConfig(6, 20, 2)
	assertRuleWithConfig(t, config, "function-arguments-number", "func a(p, q): pass\n")
	assertRuleWithConfig(t, config, "function-arguments-number", "@warning_ignore(\"x\")\nfunc a(p, q, r): pass\n", 2)
	found := lintSourceWithConfig(t, config, "function-arguments-number", "@rpc(\"any\") func a(p, q, r): pass\n")
	if len(found) != 1 || found[0].Column != 13 || found[0].Message != `Function "a" has more than 2 arguments` {
		t.Fatalf("unexpected diagnostics %+v", found)
	}
}

func TestFunctionArgumentsNumberDefaultIsTen(t *testing.T) {
	ten := "func a(a1, a2, a3, a4, a5, a6, a7, a8, a9, a10): pass\n"
	assertNoRule(t, "function-arguments-number", ten)
	assertRule(t, "function-arguments-number", "func a(a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11): pass\n", 1)
}

func TestFunctionArgumentsNumberCountsVariadicNotLambdasOrStatics(t *testing.T) {
	config := designConfig(6, 20, 1)
	source := `func a(p, ...rest):
	var f = func(x, y, z):
		return 1
	return f

func b(p): pass

static func s(p, q, r): pass

class Inner:
	func m(p, q): pass
	static func t(p, q): pass

@abstract func ab(p, q)
`
	assertRuleWithConfig(t, config, "function-arguments-number", source, 1, 11, 14)
}
