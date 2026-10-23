package generate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const valueObjectPair = "class_name Coordinate\nextends RefCounted\n\nvar q: int\n"

func TestDeepEqualsRecursesIntoAProjectClassField(t *testing.T) {
	got := emitFor(t, "unit.gd", deepEqualsGenerator{}, map[string]string{
		"coordinate.gd": valueObjectPair,
		"unit.gd":       "class_name Unit\nextends RefCounted\n\nvar name: String\nvar position: Coordinate\n",
	})
	// The same instance is strictly equal, which is also what makes the
	// realistic recursive shapes terminate.
	assert.True(t, strings.HasPrefix(got, "func deep_equals(p_other: Variant) -> bool:\n\tif self == p_other:\n\t\treturn true\n"), "no identity short-circuit")
	for _, name := range []string{"name", "position"} {
		want := "\tif not GDKitEquality.deep_equals(self." + name + ", p_other." + name + "):\n\t\treturn false\n"
		assert.Contains(t, got, want, name+" did not go through the helper")
	}
}

// max-returns reports a function at its last return, so the one suppression
// sits on the final return and names only that rule.
func TestDeepEqualsSuppressesOnlyMaxReturnsOnItsFinalReturn(t *testing.T) {
	got := emitFor(t, "unit.gd", deepEqualsGenerator{}, map[string]string{
		"unit.gd": "class_name Unit\nextends RefCounted\n\nvar name: String\n",
	})
	assert.True(t, strings.HasSuffix(got, "\treturn true  # gdkit:ignore = max-returns\n"), "final return carries no max-returns suppression")
	assert.Equal(t, 1, strings.Count(got, "gdkit:ignore"), "want exactly one suppression")
}

func TestDeepEqualsComposesWithTheParentProvider(t *testing.T) {
	got := emitFor(t, "hex.gd", deepEqualsGenerator{}, map[string]string{
		"coordinate.gd": "class_name Coordinate\nextends RefCounted\n\nvar q: int\n\n" +
			"func deep_equals(p_other: Variant) -> bool:\n\treturn true\n",
		"hex.gd": "class_name Hex\nextends Coordinate\n\nvar terrain: String\n",
	})
	assert.Contains(t, got, "\tif not super.deep_equals(p_other):\n\t\treturn false\n", "did not compose with the parent")
}

// A cyclic value object is pathological and cannot be shown to terminate, so
// the class is refused rather than the generator growing a visited set.
func TestACyclicFieldTypeGraphIsRefused(t *testing.T) {
	report := checkProject(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"node.gd": "class_name TreeNode\nextends RefCounted\n\n# gdkit:generate = deep_equals\n" +
			"var parent: TreeNode\nvar label: String\n",
	}))
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	assert.Contains(t, diagnostic.Message, "cycle", "the cycle was not named")
	for _, result := range report.Results {
		assert.False(t, result.Path == "node.gd" && result.Changed, "a cyclic class produced a candidate")
	}
}

func TestATwoClassFieldCycleIsRefused(t *testing.T) {
	report := checkProject(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"a.gd": "class_name Alpha\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar beta: Beta\n",
		"b.gd": "class_name Beta\nextends RefCounted\n\nvar alpha: Alpha\n",
	}))
	assertDiagnostic(t, report, ruleUnsupported)
}

// A container of the class's own type is not a cycle. The generated code does
// now recurse into a container, but tree-shaped data terminates through the
// identity check, and refusing this shape would refuse most of what
// deep_equals is for.
func TestAContainerOfTheSameClassIsNotACycle(t *testing.T) {
	report := checkProject(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"a.gd": "class_name Branch\nextends RefCounted\n\n# gdkit:generate = deep_equals\n" +
			"var children: Array[Branch]\nvar label: String\n",
	}))
	for _, diagnostic := range report.Diagnostics {
		assert.Equal(t, ruleStale, diagnostic.Rule, "a container of the same class was read as a cycle")
	}
	assert.True(t, report.HasChanges(), "nothing was generated")
}

// deep_equals is a registered generator name now, in the directive and in
// configuration.
func TestDeepEqualsIsARegisteredGeneratorName(t *testing.T) {
	require.True(t, isGeneratorName("deep_equals"), "deep_equals is not registered")
	names := GeneratorNames()
	assert.Len(t, names, 3)
	// Registry order decides the region's content, not the directive's order.
	ordered := inRegistryOrder([]string{"deep_equals", "equals", "to_string"})
	got := []string{ordered[0].Name(), ordered[1].Name(), ordered[2].Name()}
	want := []string{"to_string", "equals", "deep_equals"}
	for index := range want {
		assert.Equal(t, want[index], got[index])
	}
}

func TestDeepEqualsRoutesEveryFieldThroughTheHelper(t *testing.T) {
	contents := applyOnce(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"other.gd": "class_name Other\nextends RefCounted\n",
		"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = deep_equals\n" +
			"var origin: Other\nvar children: Array[Thing]\nvar table: Dictionary[String, Thing]\n" +
			"var loose = []\nvar count: int\n",
	}))
	text := contents["a.gd"]
	for _, field := range []string{"origin", "children", "table", "loose", "count"} {
		want := "if not GDKitEquality.deep_equals(self." + field + ", p_other." + field + "):"
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, "has_method", "the per-class method still dispatches inline; that belongs to the helper now")
	for _, guard := range []string{
		"if self == p_other:",
		"if not p_other is Object:",
		"if p_other.get_script() != get_script():",
	} {
		assert.Contains(t, text, guard)
	}
}

func TestDeepEqualsComposesWithAnAncestorThroughTheHelper(t *testing.T) {
	contents := applyOnce(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"base.gd":  "class_name Base\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar a: int\n",
		"child.gd": "class_name Child\nextends Base\n\n# gdkit:generate = deep_equals\nvar b: int\n",
	}))
	assert.Contains(t, contents["child.gd"], "if not super.deep_equals(p_other):", "the super composition was lost")
}
