package semanticsource

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
)

func TestSnapshotUsesFullUniverseAndResolvesStaticScriptPaths(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".gdkitignore":  "hidden.gd\n",
		"base.gd":       "class_name Base\n",
		"base.gd.uid":   "uid://base\n",
		"nested/sub.gd": "extends \"../base.gd\"\n",
		"hidden.gd":     "class_name Hidden\n",
		"broken.gd":     "func (((\n",
		"project.godot": "[autoload]\nGameState=\"*res://base.gd\"\n",
	}
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{Root: root, Selection: &project.Selection{SourceRoots: []string{"."}, HonorIgnoreFile: true}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(snapshot.Selected, "hidden.gd") {
		t.Fatal("fixture did not exclude hidden.gd")
	}
	source := NewSnapshot(snapshot)
	if !slices.Contains(source.Paths(), "hidden.gd") {
		t.Fatalf("Paths = %v, want ignored file in full universe", source.Paths())
	}
	index := semantic.BuildIndex(source)
	if index.Classes["hidden.gd"] == nil {
		t.Error("ignored class was dropped from semantic index")
	}
	for _, test := range []struct{ from, target, want string }{
		{"nested/sub.gd", "../base.gd", "base.gd"},
		{"nested/sub.gd", "res://base.gd", "base.gd"},
		{"nested/sub.gd", "uid://base", "base.gd"},
	} {
		if got, ok := source.ResolvePath(test.from, test.target); !ok || got != test.want {
			t.Errorf("ResolvePath(%q, %q) = %q, %v", test.from, test.target, got, ok)
		}
	}
	if _, ok := source.ResolvePath("base.gd", "res://gone.gd"); ok {
		t.Error("missing path resolved")
	}
	if _, ok := source.ResolvePath("base.gd", "uid://gone"); ok {
		t.Error("missing UID resolved")
	}
	if source.Autoloads()["GameState"] != "base.gd" {
		t.Errorf("Autoloads = %v", source.Autoloads())
	}
	if !slices.Equal(source.ParseFailures(), []string{"broken.gd"}) {
		t.Errorf("ParseFailures = %v", source.ParseFailures())
	}
}

func TestSnapshotCopiesProviderCollections(t *testing.T) {
	snapshot := &project.Snapshot{Paths: []string{"a.gd"}, Scripts: map[string]*project.Script{"a.gd": {}}, UIDs: map[string]string{"uid://a": "a.gd"}, Autoloads: map[string]string{"A": "a.gd"}}
	source := NewSnapshot(snapshot)
	snapshot.Paths[0] = "changed.gd"
	snapshot.UIDs["uid://a"] = "changed.gd"
	snapshot.Autoloads["A"] = "changed.gd"
	if !slices.Equal(source.Paths(), []string{"a.gd"}) {
		t.Errorf("Paths = %v", source.Paths())
	}
	if got, ok := source.ResolvePath("x.gd", "uid://a"); !ok || got != "a.gd" {
		t.Errorf("UID resolution = %q, %v", got, ok)
	}
	if source.Autoloads()["A"] != "a.gd" {
		t.Errorf("Autoloads = %v", source.Autoloads())
	}
}
