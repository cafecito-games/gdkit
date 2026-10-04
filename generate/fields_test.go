package generate

import (
	"slices"
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser"
)

func TestSelectFieldsTakesInstanceVarsInOrder(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n" +
		"var q: int\n" +
		"@export var name: String\n" +
		"var _cache: Dictionary  # gdkit:generate:ignore-field\n" +
		"# gdkit:generate:ignore-field\n" +
		"var skipped: int\n" +
		"@onready var label: Label\n" +
		"const ORIGIN := Vector2i()\n" +
		"static var count: int\n" +
		"var computed: int:\n\tget:\n\t\treturn 1\n"
	var got []string
	for _, field := range selectFieldsOf(t, source) {
		got = append(got, field.Name)
	}
	want := []string{"q", "name", "computed"}
	if !slices.Equal(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestSelectFieldsRecordsTheDeclaredType(t *testing.T) {
	fields := selectFieldsOf(t, "var q: int\nvar r := 0\nvar s\n")
	if len(fields) != 3 {
		t.Fatalf("fields = %+v, want three", fields)
	}
	if fields[0].Type != "int" || fields[0].Inferred {
		t.Errorf("q = %+v, want type int", fields[0])
	}
	// ":=" is static typing as far as Godot is concerned, but the inferred
	// type is not in the tree, so only the flag is recorded. deep_equals
	// depends on the distinction.
	if fields[1].Type != "" || !fields[1].Inferred {
		t.Errorf("r = %+v, want inferred with no type", fields[1])
	}
	if fields[2].Type != "" || fields[2].Inferred {
		t.Errorf("s = %+v, want untyped", fields[2])
	}
}

func TestSelectFieldsSkipsVarsInsideTheRegion(t *testing.T) {
	source := "var q: int\n\n# gdkit:generated:begin\nvar leaked: int\n# gdkit:generated:end\n"
	fields := selectFieldsOf(t, source)
	if len(fields) != 1 || fields[0].Name != "q" {
		t.Errorf("fields = %+v, want only q", fields)
	}
}

func TestSelectFieldsRecordsTheLine(t *testing.T) {
	fields := selectFieldsOf(t, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	if len(fields) != 2 || fields[0].Line != 4 || fields[1].Line != 5 {
		t.Errorf("fields = %+v, want lines 4 and 5", fields)
	}
}

func selectFieldsOf(t *testing.T, source string) []Field {
	t.Helper()
	file, err := gdparser.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	script := &project.Script{Path: "a.gd", Source: []byte(source), File: file, Lines: lineStartsOf(source)}
	span, _, err := FindRegion([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return SelectFields(file.Statements, script, span)
}

// lineStartsOf mirrors project's own line indexing for a hand-built Script.
func lineStartsOf(source string) []int {
	if source == "" {
		return nil
	}
	starts := []int{0}
	for offset := 0; offset < len(source); offset++ {
		if source[offset] == '\n' && offset+1 < len(source) {
			starts = append(starts, offset+1)
		}
	}
	return starts
}
