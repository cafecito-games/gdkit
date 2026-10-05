package uid

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/gdkit/internal/atomicwrite"
	"github.com/cafecito-games/gdkit/project"
)

// sidecarPermissions are the bits a new sidecar is created with. An existing
// file keeps the permissions it already had.
const sidecarPermissions os.FileMode = 0o644

// temporaryPrefix names the files a replacement is written through, so
// leftovers from an interrupted run are identifiable.
const temporaryPrefix = ".gdkit-uid-*"

// Apply writes everything the report says can be written, and returns the
// project-relative paths of the files it changed, in the order it changed
// them.
//
// Three kinds of change, in this order:
//
//   - every script the report found missing a sidecar is given a freshly
//     generated identifier;
//   - when repair is set, every declared identity the report cannot trust is
//     replaced — a malformed one wherever it is declared, and a duplicated one
//     beyond its first claimant — and the references that named the old value
//     are moved with it, so no run leaves the project more broken than it
//     found it. This is off by default because a new identifier changes what
//     every reference resolves to, including one in a file gdkit cannot see;
//   - every broken reference whose path= names a file with a trustworthy
//     identity is repointed at it, whether or not repair is set. The path is
//     the authority there, and it is what Godot already falls back to, so the
//     rewrite cannot change what the project loads.
//
// Every identifier already present in the project is reserved on the generator
// first, including those of files Check does not report on, so a generated
// identifier cannot collide with one already on disk.
//
// A reference is rewritten only if the line it was read from still holds the
// text it held; if any does not, Apply writes nothing at all and returns the
// error, rather than leaving half a repair behind. A sidecar that appeared
// since the snapshot was taken is never overwritten either. Once writing
// starts Apply stops at the first failure and returns the paths already
// written alongside the error, because that list is what tells the caller the
// state the project is in.
func Apply(snapshot *project.Snapshot, report Report, generator *Generator, repair bool) ([]string, error) {
	if generator == nil {
		generator = NewGenerator(nil)
	}
	reserve(snapshot, generator)

	creations, changes, err := planWrites(snapshot, report, generator, repair)
	if err != nil {
		return nil, err
	}
	// Everything is read and verified before anything is written, so a
	// reference that moved under the run stops it rather than splitting it.
	prepared, err := prepare(snapshot.Root, changes)
	if err != nil {
		return nil, err
	}

	written := []string{}
	for _, creation := range creations {
		target := filepath.Join(snapshot.Root, filepath.FromSlash(creation.path))
		if err := createSidecar(target, creation.contents); err != nil {
			return written, fmt.Errorf("write %s: %w", creation.path, err)
		}
		written = append(written, creation.path)
	}
	for _, change := range prepared {
		target := filepath.Join(snapshot.Root, filepath.FromSlash(change.path))
		if err := atomicwrite.Replace(target, temporaryPrefix, change.before, change.after); err != nil {
			return written, fmt.Errorf("write %s: %w", change.path, err)
		}
		written = append(written, change.path)
	}
	return written, nil
}

// reserve marks every identifier already on disk as taken. Both lists are read
// because a snapshot loaded without project.Config.Identities carries no
// claims, and a generated identifier must not collide with a shader's either
// way.
func reserve(snapshot *project.Snapshot, generator *Generator) {
	for _, sidecar := range snapshot.Sidecars {
		if id, valid := Decode(sidecar.Text); valid {
			generator.Reserve(id)
		}
	}
	for _, claim := range snapshot.Claims {
		if id, valid := Decode(claim.UID); valid {
			generator.Reserve(id)
		}
	}
}

// creation is one sidecar that does not exist yet.
type creation struct {
	path     string
	contents []byte
}

// change is one existing file to rewrite: either whole, for a sidecar, which
// holds nothing but the identifier, or line by line for a declaration or
// reference that sits among other text.
type change struct {
	path  string
	whole string
	edits []edit
}

// edit replaces one occurrence of an identifier on one line.
type edit struct {
	line int
	from string
	to   string
}

