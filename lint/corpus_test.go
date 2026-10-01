package lint

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

// corpusConfig is the default policy with the tool-managed directories that
// hold duplicate checkouts of a project excluded, so a corpus rooted at a
// working directory is not inflated by agent worktrees.
func corpusConfig() Config {
	config := DefaultConfig()
	config.Exclude = append(config.Exclude, ".worktrees/**", ".claude/**")
	return config
}

// loadCorpus loads the project rooted at root with config.
func loadCorpus(t *testing.T, root string, config Config) *project.Snapshot {
	t.Helper()
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// TestCorpus lints a real project given by GDKIT_CORPUS. It asserts the linter
// does not panic and that two runs over one snapshot produce byte-identical
// reports. It is skipped when the variable is unset so CI stays hermetic.
func TestCorpus(t *testing.T) {
	root := os.Getenv("GDKIT_CORPUS")
	if root == "" {
		t.Skip("GDKIT_CORPUS is not set")
	}
	config := corpusConfig()
	snapshot := loadCorpus(t, root, config)
	linter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	report := linter.Lint(snapshot)
	if string(marshalReport(t, report)) != string(marshalReport(t, linter.Lint(snapshot))) {
		t.Fatal("two runs over the same snapshot produced different reports")
	}
	t.Logf("linted %d files, %d diagnostics", len(snapshot.Paths), len(report.Diagnostics))
}

func marshalReport(t *testing.T, report Report) []byte {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
