package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexRecordsMethodsFieldsAndInheritance(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	base, sub := index.ByClassName["Base"], index.ByClassName["Sub"]
	require.NotNil(t, base, "class_name index is incomplete")
	require.NotNil(t, sub, "class_name index is incomplete")
	assert.Equal(t, "base.gd", sub.ParentID)
	require.Len(t, sub.Fields, 1)
	assert.Equal(t, "r", sub.Fields[0].Name)
	method, ok := base.Methods["equals"]
	assert.True(t, ok)
	assert.Equal(t, 1, method.Signature.Arity)
	assert.False(t, method.Signature.Static)
	assert.False(t, method.InRegion)
}

// class_name and extends on one line are a single Directive node.
func TestIndexReadsClassNameAndExtendsOnOneLine(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n",
		"sub.gd":  "class_name Sub extends Base\n",
	})
	sub := index.TopLevel["sub.gd"]
	assert.True(t, sub.HasClassName)
	assert.Equal(t, "Sub", sub.Name)
	assert.Equal(t, "base.gd", sub.ParentID)
}

// Field metadata is needed for classes that never opt in, because both
// inheritance rules ask whether some other class declares a selectable field.
func TestIndexRecordsFieldsForUnrequestedClasses(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	fields := index.ByClassName["Sub"].Fields
	assert.Len(t, fields, 1, "an unrequested class has no field metadata")
}

// An unrequested inner class can extend a generated base and add fields, which
// is what the descendant rule exists to catch.
func TestIndexRecordsInnerClasses(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":   "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"holder.gd": "class_name Holder\nextends RefCounted\n\nclass Nested extends Base:\n\tvar r: int\n",
	})
	nested := index.Classes["holder.gd#Nested"]
	require.NotNil(t, nested, "the inner class was not indexed")
	assert.True(t, nested.Inner, "the inner class is not marked Inner")
	assert.Equal(t, "base.gd", nested.ParentID)
	require.Len(t, nested.Fields, 1)
	assert.Equal(t, "r", nested.Fields[0].Name)
	assert.False(t, index.TopLevel["holder.gd"].Inner, "the file's top-level class was marked Inner")
	got := index.Descendants("base.gd")
	assert.Contains(t, got, "holder.gd#Nested", "the inner class was not a descendant")
}

// Rewriting returned IDs as parentID+name drops middle segments, so A.B.C
// becomes path#A#C and sibling subtrees collide.
func TestIndexBuildsNestedIdentitiesWithoutLosingSegments(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class A:\n\tclass B:\n\t\tclass C:\n\t\t\tvar q: int\n" +
			"\nclass D:\n\tclass C:\n\t\tvar r: int\n",
	})
	deep := index.Classes["a.gd#A#B#C"]
	require.NotNil(t, deep, "a.gd#A#B#C missing")
	require.Len(t, deep.Fields, 1)
	assert.Equal(t, "q", deep.Fields[0].Name)
	sibling := index.Classes["a.gd#D#C"]
	require.NotNil(t, sibling, "D.C is missing")
	require.Len(t, sibling.Fields, 1)
	assert.Equal(t, "r", sibling.Fields[0].Name)
}

// Calling FindRegion per class gave every inner class in a generated file the
// file's region, so an orphan scan would report one region once per inner class.
func TestAnInnerClassDoesNotInheritTheFilesRegion(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\nclass Nested:\n\tvar q: int\n\n" +
			"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Holder()\"\n# gdkit:generated:end\n",
	})
	assert.True(t, index.TopLevel["a.gd"].HasRegion, "the top-level class does not own the region")
	assert.False(t, index.Classes["a.gd#Nested"].HasRegion, "an inner class claimed the file's region")
}

// A begin sentinel inside an inner class with its end outside is contained by
// nothing, so a containment check would pass it as top-level.
func TestSentinelsNotOwnedByTheTopLevelSuiteAreRefused(t *testing.T) {
	for name, source := range map[string]string{
		"inside an inner class": "class_name Holder\nextends RefCounted\n\nclass Nested:\n" +
			"\t# gdkit:generated:begin\n\tfunc f():\n\t\tpass\n\t# gdkit:generated:end\n",
		"straddling the boundary": "class_name Holder\nextends RefCounted\n\nclass Nested:\n" +
			"\t# gdkit:generated:begin\n\tfunc f():\n\t\tpass\n# gdkit:generated:end\n",
	} {
		t.Run(name, func(t *testing.T) {
			index := indexOf(t, map[string]string{"a.gd": source})
			top := index.TopLevel["a.gd"]
			assert.False(t, top.HasRegion, "accepted as the top-level region")
			assert.NotNil(t, top.RegionError, "accepted as the top-level region")
		})
	}
}

