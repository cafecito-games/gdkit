// Package semanticsource adapts gdkit project snapshots to semantic sources.
package semanticsource

import (
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdkit/uid"
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
	resources  map[string]semantic.ResourceKind
	uidClaims  map[uint64][]string
	badClaims  map[uint64]bool
	identities bool
	incomplete bool
}

var _ semantic.SourceSet = (*Snapshot)(nil)
var _ semantic.ResourceResolver = (*Snapshot)(nil)

// NewSnapshot adapts the snapshot's complete Paths universe, deliberately not
// its filtered Selected action subset.
func NewSnapshot(snapshot *project.Snapshot) *Snapshot {
	adapter := &Snapshot{
		files:      map[string]*ast.File{},
		uids:       map[string]string{},
		autoloads:  map[string]string{},
		scriptPath: map[string]bool{},
		resources:  map[string]semantic.ResourceKind{},
		uidClaims:  map[uint64][]string{},
		badClaims:  map[uint64]bool{},
	}
	if snapshot == nil {
		return adapter
	}
	adapter.paths = append([]string(nil), snapshot.Paths...)
	adapter.files = make(map[string]*ast.File, len(snapshot.Paths))
	adapter.uids = clone(snapshot.UIDs)
	adapter.autoloads = clone(snapshot.Autoloads)
	adapter.scriptPath = make(map[string]bool, len(snapshot.Paths))
	adapter.resources = make(map[string]semantic.ResourceKind, len(snapshot.Resources))
	adapter.identities = snapshot.IdentityEvidence
	adapter.incomplete = snapshot.IdentityIncomplete
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
	for _, resource := range snapshot.Resources {
		if !canonicalProjectPath(resource.Path) {
			continue
		}
		kind, ok := semanticResourceKind(resource.Kind)
		if !ok {
			continue
		}
		adapter.resources[resource.Path] = kind
	}
	if adapter.identities {
		claims := make(map[uint64]map[string]bool)
		for _, claim := range snapshot.Claims {
			identifier, ok := uid.Decode(claim.UID)
			if !ok {
				continue
			}
			if !canonicalProjectPath(claim.Owner) {
				adapter.badClaims[identifier] = true
				continue
			}
			if claims[identifier] == nil {
				claims[identifier] = map[string]bool{}
			}
			claims[identifier][claim.Owner] = true
		}
		for identifier, owners := range claims {
			for owner := range owners {
				adapter.uidClaims[identifier] = append(adapter.uidClaims[identifier], owner)
			}
			sort.Strings(adapter.uidClaims[identifier])
		}
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

// ResolvePreloadResource resolves one literal preload spelling exclusively
// from the copied project inventory and lossless UID claims. A relative
// preload is relative to the script that holds it. It performs no I/O and
// never falls back to Snapshot.UIDs, whose selected winner cannot establish a
// unique identity claimant.
func (s *Snapshot) ResolvePreloadResource(from, target string) semantic.ResourceResolution {
	return s.resolveResource(from, target, true)
}

// ResolveLoadResource resolves one literal load spelling exclusively from the
// copied project inventory and lossless UID claims. Godot resolves a relative
// load from res:// rather than from its calling script. It performs no I/O and
// never falls back to Snapshot.UIDs, whose selected winner cannot establish a
// unique identity claimant.
func (s *Snapshot) ResolveLoadResource(from, target string) semantic.ResourceResolution {
	return s.resolveResource(from, target, false)
}

func (s *Snapshot) resolveResource(from, target string, preload bool) semantic.ResourceResolution {
	if s == nil {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, resourceProvenance(target), "resource resolver is unavailable")
	}
	if !s.scriptPath[from] || !canonicalProjectPath(from) {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, resourceProvenance(target), "resource source path is not a known project script")
	}
	if strings.HasPrefix(target, uid.Prefix) {
		return s.resolveUID(target)
	}
	if target == "" || path.IsAbs(target) || strings.Contains(target, "\\") {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceLiteralPath, "resource path is empty, absolute, or malformed")
	}
	var canonical string
	switch {
	case strings.HasPrefix(target, project.ResourceScheme):
		canonical = path.Clean(strings.TrimPrefix(target, project.ResourceScheme))
	case strings.Contains(target, "://"):
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceLiteralPath, "resource path uses an unsupported scheme")
	case preload:
		canonical = path.Clean(path.Join(path.Dir(from), target))
	default:
		canonical = path.Clean(target)
	}
	if canonical == "." || canonical == "" {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceLiteralPath, "resource path names no project resource")
	}
	if canonical == ".." || strings.HasPrefix(canonical, "../") {
		return semantic.UnresolvedResource(semantic.ResourceEscapesProject, semantic.ResourceUnknown, target, semantic.ResourceLiteralPath, "resource path escapes the project")
	}
	if !canonicalProjectPath(canonical) {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceLiteralPath, "resource path is malformed")
	}
	return s.resolveInventory(target, canonical, semantic.ResourceLiteralPath)
}

