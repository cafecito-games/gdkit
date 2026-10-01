package format

import (
	"strings"
	"testing"

	gdformat "github.com/cafecito-games/gdparser/format"
)

func TestVerifyAcceptsIntendedNormalisations(t *testing.T) {
	cases := map[string]struct{ source, formatted string }{
		"quotes":           {"var a = 'x'\n", "var a = \"x\"\n"},
		"string names":     {"var a = &'x'\nvar b = ^'A/B'\n", "var a = &\"x\"\nvar b = ^\"A/B\"\n"},
		"numbers":          {"var a = .5\nvar b = 0XFF\nvar c = 5.\n", "var a = 0.5\nvar b = 0xff\nvar c = 5.0\n"},
		"comment spacing":  {"#note\n##doc\nvar a = 1  #why\n", "# note\n## doc\nvar a = 1  # why\n"},
		"nested comments":  {"var a = [\n\t1, #one\n\t#two\n\t2,\n]\n", "var a = [\n\t1,  # one\n\t# two\n\t2,\n]\n"},
		"operators":        {"var a = b && !c || d\n", "var a = b and not c or d\n"},
		"trailing comma":   {"var a = [\n\t1,\n\t2\n]\n", "var a = [\n\t1,\n\t2,\n]\n"},
		"line breaks":      {"var a = [1, 2]\n", "var a = [\n\t1,\n\t2,\n]\n"},
		"blank lines":      {"var a = 1\n\n\n\nvar b = 2\nfunc f():\n\tpass\n", "var a = 1\n\nvar b = 2\n\n\nfunc f():\n\tpass\n"},
		"spacing":          {"var a=1+2\nfunc f( x ):\n    return x\n", "var a = 1 + 2\n\n\nfunc f(x):\n\treturn x\n"},
		"identical source": {"var a = 1\n", "var a = 1\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if err := verify("a.gd", []byte(testCase.source), []byte(testCase.formatted), gdformat.GodotStyle()); err != nil {
				t.Fatalf("verify rejected an intended normalisation: %v", err)
			}
		})
	}
}

func TestVerifyRejectsChangedMeaning(t *testing.T) {
	cases := map[string]struct{ source, formatted, want string }{
		"changed identifier": {"var alpha = 1\n", "var beta = 1\n", "formatting changed the syntax tree"},
		"dropped statement":  {"var a = 1\nvar b = 2\n", "var a = 1\n", "formatting changed the syntax tree"},
		"changed string":     {"var a = 'x'\n", "var a = \"y\"\n", "formatting changed the syntax tree"},
		"changed number":     {"var a = .5\n", "var a = 0.6\n", "formatting changed the syntax tree"},
		"changed operator":   {"var a = b && c\n", "var a = b or c\n", "formatting changed the syntax tree"},
		"regrouped operands": {"var a = (b + c) * d\n", "var a = b + c * d\n", "formatting changed the syntax tree"},
		"dropped comment":    {"# keep\nvar a = 1\n", "var a = 1\n", "formatting changed the syntax tree"},
		"dropped trailing":   {"var a = 1  # keep\n", "var a = 1\n", "formatting changed the syntax tree"},
		"changed comment":    {"#keep\nvar a = 1\n", "# kept\nvar a = 1\n", "formatting changed the syntax tree"},
		"unparseable output": {"var a = 1\n", "var = = 1\n", "formatted output does not parse: "},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			err := verify("a.gd", []byte(testCase.source), []byte(testCase.formatted), gdformat.GodotStyle())
			if err == nil || !strings.HasPrefix(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestVerifyFollowsOptions(t *testing.T) {
	preserve := gdformat.GodotStyle()
	preserve.Operators = gdformat.PreserveOperators
	preserve.QuoteStyle = gdformat.PreserveQuotes
	preserve.Numbers = gdformat.PreserveNumbers
	preserve.CommentSpacing = gdformat.PreserveComments
	cases := map[string]struct{ source, formatted string }{
		"operators": {"var a = b && c\n", "var a = b and c\n"},
		"quotes":    {"var a = 'x'\n", "var a = \"x\"\n"},
		"numbers":   {"var a = .5\n", "var a = 0.5\n"},
		"comments":  {"#note\n", "# note\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if err := verify("a.gd", []byte(testCase.source), []byte(testCase.formatted), preserve); err == nil {
				t.Fatal("verify accepted a rewrite the options do not ask for")
			}
			if err := verify("a.gd", []byte(testCase.source), []byte(testCase.source), preserve); err != nil {
				t.Fatalf("verify rejected preserved source: %v", err)
			}
		})
	}
}

func TestVerifyReportsUnparseableSource(t *testing.T) {
	err := verify("a.gd", []byte("var = = 1\n"), []byte("var a = 1\n"), gdformat.GodotStyle())
	if err == nil || !strings.HasPrefix(err.Error(), "source does not parse: ") {
		t.Fatalf("error = %v", err)
	}
}
