package format

import (
	"testing"

	gdformat "github.com/cafecito-games/gdparser/format"
)

const suppressionMovedMessage = "formatting would change the code a lint suppression comment applies to"

// headerSuppressions are directives that end a block header's line, each with
// the rewrite that moves the comment onto the first line of the block, where
// it no longer covers the header.
var headerSuppressions = map[string]struct {
	source, moved string
	line, column  int
}{
	"function":        {"func BadName():  # gdlint:ignore = function-name\n\tpass\n", "func BadName():\n\t# gdlint:ignore = function-name\n\tpass\n", 1, 18},
	"if":              {"func f():\n\tif x:  # gdlint:ignore = a\n\t\tpass\n", "func f():\n\tif x:\n\t\t# gdlint:ignore = a\n\t\tpass\n", 2, 9},
	"elif":            {"func f():\n\tif x:\n\t\tpass\n\telif y:  # gdlint: disable = a\n\t\tpass\n", "func f():\n\tif x:\n\t\tpass\n\telif y:\n\t\t# gdlint: disable = a\n\t\tpass\n", 4, 11},
	"else":            {"func f():\n\tif x:\n\t\tpass\n\telse:  # gdlint: enable = a\n\t\tpass\n", "func f():\n\tif x:\n\t\tpass\n\telse:\n\t\t# gdlint: enable = a\n\t\tpass\n", 4, 9},
	"for":             {"func f():\n\tfor i in x:  # gdkit:ignore = a\n\t\tpass\n", "func f():\n\tfor i in x:\n\t\t# gdkit:ignore = a\n\t\tpass\n", 2, 15},
	"while":           {"func f():\n\twhile x:  # gdlint:ignore = a\n\t\tpass\n", "func f():\n\twhile x:\n\t\t# gdlint:ignore = a\n\t\tpass\n", 2, 12},
	"match":           {"func f():\n\tmatch x:  # gdlint:ignore = a\n\t\t1:\n\t\t\tpass\n", "func f():\n\tmatch x:\n\t\t# gdlint:ignore = a\n\t\t1:\n\t\t\tpass\n", 2, 12},
	"match arm":       {"func f():\n\tmatch x:\n\t\t1:  # gdlint:ignore = a\n\t\t\tpass\n", "func f():\n\tmatch x:\n\t\t1:\n\t\t\t# gdlint:ignore = a\n\t\t\tpass\n", 3, 7},
	"class":           {"class A:  # gdlint:ignore = a\n\tpass\n", "class A:\n\t# gdlint:ignore = a\n\tpass\n", 1, 11},
	"second of two":   {"var A = 1  # gdlint:ignore = a\n\n\nfunc f():  # gdlint:ignore = b\n\tpass\n", "var A = 1  # gdlint:ignore = a\n\n\nfunc f():\n\t# gdlint:ignore = b\n\tpass\n", 4, 12},
	"after non-moved": {"# gdlint:disable = a\n\n\nfunc f():  # gdlint:ignore = b\n\tpass\n", "# gdlint:disable = a\n\n\nfunc f():\n\t# gdlint:ignore = b\n\tpass\n", 4, 12},
}

