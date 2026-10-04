package generate

import "testing"

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
	if provider, ok := capabilities.Provider(index, "c.gd", equalsSignature); ok {
		t.Errorf("Provider(C) = %q, want none: the walk passed an incompatible override", provider)
	}
	if provider, ok := capabilities.Provider(index, "b.gd", equalsSignature); ok {
		t.Errorf("Provider(B) = %q, want none: B's own equals is incompatible", provider)
	}
	if provider, ok := capabilities.Provider(index, "a.gd", equalsSignature); !ok || provider != "a.gd" {
		t.Errorf("Provider(A) = %q, %v, want a.gd", provider, ok)
	}
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
	if provider, ok := capabilities.Provider(index, "base.gd", equalsSignature); ok {
		t.Errorf("Provider(Base) = %q, want none: an orphaned region supplied a capability", provider)
	}
	if capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub composed with an orphaned parent")
	}
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
	if provider, ok := capabilities.Provider(index, "base.gd", equalsSignature); !ok || provider != "base.gd" {
		t.Errorf("Provider(Base) = %q, %v, want base.gd as a virtual provider", provider, ok)
	}
	if !capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub could not compose with a parent that is itself newly requested")
	}
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
	if capabilities.Realizable("b.gd", equalsSignature) {
		t.Error("B was not demoted by its field-adding descendant C")
	}
	if capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("A was not demoted after B's demotion moved provider(C) to A")
	}
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
	if !capabilities.Realizable("base.gd", equalsSignature) {
		t.Error("a blocked generator erased the hand-written equals")
	}
	if !capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub was demoted although its parent still provides equals")
	}
}

func TestResolveRefusesAClassWhoseAncestryHasFieldsButNoProvider(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"sub.gd": {"equals"}}, nil)
	if capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub generated equals with no provider for Base's q")
	}
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
	if !capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("A was refused although B overrides equals and shields D")
	}
}

// A field-free descendant adds no state an inherited comparison could miss.
func TestAFieldFreeDescendantDoesNotRefuse(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n",
	})
	capabilities := Resolve(index, map[string][]string{"base.gd": {"equals"}}, nil)
	if !capabilities.Realizable("base.gd", equalsSignature) {
		t.Error("a field-free subclass caused a refusal")
	}
}

// A grandchild inheriting the base's method is as unsound as a child.
func TestAFieldAddingGrandchildRefusesTheBase(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":  "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"mid.gd":   "class_name Mid\nextends Base\n",
		"grand.gd": "class_name Grand\nextends Mid\n\nvar s: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"base.gd": {"equals"}}, nil)
	if capabilities.Realizable("base.gd", equalsSignature) {
		t.Error("a field-adding grandchild did not refuse the base")
	}
}

func TestResolveRefusesAClassWhoseAncestryReachesACycle(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends B\n",
		"b.gd": "class_name B\nextends A\n",
		"c.gd": "class_name C\nextends A\n\nvar q: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"c.gd": {"equals"}}, nil)
	if capabilities.Realizable("c.gd", equalsSignature) {
		t.Error("a class above an inheritance cycle generated equals")
	}
}

// Reporting a parse failure is not enough: write applies candidates despite
// unrelated diagnostics, so the capability has to be withdrawn.
func TestAUniverseParseFailureDemotesEquals(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd":      "class_name Hex\nextends RefCounted\n\nvar q: int\n",
		"broken.gd": "func (((\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	if capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("equals stayed realizable with an unreadable file in the universe")
	}
	if capabilities.UniverseCause == "" {
		t.Error("no cause recorded, so the refusal cannot name the file to fix")
	}
}

// An unresolvable project base leaves the graph incomplete in the same way.
func TestAnUnresolvableBaseDemotesEquals(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd":     "class_name Hex\nextends RefCounted\n\nvar q: int\n",
		"stray.gd": "class_name Stray\nextends \"res://gone.gd\"\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	if capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("equals generated with an incomplete inheritance graph")
	}
	if capabilities.UniverseCause == "" {
		t.Error("no cause recorded naming the unresolved class")
	}
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
	if !capabilities.Realizable("sub.gd", toStringSignature) {
		t.Error("to_string was refused by a condition that cannot affect it")
	}
}

// An inner class can never be a generation target.
func TestAnInnerClassIsNeverRealizable(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\nclass Nested:\n\tvar q: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd#Nested": {"to_string"}}, nil)
	if capabilities.Realizable("a.gd#Nested", toStringSignature) {
		t.Error("an inner class was realizable")
	}
}
