// Package atomicwrite replaces a file's contents in place, without losing an
// edit made while the caller was computing them.
//
// It is shared by every gdkit tool that writes: format, uid, and generate all
// need the same two guarantees, and a second copy of this would drift.
package atomicwrite

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// ErrChangedOnDisk reports a target that was edited after the caller read it.
var ErrChangedOnDisk = errors.New("file changed on disk since it was read")

// Replace swaps target's contents for contents, keeping its permission bits.
//
// The new contents are written beside the target and renamed over it, so an
// interrupted run leaves either the old file or the new one, never a truncated
// script. The target is read again just before the rename and must still hold
// expected, the contents were computed from, so an edit made while the run was
// in progress is not lost.
//
// prefix names the temporary file, so a tool's leftovers are identifiable.
func Replace(target, prefix string, expected, contents []byte) (err error) {
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), prefix)
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
		err = ErrChangedOnDisk
		return err
	}
	err = os.Rename(temporary.Name(), target)
	return err
}
