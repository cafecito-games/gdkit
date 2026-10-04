package generate

import (
	"fmt"
	"path/filepath"

	"github.com/cafecito-games/gdkit/internal/atomicwrite"
	"github.com/cafecito-games/gdkit/project"
)

// Apply writes every changed candidate back to its file under snapshot.Root
// and returns the project-relative paths written, in order. When prune is set
// it also removes orphaned regions.
//
// A file whose contents no longer match the snapshot is not overwritten. It
// stops at the first such file or I/O failure, returning the paths already
// written alongside the error, because that list is what tells the caller the
// state the project is in.
func Apply(snapshot *project.Snapshot, plan Plan, prune bool) ([]string, error) {
	written := []string{}
	for _, candidate := range plan.Candidates {
		if !candidate.Changed {
			continue
		}
		script := snapshot.Scripts[candidate.Path]
		if script == nil {
			return written, fmt.Errorf("write %s: file is not in the snapshot", candidate.Path)
		}
		target := filepath.Join(snapshot.Root, filepath.FromSlash(candidate.Path))
		if err := atomicwrite.Replace(target, ".gdkit-generate-*", script.Source, candidate.Contents); err != nil {
			return written, fmt.Errorf("write %s: %w", candidate.Path, err)
		}
		written = append(written, candidate.Path)
	}
	if !prune {
		return written, nil
	}
	selected := map[string]bool{}
	for _, path := range snapshot.Selected {
		selected[path] = true
	}
	for _, path := range plan.Orphans {
		// --prune does not write outside the selection. The diagnostic says
		// the file must re-enter it, rather than the tool quietly reaching
		// into a directory the config excluded.
		if !selected[path] {
			continue
		}
		script := snapshot.Scripts[path]
		if script == nil {
			continue
		}
		span, found, err := FindRegion(script.Source)
		if err != nil || !found {
			continue
		}
		pruned := make([]byte, 0, len(script.Source)-(span.End-span.Start))
		pruned = append(pruned, script.Source[:span.Start]...)
		pruned = append(pruned, script.Source[span.End:]...)
		target := filepath.Join(snapshot.Root, filepath.FromSlash(path))
		if err := atomicwrite.Replace(target, ".gdkit-generate-*", script.Source, pruned); err != nil {
			return written, fmt.Errorf("prune %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}
