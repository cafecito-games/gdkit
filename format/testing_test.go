package format

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

// loadProject writes files into a temp project and loads it with config's
// discovery settings.
func loadProject(t *testing.T, config Config, files map[string]string) *project.Snapshot {
	t.Helper()
	return loadProjectInto(t, t.TempDir(), config, files)
}

// loadProjectInto writes files under root and loads the project there.
func loadProjectInto(t testing.TB, root string, config Config, files map[string]string) *project.Snapshot {
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
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// formatProject writes files into a temp project and formats it with config.
func formatProject(t *testing.T, config Config, files map[string]string) (Report, *project.Snapshot) {
	t.Helper()
	snapshot := loadProject(t, config, files)
	formatter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return formatter.Format(snapshot), snapshot
}

// formatSource formats one file named a.gd and returns its output, failing the
// test when the file produced a diagnostic. Unchanged input is returned as is.
func formatSource(t *testing.T, config Config, source string) string {
	t.Helper()
	report, _ := formatProject(t, config, map[string]string{"a.gd": source})
	if report.HasDiagnostics() {
		t.Fatalf("fixture did not format: %s", report.Diagnostics[0])
	}
	if len(report.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(report.Results))
	}
	if !report.Results[0].Changed {
		return source
	}
	return string(report.Results[0].Formatted)
}
