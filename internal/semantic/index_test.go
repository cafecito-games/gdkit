package semantic

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cafecito-games/gdparser"
	gdast "github.com/cafecito-games/gdparser/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	got := index.ClassIDs()
	require.Equal(t, wantIDs, got)
	top := index.Classes["holder.gd"]
	require.Equal(t, "Holder", top.Name)
	require.True(t, top.HasClassName)
	require.Equal(t, "RefCounted", top.ExternalBase)
	gotDeclarations := make([]string, 0, len(top.Declarations))
	for _, declaration := range top.Declarations {
		gotDeclarations = append(gotDeclarations, declaration.Name+":"+declaration.Kind.String())
	}
	wantDeclarations := []string{"field:variable", "LIMIT:constant", "changed:signal", "Mode:enum", "FLAG:enum-member", "run:method", "A:class", "D:class"}
	assert.Equal(t, wantDeclarations, gotDeclarations)
	assert.False(t, slices.ContainsFunc(top.Declarations, func(d Declaration) bool { return d.Name == "local" }))
}

func TestIndexResolvesInnerClassesThroughEnclosingAndInheritedScopes(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"base.gd":    "class_name Base\nclass Inherited:\n\tpass\n",
		"derived.gd": "extends Base\nclass Local:\n\tpass\nclass ByLocal extends Local:\n\tpass\nclass ByInherited extends Inherited:\n\tpass\n",
	}))
	got := index.Classes["derived.gd#ByLocal"].ParentID
	assert.Equal(t, "derived.gd#Local", got)
	got = index.Classes["derived.gd#ByInherited"].ParentID
	assert.Equal(t, "base.gd#Inherited", got)
}

func TestScopeLookupWaitsForNearerInheritedScopesToSettle(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"base.gd":    "class_name Base\n",
		"derived.gd": "extends Base\nclass Thing:\n\tpass\nclass E extends P:\n\tclass X extends Thing:\n\t\tpass\nclass P extends Q:\n\tpass\nclass Q extends Base:\n\tclass Thing:\n\t\tpass\n",
	}))
	got := index.Classes["derived.gd#E#X"].ParentID
	assert.Equal(t, "derived.gd#Q#Thing", got)
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
		assert.Equal(t, "base.gd", index.Classes[path].ParentID)
	}
	assert.Equal(t, "base.gd#Inner#Deep", index.Classes["chain.gd"].ParentID)
}

func TestIndexResolvesInnerClassPathExtends(t *testing.T) {
	s := sources(t, map[string]string{
		"base.gd":     "extends RefCounted\nclass Inner:\n\tpass\n",
		"holder.gd":   "extends RefCounted\nclass Child extends \"res://base.gd\":\n\tpass\n",
		"deep.gd":     "extends RefCounted\nclass Child extends \"res://base.gd\".Inner:\n\tpass\n",
		"relative.gd": "extends RefCounted\nclass Child extends \"base.gd\":\n\tpass\n",
	})
	s.resolved["holder.gd\x00res://base.gd"] = "base.gd"
	s.resolved["deep.gd\x00res://base.gd"] = "base.gd"
	s.resolved["relative.gd\x00base.gd"] = "base.gd"
	index := BuildIndex(s)
	wantParents := map[string]string{
		"holder.gd#Child":   "base.gd",
		"deep.gd#Child":     "base.gd#Inner",
		"relative.gd#Child": "base.gd",
	}
	for id, want := range wantParents {
		assert.Equal(t, want, index.Classes[id].ParentID)
	}
	got := index.Descendants("base.gd")
	assert.Equal(t, []string{"holder.gd#Child", "relative.gd#Child"}, got)
}

// An inner class can spell its base as an extends statement inside its body, not
// only on the declaration line. The body form must resolve to the same engine
// or project base.
func TestIndexResolvesInnerClassBodyExtends(t *testing.T) {
	index := BuildIndex(sources(t, map[string]string{
		"base.gd":   "class_name Base\nextends RefCounted\n",
		"holder.gd": "extends Node\nclass Canvas:\n\textends Control\n\tpass\nclass Child:\n\textends Base\n\tpass\n",
	}))
	require.Equal(t, "Control", index.Classes["holder.gd#Canvas"].ExternalBase)
	require.Equal(t, "base.gd", index.Classes["holder.gd#Child"].ParentID)
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
		"outer.gd":            "class Bad extends \"res://gone.gd\":\n\tpass\n",
	})
	s.autoloads["MissingAuto"] = "gone.gd"
	index := BuildIndex(s)
	got := index.DuplicateClassNames["Dup"]
	require.Equal(t, []string{"one.gd", "two.gd"}, got)
	for _, path := range []string{"ambiguous.gd", "missing_path.gd", "missing_alias.gd", "missing_autoload.gd", "missing_inner.gd", "outer.gd#Bad"} {
		class := index.Classes[path]
		assert.True(t, class.UnresolvedBase)
		assert.NotEmpty(t, class.UnresolvedCause)
		assert.Empty(t, class.ParentID)
		assert.Empty(t, class.ExternalBase)
	}
	wantCauses := map[string]string{
		"ambiguous.gd":        "Dup",
		"missing_path.gd":     "res://gone.gd",
		"missing_alias.gd":    "res://gone.gd",
		"missing_autoload.gd": "gone.gd",
		"missing_inner.gd":    "Unique.Nope",
	}
	for path, want := range wantCauses {
		assert.Equal(t, want, index.Classes[path].UnresolvedCause)
	}
	assert.Equal(t, "Control", index.Classes["engine.gd"].ExternalBase)
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
	assert.Equal(t, []string{"broken.gd", "nil.gd"}, index.ParseFailures)
	assert.True(t, index.InCycle["a.gd"])
	assert.True(t, index.InCycle["b.gd"])
	assert.False(t, index.InCycle["c.gd"])
	assert.Equal(t, []string{"c.gd", "a.gd", "b.gd"}, index.Ancestry("c.gd"))
	assert.Equal(t, []string{"b.gd", "c.gd"}, index.Descendants("a.gd"))
}

func TestBuildIndexNormalizesCopiesAndIsDeterministic(t *testing.T) {
	s := sources(t, map[string]string{"z.gd": "class_name Z\n", "a.gd": "class_name A\n"})
	s.paths = []string{"z.gd", "a.gd", "a.gd", "broken.gd"}
	s.failures = []string{"broken.gd", "broken.gd"}
	s.autoloads["State"] = "z.gd"
	first, second := BuildIndex(s), BuildIndex(s)
	require.Equal(t, first, second, "repeated builds differ")
	s.paths[0] = "mutated.gd"
	s.failures[0] = "mutated.gd"
	s.autoloads["State"] = "mutated.gd"
	assert.Equal(t, []string{"a.gd", "z.gd"}, first.ClassIDs())
	assert.Equal(t, []string{"broken.gd"}, first.ParseFailures)
	assert.Equal(t, "z.gd", first.Autoloads()["State"])
	copy := first.Autoloads()
	copy["State"] = "changed.gd"
	assert.NotEqual(t, first.Autoloads(), copy)
}

func TestSemanticNonTestImportsStayPortable(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	allowed := map[string]bool{
		"github.com/cafecito-games/gdparser/ast":   true,
		"github.com/cafecito-games/gdparser/token": true,
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", entry.Name()), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range file.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			if strings.Contains(path, ".") {
				assert.True(t, allowed[path])
			}
		}
	}
}
