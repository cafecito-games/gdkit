package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// GDScript's runtime lookup does not walk past an incompatible override, so
// neither may Provider. A has a good equals, B shadows it with a wrong one, so
// C has no provider rather than A.
func TestProviderStopsAtAnIncompatibleDeclaration(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends RefCounted\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"b.gd": "class_name B\nextends A\n\nfunc equals(x, y) -> bool:\n\treturn true\n",
		"c.gd": "class_name C\nextends B\n",
	})
	capabilities := Resolve(index, nil, nil)
	_, ok := capabilities.Provider(index, "c.gd", equalsSignature)
	assert.False(t, ok, "the walk passed an incompatible override")
	_, ok = capabilities.Provider(index, "b.gd", equalsSignature)
	assert.False(t, ok, "B's own equals is incompatible")
	provider, ok := capabilities.Provider(index, "a.gd", equalsSignature)
	assert.True(t, ok)
	assert.Equal(t, "a.gd", provider)
}

// An orphan's method physically exists, so the walk must not fall through as
// though it were absent; and --prune may delete it, so nothing may compose with
// it. Barrier is the only answer safe both before and after pruning.
func TestProviderTreatsAnOrphanedRegionAsABarrier(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\n" +
			"# gdkit:generated:begin\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n# gdkit:generated:end\n",
		"sub.gd": "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	// Base does not opt in, so its region is orphaned.
	capabilities := Resolve(index, map[string][]string{"sub.gd": {"equals"}}, nil)
	_, ok := capabilities.Provider(index, "base.gd", equalsSignature)
	assert.False(t, ok, "an orphaned region supplied a capability")
	assert.False(t, capabilities.Realizable("sub.gd", equalsSignature), "Sub composed with an orphaned parent")
}

// A newly requested parent declares nothing yet, so without the virtual
// provider row a first adoption across a hierarchy could never start.
func TestANewlyRequestedParentIsAVirtualProvider(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	requested := map[string][]string{"base.gd": {"equals"}, "sub.gd": {"equals"}}
	capabilities := Resolve(index, requested, nil)
	provider, ok := capabilities.Provider(index, "base.gd", equalsSignature)
	assert.True(t, ok, "Base is not a virtual provider")
	assert.Equal(t, "base.gd", provider)
	assert.True(t, capabilities.Realizable("sub.gd", equalsSignature), "Sub could not compose with a parent that is itself newly requested")
}

// The descendant rule demotes B, which moves provider(C) from B to A, which
// must then demote A. A single parents-first pass settles A too early.
func TestDemotionCascadesUpTheHierarchy(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends RefCounted\n\nvar q: int\n",
		"b.gd": "class_name B\nextends A\n\nvar r: int\n",
		"c.gd": "class_name C\nextends B\n\nvar s: int\n",
	})
	requested := map[string][]string{"a.gd": {"equals"}, "b.gd": {"equals"}}
	capabilities := Resolve(index, requested, nil)
	assert.False(t, capabilities.Realizable("b.gd", equalsSignature), "B was not demoted by its field-adding descendant C")
	assert.False(t, capabilities.Realizable("a.gd", equalsSignature), "A was not demoted after B's demotion moved provider(C) to A")
}

// A blocked generator must not erase a hand-written provider something else
// relies on.
func TestAHandwrittenProviderSurvivesABlockedGenerator(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	requested := map[string][]string{"base.gd": {"equals"}, "sub.gd": {"equals"}}
	capabilities := Resolve(index, requested, map[string]bool{"base.gd": true})
	assert.True(t, capabilities.Realizable("base.gd", equalsSignature), "a blocked generator erased the hand-written equals")
	assert.True(t, capabilities.Realizable("sub.gd", equalsSignature), "Sub was demoted although its parent still provides equals")
}

func TestResolveRefusesAClassWhoseAncestryHasFieldsButNoProvider(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"sub.gd": {"equals"}}, nil)
	assert.False(t, capabilities.Realizable("sub.gd", equalsSignature), "Sub generated equals with no provider for Base's q")
}

