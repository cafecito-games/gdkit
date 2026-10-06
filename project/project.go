// Package project discovers and parses the GDScript sources of a Godot
// project. It is the only place in gdkit that reads a project from disk, so
// every tool agrees on which files are in scope.
package project

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/glob"
	"github.com/cafecito-games/gdkit/internal/ignore"
	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/lexer"
	"github.com/cafecito-games/gdparser/parser"
	"github.com/cafecito-games/gdparser/token"
)

// IgnoreFileName is the gitignore-style file at the project root that lists
// paths a tool skips when it sets Config.HonorIgnoreFile.
const IgnoreFileName = ".gdkitignore"

// ManifestFileName is Godot's project manifest, read for its [autoload]
// section. It is the only file this package reads that is not a source or a
// resource.
const ManifestFileName = "project.godot"

// maxResourceLine caps a line read from a .tscn, .tres, or .import file. A
// header or uid= line is short; a longer line holds something else.
const maxResourceLine = 64 * 1024

// Selection narrows which parsed scripts a tool acts on. Scripts outside it
// are still walked, parsed, and present in the snapshot.
//
// It exists for a tool that must index more than it writes. generate resolves
// a class's equals against the whole inheritance graph, so a file hidden by
// .gdkitignore has to keep its class_name and its extends edge in the index
// even though generate will never rewrite it. Putting those filters on Config
// instead would drop the file from the snapshot, and absence from the index is
// indistinguishable from a type generate knows nothing about.
type Selection struct {
	SourceRoots     []string
	Exclude         []string
	HonorIgnoreFile bool
}

