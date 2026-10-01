package format

import (
	"reflect"
	"testing"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
)

func parseTree(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := gdparser.ParseFile("a.gd", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestSameTreeIgnoresPositionsAndFileName(t *testing.T) {
	before := parseTree(t, "var a = 1\n\n\n\nfunc f(x):\n\treturn x\n")
	after := parseTree(t, "var   a  =  1\nfunc f( x ):\n    return x\n")
	after.Name = "elsewhere.gd"
	if !sameTree(before, after, gdformat.GodotStyle()) {
		t.Fatal("trees that differ only in layout were reported as different")
	}
}

func TestSameTreeSeesEveryMeaningfulDifference(t *testing.T) {
	cases := map[string]struct{ before, after string }{
		"statement kind":     {"var a = 1\n", "const a = 1\n"},
		"expression kind":    {"var a = b\n", "var a = b()\n"},
		"type":               {"var a: int = 1\n", "var a: float = 1\n"},
		"inferred type":      {"var a := 1\n", "var a = 1\n"},
		"static":             {"static func f():\n\tpass\n", "func f():\n\tpass\n"},
		"parameter default":  {"func f(x = 1):\n\tpass\n", "func f(x = 2):\n\tpass\n"},
		"inferred default":   {"func f(x := 1):\n\tpass\n", "func f(x = 1):\n\tpass\n"},
		"lambda default":     {"var a = func(x := 1): return x\n", "var a = func(x = 1): return x\n"},
		"typed default":      {"func f(x: int := 1):\n\tpass\n", "func f(x: int = 1):\n\tpass\n"},
		"missing value":      {"func f():\n\treturn\n", "func f():\n\treturn null\n"},
		"else branch":        {"func f():\n\tif x:\n\t\tpass\n", "func f():\n\tif x:\n\t\tpass\n\telse:\n\t\tpass\n"},
		"statement order":    {"var a = 1\nvar b = 2\n", "var b = 2\nvar a = 1\n"},
		"annotation":         {"@export var a = 1\n", "@onready var a = 1\n"},
		"annotation line":    {"@export\nvar a = 1\n", "@export var a = 1\n"},
		"header comment":     {"func f():  # c\n\tpass\n", "func f():\n\t# c\n\tpass\n"},
		"branch comment":     {"func f():\n\tif x:  # c\n\t\tpass\n", "func f():\n\tif x:\n\t\t# c\n\t\tpass\n"},
		"trailing comment":   {"var a = 1  # c\n", "var a = 1\n# c\n"},
		"collection comment": {"var a = [\n\t1,  # c\n\t2,\n]\n", "var a = [\n\t1,\n\t# c\n\t2,\n]\n"},
		"dictionary style":   {"var a = {b = 1}\n", "var a = {\"b\": 1}\n"},
		"node path":          {"var a = $A/B\n", "var a = $A/C\n"},
		"unique node":        {"var a = %A\n", "var a = $A\n"},
		"lambda inline":      {"var a = func(): return 1\n", "var a = func():\n\treturn 1\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			before, after := parseTree(t, testCase.before), parseTree(t, testCase.after)
			if !sameTree(before, before, gdformat.GodotStyle()) || !sameTree(after, after, gdformat.GodotStyle()) {
				t.Fatal("a tree differs from itself")
			}
			if sameTree(before, after, gdformat.GodotStyle()) {
				t.Fatal("different trees were reported as the same")
			}
		})
	}
}

// Every node type the comparison meets must contribute at least one field
// besides its position, or a change to it would go unseen.
func TestComparedFieldsSkipOnlySourceMetadata(t *testing.T) {
	skipped := map[string]bool{}
	for _, node := range []any{
		ast.File{}, ast.Comment{}, ast.Literal{}, ast.BinaryExpression{}, ast.FunctionDeclaration{},
		ast.VariableDeclaration{}, ast.IfStatement{}, ast.Branch{}, ast.MatchCase{}, ast.Parameter{},
		ast.NodePathExpression{}, ast.Trivia{}, ast.Base{},
	} {
		nodeType := reflect.TypeOf(node)
		compared := map[int]bool{}
		for _, field := range comparedFields(nodeType) {
			compared[field.index] = true
		}
		for index := range nodeType.NumField() {
			if !compared[index] {
				skipped[nodeType.Name()+"."+nodeType.Field(index).Name] = true
			}
		}
	}
	for _, name := range []string{"File.Name", "Base.SourceSpan", "Trivia.BlankLinesBefore", "FunctionDeclaration.NameSpan", "Branch.KeywordSpan"} {
		if !skipped[name] {
			t.Errorf("%s is compared, want it skipped", name)
		}
		delete(skipped, name)
	}
	for name := range skipped {
		if len(name) < 4 || name[len(name)-4:] != "Span" {
			t.Errorf("%s is skipped, want it compared", name)
		}
	}
}

func TestSameTreeKeepsAnInferredParameterDefaultAcrossSpacing(t *testing.T) {
	before := parseTree(t, "func f(x:=1, y=2):\n\tpass\n")
	after := parseTree(t, "func f(x := 1, y = 2):\n\tpass\n")
	if !sameTree(before, after, gdformat.GodotStyle()) {
		t.Fatal("parameters that differ only in spacing were reported as different")
	}
}
