package format

import (
	"testing"

	"github.com/cafecito-games/gdparser"
	gdformat "github.com/cafecito-games/gdparser/format"
)

func TestChangedTokenCatchesAChangedDroppedOrAddedToken(t *testing.T) {
	cases := map[string]struct {
		source, formatted string
		line, column      int
	}{
		"inferred parameter default": {"func f(a := 1):\n\treturn a\n", "func f(a = 1):\n\treturn a\n", 1, 10},
		"inferred after a wide rune": {"func f(é := 1):\n\treturn é\n", "func f(é = 1):\n\treturn é\n", 1, 10},
		"inferred lambda default":    {"var f = func(a := 1): return a\n", "var f = func(a = 1): return a\n", 1, 16},
		"untyped made inferred":      {"func f(a = 1):\n\tpass\n", "func f(a := 1):\n\tpass\n", 1, 10},
		"dropped keyword":            {"static func f():\n\tpass\n", "func f():\n\tpass\n", 1, 1},
		"dropped await":              {"func f():\n\tawait g()\n", "func f():\n\tg()\n", 2, 2},
		"added keyword":              {"func f():\n\tpass\n", "static func f():\n\tpass\n", 1, 1},
		"added token at the end":     {"var a = b\n", "var a = b.c\n", 1, 1},
		"changed keyword":            {"var a = 1\n", "const a = 1\n", 1, 1},
		"changed identifier":         {"var alpha = 1\n", "var beta = 1\n", 1, 5},
		"changed operator":           {"var a = b + c\n", "var a = b - c\n", 1, 11},
		"changed boolean operator":   {"var a = b && c\n", "var a = b or c\n", 1, 11},
		"changed string":             {"var a = 'x'\n", "var a = \"y\"\n", 1, 9},
		"changed number":             {"var a = .5\n", "var a = 0.6\n", 1, 9},
		"changed comment":            {"#keep\nvar a = 1\n", "# kept\nvar a = 1\n", 1, 1},
		"dropped comment":            {"var a = 1  # keep\n", "var a = 1\n", 1, 12},
		"changed bracket":            {"var a = [1]\n", "var a = {1}\n", 1, 9},
		"dropped annotation":         {"@export var a = 1\n", "var a = 1\n", 1, 1},
		"dropped return arrow":       {"func f() -> int:\n\treturn 1\n", "func f():\n\treturn 1\n", 1, 10},
		"unlexable output":           {"var a = 1\n", "var a = '\n", 1, 1},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			line, column, changed := changedToken([]byte(testCase.source), []byte(testCase.formatted), gdformat.GodotStyle())
			if !changed || line != testCase.line || column != testCase.column {
				t.Fatalf("changedToken = %d:%d %v, want %d:%d true", line, column, changed, testCase.line, testCase.column)
			}
		})
	}
}

func TestChangedTokenAcceptsOnlyTheRespellingsTheOptionsAskFor(t *testing.T) {
	preserve := gdformat.GodotStyle()
	preserve.Operators = gdformat.PreserveOperators
	preserve.QuoteStyle = gdformat.PreserveQuotes
	preserve.Numbers = gdformat.PreserveNumbers
	preserve.CommentSpacing = gdformat.PreserveComments
	cases := map[string]struct{ source, formatted string }{
		"and":      {"var a = b && c\n", "var a = b and c\n"},
		"or":       {"var a = b || c\n", "var a = b or c\n"},
		"not":      {"var a = !b\n", "var a = not b\n"},
		"quotes":   {"var a = 'x'\n", "var a = \"x\"\n"},
		"numbers":  {"var a = .5\n", "var a = 0.5\n"},
		"comments": {"#note\n", "# note\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			source, formatted := []byte(testCase.source), []byte(testCase.formatted)
			if _, _, changed := changedToken(source, formatted, gdformat.GodotStyle()); changed {
				t.Fatal("the default style rejected a respelling it asks for")
			}
			if _, _, changed := changedToken(source, formatted, preserve); !changed {
				t.Fatal("a respelling the options do not ask for was accepted")
			}
			if _, _, changed := changedToken(source, source, preserve); changed {
				t.Fatal("preserved source was rejected")
			}
		})
	}
}