// Config selects the files that belong to a project.
type Config struct {
	Root        string
	SourceRoots []string
	Exclude     []string
	// HonorIgnoreFile also skips the paths listed in the root IgnoreFileName.
	// Exclude still applies: a path is skipped when either one covers it, so a
	// negated ignore pattern cannot bring back an excluded path.
	HonorIgnoreFile bool
	// Selection, when non-nil, narrows what the caller acts on without
	// narrowing the universe that is walked and parsed. A nil Selection, which
	// every tool but generate passes, selects everything discovered.
	Selection *Selection
	// Identities populates Snapshot.Claims and Snapshot.References: every
	// declaration of a uid:// identity and every use of one. It reads every
	// .tscn and .tres file through rather than only its header line, and checks
	// otherwise unmodelled file headers for opaque binary resource claim
	// evidence, which the other tools have no use for.
	//
	// A claim is recorded even inside a path HonorIgnoreFile hides, carrying
	// Claim.Ignored: an ignored file still owns its identity, and dropping it
	// from the table would make every reference to it look dangling. Root
	// .git and .godot metadata are the exception: neither belongs to the
	// project claimant universe and are omitted from the resource inventory in
	// every mode. A reference inside an ignored path is not recorded at all,
	// because nothing reports or rewrites one.
	Identities bool
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

// ParseFailure describes ParseError as a one-based position and a message
// that does not repeat the file name or the position. A failure that carries no
// position is placed at the start of the file. It must only be called when
// ParseError is set.
func (s *Script) ParseFailure() (line, column int, message string) {
	position, message := token.Position{}, s.ParseError.Error()
	var syntaxError *parser.Error
	var lexicalError *lexer.Error
	switch {
	case errors.As(s.ParseError, &syntaxError):
		position, message = syntaxError.Token.Span.Start, syntaxError.Message
	case errors.As(s.ParseError, &lexicalError):
		position, message = lexicalError.Position, lexicalError.Message
	}
	if position.Line < 1 {
		return 1, 1, message
	}
	return position.Line, max(position.Column, 1), message
}

// LineCount is the number of lines in the file.
func (s *Script) LineCount() int { return len(s.Lines) }

// Sidecar is one .uid file discovered beside a source file, recorded exactly
// as it was read. Unlike Snapshot.UIDs it keeps malformed contents and every
// member of a duplicated identifier, so a tool can report on the sidecars
// themselves rather than only resolve them.
type Sidecar struct {
	// Path is the .uid file, project-relative and slash-separated.
	Path string
	// Owner is the path the sidecar sits beside: Path without the .uid
	// suffix. The file itself need not exist.
	Owner string
	// Text is the trimmed contents, whatever they are. It is not a uid://
	// identifier unless the file holds a well-formed one.
	Text string
}

// ResourceKind identifies the on-disk producer evidence a Snapshot retained.
// It is intentionally an inventory category, not a semantic type: adapters
// decide what a selected engine can soundly infer from each kind.
type ResourceKind uint8

const (
	ResourceScript ResourceKind = iota + 1
	ResourceScene
	ResourceText
	ResourceImported
)

func (k ResourceKind) String() string {
	switch k {
	case ResourceScript:
		return "script"
	case ResourceScene:
		return "scene"
	case ResourceText:
		return "text"
	case ResourceImported:
		return "imported"
	default:
		return fmt.Sprintf("ResourceKind(%d)", k)
	}
}

// Resource is one discovered project-relative resource owner. Path is always
// slash-separated; Resources is sorted by Path in Snapshot. A path hidden by
// HonorIgnoreFile is never inventoried, even when Identities retains its
// claimant evidence, so an ignored declaration cannot establish a typeable
// resource target.
type Resource struct {
	Path string
	Kind ResourceKind
}

// Snapshot is an immutable view of one project.
type Snapshot struct {
	Root string
	// Paths are every discovered script, sorted.
	Paths []string
	// Scripts is keyed by the same paths.
	Scripts map[string]*Script
	// UIDs maps every discovered uid:// identifier to the path it names: a
	// .uid sidecar's owner, a .tscn or .tres file that declares its own
	// identifier in its header, or the asset beside a .import file. A sidecar
	// wins if two sources claim one identifier.
	UIDs map[string]string
	// Sidecars is every discovered .uid file, sorted by path. It covers
	// sidecars beside files this package does not parse, such as shaders.
	Sidecars []Sidecar
	// Resources inventories every discovered, non-ignored script, scene, text
	// resource, and importer-backed owner. It is sorted by project-relative
	// Path and is the only existence evidence exported for resource-aware
	// consumers. Identities does not change its membership.
	Resources []Resource
	// Claims is every declaration of a uid:// identity, sorted by path and
	// line, and References is every use of one. Both are empty unless
	// Config.Identities was set.
	Claims     []Claim
	References []Reference
	// IdentityEvidence reports whether Claims was requested while loading this
	// snapshot. An empty claim list is meaningful only when this is true.
	IdentityEvidence bool
	// IdentityIncomplete reports that an explicitly requested claim capture was
	// narrowed, could not read one of its declaration sources, or encountered
	// an opaque binary resource declaration. Consumers that need a unique UID
	// claimant must fail closed rather than treating Claims as exhaustive when
	// this is true.
	IdentityIncomplete bool
	// Selected is the subset of Paths that Config.Selection admits, sorted. It
	// is Paths itself when Selection is nil.
	Selected []string
	// Autoloads maps each script-backed autoload name declared in
	// project.godot to the project-relative path it names.
	//
	// Godot resolves an autoload identifier as a project global while
	// analysing a base class, so "extends SomeAutoload" is a real inheritance
	// edge. A tool that reasons about inheritance needs it: treating the name
	// as an engine type would drop the edge, and a subclass reached that way
	// would be invisible to any analysis of descendants. An autoload pointing
	// at a scene rather than a script is not recorded, since it declares no
	// class.
	Autoloads map[string]string
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
	resources := make(map[string]ResourceKind)
	identityIncomplete := identityCaptureIsNarrowed(config)
	uids := make(map[string]string)
	// Identifiers declared inside a resource are merged after the walk so a
	// .uid sidecar always wins a collision; the sidecars are what gdkit uid
	// reports on.
	declared := make(map[string]string)
	var sidecars []Sidecar
	table := identities{}
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
			// Godot's cache and Git's administrative directory do not belong
			// to the project identity universe. In particular, .godot/imported
			// contains binary resource cache entries with internal UIDs that must
			// not make project claimant evidence ambiguous or incomplete.
			if entry.IsDir() && identityMetadataPath(relative) {
				return filepath.SkipDir
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
				// Apart from generated metadata above, Identities prunes no
				// ignored directory: a hidden file still owns its uid://
				// identity, and the walk has to reach it to record the claim.
				if !config.Identities && !ignored.HasNegation() && ignored.Ignored(relative, true) {
					return filepath.SkipDir
				}
				return nil
			}
			entryType := entry.Type()
			if !entryType.IsRegular() {
				if config.Identities && (entryType&fs.ModeSymlink != 0 || identityClaimSource(relative)) {
					identityIncomplete = true
				}
				return nil
			}
			switch {
			case strings.HasSuffix(relative, ".gd"):
				if ignored.Ignored(relative, false) {
					return nil
				}
				seen[relative] = struct{}{}
				addResource(resources, relative, ResourceScript)
			case strings.HasSuffix(relative, ".tscn"), strings.HasSuffix(relative, ".tres"):
				// A scene or text resource carries its own identifier in its
				// header line rather than in a sidecar, so the header is the
				// only place these can be indexed from.
				hidden := ignored.Ignored(relative, false)
				if hidden && !config.Identities {
					return nil
				}
				header, headerLine, refs, complete := scanResource(name, config.Identities && !hidden)
				if config.Identities && !complete {
					identityIncomplete = true
				}
				if header != "" {
					if !hidden {
						declared[header] = relative
					}
					if config.Identities {
						table.claims = append(table.claims, Claim{
							UID: header, Owner: relative, Path: relative,
							Line: headerLine, Kind: ClaimHeader, Ignored: hidden,
						})
					}
				}
				for _, reference := range refs {
					reference.Path = relative
					table.references = append(table.references, reference)
				}
				if !hidden {
					if strings.HasSuffix(relative, ".tscn") {
						addResource(resources, relative, ResourceScene)
					} else {
						addResource(resources, relative, ResourceText)
					}
				}
			case strings.HasSuffix(relative, ".import"):
				// An imported asset keeps its identifier in the .import file
				// beside it; the asset itself is binary and unparsed.
				owner := strings.TrimSuffix(relative, ".import")
				hidden := ignored.Ignored(relative, false) || ignored.Ignored(owner, false)
				if hidden && !config.Identities {
					return nil
				}
				uid, line, complete := importClaim(name)
				if config.Identities && !complete {
					identityIncomplete = true
				}
				if uid != "" {
					if !hidden {
						declared[uid] = owner
					}
					if config.Identities {
						table.claims = append(table.claims, Claim{
							UID: uid, Owner: owner, Path: relative,
							Line: line, Kind: ClaimImport, Ignored: hidden,
						})
					}
				}
				if !hidden {
					addResource(resources, owner, ResourceImported)
				}
			case strings.HasSuffix(relative, ".uid"):
				owner := strings.TrimSuffix(relative, ".uid")
				hidden := ignored.Ignored(relative, false) || ignored.Ignored(owner, false)
				if hidden && !config.Identities {
					return nil
				}
				data, readErr := os.ReadFile(name)
				if readErr != nil {
					if config.Identities {
						identityIncomplete = true
					}
					return nil
				}
				uid := strings.TrimSpace(string(data))
				if config.Identities {
					table.claims = append(table.claims, Claim{
						UID: uid, Owner: owner, Path: relative,
						Line: 1, Kind: ClaimSidecar, Ignored: hidden,
					})
				}
				if hidden {
					return nil
				}
				sidecars = append(sidecars, Sidecar{Path: relative, Owner: owner, Text: uid})
				if strings.HasPrefix(uid, UIDScheme) {
					uids[uid] = owner
				}
			case strings.HasSuffix(relative, ".scn"), strings.HasSuffix(relative, ".res"):
				// Binary scenes and resources retain their own UID in an opaque
				// binary header. This package deliberately does not decode that
				// format, so a capture that needs unique claimants cannot call
				// itself complete while one is present.
				if config.Identities {
					identityIncomplete = true
				}
			default:
				// ResourceFormatSaverBinary supports each resource's base
				// extension, not only .scn and .res. Its RSRC/RSCC magic is the
				// evidence that an otherwise unmodelled regular file can carry
				// an internal UID. A read failure is equally inconclusive.
				if config.Identities && !identityIncomplete {
					binary, complete := binaryResourceClaimSource(name)
					if binary || !complete {
						identityIncomplete = true
					}
				}
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("discover GDScript in %q: %w", sourceRoot, walkErr)
		}
	}

	for uid, owner := range declared {
		if _, exists := uids[uid]; !exists {
			uids[uid] = owner
		}
	}

	sort.Slice(sidecars, func(i, j int) bool { return sidecars[i].Path < sidecars[j].Path })

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
	if config.Identities {
		for _, name := range paths {
			table.references = append(table.references, scriptReferences(scripts[name])...)
		}
		table.sort()
	}
	selected, err := selectPaths(root, config.Selection, paths)
	if err != nil {
		return nil, err
	}
	autoloads, err := loadAutoloads(root)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Root:               root,
		Paths:              paths,
		Scripts:            scripts,
		UIDs:               uids,
		Sidecars:           sidecars,
		Resources:          sortedResources(resources),
		Claims:             table.claims,
		References:         table.references,
		IdentityEvidence:   config.Identities,
		IdentityIncomplete: identityIncomplete,
		Selected:           selected,
		Autoloads:          autoloads,
	}, nil
}

