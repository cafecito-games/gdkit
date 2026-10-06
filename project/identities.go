package project

import (
	"bufio"
	"os"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
)

// UIDScheme precedes every uid:// identifier Godot writes, and ResourceScheme
// precedes a project-relative resource path.
const (
	UIDScheme      = "uid://"
	ResourceScheme = "res://"
)

// ClaimKind names the mechanism a file declares its uid:// identity with.
// Godot has three, and which one it is decides how a repair rewrites the
// declaration: a sidecar holds nothing but the identifier, while a header and
// an import line hold it among other text.
type ClaimKind string

const (
	// ClaimSidecar is a .uid file beside the file it names.
	ClaimSidecar ClaimKind = "sidecar"
	// ClaimHeader is the [gd_scene] or [gd_resource] line of a .tscn or
	// .tres file, which carries that file's own identity.
	ClaimHeader ClaimKind = "header"
	// ClaimImport is the uid= line in the [remap] section of a .import file,
	// which carries the identity of the asset beside it.
	ClaimImport ClaimKind = "import"
)

// Claim is one declaration of a uid:// identity, recorded exactly as it was
// read. Unlike Snapshot.UIDs it keeps a text no decoder accepts and every
// member of a collision, so a tool can report on the declarations themselves
// rather than only resolve them.
type Claim struct {
	// UID is the declared text, verbatim. It is not an identifier Godot
	// could have written unless it decodes.
	UID string
	// Owner is the file the identity belongs to: the sidecar's neighbour, the
	// scene or resource itself, or the asset beside the .import file.
	Owner string
	// Path is the file the declaring text lives in, project-relative and
	// slash-separated. It is Owner itself for ClaimHeader.
	Path string
	// Line is the one-based line of Path that holds the text.
	Line int
	Kind ClaimKind
	// Ignored reports a declaration inside a path .gdkitignore covers. It is
	// recorded anyway, because hiding a claimant would turn every reference
	// to it into a dangling one.
	Ignored bool
}

// ReferenceKind names the mechanism one file refers to another's identity
// with. Only ReferenceExternal carries a path alongside the identifier, so it
// is the only kind a repair can resolve on its own.
type ReferenceKind string

const (
	// ReferenceExternal is an [ext_resource] line in a .tscn or .tres file.
	ReferenceExternal ReferenceKind = "ext_resource"
	// ReferenceLoad is a load, preload, or ResourceLoader.load call in a
	// script whose first argument is a uid:// string literal.
	ReferenceLoad ReferenceKind = "load"
)

// Reference is one use of a uid:// identity somewhere other than where it is
// declared.
type Reference struct {
	// UID is the referenced text, verbatim.
	UID string
	// Target is the project-relative path the reference names alongside the
	// identifier, from the path= of an [ext_resource]. It is empty when the
	// reference names no path, which a script's load call never does.
	Target string
	// Path is the file holding the reference, project-relative and
	// slash-separated, and Line is the one-based line within it.
	Path string
	Line int
	Kind ReferenceKind
}

// claimsAndReferences is populated only when Config.Identities is set. The
// walk collects into it so the two slices can be sorted together at the end.
type identities struct {
	claims     []Claim
	references []Reference
}

func (i *identities) sort() {
	sort.SliceStable(i.claims, func(a, b int) bool {
		x, y := i.claims[a], i.claims[b]
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		return x.Line < y.Line
	})
	sort.SliceStable(i.references, func(a, b int) bool {
		x, y := i.references[a], i.references[b]
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		return x.Line < y.Line
	})
}

