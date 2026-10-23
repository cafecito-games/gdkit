package semantic

import (
	"reflect"
	"testing"
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
	if id, ok := analyzer.ClassID("base.gd"); !ok || id != "base.gd" {
		t.Fatalf("ClassID(base.gd) = %q, %t", id, ok)
	}
	if id, ok := analyzer.ClassID("leaf.gd"); !ok || id != "leaf.gd" {
		t.Fatalf("ClassID(leaf.gd) = %q, %t", id, ok)
	}
	if _, ok := analyzer.ClassID("missing.gd"); ok {
		t.Fatal("ClassID(missing.gd) resolved an unindexed path")
	}
}

func TestAnalyzerAncestorClassIDs(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	if got := analyzer.AncestorClassIDs("leaf.gd"); !reflect.DeepEqual(got, []string{"mid.gd", "base.gd"}) {
		t.Fatalf("AncestorClassIDs(leaf.gd) = %v, want [mid.gd base.gd]", got)
	}
	if got := analyzer.AncestorClassIDs("mid.gd"); !reflect.DeepEqual(got, []string{"base.gd"}) {
		t.Fatalf("AncestorClassIDs(mid.gd) = %v, want [base.gd]", got)
	}
	// A class whose base is an engine class has no project ancestor.
	if got := analyzer.AncestorClassIDs("base.gd"); len(got) != 0 {
		t.Fatalf("AncestorClassIDs(base.gd) = %v, want none", got)
	}
	if got := analyzer.AncestorClassIDs("standalone.gd"); len(got) != 0 {
		t.Fatalf("AncestorClassIDs(standalone.gd) = %v, want none", got)
	}
	if got := analyzer.AncestorClassIDs("missing.gd"); len(got) != 0 {
		t.Fatalf("AncestorClassIDs(missing.gd) = %v, want none", got)
	}
}

func TestAnalyzerDescendantClassIDs(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	if got := analyzer.DescendantClassIDs("base.gd"); !reflect.DeepEqual(got, []string{"leaf.gd", "mid.gd"}) {
		t.Fatalf("DescendantClassIDs(base.gd) = %v, want [leaf.gd mid.gd]", got)
	}
	if got := analyzer.DescendantClassIDs("mid.gd"); !reflect.DeepEqual(got, []string{"leaf.gd"}) {
		t.Fatalf("DescendantClassIDs(mid.gd) = %v, want [leaf.gd]", got)
	}
	if got := analyzer.DescendantClassIDs("leaf.gd"); len(got) != 0 {
		t.Fatalf("DescendantClassIDs(leaf.gd) = %v, want none", got)
	}
}

func TestAnalyzerClassPath(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	if path, ok := analyzer.ClassPath("base.gd"); !ok || path != "base.gd" {
		t.Fatalf("ClassPath(base.gd) = %q, %t", path, ok)
	}
	// An inner class shares its enclosing file's path.
	if path, ok := analyzer.ClassPath("leaf.gd#Inner"); !ok || path != "leaf.gd" {
		t.Fatalf("ClassPath(leaf.gd#Inner) = %q, %t", path, ok)
	}
	if _, ok := analyzer.ClassPath("missing.gd"); ok {
		t.Fatal("ClassPath(missing.gd) resolved an unindexed class")
	}
}

func TestAnalyzerDeclaresMethod(t *testing.T) {
	analyzer := hierarchyAnalyzer(t)
	for _, name := range []string{"_helper", "helper"} {
		if !analyzer.DeclaresMethod("base.gd", name) {
			t.Errorf("DeclaresMethod(base.gd, %q) = false, want true", name)
		}
	}
	if analyzer.DeclaresMethod("base.gd", "missing") {
		t.Error("DeclaresMethod(base.gd, missing) = true, want false")
	}
	// A descendant inherits the method but does not directly declare it.
	if analyzer.DeclaresMethod("mid.gd", "_helper") {
		t.Error("DeclaresMethod(mid.gd, _helper) = true, want false")
	}
	if analyzer.DeclaresMethod("missing.gd", "_helper") {
		t.Error("DeclaresMethod(missing.gd, _helper) = true, want false")
	}
}