// An override is a barrier: provider(D) becomes B, so A is not refused for D's
// sake, and B is checked against its own descendants instead.
func TestAnOverridingIntermediateIsABarrier(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends RefCounted\n\nvar q: int\n",
		"b.gd": "class_name B\nextends A\n\nvar r: int\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"d.gd": "class_name D\nextends B\n\nvar s: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	assert.True(t, capabilities.Realizable("a.gd", equalsSignature), "A was refused although B overrides equals and shields D")
}

// A field-free descendant adds no state an inherited comparison could miss.
func TestAFieldFreeDescendantDoesNotRefuse(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n",
	})
	capabilities := Resolve(index, map[string][]string{"base.gd": {"equals"}}, nil)
	assert.True(t, capabilities.Realizable("base.gd", equalsSignature), "a field-free subclass caused a refusal")
}

// A grandchild inheriting the base's method is as unsound as a child.
func TestAFieldAddingGrandchildRefusesTheBase(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":  "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"mid.gd":   "class_name Mid\nextends Base\n",
		"grand.gd": "class_name Grand\nextends Mid\n\nvar s: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"base.gd": {"equals"}}, nil)
	assert.False(t, capabilities.Realizable("base.gd", equalsSignature), "a field-adding grandchild did not refuse the base")
}

func TestResolveRefusesAClassWhoseAncestryReachesACycle(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends B\n",
		"b.gd": "class_name B\nextends A\n",
		"c.gd": "class_name C\nextends A\n\nvar q: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"c.gd": {"equals"}}, nil)
	assert.False(t, capabilities.Realizable("c.gd", equalsSignature), "a class above an inheritance cycle generated equals")
}

// Reporting a parse failure is not enough: write applies candidates despite
// unrelated diagnostics, so the capability has to be withdrawn.
func TestAUniverseParseFailureDemotesEquals(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd":      "class_name Hex\nextends RefCounted\n\nvar q: int\n",
		"broken.gd": "func (((\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	assert.False(t, capabilities.Realizable("a.gd", equalsSignature), "equals stayed realizable with an unreadable file in the universe")
	assert.NotEmpty(t, capabilities.UniverseCause, "no cause recorded, so the refusal cannot name the file to fix")
}

// An unresolvable project base leaves the graph incomplete in the same way.
func TestAnUnresolvableBaseDemotesEquals(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd":     "class_name Hex\nextends RefCounted\n\nvar q: int\n",
		"stray.gd": "class_name Stray\nextends \"res://gone.gd\"\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	assert.False(t, capabilities.Realizable("a.gd", equalsSignature), "equals generated with an incomplete inheritance graph")
	assert.NotEmpty(t, capabilities.UniverseCause, "no cause recorded naming the unresolved class")
}

// _to_string is subject to local blockers alone: none of the
// inheritance-sensitive conditions can affect it.
func TestToStringIsNotRefusedByInheritanceConditions(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":    "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":     "class_name Sub\nextends Base\n\nvar r: int\n",
		"cycle_a.gd": "class_name CycleA\nextends CycleB\n",
		"cycle_b.gd": "class_name CycleB\nextends CycleA\n",
		"broken.gd":  "func (((\n",
		"stray.gd":   "class_name Stray\nextends \"res://gone.gd\"\n",
	})
	capabilities := Resolve(index, map[string][]string{"sub.gd": {"to_string"}}, nil)
	assert.True(t, capabilities.Realizable("sub.gd", toStringSignature), "to_string was refused by a condition that cannot affect it")
}

// An inner class can never be a generation target.
func TestAnInnerClassIsNeverRealizable(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\nclass Nested:\n\tvar q: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd#Nested": {"to_string"}}, nil)
	assert.False(t, capabilities.Realizable("a.gd#Nested", toStringSignature), "an inner class was realizable")
}
