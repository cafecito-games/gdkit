package format

import (
	"fmt"
	"path/filepath"

	"github.com/cafecito-games/gdkit/internal/atomicwrite"
	"github.com/cafecito-games/gdkit/project"
)

// Apply writes every changed result back to its file under snapshot.Root and
// returns the project-relative paths written, in order. A file whose contents
// no longer match the snapshot is not overwritten. It stops at the first such
// file or I/O failure, returning the paths already written alongside the error.
func Apply(snapshot *project.Snapshot, report Report) ([]string, error) {
	written := []string{}
	for _, result := range report.Results {
		if !result.Changed {
			continue
		}
		target := filepath.Join(snapshot.Root, filepath.FromSlash(result.Path))
		script := snapshot.Scripts[result.Path]
		if script == nil {
			return written, fmt.Errorf("write %s: file is not in the snapshot", result.Path)
		}
		if err := replaceFile(target, script.Source, result.Formatted); err != nil {
			return written, fmt.Errorf("write %s: %w", result.Path, err)
		}
		written = append(written, result.Path)
	}
	return written, nil
}

// errChangedOnDisk reports a target that was edited after the snapshot read it.
// It is kept as an alias so this package's tests and callers keep their name
// for the condition.
var errChangedOnDisk = atomicwrite.ErrChangedOnDisk

// replaceFile swaps target's contents for contents atomically, keeping its
// permission bits and refusing a target that changed under the run.
func replaceFile(target string, expected, contents []byte) error {
	return atomicwrite.Replace(target, ".gdkit-format-*", expected, contents)
}
