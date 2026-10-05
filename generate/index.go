package generate

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

// Signature identifies a method shape. GDScript has no overloading, so a name
// identifies at most one method in a class; the rest of the shape decides
// whether an existing method can stand in for a generated one.
type Signature struct {
	Name   string
	Static bool
	Arity  int
}

// Method is one declared method of a class.
type Method struct {
	Signature Signature
	// InRegion reports a declaration inside the generated region. Such a
	// method is never an immutable provider: its existence is contingent on
	// this run, and --prune may remove it.
	InRegion bool
	Line     int
}

// Class is one class in the universe, top-level or inner.
//
// Inner classes are indexed although they can never be generation targets. An
// unrequested inner class can extend a generated base and add fields, which is
// what the descendant refusal rule exists to catch, so leaving them out of the
// graph would defeat it. Indexing them is also what lets a marker on one be
// reported rather than silently ignored.
type Class struct {
	// ID identifies the class across the universe: the path for a top-level
	// class, and the enclosing ID plus "#" plus the name for an inner one.
	// Every map and traversal in Index is keyed on this, not on Path.
	ID string
	// Path is project-relative and slash-separated. An inner class shares its
	// file's path with the class enclosing it.
	Path string
	// Inner reports a class declared with "class" inside another.
	Inner bool
	// Name is the class_name, the inner class's name, or the file's base name
	// without .gd. It is what _to_string prints.
	Name string
	// HasClassName reports an explicit class_name declaration.
	HasClassName bool
	// Extends is the base rendered for a diagnostic message.
	Extends string
	// ExtendsExpr is the base-class expression as gdparser built it: a Literal
	// or Identifier, optionally under a MemberExpression chain. Reading the
	// tree rather than the rendered text is what keeps every inheritance form
	// resolvable without re-parsing anything.
	ExtendsExpr ast.Expression
	// ParentID is Extends resolved to another indexed class's ID, empty when
	// it names an engine type or did not resolve.
	ParentID string
	// UnresolvedBase reports a base that had to name a project script and did
	// not. It makes the inheritance graph incomplete, so it demotes every
	// inheritance-sensitive pair in the universe: this class could be an
	// unseen field-adding descendant of any requested one.
	UnresolvedBase bool
	// UnresolvedCause names the class or path that did not resolve, so a
	// refusal can say which rather than only that the graph is incomplete.
	UnresolvedCause string
	Fields          []Field
	Methods         map[string]Method
	// Body is the span of the class's body in the file.
	Body Span
	// Statements are the class's own body statements. A directive or a
	// sentinel counts only when it is a direct statement here: a class's body
	// span also covers its functions, so span containment alone cannot tell a
	// class-level marker from one written inside a method.
	Statements []ast.Statement
	// PreloadAliases maps a constant's name to the project path it preloads.
	// Godot treats such a constant as a type, so it can appear as a base.
	PreloadAliases map[string]string
	// Region is the generated region this class owns, zero when it owns none.
	// Only a top-level class can own one.
	Region Span
	// HasRegion reports a generated region owned by this class.
	HasRegion bool
	// RegionError is why a region present in the file is unusable.
	RegionError error
	Line        int
	Column      int
}

// Index is every fact the capability rules need, over the whole universe.
type Index struct {
	// Classes is keyed by Class.ID, so it holds inner classes too.
	Classes map[string]*Class
	// TopLevel is keyed by path and holds only each file's outermost class,
	// which is the only kind that can be a generation target.
	TopLevel map[string]*Class
	// HelpersClass is the class at the configured helpers_path, nil when the
	// project declares none. BuildIndex does not set it — it knows nothing of
	// configuration — so Check assigns it after building. Emitters read the
	// class name from here rather than hardcoding one, which is what lets a
	// project rename the class.
	HelpersClass        *Class
	ByClassName         map[string]*Class
	DuplicateClassNames map[string][]string
	// Children maps a class ID to the IDs extending it directly.
	Children map[string][]string
	// InCycle marks a class on an inheritance cycle. project.Load parses
	// syntactically and does not check that extends edges are acyclic, and
	// cycle detection lives in architecture, so generate finds its own.
	InCycle map[string]bool
	// ParseFailures are the paths that did not parse, sorted.
	ParseFailures []string
	// Autoloads is the manifest's name-to-path table, which Godot resolves as
	// project globals while analysing a base class.
	Autoloads map[string]string
}

