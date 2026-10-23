package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/require"
)

// parsedScript loads one a.gd script through project.Load so it carries a
// parsed tree, the way a real lint run hands a script to a rule.
func parsedScript(tb testing.TB, source string) *project.Script {
	tb.Helper()
	root := tb.TempDir()
	err := os.WriteFile(filepath.Join(root, "a.gd"), []byte(source), 0o644)
	require.NoError(tb, err)
	snapshot, err := project.Load(project.Config{
		Root:            root,
		SourceRoots:     []string{"."},
		Exclude:         []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"},
		HonorIgnoreFile: true,
	})
	require.NoError(tb, err)
	script := snapshot.Scripts["a.gd"]
	require.NotNil(tb, script, "fixture did not parse")
	require.NotNil(tb, script.File, "fixture did not parse")
	return script
}

func TestDeclarationsForCachesPerScript(t *testing.T) {
	script := parsedScript(t, gd(`extends Node
class_name Thing

func doThing(BadArg):
    var Bad = 1
    pass

signal bad_signal

var bad_var = 1
`))
	context := &Context{}
	first := context.declarationsFor(script)
	require.NotEmpty(t, first, "expected name declarations")
	second := context.declarationsFor(script)
	// A cached collection returns the same backing array; a re-collection
	// allocates a fresh slice.
	require.Same(t, &first[0], &second[0], "declarationsFor must cache the collected slice per script")
}

func TestTypingSitesForCachesPerScript(t *testing.T) {
	script := parsedScript(t, gd(`extends Node

func do_thing(bad_arg):
    var bad = 1
    pass
`))
	context := &Context{}
	first := context.typingSitesFor(script)
	require.NotEmpty(t, first, "expected typing sites")
	second := context.typingSitesFor(script)
	require.Same(t, &first[0], &second[0], "typingSitesFor must cache the collected slice per script")
}

// benchmarkScript returns a script with enough declarations that a full walk is
// not free, so the shared-walk and repeated-walk subtests separate.
func benchmarkScript(b *testing.B) *project.Script {
	var source strings.Builder
	source.WriteString("extends Node\nclass_name Bench\n\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&source, "func method_%d(arg_%d):\n\tvar local_%d = %d\n\treturn local_%d\n\n", i, i, i, i, i)
	}
	return parsedScript(b, source.String())
}

// BenchmarkNameRuleCollection compares one shared collector walk per script
// against the previous behavior, which walked the tree once per name rule.
func BenchmarkNameRuleCollection(b *testing.B) {
	script := benchmarkScript(b)
	b.Run("shared-walk", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			context := &Context{}
			for range len(nameMessages) {
				_ = context.declarationsFor(script)
			}
		}
	})
	b.Run("repeated-walk", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for range len(nameMessages) {
				_ = collectNames(script)
			}
		}
	})
}
