package format

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
	"github.com/stretchr/testify/require"
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
		err := os.MkdirAll(filepath.Dir(absolute), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(absolute, []byte(contents), 0o644)
		require.NoError(t, err)
	}
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude})
	require.NoError(t, err)
	return snapshot
}

// formatProject writes files into a temp project and formats it with config.
func formatProject(t *testing.T, config Config, files map[string]string) (Report, *project.Snapshot) {
	t.Helper()
	snapshot := loadProject(t, config, files)
	formatter, err := New(config)
	require.NoError(t, err)
	return formatter.Format(snapshot), snapshot
}

// formatSource formats one file named a.gd and returns its output, failing the
// test when the file produced a diagnostic. Unchanged input is returned as is.
func formatSource(t *testing.T, config Config, source string) string {
	t.Helper()
	report, _ := formatProject(t, config, map[string]string{"a.gd": source})
	require.False(t, report.HasDiagnostics(), "fixture did not format")
	require.Len(t, report.Results, 1)
	if !report.Results[0].Changed {
		return source
	}
	return string(report.Results[0].Formatted)
}

// formatForged formats one file named a.gd with a formatter that emits forged
// whatever the source says, standing in for a formatter that damages its input.
func formatForged(t *testing.T, source, forged string) Report {
	t.Helper()
	snapshot := loadProject(t, DefaultConfig(), map[string]string{"a.gd": source})
	formatter, err := New(DefaultConfig())
	require.NoError(t, err)
	formatter.emit = func(*ast.File, gdformat.Options) string { return forged }
	return formatter.Format(snapshot)
}

// assertRefused fails unless report holds no result and the one diagnostic
// want.
func assertRefused(t *testing.T, report Report, want Diagnostic) {
	t.Helper()
	require.Empty(t, report.Results)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, want, report.Diagnostics[0])
}

// formatForgedPastTheTreeCheck is formatForged with the tree comparison stood
// down, so a later check is what decides the file. It stands in for a parser
// that records nothing of the difference between source and output.
func formatForgedPastTheTreeCheck(t *testing.T, source, forged string) Report {
	t.Helper()
	snapshot := loadProject(t, DefaultConfig(), map[string]string{"a.gd": source})
	formatter, err := New(DefaultConfig())
	require.NoError(t, err)
	formatter.emit = func(*ast.File, gdformat.Options) string { return forged }
	formatter.verifyTree = func(string, *ast.File, []byte, gdformat.Options) error { return nil }
	return formatter.Format(snapshot)
}
