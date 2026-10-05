package semantic

import (
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cafecito-games/gdparser"
	gdast "github.com/cafecito-games/gdparser/ast"
)

type memorySources struct {
	paths     []string
	files     map[string]*gdast.File
	resolved  map[string]string
	autoloads map[string]string
	failures  []string
}

func sources(t *testing.T, files map[string]string) *memorySources {
	t.Helper()
	s := &memorySources{files: map[string]*gdast.File{}, resolved: map[string]string{}, autoloads: map[string]string{}}
	for path, source := range files {
		s.paths = append(s.paths, path)
		file, err := gdparser.ParseFile(path, []byte(source))
		if err != nil {
			s.failures = append(s.failures, path)
			continue
		}
		s.files[path] = file
	}
	slices.Sort(s.paths)
	return s
}

func (s *memorySources) Paths() []string              { return s.paths }
func (s *memorySources) File(path string) *gdast.File { return s.files[path] }
func (s *memorySources) Autoloads() map[string]string { return s.autoloads }
func (s *memorySources) ParseFailures() []string      { return s.failures }
func (s *memorySources) ResolvePath(from, target string) (string, bool) {
	path, ok := s.resolved[from+"\x00"+target]
	return path, ok
}

func TestIndexDiscoversStableNestedIdentitiesAndDeclarations(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"holder.gd": "class_name Holder extends RefCounted\nvar field: int\nconst LIMIT = 3\nsignal changed(value)\nenum Mode { ONE }\nenum { FLAG }\nfunc run():\n\tvar local = 1\nclass A:\n\tclass B:\n\t\tclass C:\n\t\t\tvar deep: int\nclass D:\n\tclass C:\n\t\tpass\n",
	}))
	wantIDs := []string{"holder.gd", "holder.gd#A", "holder.gd#A#B", "holder.gd#A#B#C", "holder.gd#D", "holder.gd#D#C"}
	if got := index.ClassIDs(); !slices.Equal(got, wantIDs) {
		t.Fatalf("ClassIDs = %v, want %v", got, wantIDs)
	}
	top := index.Classes["holder.gd"]
	if top.Name != "Holder" || !top.HasClassName || top.ExternalBase != "RefCounted" {
		t.Errorf("top-level class = %+v", top)
	}
	gotDeclarations := make([]string, 0, len(top.Declarations))
	for _, declaration := range top.Declarations {
		gotDeclarations = append(gotDeclarations, declaration.Name+":"+declaration.Kind.String())
	}
	wantDeclarations := []string{"field:variable", "LIMIT:constant", "changed:signal", "Mode:enum", "FLAG:enum-member", "run:method", "A:class", "D:class"}
	if !slices.Equal(gotDeclarations, wantDeclarations) {
		t.Errorf("Declarations = %v, want %v", gotDeclarations, wantDeclarations)
	}
	if slices.ContainsFunc(top.Declarations, func(d Declaration) bool { return d.Name == "local" }) {
		t.Error("a function-body declaration was indexed")
	}
}

func TestIndexResolvesInnerClassesThroughEnclosingAndInheritedScopes(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"base.gd":    "class_name Base\nclass Inherited:\n\tpass\n",
		"derived.gd": "extends Base\nclass Local:\n\tpass\nclass ByLocal extends Local:\n\tpass\nclass ByInherited extends Inherited:\n\tpass\n",
	}))
	if got := index.Classes["derived.gd#ByLocal"].ParentID; got != "derived.gd#Local" {
		t.Errorf("local-scope parent = %q", got)
	}
	if got := index.Classes["derived.gd#ByInherited"].ParentID; got != "base.gd#Inherited" {
		t.Errorf("inherited-scope parent = %q", got)
	}
}

func TestScopeLookupWaitsForNearerInheritedScopesToSettle(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"base.gd":    "class_name Base\n",
		"derived.gd": "extends Base\nclass Thing:\n\tpass\nclass E extends P:\n\tclass X extends Thing:\n\t\tpass\nclass P extends Q:\n\tpass\nclass Q extends Base:\n\tclass Thing:\n\t\tpass\n",
	}))
	if got := index.Classes["derived.gd#E#X"].ParentID; got != "derived.gd#Q#Thing" {
		t.Errorf("ParentID = %q, want the nearer inherited Thing", got)
	}
}

func TestIndexResolvesEveryInheritanceForm(t *testing.T) {
	s := sources(t, map[string]string{
		"base.gd":     "class_name Base\nclass Inner:\n\tclass Deep:\n\t\tpass\n",
		"name.gd":     "extends Base\n",
		"path.gd":     "extends \"res://base.gd\"\n",
		"relative.gd": "extends \"base.gd\"\n",
		"uid.gd":      "extends \"uid://base\"\n",
		"chain.gd":    "extends Base.Inner.Deep\n",
		"preload.gd":  "const Loaded = preload(\"res://base.gd\")\nextends Loaded\n",
		"autoload.gd": "extends GameState\n",
	})
	for _, path := range []string{"path.gd", "relative.gd", "uid.gd"} {
		target := map[string]string{"path.gd": "res://base.gd", "relative.gd": "base.gd", "uid.gd": "uid://base"}[path]
		s.resolved[path+"\x00"+target] = "base.gd"
	}
	s.resolved["preload.gd\x00res://base.gd"] = "base.gd"
	s.autoloads["GameState"] = "base.gd"
	index := BuildIndex(s)
	for _, path := range []string{"name.gd", "path.gd", "relative.gd", "uid.gd", "preload.gd", "autoload.gd"} {
		if got := index.Classes[path].ParentID; got != "base.gd" {
			t.Errorf("%s ParentID = %q, want base.gd", path, got)
		}
	}
	if got := index.Classes["chain.gd"].ParentID; got != "base.gd#Inner#Deep" {
		t.Errorf("chain ParentID = %q", got)
	}
}