// planWrites turns the report into the files to create and the files to
// rewrite, minting an identifier for each declaration that needs one. The
// order is the report's: the missing sidecars, then the reissues, then the
// reference rewrites.
func planWrites(snapshot *project.Snapshot, report Report, generator *Generator, repair bool) ([]creation, []change, error) {
	var creations []creation
	changes := newChangeSet()
	for _, diagnostic := range report.Missing() {
		identifier, err := generator.Next()
		if err != nil {
			return nil, nil, err
		}
		creations = append(creations, creation{
			path:     diagnostic.Path + sidecarExtension,
			contents: []byte(Encode(identifier) + "\n"),
		})
	}
	if repair {
		for _, entry := range report.work.reissues {
			identifier, err := generator.Next()
			if err != nil {
				return nil, nil, err
			}
			replacement := Encode(identifier)
			if entry.claim.Kind == project.ClaimSidecar {
				changes.whole(entry.claim.Path, replacement+"\n")
			} else {
				changes.edit(entry.claim.Path, edit{line: entry.claim.Line, from: entry.claim.UID, to: replacement})
			}
			for _, reference := range entry.references {
				changes.edit(reference.Path, edit{line: reference.Line, from: reference.UID, to: replacement})
			}
		}
	}
	for _, rewrite := range report.work.rewrites {
		changes.edit(rewrite.reference.Path, edit{
			line: rewrite.reference.Line,
			from: rewrite.reference.UID,
			to:   rewrite.to,
		})
	}
	return creations, changes.ordered, nil
}

// sidecarExtension is appended to a file's path to name its sidecar.
const sidecarExtension = ".uid"

// changeSet collects edits per file while keeping the order the files were
// first touched in, so a file edited twice is written once and the reported
// order stays the report's.
type changeSet struct {
	ordered []change
	index   map[string]int
}

func newChangeSet() *changeSet { return &changeSet{index: map[string]int{}} }

func (c *changeSet) at(path string) *change {
	if position, found := c.index[path]; found {
		return &c.ordered[position]
	}
	c.index[path] = len(c.ordered)
	c.ordered = append(c.ordered, change{path: path})
	return &c.ordered[len(c.ordered)-1]
}

func (c *changeSet) whole(path, contents string) { c.at(path).whole = contents }

func (c *changeSet) edit(path string, e edit) {
	entry := c.at(path)
	entry.edits = append(entry.edits, e)
}

// prepared is one file's old and new contents, computed before anything is
// written so the whole run can be abandoned instead of half-applied.
type prepared struct {
	path   string
	before []byte
	after  []byte
}

// prepare reads every file a change touches and computes its new contents. A
// line that no longer holds the identifier it was read with fails the whole
// run: the file has been edited since the snapshot, and guessing which
// occurrence was meant is how a repair corrupts a scene.
func prepare(root string, changes []change) ([]prepared, error) {
	results := make([]prepared, 0, len(changes))
	for _, item := range changes {
		target := filepath.Join(root, filepath.FromSlash(item.path))
		before, err := os.ReadFile(target)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", item.path, err)
		}
		after := before
		if item.whole != "" {
			after = []byte(item.whole)
		}
		for _, e := range item.edits {
			after, err = applyEdit(after, e)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", item.path, e.line, err)
			}
		}
		results = append(results, prepared{path: item.path, before: before, after: after})
	}
	return results, nil
}

// applyEdit replaces the identifier on one line. The line must hold it exactly
// once: nothing else makes the rewrite provably the one that was planned.
func applyEdit(contents []byte, e edit) ([]byte, error) {
	lines := splitLines(contents)
	if e.line < 1 || e.line > len(lines) {
		return nil, fmt.Errorf("the file has no line %d to rewrite", e.line)
	}
	line := string(lines[e.line-1])
	if count := strings.Count(line, e.from); count != 1 {
		return nil, fmt.Errorf("the line holds %s %d times, want exactly once", e.from, count)
	}
	lines[e.line-1] = []byte(strings.Replace(line, e.from, e.to, 1))
	return bytes.Join(lines, nil), nil
}

// splitLines splits contents into lines, each keeping its own terminator, so
// joining them again reproduces the file byte for byte.
func splitLines(contents []byte) [][]byte {
	var lines [][]byte
	for len(contents) > 0 {
		end := bytes.IndexByte(contents, '\n')
		if end < 0 {
			lines = append(lines, contents)
			break
		}
		lines = append(lines, contents[:end+1])
		contents = contents[end+1:]
	}
	return lines
}

// createSidecar writes a sidecar that did not exist. It is created
// exclusively, so a file that appeared since the snapshot was read is reported
// rather than clobbered.
func createSidecar(target string, contents []byte) error {
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
