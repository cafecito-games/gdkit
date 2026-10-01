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

func loadSingleScript(t *testing.T, source string) *Script {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.gd": source})
	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	script := snapshot.Scripts["a.gd"]
	if script == nil {
		t.Fatal("a.gd was not discovered")
	}
	return script
}

func TestScriptLineAndLineCount(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		wantCount int
		wantLines []string
	}{
		{"ordinary", "var a := 1\nvar b := 2\nvar c := 3\n", 3, []string{"var a := 1", "var b := 2", "var c := 3"}},
		{"no trailing newline", "var a := 1\nvar b := 2", 2, []string{"var a := 1", "var b := 2"}},
		{"trailing newline adds no line", "var a := 1\n", 1, []string{"var a := 1"}},
		{"empty file", "", 0, nil},
		{"only a newline", "\n", 1, []string{""}},
		{"blank line in the middle", "a\n\nb\n", 3, []string{"a", "", "b"}},
		{"trailing blank line", "a\n\n", 2, []string{"a", ""}},
		{"crlf", "var a := 1\r\nvar b := 2\r\n", 2, []string{"var a := 1", "var b := 2"}},
		{"crlf without trailing newline", "a\r\nb", 2, []string{"a", "b"}},
		{"trailing whitespace is preserved", "a \t\nb\n", 2, []string{"a \t", "b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := loadSingleScript(t, test.source)
			if got := script.LineCount(); got != test.wantCount {
				t.Fatalf("LineCount() = %d, want %d", got, test.wantCount)
			}
			if len(script.Lines) != script.LineCount() {
				t.Errorf("len(Lines) = %d, LineCount() = %d", len(script.Lines), script.LineCount())
			}
			if test.wantCount > 0 && script.Lines[0] != 0 {
				t.Errorf("Lines[0] = %d, want 0", script.Lines[0])
			}
			for index, want := range test.wantLines {
				if got := script.Line(index + 1); got != want {
					t.Errorf("Line(%d) = %q, want %q", index+1, got, want)
				}
			}
		})
	}
}

func TestScriptLineOutOfRangeReturnsEmpty(t *testing.T) {
	script := loadSingleScript(t, "var a := 1\nvar b := 2\nvar c := 3\n")
	for _, number := range []int{0, -1, 4, 1000} {
		if got := script.Line(number); got != "" {
			t.Errorf("Line(%d) = %q, want empty", number, got)
		}
	}
	empty := loadSingleScript(t, "")
	if got := empty.Line(1); got != "" {
		t.Errorf("Line(1) on an empty file = %q, want empty", got)
	}
}