// identityCaptureIsNarrowed reports a capture that cannot establish every
// project UID claimant. HonorIgnoreFile is deliberately not narrowing here:
// Identities walks ignored paths and records their claims. Excluding only
// generated metadata is also complete, because those directories do not carry
// project claimants. Any other exclusion or a source-root set without the
// project root omits declaration sources entirely.
func identityCaptureIsNarrowed(config Config) bool {
	if !config.Identities {
		return false
	}
	for _, pattern := range config.Exclude {
		if !identityMetadataExclude(pattern) {
			return true
		}
	}
	if len(config.SourceRoots) == 0 {
		return false
	}
	for _, root := range config.SourceRoots {
		if filepath.Clean(root) == "." {
			return false
		}
	}
	return true
}

func identityMetadataPath(resourcePath string) bool {
	return resourcePath == ".git" || strings.HasPrefix(resourcePath, ".git/") ||
		resourcePath == ".godot" || strings.HasPrefix(resourcePath, ".godot/")
}

func identityMetadataExclude(pattern string) bool {
	switch pattern {
	case ".git", ".git/", ".git/**", ".godot", ".godot/", ".godot/**":
		return true
	default:
		return false
	}
}

func identityClaimSource(resourcePath string) bool {
	return strings.HasSuffix(resourcePath, ".tscn") ||
		strings.HasSuffix(resourcePath, ".tres") ||
		strings.HasSuffix(resourcePath, ".scn") ||
		strings.HasSuffix(resourcePath, ".res") ||
		strings.HasSuffix(resourcePath, ".import") ||
		strings.HasSuffix(resourcePath, ".uid")
}