// BuildIndex walks the whole snapshot, not only its selection: equals composes
// with an ancestor and is refused when a descendant would inherit an unsound
// implementation, and a hidden file would take its class_name and its extends
// edge out of the graph that decides both.
func BuildIndex(snapshot *project.Snapshot) *Index {
	index := &Index{
		Classes:             map[string]*Class{},
		TopLevel:            map[string]*Class{},
		ByClassName:         map[string]*Class{},
		DuplicateClassNames: map[string][]string{},
		Children:            map[string][]string{},
		InCycle:             map[string]bool{},
		ParseFailures:       []string{},
		Autoloads:           snapshot.Autoloads,
	}
	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script.ParseError != nil {
			index.ParseFailures = append(index.ParseFailures, path)
			continue
		}
		region, found, regionErr := FindRegion(script.Source)
		whole := Span{Start: 0, End: len(script.Source)}
		top := buildClass(path, path, false, script, script.File.Statements, whole, region, found, regionErr)
		// Ownership of the region is decided by which suite owns each
		// sentinel, not by whether the span sits inside a class's body.
		// Containment alone accepts a begin sentinel inside an inner class
		// whose end sentinel is outside it: the containment test fails, so the
		// span looks top-level, and splicing it then crosses a class boundary.
		if found && top.RegionError == nil {
			if err := checkSentinelOwnership(top); err != nil {
				top.Region, top.HasRegion, top.RegionError = Span{}, false, err
			}
		}
		index.Classes[top.ID] = top
		index.TopLevel[path] = top
		for _, inner := range buildInnerClasses(path, path, script, script.File.Statements, region, found) {
			index.Classes[inner.ID] = inner
		}
	}
	for _, id := range sortedKeys(index.Classes) {
		class := index.Classes[id]
		if !class.HasClassName {
			continue
		}
		if existing, ok := index.ByClassName[class.Name]; ok {
			duplicates := index.DuplicateClassNames[class.Name]
			if len(duplicates) == 0 {
				duplicates = append(duplicates, existing.ID)
			}
			index.DuplicateClassNames[class.Name] = append(duplicates, id)
			continue
		}
		index.ByClassName[class.Name] = class
	}
	for _, id := range sortedKeys(index.Classes) {
		class := index.Classes[id]
		if parent, ok := index.resolveExtends(snapshot, class); ok {
			class.ParentID = parent.ID
			index.Children[parent.ID] = append(index.Children[parent.ID], id)
		}
	}
	index.findCycles()
	sort.Strings(index.ParseFailures)
	return index
}

// buildClass records one class from the statements of its body.
//
// region is the file's region span, passed in rather than recomputed, and it is
// attributed to this class only when the class is top-level. Calling FindRegion
// per class would hand every inner class in a generated file the same region,
// and the orphan scan would then report one region once per inner class.
func buildClass(id, filePath string, inner bool, script *project.Script, statements []ast.Statement, body, region Span, found bool, regionErr error) *Class {
	class := &Class{
		ID:         id,
		Path:       filePath,
		Inner:      inner,
		Name:       baseName(filePath),
		Methods:    map[string]Method{},
		Body:       body,
		Statements: statements,
		Line:       1,
		Column:     1,
	}
	if !inner {
		class.Region, class.HasRegion, class.RegionError = region, found, regionErr
	}
	for _, statement := range statements {
		switch node := statement.(type) {
		case *ast.Directive:
			// gdparser models class_name and extends as one Directive keyed
			// by the keyword, and a single line may carry both, as in
			// "class_name Hex extends RefCounted".
			switch node.Name {
			case "class_name":
				if identifier, ok := node.Value.(*ast.Identifier); ok {
					class.Name, class.HasClassName = identifier.Name, true
				}
				if node.Extends != nil {
					class.ExtendsExpr = node.Extends
				}
			case "extends":
				class.ExtendsExpr = node.Value
			}
		case *ast.FunctionDeclaration:
			offset := node.Span().Start.Offset
			class.Methods[node.Name] = Method{
				Signature: Signature{Name: node.Name, Static: node.Static, Arity: len(node.Parameters)},
				InRegion:  found && region.Start <= offset && offset < region.End,
				Line:      lineAt(script, offset),
			}
		}
	}
	class.Extends = renderExtends(class.ExtendsExpr)
	class.Fields = SelectFields(statements, script, region)
	class.PreloadAliases = collectPreloadAliases(filePath, statements)
	return class
}

