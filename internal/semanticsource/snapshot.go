// Package semanticsource adapts gdkit project snapshots to semantic sources.
package semanticsource

import (
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

// Snapshot is a read-only semantic.SourceSet backed by an already-loaded
// project snapshot. It copies provider collections and never reparses files.
type Snapshot struct {
	paths      []string
	files      map[string]*ast.File
	uids       map[string]string
	autoloads  map[string]string
	failures   []string
	scriptPath map[string]bool
}

var _ semantic.SourceSet = (*Snapshot)(nil)

// NewSnapshot adapts the snapshot's complete Paths universe, deliberately not
// its filtered Selected action subset.
func NewSnapshot(snapshot *project.Snapshot) *Snapshot {
	adapter := &Snapshot{
		paths:      append([]string(nil), snapshot.Paths...),
		files:      make(map[string]*ast.File, len(snapshot.Paths)),
		uids:       clone(snapshot.UIDs),
		autoloads:  clone(snapshot.Autoloads),
		scriptPath: make(map[string]bool, len(snapshot.Paths)),
	}
	sort.Strings(adapter.paths)
	for _, filePath := range adapter.paths {
		adapter.scriptPath[filePath] = true
		script := snapshot.Scripts[filePath]
		if script == nil || script.File == nil || script.ParseError != nil {
			adapter.failures = append(adapter.failures, filePath)
			continue
		}
		adapter.files[filePath] = script.File
	}
	sort.Strings(adapter.failures)
	return adapter
}

func (s *Snapshot) Paths() []string { return append([]string(nil), s.paths...) }

func (s *Snapshot) File(filePath string) *ast.File { return s.files[filePath] }

// ResolvePath resolves only static script targets present in the snapshot.
func (s *Snapshot) ResolvePath(from, target string) (string, bool) {
	var resolved string
	if strings.HasPrefix(target, "uid://") {
		resolved = s.uids[target]
		if resolved == "" {
			return "", false
		}
	} else if strings.HasPrefix(target, "res://") {
		resolved = path.Clean(strings.TrimPrefix(target, "res://"))
	} else {
		resolved = path.Clean(path.Join(path.Dir(from), target))
	}
	if !s.scriptPath[resolved] {
		return "", false
	}
	return resolved, true
}

func (s *Snapshot) Autoloads() map[string]string { return clone(s.autoloads) }

func (s *Snapshot) ParseFailures() []string { return append([]string(nil), s.failures...) }

func clone(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
