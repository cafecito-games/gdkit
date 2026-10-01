// Package project discovers and parses the GDScript sources of a Godot
// project. It is the only place in gdkit that reads a project from disk, so
// every tool agrees on which files are in scope.
package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/glob"
	"github.com/cafecito-games/gdkit/internal/ignore"
	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
)

// IgnoreFileName is the gitignore-style file at the project root that lists
// paths a tool skips when it sets Config.HonorIgnoreFile.
const IgnoreFileName = ".gdkitignore"

// Config selects the files that belong to a project.
type Config struct {
	Root        string
	SourceRoots []string
	Exclude     []string
	// HonorIgnoreFile also skips the paths listed in the root IgnoreFileName.
	// Exclude still applies: a path is skipped when either one covers it, so a
	// negated ignore pattern cannot bring back an excluded path.
	HonorIgnoreFile bool
}

// Script is one discovered GDScript file.
type Script struct {
	// Path is project-relative and slash-separated.
	Path string
	// Source is the exact file content. Rules about whitespace and line
	// length read this; everything else reads File.
	Source []byte
	// File is the parsed tree, or nil when ParseError is set.
	File *ast.File
	// ParseError is the parse failure, if any. Callers turn it into whatever
	// diagnostic their tool reports.
	ParseError error
	// Lines holds the byte offset at which each one-based line starts.
	Lines []int
}

// Line returns the text of the one-based line number, without its terminator.
func (s *Script) Line(number int) string {
	if number < 1 || number > len(s.Lines) {
		return ""
	}
	start := s.Lines[number-1]
	end := len(s.Source)
	if number < len(s.Lines) {
		end = s.Lines[number]
	}
	text := string(s.Source[start:end])
	text = strings.TrimSuffix(text, "\n")
	return strings.TrimSuffix(text, "\r")
}

// LineCount is the number of lines in the file.
func (s *Script) LineCount() int { return len(s.Lines) }

// Snapshot is an immutable view of one project.
type Snapshot struct {
	Root string
	// Paths are every discovered script, sorted.
	Paths []string
	// Scripts is keyed by the same paths.
	Scripts map[string]*Script
	// UIDs maps uid:// identifiers from .uid sidecars to their source path.
	UIDs map[string]string
}

// Load walks the configured source roots and parses every .gd file it finds.
// A file that fails to parse is still present in the snapshot, carrying its
// ParseError; only I/O and configuration problems return an error.
func Load(config Config) (*Snapshot, error) {
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	var ignored *ignore.Matcher
	if config.HonorIgnoreFile {
		ignored, err = loadIgnoreFile(root)
		if err != nil {
			return nil, err
		}
	}
	sourceRoots := config.SourceRoots
	if len(sourceRoots) == 0 {
		sourceRoots = []string{"."}
	}

	seen := make(map[string]struct{})
	uids := make(map[string]string)
	for _, sourceRoot := range sourceRoots {
		absolute := filepath.Join(root, filepath.FromSlash(sourceRoot))
		relativeRoot, relErr := filepath.Rel(root, absolute)
		if relErr != nil || relativeRoot == ".." || strings.HasPrefix(filepath.ToSlash(relativeRoot), "../") {
			return nil, fmt.Errorf("source root %q is outside the project root", sourceRoot)
		}
		info, statErr := os.Stat(absolute)
		if statErr != nil {
			return nil, fmt.Errorf("source root %q: %w", sourceRoot, statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("source root %q: not a directory", sourceRoot)
		}
		walkErr := filepath.WalkDir(absolute, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, relErr := filepath.Rel(root, name)
			if relErr != nil {
				return relErr
			}
			relative = filepath.ToSlash(relative)
			if relative == "." {
				return nil
			}
			if glob.MatchAny(config.Exclude, relative) || entry.IsDir() && glob.MatchAny(config.Exclude, relative+"/") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				// A negated pattern can re-include something below an ignored
				// directory, so the directory is only pruned when there is none.
				if !ignored.HasNegation() && ignored.Ignored(relative, true) {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			switch {
			case strings.HasSuffix(relative, ".gd"):
				if ignored.Ignored(relative, false) {
					return nil
				}
				seen[relative] = struct{}{}
			case strings.HasSuffix(relative, ".uid"):
				if ignored.Ignored(relative, false) || ignored.Ignored(strings.TrimSuffix(relative, ".uid"), false) {
					return nil
				}
				data, readErr := os.ReadFile(name)
				if readErr != nil {
					return nil
				}
				uid := strings.TrimSpace(string(data))
				if strings.HasPrefix(uid, "uid://") {
					uids[uid] = strings.TrimSuffix(relative, ".uid")
				}
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("discover GDScript in %q: %w", sourceRoot, walkErr)
		}
	}

	paths := make([]string, 0, len(seen))
	for name := range seen {
		paths = append(paths, name)
	}
	sort.Strings(paths)

	scripts := make(map[string]*Script, len(paths))
	for _, name := range paths {
		source, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", name, readErr)
		}
		script := &Script{Path: name, Source: source, Lines: lineStarts(source)}
		tree, parseErr := gdparser.ParseFile(name, source)
		if parseErr != nil {
			script.ParseError = parseErr
		} else {
			script.File = tree
		}
		scripts[name] = script
	}
	return &Snapshot{Root: root, Paths: paths, Scripts: scripts, UIDs: uids}, nil
}

// loadIgnoreFile reads the ignore file at the project root. A project without
// one has no matcher, which ignores nothing.
func loadIgnoreFile(root string) (*ignore.Matcher, error) {
	source, err := os.ReadFile(filepath.Join(root, IgnoreFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", IgnoreFileName, err)
	}
	matcher, err := ignore.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", IgnoreFileName, err)
	}
	return matcher, nil
}

// lineStarts returns the byte offset at which each line begins. A trailing
// newline does not start a further line.
func lineStarts(source []byte) []int {
	if len(source) == 0 {
		return nil
	}
	starts := []int{0}
	for i, b := range source {
		if b == '\n' && i+1 < len(source) {
			starts = append(starts, i+1)
		}
	}
	return starts
}
