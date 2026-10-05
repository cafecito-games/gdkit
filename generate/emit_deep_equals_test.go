package generate

import (
	"strings"
	"testing"
)

const valueObjectPair = "class_name Coordinate\nextends RefCounted\n\nvar q: int\n"

func TestDeepEqualsRecursesIntoAProjectClassField(t *testing.T) {
	got := emitFor(t, "unit.gd", deepEqualsGenerator{}, map[string]string{
		"coordinate.gd": valueObjectPair,
		"unit.gd":       "class_name Unit\nextends RefCounted\n\nvar name: String\nvar position: Coordinate\n",
	})
	// The same instance is strictly equal, which is also what makes the
	// realistic recursive shapes terminate.
	if !strings.HasPrefix(got, "func deep_equals(p_other: Variant) -> bool:\n\tif self == p_other:\n\t\treturn true\n") {
		t.Errorf("no identity short-circuit:\n%s", got)
	}
	for _, name := range []string{"name", "position"} {
		want := "\tif not GDKitEquality.deep_equals(self." + name + ", p_other." + name + "):\n\t\treturn false\n"
		if !strings.Contains(got, want) {
			t.Errorf("%s did not go through the helper:\n%s", name, got)
		}
	}
}

// max-returns reports a function at its last return, so the one suppression
// sits on the final return and names only that rule.
func TestDeepEqualsSuppressesOnlyMaxReturnsOnItsFinalReturn(t *testing.T) {
	got := emitFor(t, "unit.gd", deepEqualsGenerator{}, map[string]string{
		"unit.gd": "class_name Unit\nextends RefCounted\n\nvar name: String\n",
	})
	if !strings.HasSuffix(got, "\treturn true  # gdkit:ignore = max-returns\n") {
		t.Errorf("final return carries no max-returns suppression:\n%s", got)
	}
	if strings.Count(got, "gdkit:ignore") != 1 {
		t.Errorf("want exactly one suppression:\n%s", got)
	}
}

func TestDeepEqualsComposesWithTheParentProvider(t *testing.T) {
	got := emitFor(t, "hex.gd", deepEqualsGenerator{}, map[string]string{
		"coordinate.gd": "class_name Coordinate\nextends RefCounted\n\nvar q: int\n\n" +
			"func deep_equals(p_other: Variant) -> bool:\n\treturn true\n",
		"hex.gd": "class_name Hex\nextends Coordinate\n\nvar terrain: String\n",
	})
	if !strings.Contains(got, "\tif not super.deep_equals(p_other):\n\t\treturn false\n") {
		t.Errorf("did not compose with the parent:\n%s", got)
	}
}

// A cyclic value object is pathological and cannot be shown to terminate, so
// the class is refused rather than the generator growing a visited set.
func TestACyclicFieldTypeGraphIsRefused(t *testing.T) {
	report := checkProject(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"node.gd": "class_name TreeNode\nextends RefCounted\n\n# gdkit:generate = deep_equals\n" +
			"var parent: TreeNode\nvar label: String\n",
	}))
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	if !strings.Contains(diagnostic.Message, "cycle") {
		t.Errorf("message = %q, want the cycle named", diagnostic.Message)
	}
	for _, result := range report.Results {
		if result.Path == "node.gd" && result.Changed {
			t.Error("a cyclic class produced a candidate")
		}
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
		if diagnostic.Rule != ruleStale {
			t.Errorf("a container of the same class was read as a cycle: %s", diagnostic)
		}
	}
	if !report.HasChanges() {
		t.Error("nothing was generated")
	}
}

// deep_equals is a registered generator name now, in the directive and in
// configuration.
func TestDeepEqualsIsARegisteredGeneratorName(t *testing.T) {
	if !isGeneratorName("deep_equals") {
		t.Fatal("deep_equals is not registered")
	}
	names := GeneratorNames()
	if len(names) != 3 {
		t.Errorf("GeneratorNames() = %v, want three", names)
	}
	// Registry order decides the region's content, not the directive's order.
	ordered := inRegistryOrder([]string{"deep_equals", "equals", "to_string"})
	got := []string{ordered[0].Name(), ordered[1].Name(), ordered[2].Name()}
	want := []string{"to_string", "equals", "deep_equals"}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("order = %v, want %v", got, want)
		}
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
		if !strings.Contains(text, want) {
			t.Errorf("missing %q\ngot:\n%s", want, text)
		}
	}
	if strings.Contains(text, "has_method") {
		t.Error("the per-class method still dispatches inline; that belongs to the helper now")
	}
	for _, guard := range []string{
		"if self == p_other:",
		"if not p_other is Object:",
		"if p_other.get_script() != get_script():",
	} {
		if !strings.Contains(text, guard) {
			t.Errorf("guard %q was lost", guard)
		}
	}
}

func TestDeepEqualsComposesWithAnAncestorThroughTheHelper(t *testing.T) {
	contents := applyOnce(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"base.gd":  "class_name Base\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar a: int\n",
		"child.gd": "class_name Child\nextends Base\n\n# gdkit:generate = deep_equals\nvar b: int\n",
	}))
	if !strings.Contains(contents["child.gd"], "if not super.deep_equals(p_other):") {
		t.Errorf("the super composition was lost\ngot:\n%s", contents["child.gd"])
	}
}
