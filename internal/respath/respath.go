// Package respath resolves the resource references a Godot project writes —
// a uid:// identifier, a res:// path, or a path relative to the file holding
// it — to the project-relative path they name.
//
// It is shared rather than duplicated because two tools ask the same question
// for opposite reasons. architecture asks whether a reference crosses a
// boundary, so everything it cannot place is simply not a dependency. assets
// asks whether anything still needs a file, so everything it cannot place is a
// hole in a reachability proof and has to be distinguishable. One resolver
// with a named outcome serves both; a bool serves only the first.
package respath

import (
	"path"
	"strings"
)

// UIDScheme precedes every uid:// identifier Godot writes, and ResourceScheme
// precedes a project-relative resource path.
const (
	UIDScheme      = "uid://"
	ResourceScheme = "res://"
)

// Kind names what a reference turned out to be.
type Kind int

const (
	// Project is a reference that names a path inside the project.
	Project Kind = iota + 1
	// Unresolved is a uid:// identifier no file in the project declares.
	// Whether that is an error depends on the caller: it is the same
	// evidence either way.
	Unresolved
	// External is user:// or any scheme other than res:// and uid://. It
	// names nothing the project ships.
	External
	// Escaping is a relative path that climbs above the project root.
	Escaping
)

// Resolution is the outcome of resolving one reference.
type Resolution struct {
	Kind Kind
	// Path is project-relative and slash-separated for Project, and for
	// Escaping is the escaping path as it resolved. For Unresolved it is the
	// uid:// identifier itself, which is the only name that reference has.
	// It is empty for External.
	Path string
}

// Resolve places reference, written inside the project-relative file from,
// against the uid:// table uids. A nil table resolves no identifier, which
// makes every uid:// reference Unresolved rather than a panic.
func Resolve(from, reference string, uids map[string]string) Resolution {
	if strings.HasPrefix(reference, UIDScheme) {
		if target, declared := uids[reference]; declared {
			return Resolution{Kind: Project, Path: target}
		}
		return Resolution{Kind: Unresolved, Path: reference}
	}
	// A scheme other than res:// names something the project does not ship:
	// user:// is the player's save directory, and the rest are the engine's.
	if strings.Contains(reference, "://") && !strings.HasPrefix(reference, ResourceScheme) {
		return Resolution{Kind: External}
	}
	target := reference
	if strings.HasPrefix(reference, ResourceScheme) {
		target = strings.TrimPrefix(reference, ResourceScheme)
	} else {
		target = path.Join(path.Dir(from), reference)
	}
	target = path.Clean(strings.TrimPrefix(target, "/"))
	if target == ".." || strings.HasPrefix(target, "../") {
		return Resolution{Kind: Escaping, Path: target}
	}
	return Resolution{Kind: Project, Path: target}
}

// IsReference reports text that is written as a resource reference, whether or
// not it resolves. A bare relative path is not one: it is indistinguishable
// from any other string, and treating every string in a project as a possible
// path would make reachability meaningless.
func IsReference(text string) bool {
	return strings.HasPrefix(text, UIDScheme) || strings.HasPrefix(text, ResourceScheme)
}
