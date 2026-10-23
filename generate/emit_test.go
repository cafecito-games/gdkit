package generate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emitOf runs one generator over the single file a.gd.
func emitOf(t *testing.T, emitter Emitter, source string) string {
	t.Helper()
	return emitFor(t, "a.gd", emitter, map[string]string{"a.gd": source})
}

// emitFor runs one generator over the named class of a multi-file project.
func emitFor(t *testing.T, id string, emitter Emitter, files map[string]string) string {
	t.Helper()
	index := indexOf(t, withCompanionAddon(files))
	index.HelpersClass = index.ByClassName[helpersClassName]
	capabilities := Resolve(index, map[string][]string{id: {emitter.Name()}}, nil)
	text, diagnostics := emitter.Emit(index.Classes[id], index, capabilities)
	require.Empty(t, diagnostics, "emit produced diagnostics")
	return text
}

func TestToStringEmitsSelfQualifiedFields(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	want := "func _to_string() -> String:\n\treturn \"Hex(q=%s, r=%s)\" % [self.q, self.r]\n"
	assert.Equal(t, want, got)
}

func TestToStringUsesTheFileNameWithoutAClassName(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "extends RefCounted\n\nvar q: int\n")
	assert.Contains(t, got, `"a(q=%s)"`, "the file base name was not used")
}

func TestToStringWithNoFields(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "class_name Hex\nextends RefCounted\n")
	want := "func _to_string() -> String:\n\treturn \"Hex()\"\n"
	assert.Equal(t, want, got)
}

// A field whose name collides with the parameter must still be read as a
// field, which is what self. is for.
func TestEmittersQualifyAFieldNamedLikeTheParameter(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\nvar p_other: int\n"
	got := emitOf(t, toStringGenerator{}, source)
	assert.Contains(t, got, "self.p_other")
	got = emitOf(t, equalsGenerator{}, source)
	assert.Contains(t, got, "self.p_other == p_other.p_other")
}

func TestEqualsEmitsTheScriptIdentityGuard(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	want := "func equals(p_other: Variant) -> bool:\n" +
		"\tif not (p_other is Object):\n\t\treturn false\n" +
		"\tif p_other.get_script() != get_script():\n\t\treturn false\n" +
		"\treturn self.q == p_other.q and self.r == p_other.r\n"
	assert.Equal(t, want, got)
}

// Script identity is symmetric, which is the property a value object needs.
// "is Hex" would make Hex.new().equals(SubHex.new()) true and the reverse
// false, and could not name a class with no class_name at all.
func TestEqualsDoesNotUseAnIsCheck(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\n")
	assert.NotContains(t, got, "is Hex")
}

func TestEqualsComposesWithTheParentProvider(t *testing.T) {
	got := emitFor(t, "sub.gd", equalsGenerator{}, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd": "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	assert.Contains(t, got, "\tif not super.equals(p_other):\n\t\treturn false\n", "emit did not compose with Base.equals")
	assert.Contains(t, got, "return self.r == p_other.r", "emit did not compare its own field")
	assert.NotContains(t, got, "self.q", "emit reached into the parent's field instead of composing")
}

func TestEqualsOmitsSuperWithNoParentProvider(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\n")
	assert.NotContains(t, got, "super.equals", "emit called super with no provider")
}

// The guards have already established same-script identity, so a class with no
// state of its own has nothing further to compare.
func TestEqualsWithNoFieldsReturnsTrue(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Marker\nextends RefCounted\n")
	assert.True(t, strings.HasSuffix(got, "\treturn true\n"), "want a trailing return true")
}

// A subclass with no fields of its own still has to compose, or it compares
// nothing at all.
func TestEqualsComposesEvenWithNoLocalFields(t *testing.T) {
	got := emitFor(t, "sub.gd", equalsGenerator{}, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd": "class_name Sub\nextends Base\n",
	})
	assert.Contains(t, got, "super.equals(p_other)", "a field-free subclass did not compose")
}

// Emission order is the registry's, not the directive's, so a region's content
// depends only on the class.
func TestRegistryOrderIsIndependentOfTheDirective(t *testing.T) {
	forward := inRegistryOrder([]string{"to_string", "equals", "deep_equals"})
	reverse := inRegistryOrder([]string{"deep_equals", "equals", "to_string"})
	require.Len(t, forward, 3)
	require.Len(t, reverse, 3)
	for index := range forward {
		assert.Equal(t, reverse[index].Name(), forward[index].Name())
	}
	assert.Equal(t, "to_string", forward[0].Name())
}

func TestGeneratorNamesAreTheRegisteredOnes(t *testing.T) {
	names := GeneratorNames()
	want := []string{"deep_equals", "equals", "to_string"}
	assert.Equal(t, want, names)
	// hash is the obvious next member of the family and is not implemented, so
	// naming it must be an error rather than a silent no-op.
	assert.False(t, isGeneratorName("hash"), "hash is not registered and must not be accepted")
}