// scanResource reads a .tscn or .tres file. It returns the identity the header
// line declares, if any, and — when references is set — every [ext_resource]
// line that names one. Its final result says whether the header declaration
// was read completely; a later reference-scan failure cannot make that already
// captured claimant incomplete.
//
// Only the first line can declare the file's own identity; the identifiers on
// later lines belong to other files. The whole file is read only when the
// caller wants those, because the other tools that load a project need nothing
// past the header.
func scanResource(name string, references bool) (header string, refs []Reference, complete bool) {
	file, err := os.Open(name)
	if err != nil {
		return "", nil, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), maxResourceLine)
	headerRead := false
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if number == 1 {
			headerRead = true
			if strings.HasPrefix(line, "[gd_scene") || strings.HasPrefix(line, "[gd_resource") {
				header = quotedUID(line)
			}
			if !references {
				return header, nil, true
			}
			continue
		}
		if !strings.HasPrefix(line, "[ext_resource") {
			continue
		}
		identifier := resourceAttribute(line, "uid")
		if !strings.HasPrefix(identifier, UIDScheme) {
			continue
		}
		refs = append(refs, Reference{
			UID:    identifier,
			Target: strings.TrimPrefix(resourceAttribute(line, "path"), ResourceScheme),
			Line:   number,
			Kind:   ReferenceExternal,
		})
	}
	if scanner.Err() != nil && !headerRead {
		return header, refs, false
	}
	return header, refs, true
}

// importClaim returns the identity a .import file declares for its asset and
// the line it sits on. Only the [remap] section is read, because a later
// section describes the import's own dependencies.
func importClaim(name string) (string, int, bool) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), maxResourceLine)
	remap := false
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			if remap {
				return "", 0, true
			}
			remap = line == "[remap]"
			continue
		}
		if !remap || !strings.HasPrefix(line, "uid=") {
			continue
		}
		if identifier := quotedUID(line); identifier != "" {
			return identifier, number, true
		}
	}
	return "", 0, scanner.Err() == nil
}

// resourceAttribute returns the value of a key="value" attribute in a .tscn or
// .tres line, or "" when the line has none. It matches the key as a whole
// word, so the path= of an [ext_resource] is not read as its uid=.
func resourceAttribute(line, key string) string {
	needle := key + `="`
	for offset := 0; ; {
		index := strings.Index(line[offset:], needle)
		if index < 0 {
			return ""
		}
		start := offset + index
		offset = start + len(needle)
		// A key is preceded by whitespace or the opening bracket; anything
		// else means this is the tail of a longer key.
		if start > 0 {
			switch line[start-1] {
			case ' ', '\t', '[':
			default:
				continue
			}
		}
		end := strings.IndexByte(line[offset:], '"')
		if end < 0 {
			return ""
		}
		return line[offset : offset+end]
	}
}

// scriptReferences returns every uid:// identifier a parsed script loads. A
// script names no path alongside the identifier, so these are reportable but
// not repairable.
//
// Shadowing is not resolved: a local named load holding a Callable that is
// then handed a uid:// string is still a reference to that identity, and
// preload is not a name a script can rebind.
func scriptReferences(script *Script) []Reference {
	if script.File == nil {
		return nil
	}
	var refs []Reference
	ast.Inspect(script.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpression)
		if !ok || !isResourceLoad(call.Callee) || len(call.Arguments) == 0 {
			return true
		}
		literal, ok := call.Arguments[0].(*ast.Literal)
		if !ok || literal.Kind != ast.StringLiteral {
			return true
		}
		text := literalText(literal)
		if !strings.HasPrefix(text, UIDScheme) {
			return true
		}
		refs = append(refs, Reference{
			UID:  text,
			Path: script.Path,
			Line: literal.Span().Start.Line,
			Kind: ReferenceLoad,
		})
		return true
	})
	return refs
}

// isResourceLoad reports a callee that loads a resource by path or identity.
func isResourceLoad(expression ast.Expression) bool {
	switch value := expression.(type) {
	case *ast.Identifier:
		return value.Name == "load" || value.Name == "preload"
	case *ast.MemberExpression:
		object, ok := value.Object.(*ast.Identifier)
		return ok && object.Name == "ResourceLoader" && value.Property == "load"
	default:
		return false
	}
}

// literalText returns the text between a string literal's quotes. A uid://
// identifier holds no backslash and no quote, so no escape has to be decoded
// to recognise one; a literal that did hold an escape simply does not start
// with the scheme and is skipped.
func literalText(literal *ast.Literal) string {
	raw := strings.TrimPrefix(literal.Raw, "r")
	if literal.Triple {
		if len(raw) >= 6 {
			return raw[3 : len(raw)-3]
		}
		return ""
	}
	if len(raw) < 2 {
		return ""
	}
	return raw[1 : len(raw)-1]
}
