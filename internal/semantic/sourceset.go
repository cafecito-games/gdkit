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
