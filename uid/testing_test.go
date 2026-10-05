package uid

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

// loadProject writes files into a temp project, loads it the way the uid
// commands do, and returns the snapshot and its root.
func loadProject(t *testing.T, files map[string]string) (*project.Snapshot, string) {
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
	snapshot, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, root
}

// checkProject writes files into a temp project and checks their identities.
func checkProject(t *testing.T, files map[string]string) Report {
	t.Helper()
	snapshot, _ := loadProject(t, files)
	return Check(snapshot)
}

// countingReader stands in for a source of randomness, handing out ids in
// ascending order so a test can name the identifier it expects. The id is
// little-endian to match the generator.
type countingReader struct{ next uint64 }

func (r *countingReader) Read(p []byte) (int, error) {
	if len(p) < 8 {
		return 0, io.ErrShortBuffer
	}
	for i := 0; i < 8; i++ {
		p[i] = byte(r.next >> (8 * i))
	}
	r.next++
	return 8, nil
}

// seeded returns a generator whose first identifier is Encode(first).
func seeded(first uint64) *Generator {
	return NewGenerator(&countingReader{next: first})
}

// rules lists the rule of every diagnostic, in report order.
func rules(report Report) []string {
	found := make([]string, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		found = append(found, diagnostic.Rule)
	}
	return found
}

// paths lists the path of every diagnostic, in report order.
func paths(report Report) []string { return pathsOf(report.Diagnostics) }

// pathsOf lists the path of every diagnostic in a group Report hands out.
func pathsOf(diagnostics []Diagnostic) []string {
	found := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		found = append(found, diagnostic.Path)
	}
	return found
}

// readFile returns the contents of a project file.
func readFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
