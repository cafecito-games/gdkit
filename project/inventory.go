package project

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/configfile"
	configast "github.com/cafecito-games/gdparser/configfile/ast"
	"github.com/cafecito-games/gdparser/shader"
	shaderast "github.com/cafecito-games/gdparser/shader/ast"

	"github.com/cafecito-games/gdkit/internal/respath"
)

// Link is one outgoing resource reference, recorded as evidence rather than
// as a resolved edge. The snapshot does not resolve it, because resolution is
// a question with more than one right answer: architecture wants to know
// whether a boundary was crossed and assets wants to know whether a file is
// still needed, and they disagree about what an unresolvable identifier means.
type Link struct {
	// Path is the file holding the reference, project-relative and
	// slash-separated, and Line is the one-based line within it.
	Path string
	Line int
	// Reference is the text as written: a uid:// identifier, a res:// path,
	// or a path relative to Path.
	Reference string
	// Explicit reports a reference written with a uid:// or res:// scheme,
	// which is text that can only have been meant as a resource reference.
	// An inferred one — a shader include, a relative path in a plugin.cfg —
	// can establish that a file is still in use, but must never be the
	// evidence that one is missing: a value that merely looks like a path is
	// not a promise that a file is there.
	Explicit bool
}

// scriptLinks returns every resource reference a parsed script makes.
//
// Every res:// and uid:// string literal counts, not only the argument of a
// load call. FileAccess.open("res://data/waves.json") is a real dependency, so
// is a path sitting in a const table, and so is one handed to a helper that
// loads it three frames later. Restricting this to known call positions would
// buy precision in exchange for reporting a live file as unused, which is the
// one mistake this evidence must not support.
func scriptLinks(script *Script) []Link {
	if script.File == nil {
		return nil
	}
	fragments := pathFragments(script.File)
	var links []Link
	ast.Inspect(script.File, func(node ast.Node) bool {
		literal, ok := node.(*ast.Literal)
		if !ok || literal.Kind != ast.StringLiteral || fragments[literal] {
			return true
		}
		text := literalText(literal)
		if !respath.IsReference(text) {
			return true
		}
		links = append(links, Link{
			Path: script.Path, Line: literal.Span().Start.Line,
			Reference: text, Explicit: true,
		})
		return true
	})
	return links
}

// pathFragments returns the string literals that are operands of a
// concatenation or a format expression. Each is a piece of a path the program
// computes, not a path of its own: the head of "res://data/" + id names a
// directory and no file, and reading it as a reference reports every file in
// the project as missing. scriptDynamicLoads is what turns the whole
// expression into the one thing it does prove.
func pathFragments(file *ast.File) map[*ast.Literal]bool {
	fragments := map[*ast.Literal]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpression)
		if !ok || (binary.Operator != "+" && binary.Operator != "%") {
			return true
		}
		for _, operand := range []ast.Expression{binary.Left, binary.Right} {
			if literal, isLiteral := operand.(*ast.Literal); isLiteral {
				fragments[literal] = true
			}
		}
		return true
	})
	return fragments
}

// shaderLinks returns the targets of a shader's #include directives. An
// include is resolved relative to the including file when it carries no
// scheme, which is how Godot reads one and is why these are not required to
// look like references.
func shaderLinks(name, relative string) []Link {
	source, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	file, parseErr := shader.ParseFile(name, source)
	if parseErr != nil {
		return nil
	}
	var links []Link
	shaderast.Inspect(file, func(node shaderast.Node) bool {
		directive, ok := node.(*shaderast.PreprocessorDirective)
		if !ok || directive.Name != "include" {
			return true
		}
		target := unquote(strings.TrimSpace(directive.Body))
		if target == "" {
			return true
		}
		links = append(links, Link{
			Path: relative, Line: directive.Span().Start.Line,
			Reference: target, Explicit: respath.IsReference(target),
		})
		return true
	})
	return links
}

