package semantic

import "github.com/cafecito-games/gdparser/ast"

// SourceSet is the complete, already-parsed script universe consumed by the
// semantic analyzer. Implementations perform no parsing or I/O through this
// interface.
type SourceSet interface {
	Paths() []string
	File(path string) *ast.File
	ResolvePath(from, target string) (path string, ok bool)
	Autoloads() map[string]string
	ParseFailures() []string
}

// ResourceResolver is an optional immutable capability a SourceSet may expose
// for literal preload/load evidence. It deliberately sits beside SourceSet so
// #45's script-only ResolvePath contract remains source-compatible and cannot
// be mistaken for resource existence or UID uniqueness.
type ResourceResolver interface {
	ResolveResource(from, target string) ResourceResolution
}