// tokenFixtures are sources chosen to make the formatter do everything it does
// to tokens that carry no meaning: add and drop parentheses, commas, and
// semicolons, join continued lines, split one-line bodies, and reorder
// accessors.
var tokenFixtures = map[string]string{
	"redundant parentheses":  "var a = ((b + c)) * (d)\nfunc f():\n\tif (a):\n\t\treturn (a)\n\twhile (a): pass\n",
	"needed parentheses":     "var a = (b + c) * d\nvar e = -(b + c)\nvar g = (b if c else d) + 1\nvar h = (func(): return 1).call()\n",
	"wrapped logical chain":  "func f():\n\tif alpha_alpha_alpha and beta_beta_beta and gamma_gamma_gamma or delta_delta_delta:\n\t\tpass\n",
	"trailing commas":        "var a = [\n\t1,\n\t2\n]\nvar b = {\"k\": 1,}\nvar c = f(1, 2,)\nenum E {A, B,}\nfunc g(x, y,):\n\tpass\n",
	"broken collections":     "var a = [alpha_alpha, beta_beta, gamma_gamma]\nvar b = {\"alpha\": alpha_alpha, \"beta\": beta_beta}\nvar c = call_me(alpha_alpha, beta_beta, gamma_gamma)\n",
	"semicolons":             "var a = 1; var b = 2;\nfunc f():\n\tpass; return\n",
	"continuation":           "var a = 1 + \\\n\t2\nfunc f():\n\tif a \\\n\t\tand b:\n\t\tpass\n",
	"continuation comment":   "var a = 1 + \\\n\t# why\n\t2\n",
	"strings":                "var a = 'x'\nvar b = \"it's\"\nvar c = 'say \"hi\"'\nvar d = 'it\\'s'\nvar e = r'raw\\n'\nvar g = &'name'\nvar h = ^'A/B'\nvar i = '''triple'''\nvar j = \"tab\\t\\u0041\"\n",
	"numbers":                "var a = .5\nvar b = 5.\nvar c = 0XFF\nvar d = 0B101\nvar e = 1_000\nvar g = 1E5\nvar h = 1.e5\n",
	"comments":               "#note\n##doc\n#region A\nvar a = 1  #why  \n#endregion\nvar b = [\n\t1, #one\n\t#two\n\t2\n]\n",
	"header comments":        "func f():  # why\n\tif a:  # because\n\t\tpass\n\telse:  # otherwise\n\t\tpass\n",
	"one-line body comment":  "func f():\n\tif a: pass  # why\n\tfor i in a: continue  #why\n",
	"operators":              "var a = b && !c || d\nvar e = b and not c or d\nvar g = b != c\nvar h = b not in c\nvar i = b is not C\n",
	"inferred variable":      "var a := 1\nvar b: = 2\nconst C := 3\nvar d: int = 4\n",
	"parameters":             "func f(a, b: int, c = 1, d: int = 2, e := 3, g: = 4, ...rest):\n\tpass\nfunc g( a,b ) -> void: pass\n",
	"accessors":              "var a: int = 1:\n\tset(value):\n\t\ta = value\n\tget:\n\t\treturn a\nvar b: int: get = _get_b, set = _set_b\nvar c: int: set = _set_c, get = _get_c\n",
	"lambdas":                "var a = func(): return 1\nvar b = func named(x): x += 1; return x\nvar c = f(func():\n\tpass\n\treturn 2\n)\nvar d = [func(): pass, func(): pass]\n",
	"match":                  "func f(a):\n\tmatch a:\n\t\t1, 2:\n\t\t\tpass\n\t\t[var b, ..]:\n\t\t\tpass\n\t\t{\"k\": var c, ..}:\n\t\t\tpass\n\t\tvar d when d > 1:\n\t\t\tpass\n\t\t_:\n\t\t\tpass\n",
	"dictionaries":           "var a = {b = 1, c = 2}\nvar d = {\"e\": 1, 2: [3]}\nvar g = {}\n",
	"node paths":             "var a = $A/B\nvar b = %C\nvar c = $\"D/E\"\nvar d = %\"F\"\nvar e = $A/%B\n",
	"annotations":            "@tool\n@export var a = 1\n@export_range(0, 10, 1) var b = 2\n@onready\nvar c = $C\n@warning_ignore(\"unused\")\nfunc f():\n\tpass\n",
	"declarations":           "class_name A extends Node\nsignal s\nsignal t()\nsignal u(a, b: int)\nenum {X, Y = 2}\nenum Named {Z}\nclass Inner extends RefCounted:\n\tstatic var a = 1\n\tstatic func f() -> Array[int]:\n\t\treturn []\n",
	"statements":             "func f():\n\tfor i: int in range(3):\n\t\tcontinue\n\twhile true:\n\t\tbreak\n\tassert(a, \"b\")\n\tawait g()\n\tvar h = a as B\n\tvar i = a if b else c\n\ta += 1\n\ta[0] = self.b.c(super.d())\n\treturn\n",
	"typed collections":      "var a: Array[int] = []\nvar b: Dictionary[String, int] = {}\nvar c: A.B = null\n",
	"extends string":         "extends 'res://a.gd'\n",
	"commented collection":   "var a = f(\n\t1,  # one\n\t2  # two\n)\nvar b = [\n\t# first\n\t1\n\t# last\n]\n",
	"comment ended operand":  "func f():\n\tif (a  # why\n\t\t\tand b):\n\t\tpass\n",
	"multiline lambda item":  "func f():\n\tg(func():\n\t\tmatch a:\n\t\t\t1:\n\t\t\t\tpass\n\t, 2)\n",
	"blank and crlf":         "var a = 1\r\n\r\n\r\n\r\nvar b = 2\r\n",
	"byte order mark":        "\ufeffvar a=1\n",
	"spaces and indentation": "func f( x ):\n    return x+1\n",
}

