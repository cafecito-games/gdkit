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
	// A String is not a project class, so it compares with ==.
	if !strings.Contains(got, "\tif self.name != p_other.name:\n\t\treturn false\n") {
		t.Errorf("a non-project field did not use ==:\n%s", got)
	}
	// is Object comes before has_method, which is declared on Object.
	if !strings.Contains(got, "if self.position is Object and self.position.has_method(\"deep_equals\"):") {
		t.Errorf("no guarded dispatch for the object field:\n%s", got)
	}
	// A field type with only a hand-written equals is used, not refused.
	if !strings.Contains(got, "elif self.position is Object and self.position.has_method(\"equals\"):") {
		t.Errorf("no fallback to a hand-written equals:\n%s", got)
	}
	// null.has_method(...) is a runtime error. Inside the inequality block at
	// most one side can be null, so either being null settles it.
	if !strings.Contains(got, "if self.position == null or p_other.position == null:") {
		t.Errorf("the dispatch is not null-aware:\n%s", got)
	}
}

// An unknown type could be anything, so it dispatches rather than assuming.
func TestDeepEqualsDispatchesOnFieldsWithNoStaticType(t *testing.T) {
	got := emitOf(t, deepEqualsGenerator{},
		"class_name Bag\nextends RefCounted\n\nvar loose\nvar anything: Variant\nvar inferred := 0\n")
	for _, name := range []string{"loose", "anything", "inferred"} {
		if !strings.Contains(got, "self."+name+" is Object and self."+name+".has_method(\"deep_equals\")") {
			t.Errorf("%s did not dispatch:\n%s", name, got)
		}
	}
}

// A builtin and an engine class both compare with ==: a builtin by value, and
// an engine object has no value equality to recurse into.
func TestDeepEqualsDoesNotDispatchOnBuiltinOrEngineTypes(t *testing.T) {
	got := emitOf(t, deepEqualsGenerator{},
		"class_name Thing\nextends RefCounted\n\nvar at: Vector2\nvar node: Node\nvar many: Array\n")
	for _, name := range []string{"at", "node", "many"} {
		if strings.Contains(got, "self."+name+".has_method") {
			t.Errorf("%s dispatched when == would do:\n%s", name, got)
		}
		if !strings.Contains(got, "\tif self."+name+" != p_other."+name+":\n\t\treturn false\n") {
			t.Errorf("%s did not use ==:\n%s", name, got)
		}
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
	report := checkProject(t, DefaultConfig(), map[string]string{
		"node.gd": "class_name TreeNode\nextends RefCounted\n\n# gdkit:generate = deep_equals\n" +
			"var parent: TreeNode\nvar label: String\n",
	})
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	if !strings.Contains(diagnostic.Message, "cycle") {
		t.Errorf("message = %q, want the cycle named", diagnostic.Message)
	}
	if report.HasChanges() {
		t.Error("a cyclic class produced a candidate")
	}
}

func TestATwoClassFieldCycleIsRefused(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Alpha\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar beta: Beta\n",
		"b.gd": "class_name Beta\nextends RefCounted\n\nvar alpha: Alpha\n",
	})
	assertDiagnostic(t, report, ruleUnsupported)
}

// An Array[T] is handed to "==", which Godot 4 evaluates by value, so the
// emitted code never recurses into its elements and a container must not make
// a graph look cyclic.
func TestAContainerOfTheSameClassIsNotACycle(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Branch\nextends RefCounted\n\n# gdkit:generate = deep_equals\n" +
			"var children: Array[Branch]\nvar label: String\n",
	})
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
