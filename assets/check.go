package assets

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/respath"
	"github.com/cafecito-games/gdkit/project"
)

// Checker answers whether a project still uses each of its files.
type Checker struct {
	config   Config
	compiled *compiledConfig
}

// NewChecker validates the config and returns a checker, so a bad glob fails
// as a configuration error rather than silently matching nothing.
func NewChecker(config Config) (*Checker, error) {
	compiled, err := config.validate()
	if err != nil {
		return nil, err
	}
	return &Checker{config: config, compiled: compiled}, nil
}

// Check is pure: it reads nothing from disk and writes nothing. Everything it
// needs is in the snapshot, which must have been loaded with
// project.Config.Inventory set.
func (c *Checker) Check(snapshot *project.Snapshot) Report {
	graph := c.build(snapshot)
	reachable := graph.mark()

	report := Report{
		Inventoried: len(snapshot.Files),
		Diagnostics: []Diagnostic{},
	}
	for _, file := range snapshot.SelectedFiles {
		if c.reportable(file) {
			report.Reportable++
		}
	}
	report.Diagnostics = append(report.Diagnostics, graph.unresolvedFindings(c, selectedSet(snapshot))...)
	report.Diagnostics = append(report.Diagnostics, c.reachabilityFindings(snapshot, graph, reachable)...)
	sortDiagnostics(report.Diagnostics)
	return report
}

// reachabilityFindings reports the two tiers. A selected, reportable file the
// mark never reached is unreferenced when nothing names it at all, and
// unreachable when something does but that something is dead too.
func (c *Checker) reachabilityFindings(snapshot *project.Snapshot, graph *graph, reachable map[string]bool) []Diagnostic {
	var diagnostics []Diagnostic
	for _, file := range snapshot.SelectedFiles {
		if reachable[file] || !c.reportable(file) {
			continue
		}
		rule, message := RuleUnreferenced, "nothing in the project references this file"
		if len(graph.inbound[file]) > 0 {
			rule = RuleUnreachable
			message = fmt.Sprintf("referenced only by %s, which %s unreachable",
				describeSources(graph.inbound[file]), plural(len(graph.inbound[file])))
		}
		diagnostics = append(diagnostics, Diagnostic{
			Rule: rule, Severity: c.compiled.severity[rule], Path: file, Message: message,
		})
	}
	return diagnostics
}

// reportable reports a file that may appear in a finding: one whose extension
// the config lists. Everything else is still a node carrying its references.
func (c *Checker) reportable(file string) bool {
	return c.compiled.extensions[strings.ToLower(path.Ext(file))]
}

// kept reports a file the config pins as an additional root.
func (c *Checker) kept(file string) bool {
	for _, pattern := range c.compiled.keep {
		if matched, _ := pattern.Match(file); matched {
			return true
		}
	}
	return false
}

// selectedSet is the files a finding may be reported against, as a set.
func selectedSet(snapshot *project.Snapshot) map[string]bool {
	selected := make(map[string]bool, len(snapshot.SelectedFiles))
	for _, file := range snapshot.SelectedFiles {
		selected[file] = true
	}
	return selected
}

// graph is the project as nodes and edges, built once per check.
type graph struct {
	// files is every path the project holds, for membership tests.
	files map[string]bool
	// directories is every directory those files sit in, including the
	// project root as ".". A reference can name one: DirAccess.open and an
	// export plugin's bin/ path both do, and neither is a missing file.
	directories map[string]bool
	// outbound maps a file to the files it needs, and inbound the reverse.
	// Both are needed: the mark walks outbound, and the tier of a finding is
	// decided by whether anything at all points at the file.
	outbound map[string][]string
	inbound  map[string][]string
	// roots are the files the project starts from.
	roots []string
	// dynamic is every load site whose path could not be read, and missing
	// every explicit reference that resolved to no file.
	dynamic []project.Link
	missing []project.Link
}

// godotRoots are the files Godot itself reads to decide what a project is,
// and so are where reachability starts.
//
// export_presets.cfg belongs here with the manifest. It names the platform
// icons, the splash images, and the export filters, and nothing in the
// project references it — so without this every app icon a project ships
// reports as unused, which is the first thing a reader would check and the
// fastest way to lose their trust.
var godotRoots = map[string]bool{
	project.ManifestFileName: true,
	"export_presets.cfg":     true,
	"override.cfg":           true,
}

