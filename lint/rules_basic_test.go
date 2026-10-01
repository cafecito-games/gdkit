package lint

import (
	"fmt"
	"strings"
	"testing"
)

// gd converts leading groups of four spaces into tabs, so fixtures can be
// written without invisible indentation. Every expectation in this file was
// confirmed against gdlint from godot-gdscript-toolkit.
func gd(source string) string {
	lines := strings.Split(source, "\n")
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := (len(line) - len(trimmed)) / 4
		lines[index] = strings.Repeat("\t", indent) + trimmed
	}
	return strings.Join(lines, "\n")
}

func assertMessages(t *testing.T, rule, source string, messages ...string) {
	t.Helper()
	found := lintSource(t, rule, source)
	if len(found) != len(messages) {
		t.Fatalf("%s fired %d times, want %d: %+v", rule, len(found), len(messages), found)
	}
	for index, diagnostic := range found {
		if diagnostic.Message != messages[index] {
			t.Fatalf("message = %q, want %q", diagnostic.Message, messages[index])
		}
	}
}

func TestUnnecessaryPassFiresWhenAnotherStatementSharesTheBlock(t *testing.T) {
	source := gd(`extends Node

var a = 1
pass

func with_statement():
    pass
    print(1)

func after_comment():
    pass
    # a comment
    return 1

func in_branches(x):
    if x:
        pass
        return 1
    else:
        pass
        x = 2
    while x:
        pass
        break
    match x:
        1:
            pass
            print(1)

func in_lambda():
    var callback = func():
        pass
        print(1)
    return callback

class Inner:
    pass
    var q = 1

var watched: int:
    set(value):
        pass
        watched = value
    get:
        pass
        return 1
`)
	assertRule(t, "unnecessary-pass", source, 4, 7, 11, 17, 20, 23, 27, 32, 37, 42, 45)
	assertMessages(t, "unnecessary-pass", "extends Node\nvar a = 1\npass\n", `"pass" statement not necessary`)
}

func TestUnnecessaryPassStaysSilentOnNearMisses(t *testing.T) {
	source := gd(`@tool
extends Node

func alone():
    pass

func with_comment_only():
    # nothing else here
    pass

func doubled():
    pass
    pass

func semicolons():
    pass; pass

func only_branches(x):
    if x:
        pass
    else:
        pass
    for i in 3: pass

func only_annotation():
    pass
    @warning_ignore("unused_variable")

class Empty:
    pass

class Neighbours:
    pass
    func method(): pass
`)
	assertNoRule(t, "unnecessary-pass", source)
	assertNoRule(t, "unnecessary-pass", "@tool\npass\n")
	assertNoRule(t, "unnecessary-pass", "pass\nfunc a(): pass\n")
}

func TestUnnecessaryPassCountsTheExtendsClauseOfAClass(t *testing.T) {
	source := gd(`class Multiline extends Node:
    pass

class Inline extends Node: pass

class Plain:
    pass
`)
	assertRule(t, "unnecessary-pass", source, 2, 4)
}

func TestExpressionNotAssignedFiresOnDiscardedValues(t *testing.T) {
	source := gd(`func f(x, node, a, b, arr):
    x
    1
    r"raw"
    &"name"
    ^"path"
    $Node
    %Unique
    $"a/b"
    node.prop
    node.call().prop
    arr[0]
    foo()[0]
    (x)
    ((x))
    (a + b)
    (a) + (b)
    a == b
    a and b
    not a
    -a
    x as int
    x is Node
    x if a else b
    [1, 2]
    {"a": 1}
    true
    null
    a in b
    self
`)
	assertRule(t, "expression-not-assigned", source,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30)
	assertMessages(t, "expression-not-assigned", "func f(x):\n\tx\n", "expression is not asigned, and hence it can be removed")
}

func TestExpressionNotAssignedAcceptsCallsAwaitsStringsAndLambdas(t *testing.T) {
	source := gd(`func f(x, node):
    foo()
    node.call()
    node.call().other()
    "docstring"
    """long docstring"""
    await foo()
    await x
    (await x)
    (foo())
    ((foo()))
    x = 1
    x += 1
    preload("res://a.gd")
    node.get(1)
    get(x)
    set(x, 1)
`)
	assertNoRule(t, "expression-not-assigned", source)
}

func TestExpressionNotAssignedReportsTheOuterExpressionStart(t *testing.T) {
	source := gd(`func f(a, b):
    (
        a + b
    )
    (
        a
    ) + (
        b
    )
    ((a) + (b))
    (
        foo()
    )
`)
	assertRule(t, "expression-not-assigned", source, 3, 5, 10)
}

