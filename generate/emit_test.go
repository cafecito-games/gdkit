package generate

import (
	"slices"
	"strings"
	"testing"
)

// emitOf runs one generator over the single file a.gd.
func emitOf(t *testing.T, emitter Emitter, source string) string {
	t.Helper()
	return emitFor(t, "a.gd", emitter, map[string]string{"a.gd": source})
}

// emitFor runs one generator over the named class of a multi-file project.
func emitFor(t *testing.T, id string, emitter Emitter, files map[string]string) string {
	t.Helper()
	index := indexOf(t, files)
	capabilities := Resolve(index, map[string][]string{id: {emitter.Name()}}, nil)
	text, diagnostics := emitter.Emit(index.Classes[id], index, capabilities)
	if len(diagnostics) > 0 {
		t.Fatalf("emit produced diagnostics: %+v", diagnostics)
	}
	return text
}

func TestToStringEmitsSelfQualifiedFields(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	want := "func _to_string() -> String:\n\treturn \"Hex(q=%s, r=%s)\" % [self.q, self.r]\n"
	if got != want {
		t.Errorf("emit =\n%q\nwant\n%q", got, want)
	}
}

func TestToStringUsesTheFileNameWithoutAClassName(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "extends RefCounted\n\nvar q: int\n")
	if !strings.Contains(got, `"a(q=%s)"`) {
		t.Errorf("emit = %q, want the file base name", got)
	}
}

func TestToStringWithNoFields(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "class_name Hex\nextends RefCounted\n")
	want := "func _to_string() -> String:\n\treturn \"Hex()\"\n"
	if got != want {
		t.Errorf("emit = %q, want %q", got, want)
	}
}

// A field whose name collides with the parameter must still be read as a
// field, which is what self. is for.
func TestEmittersQualifyAFieldNamedLikeTheParameter(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\nvar p_other: int\n"
	if got := emitOf(t, toStringGenerator{}, source); !strings.Contains(got, "self.p_other") {
		t.Errorf("to_string = %q, want self.p_other", got)
	}
	got := emitOf(t, equalsGenerator{}, source)
	if !strings.Contains(got, "self.p_other == p_other.p_other") {
		t.Errorf("equals = %q, want self.p_other == p_other.p_other", got)
	}
}

func TestEqualsEmitsTheScriptIdentityGuard(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	want := "func equals(p_other: Variant) -> bool:\n" +
		"\tif not (p_other is Object):\n\t\treturn false\n" +
		"\tif p_other.get_script() != get_script():\n\t\treturn false\n" +
		"\treturn self.q == p_other.q and self.r == p_other.r\n"
	if got != want {
		t.Errorf("emit =\n%s\nwant\n%s", got, want)
	}
}

// Script identity is symmetric, which is the property a value object needs.
// "is Hex" would make Hex.new().equals(SubHex.new()) true and the reverse
// false, and could not name a class with no class_name at all.
func TestEqualsDoesNotUseAnIsCheck(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\n")
	if strings.Contains(got, "is Hex") {
		t.Errorf("emit used an is check: %q", got)
	}
}

func TestEqualsComposesWithTheParentProvider(t *testing.T) {
	got := emitFor(t, "sub.gd", equalsGenerator{}, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd": "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	if !strings.Contains(got, "\tif not super.equals(p_other):\n\t\treturn false\n") {
		t.Errorf("emit did not compose with Base.equals:\n%s", got)
	}
	if !strings.Contains(got, "return self.r == p_other.r") {
		t.Errorf("emit did not compare its own field:\n%s", got)
	}
	if strings.Contains(got, "self.q") {
		t.Errorf("emit reached into the parent's field instead of composing:\n%s", got)
	}
}

func TestEqualsOmitsSuperWithNoParentProvider(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\n")
	if strings.Contains(got, "super.equals") {
		t.Errorf("emit called super with no provider:\n%s", got)
	}
}

// The guards have already established same-script identity, so a class with no
// state of its own has nothing further to compare.
func TestEqualsWithNoFieldsReturnsTrue(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Marker\nextends RefCounted\n")
	if !strings.HasSuffix(got, "\treturn true\n") {
		t.Errorf("emit = %q, want a trailing return true", got)
	}
}

// A subclass with no fields of its own still has to compose, or it compares
// nothing at all.
func TestEqualsComposesEvenWithNoLocalFields(t *testing.T) {
	got := emitFor(t, "sub.gd", equalsGenerator{}, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd": "class_name Sub\nextends Base\n",
	})
	if !strings.Contains(got, "super.equals(p_other)") {
		t.Errorf("a field-free subclass did not compose:\n%s", got)
	}
}

// Emission order is the registry's, not the directive's, so a region's content
// depends only on the class.
func TestRegistryOrderIsIndependentOfTheDirective(t *testing.T) {
	forward := inRegistryOrder([]string{"to_string", "equals", "deep_equals"})
	reverse := inRegistryOrder([]string{"deep_equals", "equals", "to_string"})
	if len(forward) != 3 || len(reverse) != 3 {
		t.Fatalf("got %d and %d generators", len(forward), len(reverse))
	}
	for index := range forward {
		if forward[index].Name() != reverse[index].Name() {
			t.Errorf("order differed at %d: %q vs %q", index, forward[index].Name(), reverse[index].Name())
		}
	}
	if forward[0].Name() != "to_string" {
		t.Errorf("first = %q, want to_string", forward[0].Name())
	}
}

func TestGeneratorNamesAreTheRegisteredOnes(t *testing.T) {
	names := GeneratorNames()
	want := []string{"deep_equals", "equals", "to_string"}
	if !slices.Equal(names, want) {
		t.Errorf("GeneratorNames() = %v, want %v", names, want)
	}
	// hash is the obvious next member of the family and is not implemented, so
	// naming it must be an error rather than a silent no-op.
	if isGeneratorName("hash") {
		t.Error("hash is not registered and must not be accepted")
	}
}
