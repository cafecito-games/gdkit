package generate

import (
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Equal(t, want, got)
}

func TestSelectFieldsRecordsTheDeclaredType(t *testing.T) {
	fields := selectFieldsOf(t, "var q: int\nvar r := 0\nvar s\n")
	require.Len(t, fields, 3)
	assert.Equal(t, "int", fields[0].Type)
	assert.False(t, fields[0].Inferred)
	// ":=" is static typing as far as Godot is concerned, but the inferred
	// type is not in the tree, so only the flag is recorded. deep_equals
	// depends on the distinction.
	assert.Empty(t, fields[1].Type)
	assert.True(t, fields[1].Inferred)
	assert.Empty(t, fields[2].Type)
	assert.False(t, fields[2].Inferred)
}

func TestSelectFieldsSkipsVarsInsideTheRegion(t *testing.T) {
	source := "var q: int\n\n# gdkit:generated:begin\nvar leaked: int\n# gdkit:generated:end\n"
	fields := selectFieldsOf(t, source)
	require.Len(t, fields, 1)
	assert.Equal(t, "q", fields[0].Name)
}

func TestSelectFieldsRecordsTheLine(t *testing.T) {
	fields := selectFieldsOf(t, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	require.Len(t, fields, 2)
	assert.Equal(t, 4, fields[0].Line)
	assert.Equal(t, 5, fields[1].Line)
}

func selectFieldsOf(t *testing.T, source string) []Field {
	t.Helper()
	file, err := gdparser.Parse([]byte(source))
	require.NoError(t, err)
	script := &project.Script{Path: "a.gd", Source: []byte(source), File: file, Lines: lineStartsOf(source)}
	span, _, err := FindRegion([]byte(source))
	require.NoError(t, err)
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
