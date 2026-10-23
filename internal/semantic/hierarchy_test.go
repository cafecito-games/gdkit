package semantic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hierarchyAnalyzer builds an Analyzer over a small project hierarchy for the
// graph-accessor tests below. The engine only needs RefCounted/Object so the
// classes' terminal engine bases resolve.
func hierarchyAnalyzer(t *testing.T) *Analyzer {
	t.Helper()
	return NewAnalyzer(sources(t, map[string]string{
		"base.gd":       "class_name Base\nextends RefCounted\nfunc _helper():\n\tpass\nfunc helper():\n\tpass\n",
		"mid.gd":        "class_name Mid\nextends Base\n",
		"leaf.gd":       "extends Mid\nclass Inner:\n\tpass\n",
		"standalone.gd": "extends RefCounted\n",
	}), interfaceTestEngine(t))
}

func TestAnalyzerClassID(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	id, ok := analyzer.ClassID("base.gd")
	require.True(t, ok)
	require.Equal(t, "base.gd", id)
	id, ok = analyzer.ClassID("leaf.gd")
	require.True(t, ok)
	require.Equal(t, "leaf.gd", id)
	_, ok = analyzer.ClassID("missing.gd")
	require.False(t, ok)
}

func TestAnalyzerAncestorClassIDs(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	got := analyzer.AncestorClassIDs("leaf.gd")
	require.Equal(t, []string{"mid.gd", "base.gd"}, got)
	got = analyzer.AncestorClassIDs("mid.gd")
	require.Equal(t, []string{"base.gd"}, got)
	// A class whose base is an engine class has no project ancestor.
	require.Empty(t, analyzer.AncestorClassIDs("base.gd"))
	require.Empty(t, analyzer.AncestorClassIDs("standalone.gd"))
	require.Empty(t, analyzer.AncestorClassIDs("missing.gd"))
}

func TestAnalyzerDescendantClassIDs(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	got := analyzer.DescendantClassIDs("base.gd")
	require.Equal(t, []string{"leaf.gd", "mid.gd"}, got)
	got = analyzer.DescendantClassIDs("mid.gd")
	require.Equal(t, []string{"leaf.gd"}, got)
	require.Empty(t, analyzer.DescendantClassIDs("leaf.gd"))
}

func TestAnalyzerClassPath(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	path, ok := analyzer.ClassPath("base.gd")
	require.True(t, ok)
	require.Equal(t, "base.gd", path)
	// An inner class shares its enclosing file's path.
	path, ok = analyzer.ClassPath("leaf.gd#Inner")
	require.True(t, ok)
	require.Equal(t, "leaf.gd", path)
	_, ok = analyzer.ClassPath("missing.gd")
	require.False(t, ok)
}

func TestAnalyzerDeclaresMethod(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	for _, name := range []string{"_helper", "helper"} {
		assert.True(t, analyzer.DeclaresMethod("base.gd", name))
	}
	assert.False(t, analyzer.DeclaresMethod("base.gd", "missing"))
	// A descendant inherits the method but does not directly declare it.
	assert.False(t, analyzer.DeclaresMethod("mid.gd", "_helper"))
	assert.False(t, analyzer.DeclaresMethod("missing.gd", "_helper"))
}