// buildInnerClasses records every class declared inside statements,
// recursively. An identity is its enclosing class's ID plus "#" plus its name,
// so Outer.Inner.Deep is "path#Outer#Inner#Deep".
//
// The enclosing ID is passed down rather than patched onto the returned classes
// afterwards: rewriting each returned ID as parentID + "#" + name drops every
// middle segment, so A.B.C becomes path#A#C and two sibling subtrees holding a
// C collide on one ID.
func buildInnerClasses(enclosingID, filePath string, script *project.Script, statements []ast.Statement, region Span, found bool) []*Class {
	classes := []*Class{}
	for _, statement := range statements {
		declaration, ok := statement.(*ast.ClassDeclaration)
		if !ok {
			continue
		}
		id := enclosingID + "#" + declaration.Name
		span := declaration.Span()
		body := Span{Start: span.Start.Offset, End: span.End.Offset}
		class := buildClass(id, filePath, true, script, declaration.Body, body, region, found, nil)
		class.Name = declaration.Name
		// gdparser gives an inner class's base as a rendered string rather
		// than an expression, so there is no tree to read here.
		class.Extends = declaration.Extends
		class.Line = lineAt(script, span.Start.Offset)
		classes = append(classes, class)
		classes = append(classes, buildInnerClasses(id, filePath, script, declaration.Body, region, found)...)
	}
	return classes
}

// checkSentinelOwnership requires both sentinels to be direct Comment
// statements of the class's own suite.
//
// Containment is the wrong test. A begin sentinel inside an inner class whose
// end sentinel sits outside it is contained by nothing, so a containment check
// passes it as top-level, and splicing or pruning the span then crosses a class
// boundary and rewrites at the wrong indentation.
func checkSentinelOwnership(class *Class) error {
	begin, end := -1, -1
	for _, statement := range class.Statements {
		comment, ok := statement.(*ast.Comment)
		if !ok {
			continue
		}
		switch strings.TrimSpace(comment.Text) {
		case beginSentinel:
			begin = comment.Span().Start.Offset
		case endSentinel:
			end = comment.Span().Start.Offset
		}
	}
	switch {
	case begin < 0 || end < 0:
		return fmt.Errorf("a generated region's sentinels are not both statements of this class's own body")
	case begin > end:
		return fmt.Errorf("a generated region's end sentinel precedes its begin sentinel")
	}
	return nil
}

// collectPreloadAliases records "const Name = preload(path)", which Godot
// treats as a type and which may therefore appear as a base class.
//
// It has to be indexed because the descendant rule needs every edge that could
// name a requested class: a field-adding subclass hidden behind an alias would
// otherwise be invisible and the generated equals unsound with nothing saying
// so. Both an absolute res:// path and a path relative to the declaring script
// are valid GDScript and both are recorded. A computed path is not a static
// edge and is skipped.
func collectPreloadAliases(filePath string, statements []ast.Statement) map[string]string {
	aliases := map[string]string{}
	for _, statement := range statements {
		declaration, ok := statement.(*ast.VariableDeclaration)
		if !ok || !declaration.Constant {
			continue
		}
		call, ok := declaration.Value.(*ast.CallExpression)
		if !ok || len(call.Arguments) != 1 {
			continue
		}
		callee, ok := call.Callee.(*ast.Identifier)
		if !ok || callee.Name != "preload" {
			continue
		}
		target, ok := literalText(call.Arguments[0])
		if !ok || !strings.HasSuffix(target, ".gd") {
			continue
		}
		aliases[declaration.Name] = resolveScriptPath(filePath, target)
	}
	return aliases
}

// literalText is a string literal's content. gdparser keeps the exact source
// spelling in Raw, including the quotes, any triple quoting, and an r prefix,
// so the quoting is stripped here. A path needs no escape processing.
func literalText(expression ast.Expression) (string, bool) {
	literal, ok := expression.(*ast.Literal)
	if !ok || literal.Kind != ast.StringLiteral {
		return "", false
	}
	raw := literal.Raw
	if literal.RawPrefix {
		raw = strings.TrimPrefix(raw, "r")
	}
	quote := string(literal.Quote)
	if literal.Triple {
		quote = strings.Repeat(quote, 3)
	}
	return strings.TrimSuffix(strings.TrimPrefix(raw, quote), quote), true
}

// resolveScriptPath turns a res:// or relative script reference into a
// project-relative path.
func resolveScriptPath(from, target string) string {
	if strings.HasPrefix(target, "res://") {
		return strings.TrimPrefix(target, "res://")
	}
	directory := path.Dir(from)
	if directory == "." {
		return path.Clean(target)
	}
	return path.Clean(directory + "/" + target)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// baseName is the file's name without directories or the .gd suffix, which is
// what _to_string prints for a class declaring no class_name.
func baseName(filePath string) string {
	return strings.TrimSuffix(path.Base(filePath), ".gd")
}