func TestDuplicatedLoadFiresOnSecondAndLaterLoadsOfTheSameLiteral(t *testing.T) {
	source := gd(`const A = preload("res://a.gd")
const B = preload("res://a.gd")
var c = load("res://a.gd")
var d = load("res://a.gd", "ignored")
var e = load("""res://b.gd""")
var f = load("""res://b.gd""")

func f1():
    var inner = load("res://a.gd")
    var pair = [load("res://z.gd"), load("res://z.gd")]
    return pair + [inner]

class Inner:
    var z = load("res://a.gd")
`)
	assertRule(t, "duplicated-load", source, 2, 3, 4, 6, 9, 10, 14)
	assertMessages(t, "duplicated-load", "var a = load(\"res://a.gd\")\nvar b = load(\"res://a.gd\")\n", `duplicated loading of "res://a.gd"`)
	assertMessages(t, "duplicated-load", "var a = load(\"\"\"res://a.gd\"\"\")\nvar b = load(\"\"\"res://a.gd\"\"\")\n", `duplicated loading of """res://a.gd"""`)
}

func TestDuplicatedLoadStaysSilentOnNearMisses(t *testing.T) {
	source := gd(`var a = load("res://a.gd")
var b = load('res://a.gd')
var c = ResourceLoader.load("res://a.gd")
var d = load(r"res://a.gd")
var e = load(("res://a.gd"))
var f = load("res://a.gd" + "")
var g = load(&"res://a.gd")
var h = load(a)
var i = load("res://other.gd")
var j = load()
`)
	assertNoRule(t, "duplicated-load", source)
}

func TestDuplicatedLoadTreatsTheFirstOfTwoRawStringsAsDistinct(t *testing.T) {
	assertNoRule(t, "duplicated-load", "var a = load(r\"res://a.gd\")\nvar b = load(r\"res://a.gd\")\n")
}

func TestDuplicatedLoadKeepsNoStateAcrossFiles(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{
		"a.gd": "var x = load(\"res://shared.gd\")\n",
		"b.gd": "var y = load(\"res://shared.gd\")\nvar z = load(\"res://shared.gd\")\n",
		"c.gd": "var w = preload(\"res://shared.gd\")\n",
	})
	var got []string
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "duplicated-load" {
			got = append(got, fmt.Sprintf("%s:%d", diagnostic.Path, diagnostic.Line))
		}
	}
	if len(got) != 1 || got[0] != "b.gd:2" {
		t.Fatalf("duplicated-load fired at %v, want only b.gd:2", got)
	}
}

func TestUnusedArgumentFires(t *testing.T) {
	source := gd(`extends Node

func never_used(x):
    pass

func some_unused(x, y, z):
    return y

func ready_override_is_not_exempt(delta):
    pass

func process_override(delta):
    pass

func in_string(x):
    return "x"

func in_dictionary_key(x):
    return {"x": 1}

func in_type_annotation_only(x: Node):
    print(1 as Node)

func type_name_is_not_a_use(Thing):
    return 1 as Thing

func similar_name(x, x2):
    return x2

func get_builtin_is_not_a_use(get):
    return get(1)

static func in_static(x):
    pass

func one_line(x): pass

class Inner:
    func method(q):
        pass
`)
	assertRule(t, "unused-argument", source, 3, 6, 6, 9, 12, 15, 18, 21, 24, 27, 30, 33, 36, 39)
	assertMessages(t, "unused-argument", "func f(x):\n\tpass\n", "unused function argument 'x'")
}

func TestUnusedArgumentStaysSilentOnNearMisses(t *testing.T) {
	source := gd(`extends Node

func used(x):
    return x

func underscore_prefixed(_x, __y):
    pass

func assigned(x):
    x = 1

func in_default_of_a_later_parameter(x, y = x):
    return y

func member_access_with_the_same_name(x):
    return self.x

func member_of_another_object(x):
    return other.x

func captured_by_lambda(x):
    var callback = func(): return x
    return callback

func lambda_parameter_is_never_reported(x):
    var callback = func(unused): return x
    return callback

func node_path(x, z):
    return $x + %z

func own_name(own_name):
    pass

func named_lambda_with_the_same_name(x):
    var callback = func x(): return 1
    return callback

func named_lambda_called_get(get):
    var callback = func get(): return 1
    return callback

func method_call_on_the_name(x):
    return x.call()

func used_in_comparison(x):
    return x is Node

func used_in_a_match(x):
    match x:
        1:
            pass

func no_parameters():
    pass

@abstract
func abstract_function(x)

var watched: int:
    set(value):
        pass
`)
	assertNoRule(t, "unused-argument", source)
}

// Godot accepts a lambda parameter that reuses a name from the enclosing
// function, because parse_function_signature adds parameters to the lambda's
// suite without consulting the enclosing blocks. gdparser 06bc15a rejects it.
func TestUnusedArgumentStaysSilentOnLambdaParameterWithTheSameName(t *testing.T) {
	t.Skip(`gdparser rejects "func(x)" inside "func f(x)" with "there is already a parameter named" although Godot accepts it`)
	source := gd(`extends Node

func lambda_parameter_with_the_same_name(x):
    var callback = func(x): return 1
    return callback
`)
	assertNoRule(t, "unused-argument", source)
}

func TestUnusedArgumentKeepsNoStateAcrossFiles(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{
		"a.gd": "func f(x):\n\treturn x\n",
		"b.gd": "func f(x):\n\tpass\n",
		"c.gd": "func g(y):\n\treturn y\n",
	})
	var got []string
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "unused-argument" {
			got = append(got, fmt.Sprintf("%s:%d", diagnostic.Path, diagnostic.Line))
		}
	}
	if len(got) != 1 || got[0] != "b.gd:1" {
		t.Fatalf("unused-argument fired at %v, want only b.gd:1", got)
	}
}

