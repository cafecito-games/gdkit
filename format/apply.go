package format

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
var errChangedOnDisk = errors.New("file changed on disk since it was read")

// replaceFile swaps target's contents for contents, keeping its permission
// bits. The new contents are written beside the target and renamed over it, so
// an interrupted run leaves either the old file or the new one, never a
// truncated script. The target is read again just before the rename and must
// still hold expected, the source contents were computed from, so an edit made
// while the run was in progress is not lost.
func replaceFile(target string, expected, contents []byte) (err error) {
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".gdkit-format-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = temporary.Close()
			_ = os.Remove(temporary.Name())
		}
	}()
	if _, err = temporary.Write(contents); err != nil {
		return err
	}
	if err = temporary.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		err = errChangedOnDisk
		return err
	}
	err = os.Rename(temporary.Name(), target)
	return err
}