// configLinks returns every resource reference a Godot config file makes:
// project.godot, which names the main scene, the autoloads, the icon, the
// theme, the translations and the bus layout, and an addon's plugin.cfg,
// which names its own script.
//
// Every string value is inspected rather than a list of the keys that hold a
// path. An enumerated list cannot be complete — the manifest has hundreds of
// settings and the engine adds more — and the cost of missing one is
// condemning whatever that setting keeps alive. A blanket scan cannot rot.
//
// A value with no scheme is taken as a reference relative to the config file
// when it has a file extension, because plugin.cfg writes script="plugin.gd"
// that way. Those are inexplicit: a version string reads as a path with this
// rule, so such a link may keep a file alive but may never report one missing.
func configLinks(name, relative string) []Link {
	source, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	file, parseErr := configfile.ParseFile(name, source)
	if parseErr != nil {
		return nil
	}
	var links []Link
	configast.Inspect(file, func(node configast.Node) bool {
		literal, ok := node.(*configast.StringLiteral)
		if !ok {
			return true
		}
		// An input event or autoload path may be written with a leading *,
		// which marks the autoload as a singleton rather than part of a path.
		text := strings.TrimPrefix(strings.TrimSpace(literal.Value), "*")
		switch {
		case respath.IsReference(text):
			links = append(links, Link{
				Path: relative, Line: literal.Span().Start.Line,
				Reference: text, Explicit: true,
			})
		case !strings.Contains(text, "://") && path.Ext(text) != "":
			links = append(links, Link{
				Path: relative, Line: literal.Span().Start.Line,
				Reference: text, Explicit: false,
			})
		}
		return true
	})
	return links
}

// unquote strips one layer of matching quotes, leaving text that has none
// unchanged.
func unquote(text string) string {
	if len(text) < 2 {
		return text
	}
	first, last := text[0], text[len(text)-1]
	if first == last && (first == '"' || first == '\'') {
		return text[1 : len(text)-1]
	}
	return text
}

// inventorySidecar reports a path whose file belongs to the asset beside it
// rather than being an asset of its own. Listing them would make deleting one
// unreferenced texture look like three findings.
func inventorySidecar(relative string) bool {
	return strings.HasSuffix(relative, ".uid") || strings.HasSuffix(relative, ".import")
}

// inventoryLinks returns the references a discovered file makes, chosen by
// what kind of file it is. A script is absent here because its references
// come from the tree the loader has already parsed, in scriptLinks.
//
// A scene or resource is parsed a second time when Config.Identities is also
// set, which no tool does: uid wants identities and assets wants links, and
// each pays for one read of what it asked for.
func inventoryLinks(name, relative string) []Link {
	switch {
	case hasAnySuffix(relative, ".tscn", ".tres", ".escn"):
		document, ok := parseResourceDocument(name)
		if !ok {
			return nil
		}
		links := documentLinks(document)
		for index := range links {
			links[index].Path = relative
		}
		return links
	case hasAnySuffix(relative, ".gdshader", ".gdshaderinc"):
		return shaderLinks(name, relative)
	case hasAnySuffix(relative, ".cfg"):
		return configLinks(name, relative)
	case hasAnySuffix(relative, ".json"):
		return jsonLinks(name, relative)
	default:
		return nil
	}
}

// manifestLinks returns the references project.godot makes. Those are the
// roots of the whole project: the main scene, the autoloads, the icon, the
// default theme, the translations, the bus layout, and the enabled editor
// plugins.
//
// The manifest is read here rather than during the walk, so that it is read
// exactly once however the source roots are drawn. A tool that walks a
// subdirectory still gets the project's roots, and one that walks the project
// root does not get them twice.
func manifestLinks(root string) []Link {
	return configLinks(filepath.Join(root, ManifestFileName), ManifestFileName)
}