func (s *Snapshot) resolveUID(target string) semantic.ResourceResolution {
	identifier, valid := uid.Decode(target)
	if !valid {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceUIDClaim, "resource UID is malformed")
	}
	if !s.identities {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceUIDClaim, "resource UID claim evidence was not requested")
	}
	if s.incomplete {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceUIDClaim, "resource UID claim evidence is incomplete")
	}
	if s.badClaims[identifier] {
		return semantic.UnresolvedResource(semantic.ResourceInvalid, semantic.ResourceUnknown, target, semantic.ResourceUIDClaim, "resource UID has malformed captured claimant evidence")
	}
	owners := s.uidClaims[identifier]
	switch len(owners) {
	case 0:
		return semantic.UnresolvedResource(semantic.ResourceMissing, semantic.ResourceUnknown, target, semantic.ResourceUIDClaim, "resource UID has no captured claimant")
	case 1:
		return s.resolveInventory(target, owners[0], semantic.ResourceUIDClaim)
	default:
		return semantic.UnresolvedResource(semantic.ResourceAmbiguousUID, semantic.ResourceUnknown, target, semantic.ResourceUIDClaim, "resource UID has multiple captured claimants: "+strings.Join(owners, ", "))
	}
}

func (s *Snapshot) resolveInventory(requested, canonical string, provenance semantic.ResourceProvenance) semantic.ResourceResolution {
	kind, found := s.resources[canonical]
	if !found {
		return semantic.UnresolvedResource(semantic.ResourceMissing, semantic.ResourceUnknown, requested, provenance, "resource is absent from the immutable inventory: "+canonical)
	}
	if kind == semantic.ResourceImported {
		return semantic.UnresolvedResource(semantic.ResourceUnsupportedKind, kind, requested, provenance, "resource kind imported is not typeable: "+canonical)
	}
	return semantic.FoundResource(kind, requested, canonical, provenance)
}

func resourceProvenance(target string) semantic.ResourceProvenance {
	if strings.HasPrefix(target, uid.Prefix) {
		return semantic.ResourceUIDClaim
	}
	return semantic.ResourceLiteralPath
}

func semanticResourceKind(kind project.ResourceKind) (semantic.ResourceKind, bool) {
	switch kind {
	case project.ResourceScript:
		return semantic.ResourceScript, true
	case project.ResourceScene:
		return semantic.ResourceScene, true
	case project.ResourceText:
		return semantic.ResourceText, true
	case project.ResourceImported:
		return semantic.ResourceImported, true
	default:
		return semantic.ResourceUnknown, false
	}
}

func canonicalProjectPath(value string) bool {
	if value == "" || value == "." || path.IsAbs(value) || strings.Contains(value, "\\") || strings.Contains(value, "://") {
		return false
	}
	if path.Clean(value) != value {
		return false
	}
	return value != ".." && !strings.HasPrefix(value, "../")
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
