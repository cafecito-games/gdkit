package generate

import (
	"slices"
	"testing"
)

func TestIndexRecordsMethodsFieldsAndInheritance(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	base, sub := index.ByClassName["Base"], index.ByClassName["Sub"]
	if base == nil || sub == nil {
		t.Fatal("class_name index is incomplete")
	}
	if sub.ParentID != "base.gd" {
		t.Errorf("Sub.ParentID = %q, want base.gd", sub.ParentID)
	}
	if len(sub.Fields) != 1 || sub.Fields[0].Name != "r" {
		t.Errorf("Sub.Fields = %+v", sub.Fields)
	}
	method, ok := base.Methods["equals"]
	if !ok || method.Signature.Arity != 1 || method.Signature.Static || method.InRegion {
		t.Errorf("Base.equals = %+v, %v", method, ok)
	}
}

// class_name and extends on one line are a single Directive node.
func TestIndexReadsClassNameAndExtendsOnOneLine(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n",
		"sub.gd":  "class_name Sub extends Base\n",
	})
	sub := index.TopLevel["sub.gd"]
	if !sub.HasClassName || sub.Name != "Sub" {
		t.Errorf("Sub = %+v, want a class_name of Sub", sub)
	}
	if sub.ParentID != "base.gd" {
		t.Errorf("ParentID = %q, want base.gd", sub.ParentID)
	}
}

// Field metadata is needed for classes that never opt in, because both
// inheritance rules ask whether some other class declares a selectable field.
func TestIndexRecordsFieldsForUnrequestedClasses(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	if fields := index.ByClassName["Sub"].Fields; len(fields) != 1 {
		t.Errorf("an unrequested class has no field metadata: %+v", fields)
	}
}

// An unrequested inner class can extend a generated base and add fields, which
// is what the descendant rule exists to catch.
func TestIndexRecordsInnerClasses(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":   "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"holder.gd": "class_name Holder\nextends RefCounted\n\nclass Nested extends Base:\n\tvar r: int\n",
	})
	nested := index.Classes["holder.gd#Nested"]
	if nested == nil {
		t.Fatalf("the inner class was not indexed; have %v", sortedKeys(index.Classes))
	}
	if !nested.Inner {
		t.Error("the inner class is not marked Inner")
	}
	if nested.ParentID != "base.gd" {
		t.Errorf("Nested.ParentID = %q, want base.gd", nested.ParentID)
	}
	if len(nested.Fields) != 1 || nested.Fields[0].Name != "r" {
		t.Errorf("Nested.Fields = %+v, want [r]", nested.Fields)
	}
	if index.TopLevel["holder.gd"].Inner {
		t.Error("the file's top-level class was marked Inner")
	}
	if got := index.Descendants("base.gd"); !slices.Contains(got, "holder.gd#Nested") {
		t.Errorf("Descendants(base) = %v, want the inner class", got)
	}
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
	if deep == nil {
		t.Fatalf("a.gd#A#B#C missing; have %v", sortedKeys(index.Classes))
	}
	if len(deep.Fields) != 1 || deep.Fields[0].Name != "q" {
		t.Errorf("A.B.C fields = %+v, want [q]", deep.Fields)
	}
	sibling := index.Classes["a.gd#D#C"]
	if sibling == nil || len(sibling.Fields) != 1 || sibling.Fields[0].Name != "r" {
		t.Errorf("D.C = %+v, want its own identity holding [r]", sibling)
	}
}

// Calling FindRegion per class gave every inner class in a generated file the
// file's region, so an orphan scan would report one region once per inner class.
func TestAnInnerClassDoesNotInheritTheFilesRegion(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\nclass Nested:\n\tvar q: int\n\n" +
			"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Holder()\"\n# gdkit:generated:end\n",
	})
	if !index.TopLevel["a.gd"].HasRegion {
		t.Errorf("the top-level class does not own the region: %v", index.TopLevel["a.gd"].RegionError)
	}
	if index.Classes["a.gd#Nested"].HasRegion {
		t.Error("an inner class claimed the file's region")
	}
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
			if top.HasRegion || top.RegionError == nil {
				t.Errorf("accepted as the top-level region: %+v", top)
			}
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
		if got := index.TopLevel[path].ParentID; got != "domain/base.gd" {
			t.Errorf("%s ParentID = %q, want domain/base.gd", path, got)
		}
	}
	for _, path := range []string{"domain/chain.gd", "domain/dotted.gd"} {
		if got := index.TopLevel[path].ParentID; got != "domain/base.gd#Inner" {
			t.Errorf("%s ParentID = %q, want domain/base.gd#Inner", path, got)
		}
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
	if got := index.Classes["a.gd#Child"].ParentID; got != "base.gd" {
		t.Errorf("Child.ParentID = %q, want the global base.gd", got)
	}
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
		if got := index.TopLevel[path].ParentID; got != "base.gd" {
			t.Errorf("%s ParentID = %q, want base.gd via the preload alias", path, got)
		}
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
	if got := index.TopLevel["sub.gd"].ParentID; got != "state.gd" {
		t.Errorf("ParentID = %q, want state.gd via the autoload", got)
	}
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
		if !class.UnresolvedBase {
			t.Errorf("%s: UnresolvedBase = false, want true (extends %q)", path, class.Extends)
		}
		if class.UnresolvedCause == "" {
			t.Errorf("%s: no UnresolvedCause recorded", path)
		}
	}
}

// Without a native catalogue, an unrecognised bare identifier must be read as
// an engine type, or no real project can generate.
func TestABareEngineBaseIsNotUnresolved(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Panel\nextends Control\n\nvar q: int\n",
	})
	if index.TopLevel["a.gd"].UnresolvedBase {
		t.Error("an engine base was reported as unresolved")
	}
	if index.TopLevel["a.gd"].ParentID != "" {
		t.Error("an engine base produced an inheritance edge")
	}
}

func TestIndexDetectsInheritanceCyclesAndTerminates(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends B\n",
		"b.gd": "class_name B\nextends A\n",
		"c.gd": "class_name C\nextends A\n\nvar q: int\n",
	})
	if !index.InCycle["a.gd"] || !index.InCycle["b.gd"] {
		t.Error("the A/B cycle was not detected")
	}
	if index.InCycle["c.gd"] {
		t.Error("C is not itself in the cycle")
	}
	// A class above a cycle is as unresolvable as one inside it.
	if !index.ReachesCycle("c.gd") {
		t.Error("ReachesCycle(C) = false, want true")
	}
	if len(index.Ancestry("c.gd")) == 0 {
		t.Error("Ancestry returned nothing")
	}
}

func TestIndexRecordsDuplicateClassNames(t *testing.T) {
	index := indexOf(t, map[string]string{
		"one.gd": "class_name Dup\n",
		"two.gd": "class_name Dup\n",
	})
	if len(index.DuplicateClassNames["Dup"]) != 2 {
		t.Errorf("duplicates = %+v", index.DuplicateClassNames)
	}
}

func TestIndexRecordsParseFailures(t *testing.T) {
	index := indexOf(t, map[string]string{
		"ok.gd":     "class_name Fine\nextends RefCounted\n",
		"broken.gd": "func (((\n",
	})
	if !slices.Equal(index.ParseFailures, []string{"broken.gd"}) {
		t.Errorf("ParseFailures = %v, want [broken.gd]", index.ParseFailures)
	}
	if index.TopLevel["broken.gd"] != nil {
		t.Error("an unparseable file produced a class")
	}
}
