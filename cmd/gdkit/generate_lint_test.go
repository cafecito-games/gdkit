package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeGeneratedDeepEquals opts a class with the given field count into
// deep_equals, beside the companion addon, and runs gen write over it.
func writeGeneratedDeepEquals(t *testing.T, fieldCount int) string {
	t.Helper()
	root := t.TempDir()
	writeCLIFile(t, root, "project.godot", "[application]\n")
	writeCLIFile(t, root, "addons/gdkit/equality_helpers.gd",
		"class_name GDKitEquality\nextends RefCounted\n\n\n"+
			"static func deep_equals(p_lhs: Variant, p_rhs: Variant) -> bool:\n\treturn p_lhs == p_rhs\n")
	var source strings.Builder
	source.WriteString("class_name Wide\nextends RefCounted\n\n# gdkit:generate = deep_equals\n")
	for index := 1; index <= fieldCount; index++ {
		fmt.Fprintf(&source, "var field_%d: int\n", index)
	}
	writeCLIFile(t, root, "wide.gd", source.String())
	var stdout, stderr bytes.Buffer
	code := run([]string{"gen", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	return root
}

// gen and lint run over one project on default configuration must agree: the
// generated deep_equals returns once per field, which is more than max-returns
// allows, and a project cannot have caused or avoided that.
func TestGeneratedDeepEqualsIsCleanUnderDefaultLintFormatAndGen(t *testing.T) {
	root := writeGeneratedDeepEquals(t, 10)
	for _, command := range []string{"lint", "format", "gen"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{command, "check", root}, &stdout, &stderr)
		assert.Equal(t, 0, code, command)
	}
}

// The suppression is scoped to the generated function, so a hand-written
// function in the same file with too many returns is still reported.
func TestGeneratedSuppressionDoesNotHideAHandWrittenFunction(t *testing.T) {
	root := writeGeneratedDeepEquals(t, 10)
	var source strings.Builder
	source.WriteString(readCLIFile(t, root, "wide.gd") + "\n\nfunc sprawl(p_value: int) -> int:\n")
	for index := 1; index <= 8; index++ {
		fmt.Fprintf(&source, "\tif p_value == %d:\n\t\treturn %d\n", index, index)
	}
	source.WriteString("\treturn 0\n")
	writeCLIFile(t, root, "wide.gd", source.String())
	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), `Function "sprawl"`)
	assert.NotContains(t, stdout.String(), `"deep_equals"`)
}
