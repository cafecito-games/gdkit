package project

import (
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
	var links []Link
	ast.Inspect(script.File, func(node ast.Node) bool {
		literal, ok := node.(*ast.Literal)
		if !ok || literal.Kind != ast.StringLiteral {
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