func TestIndexResolvesEveryInheritanceForm(t *testing.T) {
	index := indexOf(t, map[string]string{
		"domain/base.gd":     "class_name Base\nextends RefCounted\n\nvar q: int\n\nclass Inner:\n\tvar r: int\n",
		"domain/base.gd.uid": "uid://ckb1n0mqp2v7x",
		"domain/by_name.gd":  "class_name ByName\nextends Base\n",
		"domain/by_path.gd":  "class_name ByPath\nextends \"res://domain/base.gd\"\n",
		"domain/by_rel.gd":   "class_name ByRel\nextends \"base.gd\"\n",
		"domain/by_uid.gd":   "class_name ByUID\nextends \"uid://ckb1n0mqp2v7x\"\n",
		"domain/by_raw.gd":   "class_name ByRaw\nextends r\"res://domain/base.gd\"\n",
		"domain/chain.gd":    "class_name Chain\nextends \"res://domain/base.gd\".Inner\n",
		"domain/dotted.gd":   "class_name Dotted\nextends Base.Inner\n",
	})
	for _, path := range []string{"domain/by_name.gd", "domain/by_path.gd", "domain/by_rel.gd", "domain/by_uid.gd", "domain/by_raw.gd"} {
		got := index.TopLevel[path].ParentID
		assert.Equal(t, "domain/base.gd", got, path)
	}
	for _, path := range []string{"domain/chain.gd", "domain/dotted.gd"} {
		got := index.TopLevel[path].ParentID
		assert.Equal(t, "domain/base.gd#Inner", got, path)
	}
}

// Godot searches global class names before classes in the current scope, and
// rejects an inner class that hides a global rather than preferring it.
func TestAGlobalClassNameBeatsAnInnerClassOfTheSameName(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n",
		"a.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class Base:\n\tvar q: int\n\nclass Child extends Base:\n\tvar r: int\n",
	})
	got := index.Classes["a.gd#Child"].ParentID
	assert.Equal(t, "base.gd", got)
}

// A preload constant is a type to Godot, so it is a real inheritance edge. A
// field-adding subclass hidden behind one would be invisible otherwise.
func TestAPreloadAliasIsAnInheritanceEdge(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":     "extends RefCounted\n\nvar q: int\n",
		"sub.gd":      "const Base = preload(\"res://base.gd\")\nextends Base\n\nvar r: int\n",
		"deep/sub.gd": "const Base = preload(\"../base.gd\")\nextends Base\n\nvar r: int\n",
	})
	for _, path := range []string{"sub.gd", "deep/sub.gd"} {
		got := index.TopLevel[path].ParentID
		assert.Equal(t, "base.gd", got, path)
	}
}

// An autoload identifier is a project global to Godot's base-class analysis,
// so treating it as an engine type would drop a real edge.
func TestAnAutoloadIsAnInheritanceEdge(t *testing.T) {
	index := indexOf(t, map[string]string{
		"state.gd":      "extends Node\n\nvar q: int\n",
		"sub.gd":        "extends GameState\n\nvar r: int\n",
		"project.godot": "[autoload]\nGameState=\"*res://state.gd\"\n",
	})
	got := index.TopLevel["sub.gd"].ParentID
	assert.Equal(t, "state.gd", got)
}

// Every recognised-but-unresolved project form must set UnresolvedBase, or the
// edge vanishes and a generated equals is unsound with nothing saying so.
func TestUnresolvedProjectReferencesAreRecorded(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":       "class_name Base\nextends RefCounted\n",
		"bad_path.gd":   "extends \"res://gone.gd\"\n",
		"bad_rel.gd":    "extends \"gone.gd\"\n",
		"bad_uid.gd":    "extends \"uid://nothingdeclaresthis\"\n",
		"bad_chain.gd":  "extends Base.NoSuchInner\n",
		"bad_alias.gd":  "const Base2 = preload(\"res://gone.gd\")\nextends Base2\n",
		"bad_auto.gd":   "extends Missing\n",
		"project.godot": "[autoload]\nMissing=\"*res://gone.gd\"\n",
	})
	for _, path := range []string{"bad_path.gd", "bad_rel.gd", "bad_uid.gd", "bad_chain.gd", "bad_alias.gd", "bad_auto.gd"} {
		class := index.TopLevel[path]
		assert.True(t, class.UnresolvedBase, path)
		assert.NotEmpty(t, class.UnresolvedCause, path)
	}
}

// Without a native catalogue, an unrecognised bare identifier must be read as
// an engine type, or no real project can generate.
func TestABareEngineBaseIsNotUnresolved(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Panel\nextends Control\n\nvar q: int\n",
	})
	assert.False(t, index.TopLevel["a.gd"].UnresolvedBase, "an engine base was reported as unresolved")
	assert.Empty(t, index.TopLevel["a.gd"].ParentID, "an engine base produced an inheritance edge")
}

func TestIndexDetectsInheritanceCyclesAndTerminates(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends B\n",
		"b.gd": "class_name B\nextends A\n",
		"c.gd": "class_name C\nextends A\n\nvar q: int\n",
	})
	assert.True(t, index.InCycle["a.gd"], "the A/B cycle was not detected")
	assert.True(t, index.InCycle["b.gd"], "the A/B cycle was not detected")
	assert.False(t, index.InCycle["c.gd"], "C is not itself in the cycle")
	// A class above a cycle is as unresolvable as one inside it.
	assert.True(t, index.ReachesCycle("c.gd"))
	assert.NotEmpty(t, index.Ancestry("c.gd"), "Ancestry returned nothing")
}

func TestIndexRecordsDuplicateClassNames(t *testing.T) {
	index := indexOf(t, map[string]string{
		"one.gd": "class_name Dup\n",
		"two.gd": "class_name Dup\n",
	})
	assert.Len(t, index.DuplicateClassNames["Dup"], 2)
}

func TestIndexRecordsParseFailures(t *testing.T) {
	index := indexOf(t, map[string]string{
		"ok.gd":     "class_name Fine\nextends RefCounted\n",
		"broken.gd": "func (((\n",
	})
	assert.Equal(t, []string{"broken.gd"}, index.ParseFailures)
	assert.Nil(t, index.TopLevel["broken.gd"], "an unparseable file produced a class")
}
