package generate

import (
	"os"
	"path/filepath"
	"testing"

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
