package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

// lintProject writes files into a temp project and lints it with config.
func lintProject(t *testing.T, config Config, files map[string]string) Report {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude})
	if err != nil {
		t.Fatal(err)
	}
	linter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return linter.Lint(snapshot)
}

// lintSource lints one file named a.gd with the default configuration and
// returns only the diagnostics for the named rule.
func lintSource(t *testing.T, rule, source string) []Diagnostic {
	t.Helper()
	report := lintProject(t, DefaultConfig(), map[string]string{"a.gd": source})
	var out []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == rule {
			out = append(out, diagnostic)
		}
	}
	return out
}

// assertRule fails unless the named rule fired exactly on the given lines.
func assertRule(t *testing.T, rule, source string, lines ...int) {
	t.Helper()
	found := lintSource(t, rule, source)
	if len(found) != len(lines) {
		t.Fatalf("%s fired %d times, want %d: %v", rule, len(found), len(lines), found)
	}
	for index, line := range lines {
		if found[index].Line != line {
			t.Errorf("%s[%d] on line %d, want %d", rule, index, found[index].Line, line)
		}
	}
}
