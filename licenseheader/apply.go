package licenseheader

import (
	"fmt"
	"path/filepath"

	"github.com/cafecito-games/gdkit/internal/atomicwrite"
	"github.com/cafecito-games/gdkit/project"
)

// Apply atomically writes every changed result. A file edited since the
// snapshot was loaded is not overwritten, and the run stops at that file.
func Apply(snapshot *project.Snapshot, report Report) ([]string, error) {
	written := []string{}
	for _, result := range report.Results {
		if !result.Changed {
			continue
		}
		script := snapshot.Scripts[result.Path]
		if script == nil {
			return written, fmt.Errorf("write %s: file is not in the snapshot", result.Path)
		}
		target := filepath.Join(snapshot.Root, filepath.FromSlash(result.Path))
		if err := atomicwrite.Replace(target, ".gdkit-license-*", script.Source, result.Contents); err != nil {
			return written, fmt.Errorf("write %s: %w", result.Path, err)
		}
		written = append(written, result.Path)
	}
	return written, nil
}
