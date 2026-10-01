package project

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadDiscoversParsesAndExcludes(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"player.gd":           "var speed := 1\n",
		"nested/enemy.gd":     "var health := 2\n",
		"addons/plugin/no.gd": "var ignored := 3\n",
		"nested/enemy.gd.uid": "uid://abc123\n",
		"notes.txt":           "not gdscript\n",
	})

	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}, Exclude: []string{"addons/**"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nested/enemy.gd", "player.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
	script := snapshot.Scripts["player.gd"]
	if script == nil || script.File == nil || script.ParseError != nil {
		t.Fatalf("player.gd did not parse: %#v", script)
	}
	if string(script.Source) != "var speed := 1\n" {
		t.Errorf("Source = %q", script.Source)
	}
	if len(script.Lines) != 1 {
		t.Errorf("Lines = %v, want one line start", script.Lines)
	}
	if snapshot.UIDs["uid://abc123"] != "nested/enemy.gd" {
		t.Errorf("UIDs = %v", snapshot.UIDs)
	}
}

func TestLoadReportsParseFailureOnTheScript(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"broken.gd": "func (\n",
		"fine.gd":   "var ok := 1\n",
	})

	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}})
	if err != nil {
		t.Fatalf("Load should not fail on an unparseable file: %v", err)
	}
	if snapshot.Scripts["broken.gd"].ParseError == nil {
		t.Error("broken.gd should carry a ParseError")
	}
	if snapshot.Scripts["broken.gd"].File != nil {
		t.Error("broken.gd should have no tree")
	}
	if snapshot.Scripts["fine.gd"].ParseError != nil {
		t.Error("fine.gd should have parsed")
	}
}

func TestLoadRejectsSourceRootOutsideProject(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(Config{Root: root, SourceRoots: []string{".."}}); err == nil {
		t.Error("a source root above the project root must be an error")
	}
}

func TestLineStartsCoverEveryLine(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.gd": "var a := 1\nvar b := 2\nvar c := 3\n"})
	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	lines := snapshot.Scripts["a.gd"].Lines
	if !slices.Equal(lines, []int{0, 11, 22}) {
		t.Errorf("Lines = %v, want [0 11 22]", lines)
	}
}
