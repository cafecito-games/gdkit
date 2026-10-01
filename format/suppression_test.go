package format

import (
	"testing"
)

const suppressionMovedMessage = "formatting would move a lint suppression comment off the line it applies to"

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
		"second of two":   {"var A = 1  # gdlint:ignore = a\nfunc f():  # gdlint:ignore = b\n\tpass\n", 2, 12},
		"after non-moved": {"# gdlint:disable = a\nfunc f():  # gdlint:ignore = b\n\tpass\n", 2, 12},
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

func TestFormatStillMovesAnOrdinaryHeaderComment(t *testing.T) {
	source := "func f():  # why\n\tif x:  # ignore this\n\t\tpass\n"
	want := "func f():\n\t# why\n\tif x:\n\t\t# ignore this\n\t\tpass\n"
	if got := formatSource(t, DefaultConfig(), source); got != want {
		t.Fatalf("formatted = %q, want %q", got, want)
	}
}