// binaryResourceClaimSource identifies an opaque Godot binary resource by
// the initial magic that ResourceFormatLoaderBinary accepts. It returns a
// separate completeness signal so a file that cannot be read never lets a
// requested identity capture claim there was no binary UID declaration.
func binaryResourceClaimSource(name string) (binary, complete bool) {
	file, err := os.Open(name)
	if err != nil {
		return false, false
	}
	defer file.Close()
	var header [4]byte
	n, err := io.ReadFull(file, header[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, false
	}
	if n != len(header) {
		return false, true
	}
	return string(header[:]) == "RSRC" || string(header[:]) == "RSCC", true
}

func addResource(resources map[string]ResourceKind, resourcePath string, kind ResourceKind) {
	if existing, found := resources[resourcePath]; found && resourceRank(existing) <= resourceRank(kind) {
		return
	}
	resources[resourcePath] = kind
}

func resourceRank(kind ResourceKind) int {
	switch kind {
	case ResourceScript:
		return 0
	case ResourceScene:
		return 1
	case ResourceText:
		return 2
	case ResourceImported:
		return 3
	default:
		return 4
	}
}

func sortedResources(resources map[string]ResourceKind) []Resource {
	paths := make([]string, 0, len(resources))
	for resourcePath := range resources {
		paths = append(paths, resourcePath)
	}
	sort.Strings(paths)
	result := make([]Resource, len(paths))
	for index, resourcePath := range paths {
		result[index] = Resource{Path: resourcePath, Kind: resources[resourcePath]}
	}
	return result
}

// loadAutoloads reads the [autoload] section of project.godot. A project
// without a manifest has none, which is not an error: a tool may be pointed at
// a directory of scripts.
//
// An entry's value is a path optionally prefixed with "*", which marks the
// singleton as enabled; the prefix is not part of the path. Only a .gd target
// is recorded, because only a script declares a class that something could
// extend.
func loadAutoloads(root string) (map[string]string, error) {
	file, err := os.Open(filepath.Join(root, ManifestFileName))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ManifestFileName, err)
	}
	defer file.Close()
	autoloads := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), maxResourceLine)
	inSection := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inSection = line == "[autoload]"
			continue
		}
		if !inSection || line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		target := strings.TrimPrefix(strings.Trim(strings.TrimSpace(value), `"`), "*")
		if name == "" || !strings.HasPrefix(target, "res://") || !strings.HasSuffix(target, ".gd") {
			continue
		}
		autoloads[name] = strings.TrimPrefix(target, "res://")
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", ManifestFileName, err)
	}
	return autoloads, nil
}

