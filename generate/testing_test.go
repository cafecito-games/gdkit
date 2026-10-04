package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/format"
	"github.com/cafecito-games/gdkit/project"
)

// loadProject writes files into a temp project and loads it the way generate
// does: the universe unfiltered, the selection carrying the filters.
func loadProject(t *testing.T, files map[string]string) *project.Snapshot {
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
	snapshot, err := project.Load(project.Config{
		Root:      root,
		Selection: &project.Selection{SourceRoots: []string{"."}, HonorIgnoreFile: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func indexOf(t *testing.T, files map[string]string) *Index {
	t.Helper()
	return BuildIndex(loadProject(t, files))
}

// checkProject writes files into a temp project and runs a full check.
func checkProject(t *testing.T, config Config, files map[string]string) Report {
	t.Helper()
	plan, _ := planProject(t, config, files)
	return plan.Report()
}

// planProject runs a check and returns the plan alongside the snapshot.
func planProject(t *testing.T, config Config, files map[string]string) (Plan, *project.Snapshot) {
	t.Helper()
	snapshot := loadSelected(t, config, files)
	generator, err := New(config, format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return generator.Check(snapshot), snapshot
}

// loadSelected loads a project with config's filters as the selection.
func loadSelected(t *testing.T, config Config, files map[string]string) *project.Snapshot {
	t.Helper()
	root := t.TempDir()
	writeInto(t, root, files)
	snapshot, err := project.Load(project.Config{
		Root: root,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeInto(t *testing.T, root string, files map[string]string) {
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

// applyOnce runs check then Apply over a temp project and returns the files as
// they ended up on disk.
func applyOnce(t *testing.T, config Config, files map[string]string) map[string]string {
	t.Helper()
	root := t.TempDir()
	writeInto(t, root, files)
	snapshot, err := project.Load(project.Config{
		Root: root,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := New(config, format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(snapshot, generator.Check(snapshot), false); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for name := range files {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(contents)
	}
	return out
}

// assertDiagnostic fails unless report holds a diagnostic with rule.
func assertDiagnostic(t *testing.T, report Report, rule string) Diagnostic {
	t.Helper()
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == rule {
			return diagnostic
		}
	}
	t.Fatalf("diagnostics = %+v, want one with rule %s", report.Diagnostics, rule)
	return Diagnostic{}
}

// formatDefault is the project's default format config, named so tests read
// without importing format at every call site.
func formatDefault() format.Config { return format.DefaultConfig() }

func osReadFile(root, name string) (string, error) {
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	return string(contents), err
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