func TestIndexFailsClosedForAmbiguityAndMissingProjectReferences(t *testing.T) {
	s := sources(t, map[string]string{
		"one.gd":              "class_name Dup\n",
		"two.gd":              "class_name Dup\n",
		"ambiguous.gd":        "extends Dup\n",
		"missing_path.gd":     "extends \"res://gone.gd\"\n",
		"missing_alias.gd":    "const Gone = preload(\"res://gone.gd\")\nextends Gone\n",
		"missing_autoload.gd": "extends MissingAuto\n",
		"missing_inner.gd":    "extends Unique.Nope\n",
		"unique.gd":           "class_name Unique\n",
		"engine.gd":           "extends Control\n",
		"outer.gd":            "class Bad extends \"res://one.gd\":\n\tpass\n",
	})
	s.autoloads["MissingAuto"] = "gone.gd"
	index := BuildIndex(s)
	if got := index.DuplicateClassNames["Dup"]; !slices.Equal(got, []string{"one.gd", "two.gd"}) {
		t.Fatalf("duplicate claimants = %v", got)
	}
	for _, path := range []string{"ambiguous.gd", "missing_path.gd", "missing_alias.gd", "missing_autoload.gd", "missing_inner.gd", "outer.gd#Bad"} {
		class := index.Classes[path]
		if !class.UnresolvedBase || class.UnresolvedCause == "" || class.ParentID != "" || class.ExternalBase != "" {
			t.Errorf("%s did not fail closed: %+v", path, class)
		}
	}
	wantCauses := map[string]string{
		"ambiguous.gd":        "Dup",
		"missing_path.gd":     "res://gone.gd",
		"missing_alias.gd":    "res://gone.gd",
		"missing_autoload.gd": "gone.gd",
		"missing_inner.gd":    "Unique.Nope",
	}
	for path, want := range wantCauses {
		if got := index.Classes[path].UnresolvedCause; got != want {
			t.Errorf("%s cause = %q, want %q", path, got, want)
		}
	}
	if got := index.Classes["engine.gd"].ExternalBase; got != "Control" {
		t.Errorf("ExternalBase = %q, want Control", got)
	}
}

func TestIndexRecordsFailuresCyclesChildrenAndTerminates(t *testing.T) {
	s := sources(t, map[string]string{
		"a.gd": "class_name A\nextends B\n",
		"b.gd": "class_name B\nextends A\n",
		"c.gd": "extends A\n",
	})
	s.paths = append(s.paths, "broken.gd", "nil.gd")
	s.failures = append(s.failures, "broken.gd")
	index := BuildIndex(s)
	if !slices.Equal(index.ParseFailures, []string{"broken.gd", "nil.gd"}) {
		t.Errorf("ParseFailures = %v", index.ParseFailures)
	}
	if !index.InCycle["a.gd"] || !index.InCycle["b.gd"] || index.InCycle["c.gd"] {
		t.Errorf("InCycle = %v", index.InCycle)
	}
	if got := index.Ancestry("c.gd"); !slices.Equal(got, []string{"c.gd", "a.gd", "b.gd"}) {
		t.Errorf("Ancestry = %v", got)
	}
	if got := index.Descendants("a.gd"); !slices.Equal(got, []string{"b.gd", "c.gd"}) {
		t.Errorf("Descendants = %v", got)
	}
}

func TestBuildIndexNormalizesCopiesAndIsDeterministic(t *testing.T) {
	s := sources(t, map[string]string{"z.gd": "class_name Z\n", "a.gd": "class_name A\n"})
	s.paths = []string{"z.gd", "a.gd", "a.gd", "broken.gd"}
	s.failures = []string{"broken.gd", "broken.gd"}
	s.autoloads["State"] = "z.gd"
	first, second := BuildIndex(s), BuildIndex(s)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("repeated builds differ")
	}
	s.paths[0] = "mutated.gd"
	s.failures[0] = "mutated.gd"
	s.autoloads["State"] = "mutated.gd"
	if !slices.Equal(first.ClassIDs(), []string{"a.gd", "z.gd"}) || !slices.Equal(first.ParseFailures, []string{"broken.gd"}) {
		t.Errorf("index retained caller-owned collections: IDs=%v failures=%v", first.ClassIDs(), first.ParseFailures)
	}
	if got := first.Autoloads()["State"]; got != "z.gd" {
		t.Errorf("autoload mutated to %q", got)
	}
	copy := first.Autoloads()
	copy["State"] = "changed.gd"
	if maps.Equal(copy, first.Autoloads()) {
		t.Error("Autoloads returned mutable index state")
	}
}

func TestSemanticNonTestImportsStayPortable(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"github.com/cafecito-games/gdparser/ast":   true,
		"github.com/cafecito-games/gdparser/token": true,
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			if strings.Contains(path, ".") && !allowed[path] {
				t.Errorf("%s imports disallowed package %q", entry.Name(), path)
			}
		}
	}
}