func TestComparisonWithItselfFires(t *testing.T) {
	source := gd(`func f(a, b):
    var r1 = a == a
    var r2 = a != a
    var r3 = a < a
    var r4 = a > a
    var r5 = a <= a
    var r6 = a >= a
    var r7 = a.b == a.b
    var r8 = foo() == foo()
    var r9 = (a) == (a)
    var r10 = 1 == 1
    var r11 = "x" == "x"
    var r12 = a[0] == a[0]
    var r13 = $N == $N
    var r14 = a == a and b
    var r15 = ((a == a))
    var r16 = a.b ( ) == a.b()
    var r17 = a   .   b == a.b
    var r18 = [1, 2] == [1, 2]
    var r19 = (a + b) == (a + b)
    var r20 = null == null
    var r21 = -a == -a
    var r22 = -(a + 1) == -(a + 1)
    var r23 = foo(a + 1) == foo(a + 1)
    var r24 = a[a + 1] == a[a + 1]
    var r25 = ~a == ~a
    var r26 = await a == await a
    var r27 = ((a)) == ((a))
    var r28 = a == a if b else b
    if a == a:
        pass
    return a == a
`)
	assertRule(t, "comparison-with-itself", source,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 32)
	assertMessages(t, "comparison-with-itself", "func f(a):\n\treturn a == a\n", "Redundant comparison")
}

func TestComparisonWithItselfStaysSilentOnNearMisses(t *testing.T) {
	source := gd(`func f(a, b, Thing):
    var r1 = a == b
    var r2 = a in a
    var r3 = a is Thing
    var r4 = a.b == a.c
    var r5 = foo(1) == foo(2)
    var r6 = (a) == a
    var r7 = a == (a)
    var r8 = (a) == ((a))
    var r9 = 1 == 1.0
    var r10 = "x" == 'x'
    var r11 = a[0] == a[1]
    var r12 = a + 1 == a + 1
    var r13 = a * 2 == a * 2
    var r14 = a | 1 == a | 1
    var r15 = a & 1 == a & 1
    var r16 = a ^ 1 == a ^ 1
    var r17 = a << 1 == a << 1
    var r18 = a ** 2 == a ** 2
    var r19 = a - 1 == a - 1
    var r20 = a % 2 == a % 2
    var r21 = a is Thing == a is Thing
    var r22 = -a * 2 == -a * 2
    var r23 = a and a
`)
	assertNoRule(t, "comparison-with-itself", source)
}

func TestComparisonWithItselfReportsTheLeftOperandStart(t *testing.T) {
	source := gd(`func f(a):
    var q = (
        a
    ) == (
        a
    )
    var w = foo(
        (a) == (a)
    )
    return (a
        ) == (a
        )
`)
	assertRule(t, "comparison-with-itself", source, 2, 8, 10)
}

// gdlint's grammar calls a comparison "comparison" only where it leads its
// expression. After "and", "or", "not", or "in", and in the condition or
// alternative of a ternary, the same syntax is an "asless_comparison", which
// gdlint's check never visits.
func TestComparisonWithItselfStaysSilentWhereGdlintNamesTheComparisonDifferently(t *testing.T) {
	source := gd(`func f(a, b, foo):
    var r1 = b and a == a
    var r2 = b or a == a
    var r3 = not a == a
    var r4 = !a == a
    var r5 = b if a == a else b
    var r6 = b if b else a == a
    var r7 = b in a == a
    var r8 = b or a == a and b
    var r9 = b and a == a or b
    var r10 = (b and a == a)
    var r11 = foo.call(b or a == a)
    var r12 = b && a == a
    var r13 = b || a == a
    var r14 = b and b and a == a
    var r15 = b if b else b if b else a == a
    var r16 = b not in a == a
    var r17 = not not a == a
    var r18 = b and a == a as bool
`)
	assertNoRule(t, "comparison-with-itself", source)
}

func TestComparisonWithItselfReportsALeadingOrBracketedComparison(t *testing.T) {
	source := gd(`func f(a, b, foo):
    var r1 = a == a or b
    var r2 = a == a and b or b
    var r3 = b and (a == a)
    var r4 = not (a == a)
    var r5 = a == a if b else b
    var r6 = b and foo.call(a == a)
    var r7 = b or [a == a]
    var r8 = a == a in b
    var r9 = b if (a == a) else b
    var r10 = b and (a == a or b)
    var r11 = (a == a and b) or b
    var r12 = a == a and b if b else b
    var r13 = b or ((a == a))
    var r14 = b and a[a == a]
    var r15 = b or {a: a == a}
    var r16 = b and (func(): return a == a)
    var r17 = a == a as bool
    var r18 = b and ((a) == (a))
    var r19 = b and (a == a) == (a == a)
`)
	assertRule(t, "comparison-with-itself", source,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 20)
}