// build turns the snapshot's links into the graph.
//
// A reference under a directory prefix a dynamic load named keeps everything
// below that prefix reachable. That is the whole reason a dynamic site is
// worth recording rather than merely counting: load("res://data/" + id) names
// a directory even though it names no file, and over-keeping a directory the
// code itself pointed at is the conservative reading of real evidence.
func (c *Checker) build(snapshot *project.Snapshot) *graph {
	g := &graph{
		files:       make(map[string]bool, len(snapshot.Files)),
		directories: map[string]bool{".": true},
		outbound:    map[string][]string{},
		inbound:     map[string][]string{},
	}
	for _, file := range snapshot.Files {
		g.files[file] = true
		for directory := path.Dir(file); directory != "." && directory != "/"; directory = path.Dir(directory) {
			g.directories[directory] = true
		}
	}
	for _, file := range snapshot.Files {
		if c.kept(file) {
			g.roots = append(g.roots, file)
		}
	}
	var prefixes []string
	for _, link := range snapshot.Links {
		resolution := respath.Resolve(link.Path, link.Reference, snapshot.UIDs)
		switch resolution.Kind {
		case respath.Project:
			switch {
			case g.files[resolution.Path]:
				g.connect(link.Path, resolution.Path)
			case g.directories[resolution.Path]:
				// A reference to a directory keeps everything under it. It
				// cannot say which file it will reach, and a directory that
				// exists is not a missing file.
				g.keepUnder(directoryPrefix(resolution.Path), link, &prefixes)
			case isPattern(link.Reference):
				// A glob is a search, not a path. It names no file, so it
				// cannot name one that is absent.
				g.keepUnder(directoryPrefix(path.Dir(resolution.Path)), link, &prefixes)
			case link.Explicit:
				g.missing = append(g.missing, link)
			}
		case respath.Unresolved:
			// An identifier nothing declares names a file that is gone, or
			// one whose own declaration is unreadable. Either way it resolves
			// to nothing and keeps nothing alive.
			if link.Explicit {
				g.missing = append(g.missing, link)
			}
		case respath.External, respath.Escaping:
			// Neither names a file the project ships.
		}
	}
	for _, link := range snapshot.DynamicLinks {
		g.dynamic = append(g.dynamic, link)
		if link.Reference != "" {
			prefixes = append(prefixes, link.Reference)
		}
	}
	classEdges(snapshot, g.connect)
	for _, file := range snapshot.Files {
		if godotRoots[file] {
			g.roots = append(g.roots, file)
		}
	}
	g.roots = append(g.roots, g.prefixRoots(snapshot, prefixes)...)
	sort.Strings(g.roots)
	g.roots = deduplicate(g.roots)
	return g
}

// keepUnder records that everything below prefix may be loaded.
//
// The project root is the exception. A tool that scans res:// recursively —
// a test runner, an addon's file browser — names every file there is, and
// treating that as evidence would make every answer "still in use" and the
// whole analysis vacuous. So it is recorded as a dynamic site instead: the
// run discloses that it cannot see what this reaches, which is true, rather
// than quietly reporting a clean project.
func (g *graph) keepUnder(prefix string, link project.Link, prefixes *[]string) {
	if prefix == "" {
		g.dynamic = append(g.dynamic, project.Link{Path: link.Path, Line: link.Line})
		return
	}
	*prefixes = append(*prefixes, prefix)
}

// directoryPrefix is a directory as a path prefix: "art" becomes "art/", and
// the project root becomes "", which matches every file.
func directoryPrefix(directory string) string {
	if directory == "." || directory == "" {
		return ""
	}
	return directory + "/"
}

// isPattern reports a reference written as a glob rather than as a path.
func isPattern(reference string) bool {
	return strings.ContainsAny(reference, "*?[")
}

// prefixRoots returns every file under a directory a dynamic load named.
func (g *graph) prefixRoots(snapshot *project.Snapshot, prefixes []string) []string {
	if len(prefixes) == 0 {
		return nil
	}
	var kept []string
	for _, file := range snapshot.Files {
		for _, prefix := range prefixes {
			if strings.HasPrefix(file, prefix) {
				kept = append(kept, file)
				break
			}
		}
	}
	return kept
}

// connect records one edge, ignoring a self-edge: a file cannot keep itself
// alive, and a scene that names its own path would otherwise never be
// reported however dead it is.
func (g *graph) connect(from, to string) {
	if from == to {
		return
	}
	g.outbound[from] = append(g.outbound[from], to)
	g.inbound[to] = append(g.inbound[to], from)
}

// mark returns every file reachable from the roots.
func (g *graph) mark() map[string]bool {
	reachable := make(map[string]bool, len(g.files))
	stack := append([]string(nil), g.roots...)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reachable[current] {
			continue
		}
		reachable[current] = true
		stack = append(stack, g.outbound[current]...)
	}
	return reachable
}

// unresolvedFindings reports the two things the graph could not resolve.
func (g *graph) unresolvedFindings(c *Checker, selected map[string]bool) []Diagnostic {
	var diagnostics []Diagnostic
	for _, link := range g.missing {
		if !selected[link.Path] {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Rule: RuleMissing, Severity: c.compiled.severity[RuleMissing],
			Path: link.Path, Line: link.Line, Target: link.Reference,
			Message: fmt.Sprintf("references %s, which does not exist", link.Reference),
		})
	}
	for _, link := range g.dynamic {
		if !selected[link.Path] {
			continue
		}
		message := "load path is not a literal, so nothing here can be proven unused"
		if link.Reference != "" {
			message = fmt.Sprintf("load path is not a literal; everything under res://%s is kept", link.Reference)
		}
		diagnostics = append(diagnostics, Diagnostic{
			Rule: RuleDynamic, Severity: c.compiled.severity[RuleDynamic],
			Path: link.Path, Line: link.Line, Target: link.Reference, Message: message,
		})
	}
	return diagnostics
}

// describeSources names the files pointing at an unreachable one, capped so a
// finding stays one readable line.
func describeSources(sources []string) string {
	unique := deduplicate(append([]string(nil), sources...))
	sort.Strings(unique)
	if len(unique) > 3 {
		return fmt.Sprintf("%s and %d more", strings.Join(unique[:3], ", "), len(unique)-3)
	}
	return strings.Join(unique, ", ")
}

func plural(count int) string {
	if count == 1 {
		return "is"
	}
	return "are"
}

func deduplicate(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for index, value := range values {
		if index > 0 && value == values[index-1] {
			continue
		}
		result = append(result, value)
	}
	return result
}

// sortDiagnostics orders findings by path, line, then rule, so JSON output is
// stable between runs and diffable.
func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(a, b int) bool {
		x, y := diagnostics[a], diagnostics[b]
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		if x.Line != y.Line {
			return x.Line < y.Line
		}
		return x.Rule < y.Rule
	})
}