// tokenFixtureOptions are the option sets the fixtures are formatted under.
func tokenFixtureOptions() map[string]gdformat.Options {
	single := gdformat.GodotStyle()
	single.QuoteStyle = gdformat.SingleQuotes
	preserve := gdformat.GodotStyle()
	preserve.Operators = gdformat.PreserveOperators
	preserve.QuoteStyle = gdformat.PreserveQuotes
	preserve.Numbers = gdformat.PreserveNumbers
	preserve.CommentSpacing = gdformat.PreserveComments
	narrow := gdformat.GodotStyle()
	narrow.Indent = gdformat.Spaces
	narrow.LineWidth = 30
	narrow.TrailingCommas = gdformat.NoTrailingCommas
	return map[string]gdformat.Options{"default": gdformat.GodotStyle(), "single": single, "preserve": preserve, "narrow": narrow}
}

func TestChangedTokenAcceptsRealFormatterOutput(t *testing.T) {
	for optionsName, options := range tokenFixtureOptions() {
		for name, source := range tokenFixtures {
			t.Run(optionsName+"/"+name, func(t *testing.T) {
				file, err := gdparser.ParseFile("a.gd", []byte(source))
				if err != nil {
					t.Fatalf("fixture does not parse: %v", err)
				}
				formatted := gdformat.FileWithOptions(file, options)
				if _, err := gdparser.ParseFile("a.gd", []byte(formatted)); err != nil {
					t.Skipf("the formatter's output does not parse, which verifyTree refuses: %v", err)
				}
				if line, column, changed := changedToken([]byte(source), []byte(formatted), options); changed {
					t.Fatalf("changedToken refused real output at %d:%d\nsource:\n%s\nformatted:\n%s", line, column, source, formatted)
				}
			})
		}
	}
}

func TestFormatKeepsAnInferredParameterDefault(t *testing.T) {
	cases := map[string]struct{ source, want string }{
		"function": {"func f(a := 1): return a\n", "func f(a := 1):\n\treturn a\n"},
		"lambda":   {"var f = func(a := 1): return a\nvar b=1\n", "var f = func(a := 1): return a\nvar b = 1\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, DefaultConfig(), testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestFormatRefusesAFormatterThatUntypesAnInferredParameterDefault(t *testing.T) {
	cases := map[string]struct{ source, forged string }{
		"function":      {"func f(a := 1): return a\n", "func f(a = 1):\n\treturn a\n"},
		"lambda":        {"var f = func(a := 1): return a\nvar b=1\n", "var f = func(a = 1): return a\nvar b = 1\n"},
		"made inferred": {"func f(a = 1): return a\n", "func f(a := 1):\n\treturn a\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			report := formatForged(t, testCase.source, testCase.forged)
			assertRefused(t, report, Diagnostic{Rule: "format.unsafe", Message: "formatting changed the syntax tree", Path: "a.gd", Line: 1, Column: 1})
		})
	}
}
