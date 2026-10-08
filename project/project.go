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
// It exists for a tool that must index more than it acts on. generate resolves
// a class's equals against the whole inheritance graph, so a file hidden by
// .gdkitignore has to keep its class_name and its extends edge in the index
// even though generate will never rewrite it. lint passes one too when an
// enabled rule needs whole-project semantic analysis, for the same reason: a
// selected script's types can be declared in an excluded one. Putting those
// filters on Config instead would drop the file from the snapshot, and absence
// from the index is indistinguishable from a type the tool knows nothing about.
//
// The filters here mean exactly what the same filters mean on Config, errors
// included: a root that does not exist fails the load rather than quietly
// selecting nothing, and an exclude pattern that names a directory excludes
// what is under it.
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
	// narrowing the universe that is walked and parsed. generate always passes
	// one and lint passes one in a semantic run; a nil Selection, which the
	// other three tools pass, selects everything discovered.
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
	// FollowDirectorySymlinks enters a directory symlink found under a source
	// root and indexes what it names under the link's own project-logical
	// path. It is read-only discovery and its zero value is the behavior every
	// tool had before it existed: filepath.WalkDir reports such a link as a
	// non-regular, non-directory entry and the walk skips it.
	//
	// A project can mount a shared addon with a repository-managed directory
	// link, which is how Uzir shares one Godot addon between two projects:
	//
	//	client/addons/worldmap_runtime -> ../../common/godot-addons/worldmap_runtime
	//
	// Godot loads res://addons/worldmap_runtime/plugin.cfg and the mounted
	// scripts declare class names the project's own scripts use, so a semantic
	// analysis that cannot see them disagrees with the engine: every answer
	// that depends on the mounted class degrades to a reasoned Unknown.
	//
	// Following every symlink unconditionally would not be safe, so this is an
	// explicit capability and the only authority for it. There is no
	// environment variable, no global switch, and no suffix heuristic. Only a
	// read-only caller sets it: a semantic lint run needs the mounted
	// dependency in its universe, while format, generate, and uid stay on the
	// zero value so no write can reach an external checkout through a mount.
	//
	// It belongs here rather than on Selection because it governs what the
	// universe walk enters, and Selection narrows actions without narrowing
	// the universe.
	//
	// The walk fails the whole load rather than guessing whenever target
	// evidence cannot be established: an unresolvable or unstattable link is
	// an error naming its logical path, never an assumption that it was one of
	// the file symlinks this capability leaves alone. A link that closes a
	// directory cycle is the same kind of error. A link proven to name a
	// regular file or a special node keeps the pre-capability behavior and is
	// skipped unread.
	FollowDirectorySymlinks bool
}

// loaderHooks is the test seam for the two filesystem boundaries at which an
// accepted fact can stop being true: after a symlink target has been proven a
// directory but before its entries are read, and after a file has been
// discovered but before its bytes are opened. A test uses them to remove,
// retarget, or retype the object at exactly that instant and assert the load
// fails closed, with no sleep and no dependence on the scheduler.
//
// It is unexported and passed by value into one load, not held in a package
// variable, so two concurrent loads cannot observe each other's seam.
type loaderHooks struct {
	afterDirectoryAccepted func(logicalPath string)
	beforeFileRead         func(logicalPath string)
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
	return load(config, loaderHooks{})
}

