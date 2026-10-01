package format

import (
	"testing"
)

const suppressionMovedMessage = "formatting would change the code a lint suppression comment applies to"

func TestFormatRefusesToMoveASuppressionComment(t *testing.T) {
	cases := map[string]struct {
		source       string
		line, column int
	}{
		"function":        {"func BadName():  # gdlint:ignore = function-name\n\tpass\n", 1, 18},
		"if":              {"func f():\n\tif x:  # gdlint:ignore = a\n\t\tpass\n", 2, 9},
		"elif":            {"func f():\n\tif x:\n\t\tpass\n\telif y:  # gdlint: disable = a\n\t\tpass\n", 4, 11},
		"else":            {"func f():\n\tif x:\n\t\tpass\n\telse:  # gdlint: enable = a\n\t\tpass\n", 4, 9},
		"for":             {"func f():\n\tfor i in x:  # gdkit:ignore = a\n\t\tpass\n", 2, 15},
		"while":           {"func f():\n\twhile x:  # gdlint:ignore = a\n\t\tpass\n", 2, 12},
		"match arm":       {"func f():\n\tmatch x:\n\t\t1:  # gdlint:ignore = a\n\t\t\tpass\n", 3, 7},
		"one-line if":     {"func f():\n\tif x: pass  # gdlint:ignore = a\n", 2, 14},
		"unspaced":        {"func f():\n\tif x:  #gdlint:ignore=a\n\t\tpass\n", 2, 9},
		"second of two":   {"var A = 1  # gdlint:ignore = a\n\n\nfunc f():  # gdlint:ignore = b\n\tpass\n", 4, 12},
		"after non-moved": {"# gdlint:disable = a\n\nfunc f():  # gdlint:ignore = b\n\tpass\n", 3, 12},
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

func TestFormatStillMovesAnOrdinaryHeaderComment(t *testing.T) {
	source := "func f():  # why\n\tif x:  # ignore this\n\t\tpass\n"
	want := "func f():\n\t# why\n\tif x:\n\t\t# ignore this\n\t\tpass\n"
	if got := formatSource(t, DefaultConfig(), source); got != want {
		t.Fatalf("formatted = %q, want %q", got, want)
	}
}