// selectPaths returns the subset of paths that selection admits, sorted. A nil
// selection admits everything, which is what every tool that does not need to
// index more than it acts on passes.
func selectPaths(root string, selection *Selection, paths []string) ([]string, error) {
	if selection == nil {
		return paths, nil
	}
	var ignored *ignore.Matcher
	if selection.HonorIgnoreFile {
		matcher, err := loadIgnoreFile(root)
		if err != nil {
			return nil, err
		}
		ignored = matcher
	}
	selected := make([]string, 0, len(paths))
	for _, path := range paths {
		if !underAnyRoot(path, selection.SourceRoots) {
			continue
		}
		if glob.MatchAny(selection.Exclude, path) {
			continue
		}
		if ignored.Ignored(path, false) {
			continue
		}
		selected = append(selected, path)
	}
	return selected, nil
}

// underAnyRoot reports whether path sits under one of roots. No roots, or a
// root of "." , admits everything, matching how Load defaults SourceRoots.
func underAnyRoot(path string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	for _, root := range roots {
		root = strings.TrimSuffix(filepath.ToSlash(root), "/")
		if root == "" || root == "." || path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
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

// quotedUID returns the first double-quoted uid:// identifier in the line.
func quotedUID(line string) string {
	start := strings.Index(line, `"uid://`)
	if start < 0 {
		return ""
	}
	rest := line[start+1:]
	end := strings.IndexByte(rest, '"')
	if end <= len("uid://") {
		return ""
	}
	return rest[:end]
}