func hasAnySuffix(name string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// sortLinks orders the collected references by path, then line, then the
// reference text, so a snapshot of the same tree is always the same snapshot.
func sortLinks(links []Link) {
	sort.SliceStable(links, func(a, b int) bool {
		x, y := links[a], links[b]
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		if x.Line != y.Line {
			return x.Line < y.Line
		}
		return x.Reference < y.Reference
	})
}

// scriptDynamicLoads returns every load whose path this analysis cannot read.
//
// Reference carries the project-relative directory prefix the expression
// does name, when it names one, and is empty when the expression is opaque.
// Both are recorded: a prefix is real evidence that everything under a
// directory may be loaded, and an opaque load is the evidence that no
// reachability claim about this project is complete.
//
// Only a load call is examined, not every expression that could produce a
// path. A string built and stored is not yet a load, and a reachability
// analysis that treated it as one would keep most of a project alive.
func scriptDynamicLoads(script *Script) []Link {
	if script.File == nil {
		return nil
	}
	var links []Link
	ast.Inspect(script.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpression)
		if !ok || !isResourceLoad(call.Callee) || len(call.Arguments) == 0 {
			return true
		}
		argument := call.Arguments[0]
		if literal, isLiteral := argument.(*ast.Literal); isLiteral && literal.Kind == ast.StringLiteral {
			return true
		}
		links = append(links, Link{
			Path:      script.Path,
			Line:      argument.Span().Start.Line,
			Reference: literalPrefix(argument),
		})
		return true
	})
	return links
}

// literalPrefix returns the project-relative directory an expression names
// before the part it computes, or "" when it names none.
//
// Two shapes carry one. A concatenation keeps its leftmost operand, so
// "res://art/" + name names res://art/. A format expression keeps everything
// before its first verb, so "res://data/%s.tres" % id names res://data/. In
// both cases the prefix is cut back to the last directory separator, because
// a partial file name is not a directory and matching on one would keep a
// file whose name merely starts the same way.
func literalPrefix(expression ast.Expression) string {
	binary, ok := expression.(*ast.BinaryExpression)
	if !ok || (binary.Operator != "+" && binary.Operator != "%") {
		return ""
	}
	return directoryPrefix(leftmostLiteral(binary))
}

// directoryPrefix cuts a res:// literal back to the directory it names.
func directoryPrefix(text string) string {
	if !strings.HasPrefix(text, respath.ResourceScheme) {
		return ""
	}
	text = strings.TrimPrefix(text, respath.ResourceScheme)
	if verb := strings.IndexByte(text, '%'); verb >= 0 {
		text = text[:verb]
	}
	separator := strings.LastIndexByte(text, '/')
	if separator < 0 {
		return ""
	}
	return text[:separator+1]
}

// leftmostLiteral returns the text of the leftmost string literal in a chain
// of binary expressions, which is where a built path's fixed head sits.
func leftmostLiteral(expression ast.Expression) string {
	for {
		switch value := expression.(type) {
		case *ast.BinaryExpression:
			expression = value.Left
		case *ast.Literal:
			if value.Kind != ast.StringLiteral {
				return ""
			}
			return literalText(value)
		default:
			return ""
		}
	}
}

// jsonLinks returns the resource references a JSON data file makes. A project
// that drives content from a table writes res:// paths in it, and without
// this every file such a table is the only user of reports as unused.
//
// The document is decoded rather than searched, so a path inside a comment or
// a key name cannot create one. Decoding loses line numbers, which is
// acceptable here: these links keep files alive and never report a site.
func jsonLinks(name, relative string) []Link {
	source, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	var document any
	if json.Unmarshal(source, &document) != nil {
		return nil
	}
	var links []Link
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case string:
			if respath.IsReference(typed) {
				links = append(links, Link{Path: relative, Line: 1, Reference: typed, Explicit: true})
			}
		case []any:
			for _, element := range typed {
				walk(element)
			}
		case map[string]any:
			for _, key := range sortedJSONKeys(typed) {
				walk(typed[key])
			}
		}
	}
	walk(document)
	return links
}

// sortedJSONKeys orders an object's keys so two runs over the same file
// produce the same links in the same order.
func sortedJSONKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
