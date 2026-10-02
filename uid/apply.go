package uid

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cafecito-games/gdkit/project"
)

// sidecarPermissions are the bits a new sidecar is created with. A repaired
// one keeps the permissions it already had.
const sidecarPermissions os.FileMode = 0o644

// Apply gives every script the report found missing a sidecar a freshly
// generated identifier, and returns the sidecar paths it wrote, in order.
//
// When repair is set it also rewrites the sidecars in Report.Repairs: the
// malformed ones and every duplicate beyond the first claimant. That is left
// off by default because a new identifier changes what existing uid://
// references to the script resolve to, and gdkit does not rewrite references.
//
// Every identifier already present in the project is reserved on the
// generator first, including those of files Check does not speak for, so a
// generated identifier cannot collide with one already on disk. A sidecar that
// appeared since the snapshot was taken is never overwritten, not even to
// repair it. Apply stops at the first failure and returns the paths already
// written alongside the error.
func Apply(snapshot *project.Snapshot, report Report, generator *Generator, repair bool) ([]string, error) {
	if generator == nil {
		generator = NewGenerator(nil)
	}
	for _, sidecar := range snapshot.Sidecars {
		if id, valid := Decode(sidecar.Text); valid {
			generator.Reserve(id)
		}
	}

	written := []string{}
	work := report.Missing()
	if repair {
		work = append(work, report.Repairs()...)
	}
	for _, diagnostic := range work {
		path := diagnostic.Path + ".uid"
		id, err := generator.Next()
		if err != nil {
			return written, err
		}
		target := filepath.Join(snapshot.Root, filepath.FromSlash(path))
		contents := []byte(Encode(id) + "\n")
		replacing := diagnostic.Rule != RuleMissing
		if err := writeSidecar(target, contents, replacing); err != nil {
			return written, fmt.Errorf("write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}

// writeSidecar puts contents at target. A new sidecar is created exclusively,
// so a file that appeared since the snapshot was read is reported rather than
// clobbered. A replacement is written beside the target and renamed over it,
// keeping the permission bits, so an interrupted run leaves either the old
// identifier or the new one and never an empty file.
func writeSidecar(target string, contents []byte, replacing bool) (err error) {
	if !replacing {
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sidecarPermissions)
		if err != nil {
			return err
		}
		if _, err = file.Write(contents); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".gdkit-uid-*")
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
	err = os.Rename(temporary.Name(), target)
	return err
}