// load is Load with the test seam exposed. Public Load always passes an empty
// one, so every production load runs the real filesystem operations.
func load(config Config, hooks loaderHooks) (*Snapshot, error) {
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
	// prunesDirectory is the one decision about whether the walk enters a
	// logical directory. The callback below asks it, and so does the
	// symlink-following walk before it resolves a link: a link the walk would
	// never have entered must not be able to fail the load by being broken,
	// and the two must not be able to disagree about which directories those
	// are.
	//
	// What it prunes is the caller's filters, which is not the same as the
	// caller's lint configuration. A semantic lint run moves its filters to
	// Selection and leaves Exclude empty and HonorIgnoreFile off, because an
	// excluded script has to stay a dependency, so for that run this prunes
	// only .git and .godot. A link anywhere else is resolved and an
	// unresolvable one fails the load — including a link under a directory
	// lint excludes from findings. That is deliberate, not an oversight: the
	// one mount the pinned Uzir corpus declares sits under its own
	// "addons/**" exclusion, so an exclusion that stopped the walk entering a
	// mount would defeat the capability for the project it exists for, and one
	// that skipped an unresolvable link would be the guess this contract
	// forbids. Narrowing the universe itself is a separate question.
	prunesDirectory := func(relative string) bool {
		// Godot's cache and Git's administrative directory do not belong
		// to the project identity universe. In particular, .godot/imported
		// contains binary resource cache entries with internal UIDs that must
		// not make project claimant evidence ambiguous or incomplete.
		if identityMetadataPath(relative) {
			return true
		}
		if glob.MatchAny(config.Exclude, relative) || glob.MatchAny(config.Exclude, relative+"/") {
			return true
		}
		// A negated pattern can re-include something below an ignored
		// directory, so the directory is only pruned when there is none.
		// Apart from generated metadata above, Identities prunes no
		// ignored directory: a hidden file still owns its uid://
		// identity, and the walk has to reach it to record the claim.
		return !config.Identities && !ignored.HasNegation() && ignored.Ignored(relative, true)
	}
	// One walk across every source root, so the mount evidence it must
	// re-verify before publication covers the whole load. Its cycle ancestry
	// is empty between roots, because every frame pops on return.
	var walk *symlinkWalk
	if config.FollowDirectorySymlinks {
		walk = &symlinkWalk{root: root, prunes: prunesDirectory, hooks: hooks}
	}
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
		visit := func(name string, entry fs.DirEntry, err error) error {
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
			if entry.IsDir() {
				if prunesDirectory(relative) {
					return filepath.SkipDir
				}
				return nil
			}
			if glob.MatchAny(config.Exclude, relative) {
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
		}
		// filepath.WalkDir cannot follow a mount, and when the capability is
		// off its behavior is exactly what is wanted, so it stays the walk
		// every caller but a semantic lint run uses.
		var walkErr error
		if walk != nil {
			// visit closes over this source root's loop state, so it is bound
			// per root rather than when the walk was built.
			walk.visit = visit
			walkErr = walk.walkRoot(absolute, filepath.ToSlash(relativeRoot))
		} else {
			walkErr = filepath.WalkDir(absolute, visit)
		}
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
		// The file has been discovered but not yet opened. A test substitutes
		// it at exactly this instant; the load must fail closed rather than
		// publish a snapshot built from something else.
		if hooks.beforeFileRead != nil {
			hooks.beforeFileRead(name)
		}
		source, readErr := readDiscoveredFile(filepath.Join(root, filepath.FromSlash(name)))
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
	if walk != nil {
		if err := walk.verifyMounts(root); err != nil {
			return nil, err
		}
	}
	var mounts []string
	if walk != nil {
		mounts = walk.mountPaths()
	}
	selected, err := selectPaths(root, config.Selection, mounts, paths)
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
// selection admits everything, which is what a tool that does not need to index
// more than it acts on passes.
func selectPaths(root string, selection *Selection, mounts []string, paths []string) ([]string, error) {
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
	// Selection must admit exactly what a filtered walk of the same three
	// filters would have discovered, because a tool that indexes more than it
	// acts on reports on the selected set. So the roots are resolved and
	// checked the way Load checks Config.SourceRoots, and an exclude pattern
	// prunes a directory here as it does there.
	roots, err := selectionRoots(root, selection.SourceRoots)
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(paths))
	for _, path := range paths {
		if ignored.Ignored(path, false) {
			continue
		}
		if !admitted(selection.Exclude, roots, mounts, path) {
			continue
		}
		selected = append(selected, path)
	}
	return selected, nil
}

// selectionRoots resolves each configured root to a project-relative,
// slash-separated path and applies the same outside-root, existence, and
// directory checks Load applies while walking. Without the resolution a
// spelling the configuration accepts, such as "./src", selects nothing; without
// the checks a root that does not exist selects nothing where the filtered walk
// would have failed, and a tool would report a clean run on a typo.
func selectionRoots(root string, sourceRoots []string) ([]string, error) {
	resolved := make([]string, 0, len(sourceRoots))
	for _, sourceRoot := range sourceRoots {
		absolute := filepath.Join(root, filepath.FromSlash(sourceRoot))
		relative, relErr := filepath.Rel(root, absolute)
		if relErr != nil || relative == ".." || strings.HasPrefix(filepath.ToSlash(relative), "../") {
			return nil, fmt.Errorf("source root %q is outside the project root", sourceRoot)
		}
		info, statErr := os.Stat(absolute)
		if statErr != nil {
			return nil, fmt.Errorf("source root %q: %w", sourceRoot, statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("source root %q: not a directory", sourceRoot)
		}
		resolved = append(resolved, filepath.ToSlash(relative))
	}
	return resolved, nil
}

// admitted reports whether one of roots admits path with none of patterns
// excluding it, which is what the walk discovers: each root is walked
// separately, so a path pruned under one root is still discovered under another
// that admits it.
func admitted(patterns []string, roots []string, mounts []string, path string) bool {
	if len(roots) == 0 {
		// Load defaults an empty source-root list to the project root.
		return filteredWalkReaches(mounts, ".", path) && !excludedUnder(patterns, ".", path)
	}
	for _, root := range roots {
		if root == "" || root == "." || path == root || strings.HasPrefix(path, root+"/") {
			if filteredWalkReaches(mounts, root, path) && !excludedUnder(patterns, root, path) {
				return true
			}
		}
	}
	return false
}

// filteredWalkReaches reports whether a filtered walk rooted at root would have
// discovered path, given the directory mounts the universe walk followed to
// reach it. It answers true for every path when no mount was followed, which is
// every load but a semantic lint run.
//
// Following mounts is the one thing that can make the two walks disagree, and
// the disagreement has to be resolved here or the selection equality this
// package promises stops holding. A filtered walk is capability-off, and
// filepath.WalkDir resolves the symlinks *inside* the path of the root it is
// handed — the kernel does, when it stats it — while skipping any link it meets
// below that root. So a mount strictly above a source root is transparent to
// both walks, and what it holds is selectable: that is the layout of a project
// whose whole source tree is mounted, and the case this capability had to keep
// working. A mount at or below a source root is reached only by the universe
// walk, so what it holds stays a read-only dependency.
//
// Without this, enabling one semantic rule would make every *other* rule start
// reporting on an external checkout that a run with the rule off never reads —
// a project would see new findings in files it does not own from turning on an
// unrelated rule, which is exactly what the universe/selection split exists to
// prevent.
func filteredWalkReaches(mounts []string, root, path string) bool {
	for _, mount := range mounts {
		if !pathWithinDirectory(mount, path) {
			continue
		}
		// A root strictly below the mount is a path whose symlink the kernel
		// resolves for the filtered walk too. Anything else — including a root
		// that is itself the mount, which WalkDir lstats and never enters — is
		// not reachable without this capability.
		if !strictlyBelowDirectory(mount, root) {
			return false
		}
	}
	return true
}

// pathWithinDirectory reports whether path is directory itself or lies inside
// it, with "." standing for the project root and so containing everything. The
// project root is a directory the walk can mount, because a source root may be
// the project root and may itself be a link.
func pathWithinDirectory(directory, path string) bool {
	if directory == "." || directory == "" {
		return true
	}
	return path == directory || strings.HasPrefix(path, directory+"/")
}

// strictlyBelowDirectory reports whether path lies inside directory and is not
// directory itself. The distinction is the whole rule: a filtered walk handed a
// root strictly inside a mount has the link resolved for it by the kernel,
// while one handed the mount itself lstats a symlink and walks nothing.
func strictlyBelowDirectory(directory, path string) bool {
	if directory == "." || directory == "" {
		return path != "." && path != ""
	}
	return strings.HasPrefix(path, directory+"/")
}

// mountPaths returns the logical path of every directory symlink this walk
// followed, sorted. It is the only part of the walk's physical evidence that
// outlives it, and it carries no resolved path.
func (w *symlinkWalk) mountPaths() []string {
	logical := make([]string, 0, len(w.mounts))
	for _, mount := range w.mounts {
		logical = append(logical, mount.logical)
	}
	sort.Strings(logical)
	return logical
}

// excludedUnder reports whether patterns cover path or any directory between
// root and path, matching each directory as both "dir" and "dir/" the way the
// walk prunes one. Two bounds are load-bearing. A pattern naming a directory
// rather than the files beneath it, such as "generated" or "addons/*", has to
// exclude its contents, so every directory under root is tested. And the walk
// starts at root and never visits anything above it, so a pattern that happens
// to match an ancestor of root must not exclude what is inside it: with a root
// of "addons/mine/src", "addons/*" prunes nothing.
func excludedUnder(patterns []string, root, path string) bool {
	if glob.MatchAny(patterns, path) {
		return true
	}
	// The walk skips the "." callback, so the project root itself is never
	// matched; a named root is.
	start := 0
	if root != "" && root != "." {
		if glob.MatchAny(patterns, root) || glob.MatchAny(patterns, root+"/") {
			return true
		}
		start = len(root) + 1
	}
	for index := start; index < len(path); index++ {
		if path[index] != '/' {
			continue
		}
		directory := path[:index]
		if glob.MatchAny(patterns, directory) || glob.MatchAny(patterns, directory+"/") {
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

// symlinkWalk is the read-only directory walk Config.FollowDirectorySymlinks
// selects. filepath.WalkDir cannot do this job: it reports a directory symlink
// below the walked root as a non-regular, non-directory entry, so an explicitly
// mounted addon never enters the universe at all. It also Lstats the walked
// root, so a source root that is itself a link discovers nothing rather than
// the directory it names.
//
// The walk carries three things WalkDir does not.
//
// It threads the logical, project-relative path alongside the path it opens,
// and that logical path is the only identity it hands the callback or names in
// an error. A mounted script is published as
// addons/worldmap_runtime/pack_manifest.gd and never as the host directory it
// happens to live in. The logical path is built by appending entry names, so
// no resolved path can reach a snapshot or a diagnostic even if a resolution
// goes wrong.
//
// Physical evidence — the resolved target, the stat that proved it a
// directory, and the handle its entries are read through — is validation only
// and is discarded here. Nothing downstream performs canonical-target I/O.
//
// And the canonical directories on the *current recursion ancestry* are what
// terminate cycles. A global visited set is deliberately not used: it would
// collapse one target intentionally mounted at two logical paths into a single
// logical source, and two logical mounts must stay two logical sources so the
// existing duplicate-class and duplicate-identity handling can see both
// claimants.
type symlinkWalk struct {
	root  string
	visit fs.WalkDirFunc
	// prunes is Load's single decision about entering a logical directory. A
	// pruned link is left exactly as the capability-off walk leaves it, so
	// resolving a broken link the project already excluded cannot fail a load
	// that would otherwise have succeeded.
	prunes func(logicalPath string) bool
	hooks  loaderHooks
	// mounts is every followed link, kept until the load has finished reading
	// through all of them. See verifyMounts.
	mounts []mountEvidence
	// ancestry holds one frame per directory on the stack below.
	// Pushed before descending and popped on return, so it is per-load state
	// held in a value the caller owns and two concurrent loads share nothing.
	ancestry []ancestorFrame
}

// walkRoot walks one configured source root, which Load has already checked is
// inside the project and is a directory.
func (w *symlinkWalk) walkRoot(absolute, logical string) error {
	if _, err := filepath.EvalSymlinks(absolute); err != nil {
		return fmt.Errorf("resolve %s: %w", logical, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", logical, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: not a directory", logical)
	}
	// A source root that is itself a link is a mount like any other, and has
	// to be recorded as one. filepath.WalkDir lstats the root it is handed, so
	// a filtered walk of this root discovers nothing at all; without the
	// record, selection would not know that and would admit the whole tree.
	// Load's own os.Stat check passes either way, because it follows the link,
	// which is why nothing above here notices.
	if lstat, lstatErr := os.Lstat(absolute); lstatErr == nil && lstat.Mode()&fs.ModeSymlink != 0 {
		w.mounts = append(w.mounts, mountEvidence{logical: logical, target: info})
	}
	// WalkDir reports the walked root to the callback, and an exclude pattern
	// naming a source root prunes it there, so the root is reported here too.
	return w.walkDirectory(absolute, logical, fs.FileInfoToDirEntry(info), nil)
}

// walkDirectory reports one directory to the callback and, unless the callback
// prunes it, reads and walks its entries. validated is the stat that proved a
// followed symlink target was a directory, or nil for a directory the walk
// reached without resolving a link.
func (w *symlinkWalk) walkDirectory(absolute, logical string, entry fs.DirEntry, validated fs.FileInfo) error {
	if err := w.visit(absolute, entry, nil); err != nil {
		if errors.Is(err, filepath.SkipDir) {
			return nil
		}
		return err
	}
	// The target has been accepted as a directory but nothing has been read
	// from it yet. A test substitutes the object at exactly this instant.
	if w.hooks.afterDirectoryAccepted != nil {
		w.hooks.afterDirectoryAccepted(logical)
	}
	entries, opened, err := w.readDirectory(absolute, logical, validated)
	if err != nil {
		return err
	}
	// The ancestry frame carries the identity of the object whose entries are
	// about to be walked, which is the one readDirectory just read from.
	w.ancestry = append(w.ancestry, ancestorFrame{logical: logical, directory: opened})
	defer func() { w.ancestry = w.ancestry[:len(w.ancestry)-1] }()
	for _, child := range entries {
		childLogical := child.Name()
		if logical != "." {
			childLogical = logical + "/" + child.Name()
		}
		err := w.walkEntry(filepath.Join(absolute, child.Name()), childLogical, child)
		if errors.Is(err, filepath.SkipDir) {
			// WalkDir's contract: SkipDir from a non-directory entry skips the
			// remaining entries of the containing directory. Load's callback
			// never returns it for one, but the walk keeps the meaning so the
			// two walks cannot diverge on it.
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// readDirectory reads one directory's entries through a handle it stats, so the
// entries come from the same object the walk accepted. A target that was
// removed, retargeted, or replaced by a file after acceptance fails the load by
// its logical path rather than contributing some other directory's contents,
// and there is no retry against the new target within one load.
func (w *symlinkWalk) readDirectory(absolute, logical string, validated fs.FileInfo) ([]fs.DirEntry, fs.FileInfo, error) {
	handle, err := os.Open(absolute)
	if err != nil {
		return nil, nil, fmt.Errorf("read directory %s: %w", logical, err)
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("read directory %s: %w", logical, err)
	}
	if !opened.IsDir() {
		return nil, nil, fmt.Errorf("read directory %s: no longer a directory", logical)
	}
	if validated != nil && !os.SameFile(validated, opened) {
		return nil, nil, fmt.Errorf("read directory %s: target changed during the load", logical)
	}
	entries, err := handle.ReadDir(-1)
	if err != nil {
		return nil, nil, fmt.Errorf("read directory %s: %w", logical, err)
	}
	// ReadDir on a handle returns entries in directory order. Sorting them is
	// what makes two loads over the same bytes produce identical snapshots.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, opened, nil
}

// walkEntry walks one directory entry.
func (w *symlinkWalk) walkEntry(absolute, logical string, entry fs.DirEntry) error {
	if entry.Type()&fs.ModeSymlink == 0 {
		if !entry.IsDir() {
			return w.visit(absolute, entry, nil)
		}
		// An ordinary directory is strictly below everything on the ancestry,
		// so it cannot close a cycle and needs no resolution of its own.
		return w.walkDirectory(absolute, logical, entry, nil)
	}
	if w.prunes(logical) {
		// The walk would never enter this directory, so whether its target
		// resolves is not evidence this load needs. Reporting the link as the
		// capability-off walk would is what keeps the two in agreement,
		// including about the identity evidence a skipped entry marks.
		return w.visit(absolute, entry, nil)
	}
	// This is where WalkDir gives up. Resolve the link before anything can
	// treat its type as known: the fail-closed rule is that an unresolvable
	// link is an error, never a guess that it was one of the file symlinks this
	// capability leaves alone.
	if _, err := filepath.EvalSymlinks(absolute); err != nil {
		return fmt.Errorf("resolve symlink %s: %w", logical, err)
	}
	target, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("resolve symlink %s: %w", logical, err)
	}
	if !target.IsDir() {
		// Positively established as a regular file or a special node. Those
		// are out of scope, so the pre-capability behavior for a non-regular
		// entry is preserved exactly, including the identity evidence it marks
		// incomplete.
		return w.visit(absolute, entry, nil)
	}
	// Cycles are decided on file identity, not on the text of a resolved path.
	// filepath.EvalSymlinks does not fold case, so on a case-insensitive
	// filesystem — the default on macOS, and the norm on Windows — a link to an
	// ancestor spelled with different case resolves to a string that does not
	// match it. Comparing strings would let that cycle through and the walk
	// would re-read the same tree until the kernel refused the path, failing
	// with a name-too-long error instead of this one. os.SameFile is the same
	// identity test readDirectory and verifyMounts use, so all three agree
	// about when two paths are one directory.
	for _, ancestor := range w.ancestry {
		if os.SameFile(ancestor.directory, target) {
			return fmt.Errorf("symlink %s closes a directory cycle back to %s", logical, ancestor.logical)
		}
	}
	// A nested link may resolve outside the project root. That is allowed, and
	// only because the capability is on and the link was reached during the
	// authorized walk; the external location is not another res:// root, and
	// the same validation and cycle policy applies below it.
	w.mounts = append(w.mounts, mountEvidence{logical: logical, target: target})
	return w.walkDirectory(absolute, logical, mountedDirEntry{name: entry.Name(), info: target}, target)
}

// ancestorFrame is one directory on the walk's current recursion stack: the
// identity of the object its entries were read from, and the logical path to
// name in a cycle error. Only the logical half can ever be reported.
type ancestorFrame struct {
	logical   string
	directory fs.FileInfo
}

// mountEvidence is one followed directory symlink and the object its contents
// were attributed to. It is validation evidence, discarded before publication.
type mountEvidence struct {
	logical string
	target  fs.FileInfo
}

// verifyMounts re-checks every followed mount after the load has finished
// reading through it.
//
// Every file is read by its logical path, which is what keeps the logical
// identity primary and is how the loader has always read a file. But it also
// means the kernel resolves the link again at read time, so a mount retargeted
// mid-load could substitute content under a logical path the walk had
// validated against a different object. Target validation and the reads it
// authorizes are tied together here instead: no snapshot is published unless
// every mount is still the object its contents were attributed to, and a
// mismatch is an error rather than a retry against the new target.
func (w *symlinkWalk) verifyMounts(root string) error {
	for _, mount := range w.mounts {
		current, err := os.Stat(filepath.Join(root, filepath.FromSlash(mount.logical)))
		if err != nil {
			return fmt.Errorf("verify mount %s: %w", mount.logical, err)
		}
		if !current.IsDir() {
			return fmt.Errorf("verify mount %s: no longer a directory", mount.logical)
		}
		if !os.SameFile(mount.target, current) {
			return fmt.Errorf("verify mount %s: target changed during the load", mount.logical)
		}
	}
	return nil
}

// mountedDirEntry presents a followed directory symlink to the walk callback as
// the directory it names, under the link's own name. The callback's exclude,
// ignore, and metadata rules all ask whether an entry is a directory, and a
// mount has to answer the way the directory it stands for would.
type mountedDirEntry struct {
	name string
	info fs.FileInfo
}

func (e mountedDirEntry) Name() string               { return e.name }
func (e mountedDirEntry) IsDir() bool                { return true }
func (e mountedDirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (e mountedDirEntry) Info() (fs.FileInfo, error) { return e.info, nil }

// readDiscoveredFile reads a file the walk discovered, through a handle it
// stats. A file that was removed, replaced by a directory, or replaced by a
// special node between discovery and publication fails the load instead of
// contributing bytes from something that is not a script.
func readDiscoveredFile(absolute string) ([]byte, error) {
	handle, err := os.Open(absolute)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return io.ReadAll(handle)
}
