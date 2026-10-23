package assets

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdkit/project"
)

// TestCorpus runs the checker over a real project given by GDKIT_CORPUS.
//
// This tool needs a corpus more than the others do. Its failure mode is a
// confidently wrong deletion list, and no fixture will tell you about one: a
// fixture only contains what its author thought of, and the references that
// break this analysis are the ones nobody thinks of. What is asserted here is
// therefore not a finding count, which would be a snapshot of one project's
// housekeeping, but the two properties that must hold on any project at all.
func TestCorpus(t *testing.T) {
	root := os.Getenv("GDKIT_CORPUS")
	if root == "" {
		t.Skip("GDKIT_CORPUS is not set")
	}
	config := DefaultConfig()
	checker, err := NewChecker(config)
	require.NoError(t, err)

	load := project.Config{
		Root:                    root,
		Inventory:               true,
		FollowDirectorySymlinks: true,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	}
	snapshot, err := project.Load(load)
	require.NoError(t, err)
	report := checker.Check(snapshot)

	// Most of a working project must be reachable. A regression that breaks
	// one edge kind — class_name references above all — does not break a
	// fixture, it quietly condemns thousands of files, and this is the shape
	// of assertion that notices.
	require.Positive(t, report.Reportable)
	dead := 0
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == RuleUnreferenced || diagnostic.Rule == RuleUnreachable {
			dead++
		}
	}
	assert.Less(t, dead*2, report.Reportable,
		"more than half of a working project reported as unused: %d of %d", dead, report.Reportable)

	// Every finding names a file the run was allowed to report on, and no
	// finding names a file twice under the same rule.
	selected := map[string]bool{}
	for _, file := range snapshot.SelectedFiles {
		selected[file] = true
	}
	seen := map[[2]string]bool{}
	for _, diagnostic := range report.Diagnostics {
		assert.True(t, selected[diagnostic.Path], "finding outside the selected set: %s", diagnostic.Path)
		if diagnostic.Rule == RuleUnreferenced || diagnostic.Rule == RuleUnreachable {
			key := [2]string{diagnostic.Rule, diagnostic.Path}
			assert.False(t, seen[key], "duplicate finding: %s %s", diagnostic.Rule, diagnostic.Path)
			seen[key] = true
		}
	}
}

// TestCorpusIsDeterministic pins that two loads of the same tree produce the
// same report, which map iteration over the graph could easily break.
func TestCorpusIsDeterministic(t *testing.T) {
	root := os.Getenv("GDKIT_CORPUS")
	if root == "" {
		t.Skip("GDKIT_CORPUS is not set")
	}
	config := DefaultConfig()
	checker, err := NewChecker(config)
	require.NoError(t, err)
	load := project.Config{
		Root:                    root,
		Inventory:               true,
		FollowDirectorySymlinks: true,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	}
	first, err := project.Load(load)
	require.NoError(t, err)
	second, err := project.Load(load)
	require.NoError(t, err)
	assert.Equal(t, checker.Check(first), checker.Check(second))
}