func TestFormatKeepsASuppressionCommentOnItsHeaderLine(t *testing.T) {
	for name, testCase := range headerSuppressions {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, DefaultConfig(), testCase.source); got != testCase.source {
				t.Fatalf("formatted = %q, want the source kept", got)
			}
		})
	}
	respaced := map[string]struct{ source, want string }{
		"unspaced":        {"func f():\n\tif x:  #gdlint:ignore=a\n\t\tpass\n", "func f():\n\tif x:  # gdlint:ignore=a\n\t\tpass\n"},
		"unformatted":     {"func f( x ):#gdlint:ignore=unused-argument\n    pass\n", "func f(x):  # gdlint:ignore=unused-argument\n\tpass\n"},
		"blank lines":     {"# gdlint:disable = a\n\nfunc f():  # gdlint:ignore = b\n\tpass\n", "# gdlint:disable = a\n\n\nfunc f():  # gdlint:ignore = b\n\tpass\n"},
		"ordinary":        {"func f():  #why\n\tif x:  # ignore this\n\t\tpass\n", "func f():  # why\n\tif x:  # ignore this\n\t\tpass\n"},
		"one-line branch": {"func f():\n\tif x: pass  # why\n", "func f():\n\tif x:  # why\n\t\tpass\n"},
	}
	for name, testCase := range respaced {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, DefaultConfig(), testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestMovedSuppressionCatchesACommentMovedOffItsHeader(t *testing.T) {
	for name, testCase := range headerSuppressions {
		t.Run(name, func(t *testing.T) {
			source, moved := []byte(testCase.source), []byte(testCase.moved)
			line, column, refused := movedSuppression(source, moved, gdformat.GodotStyle())
			if !refused || line != testCase.line || column != testCase.column {
				t.Fatalf("movedSuppression = %d:%d %v, want %d:%d true", line, column, refused, testCase.line, testCase.column)
			}
			if _, _, refused := movedSuppression(source, source, gdformat.GodotStyle()); refused {
				t.Fatal("an unchanged file was refused")
			}
			if _, _, changed := changedToken(source, moved, gdformat.GodotStyle()); changed {
				t.Fatal("the token check caught the move, so this case does not exercise the suppression check")
			}
		})
	}
}

func TestFormatRefusesAFormatterThatMovesAHeaderComment(t *testing.T) {
	for name, testCase := range headerSuppressions {
		t.Run(name, func(t *testing.T) {
			report := formatForged(t, testCase.source, testCase.moved)
			assertRefused(t, report, Diagnostic{Rule: "format.unsafe", Message: "formatting changed the syntax tree", Path: "a.gd", Line: 1, Column: 1})
		})
	}
}

func TestFormatKeepsASuppressionCommentThatStaysOnItsLine(t *testing.T) {
	cases := map[string]struct{ source, want string }{
		"trailing a statement": {
			"var BadName=1  #gdlint:ignore = class-variable-name\n",
			"var BadName = 1  # gdlint:ignore = class-variable-name\n",
		},
		"standalone shifted by blank lines": {
			"var a=1\n# gdlint:ignore = function-name\nfunc BadName():\n\tpass\n",
			"var a = 1\n\n\n# gdlint:ignore = function-name\nfunc BadName():\n\tpass\n",
		},
		"standalone in a body": {
			"func f():\n\t# gdlint: disable = a\n\tvar x=1\n\t# gdlint: enable = a\n",
			"func f():\n\t# gdlint: disable = a\n\tvar x = 1\n\t# gdlint: enable = a\n",
		},
		"trailing a collection item": {
			"var a = [\n\t1, # gdlint:ignore = a\n]\n",
			"var a = [\n\t1,  # gdlint:ignore = a\n]\n",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, DefaultConfig(), testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestFormatRefusesToChangeTheCodeASuppressionCommentCovers(t *testing.T) {
	long := "some_function(argument_one, argument_two, argument_three, argument_four, argument_five, argument_six)"
	cases := map[string]struct {
		source       string
		line, column int
	}{
		"wrapped call":            {"func f():\n\tvar BadName = " + long + "  # gdlint:ignore=function-variable-name\n", 2, 119},
		"split statements":        {"var BadOne = 1; var BadTwo = 2 # gdlint:ignore=class-variable-name\n", 1, 32},
		"wrapped array":           {"var BadName = [\"alpha_alpha_alpha\", \"beta_beta_beta\", \"gamma_gamma_gamma\", \"delta_delta_delta\", \"epsilon\"]  # gdkit:ignore=class-variable-name\n", 1, 109},
		"one-line branch split":   {"func f():\n\tif x: pass  # gdlint:ignore = a\n", 2, 14},
		"joined lines":            {"var BadName = [\n\t1, 2]  # gdlint:ignore=class-variable-name\n", 2, 9},
		"line below pushed away":  {"var a = 1  # gdlint:ignore=function-name\nfunc BadName():\n\tpass\n", 1, 12},
		"line below wrapped":      {"# gdlint:ignore=class-variable-name\nvar BadName = " + long + "\n", 1, 1},
		"line below a disable":    {"# gdlint:disable=class-variable-name\nvar BadName = " + long + "\n", 1, 1},
		"trailing disable":        {"var BadOne = 1; var BadTwo = 2 # gdlint:disable=class-variable-name\n", 1, 32},
		"trailing enable":         {"var BadOne = 1; var BadTwo = 2 # gdlint:enable=class-variable-name\n", 1, 32},
		"directive in a string":   {"var BadOne = 1; var b = '# gdlint:ignore=class-variable-name'\n", 1, 26},
		"second directive, first": {"var a = 1  # gdlint:ignore=x\n\nvar BadOne = 1; var BadTwo = 2 # gdlint:ignore=class-variable-name\n", 3, 32},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			report, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": testCase.source})
			if len(report.Results) != 0 {
				t.Fatalf("results = %+v (%q), want none", report.Results, report.Results[0].Formatted)
			}
			want := Diagnostic{Rule: "format.unsafe", Message: suppressionMovedMessage, Path: "a.gd", Line: testCase.line, Column: testCase.column}
			if len(report.Diagnostics) != 1 || report.Diagnostics[0] != want {
				t.Fatalf("diagnostics = %+v, want %+v", report.Diagnostics, want)
			}
		})
	}
}

func TestFormatKeepsASuppressionCommentThatCoversTheSameCode(t *testing.T) {
	single := DefaultConfig()
	single.QuoteStyle = "single"
	cases := map[string]struct {
		config       Config
		source, want string
	}{
		"spacing": {
			DefaultConfig(),
			"var BadName=1+2 #gdlint:ignore=class-variable-name\nvar b=2\n",
			"var BadName = 1 + 2  # gdlint:ignore=class-variable-name\nvar b = 2\n",
		},
		"quote style": {
			DefaultConfig(),
			"var BadName = 'x'  # gdlint:ignore=class-variable-name\n",
			"var BadName = \"x\"  # gdlint:ignore=class-variable-name\n",
		},
		"dropped parentheses": {
			DefaultConfig(),
			"var BadName = (1)  # gdlint:ignore=class-variable-name\n",
			"var BadName = 1  # gdlint:ignore=class-variable-name\n",
		},
		"standalone above a short line": {
			DefaultConfig(),
			"# gdlint:ignore=class-variable-name\nvar BadName=1\n",
			"# gdlint:ignore=class-variable-name\nvar BadName = 1\n",
		},
		"standalone above a blank line": {
			DefaultConfig(),
			"# gdlint:disable=class-variable-name\n\nvar BadName=1\n",
			"# gdlint:disable=class-variable-name\n\nvar BadName = 1\n",
		},
		"inside a string requoted double": {
			DefaultConfig(),
			"var a = '# gdlint:ignore=x'\n",
			"var a = \"# gdlint:ignore=x\"\n",
		},
		"inside a string requoted single": {
			single,
			"var a = \"# gdlint:ignore=x, y\"\n",
			"var a = '# gdlint:ignore=x, y'\n",
		},
		"inside a string with an escaped quote": {
			DefaultConfig(),
			"var a = 'it\\'s # gdlint:ignore=x'\n",
			"var a = \"it's # gdlint:ignore=x\"\n",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := formatSource(t, testCase.config, testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}
