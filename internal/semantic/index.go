package semantic

import (
	"path"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
)

// DeclarationKind identifies a direct class-level declaration. Declaration
// discovery never descends into function or accessor bodies.
type DeclarationKind uint8

const (
	DeclarationVariable DeclarationKind = iota
	DeclarationConstant
	DeclarationMethod
	DeclarationSignal
	DeclarationEnum
	DeclarationEnumMember
	DeclarationClass
)

func (k DeclarationKind) String() string {
	switch k {
	case DeclarationVariable:
		return "variable"
	case DeclarationConstant:
		return "constant"
	case DeclarationMethod:
		return "method"
	case DeclarationSignal:
		return "signal"
	case DeclarationEnum:
		return "enum"
	case DeclarationEnumMember:
		return "enum-member"
	case DeclarationClass:
		return "class"
	default:
		return "unknown"
	}
}

// Declaration records one direct declaration and retains its parsed header for
// the shallow interface pass. Node is never mutated by the index.
type Declaration struct {
	Name   string
	Kind   DeclarationKind
	Line   int
	Column int
	Node   ast.Statement
}

// PreloadAlias is a class-scope constant bound to a static script preload.
// Resolved is false when the static target did not name a script in SourceSet.
type PreloadAlias struct {
	Name         string
	Target       string
	ResolvedPath string
	Resolved     bool
}

// ClassDecl is one top-level or nested user class.
type ClassDecl struct {
	ID              string
	Path            string
	Name            string
	Inner           bool
	HasClassName    bool
	Extends         string
	ParentID        string
	ExternalBase    string
	UnresolvedBase  bool
	UnresolvedCause string
	Declarations    []Declaration
	PreloadAliases  []PreloadAlias
	Line            int
	Column          int

	extendsExpr ast.Expression
}

// Index is the deterministic declaration and user-inheritance graph.
type Index struct {
	Classes             map[string]*ClassDecl
	TopLevel            map[string]*ClassDecl
	DuplicateClassNames map[string][]string
	Children            map[string][]string
	InCycle             map[string]bool
	ParseFailures       []string

	byClassName map[string]*ClassDecl
	autoloads   map[string]string
}

// BuildIndex copies and normalizes the SourceSet facts, discovers direct
// declarations, resolves inheritance, and marks cycles.
func BuildIndex(source SourceSet) *Index {
	index := &Index{
		Classes:             map[string]*ClassDecl{},
		TopLevel:            map[string]*ClassDecl{},
		DuplicateClassNames: map[string][]string{},
		Children:            map[string][]string{},
		InCycle:             map[string]bool{},
		byClassName:         map[string]*ClassDecl{},
		autoloads:           cloneMap(source.Autoloads()),
	}
	paths := sortedUnique(source.Paths())
	pathSet := make(map[string]bool, len(paths))
	for _, filePath := range paths {
		pathSet[filePath] = true
	}
	failures := append([]string(nil), source.ParseFailures()...)
	failed := make(map[string]bool, len(failures))
	for _, failure := range failures {
		if pathSet[failure] {
			failed[failure] = true
		}
	}
	for _, filePath := range paths {
		if failed[filePath] {
			continue
		}
		file := source.File(filePath)
		if file == nil {
			failed[filePath] = true
			continue
		}
		top := buildClass(source, filePath, filePath, false, file.Statements)
		index.Classes[top.ID] = top
		index.TopLevel[filePath] = top
		index.buildInnerClasses(source, top.ID, filePath, file.Statements)
	}
	for failure := range failed {
		index.ParseFailures = append(index.ParseFailures, failure)
	}
	sort.Strings(index.ParseFailures)

	claims := map[string][]string{}
	for _, id := range index.ClassIDs() {
		class := index.Classes[id]
		if class.HasClassName {
			claims[class.Name] = append(claims[class.Name], id)
		}
	}
	for name, ids := range claims {
		sort.Strings(ids)
		if len(ids) == 1 {
			index.byClassName[name] = index.Classes[ids[0]]
		} else {
			index.DuplicateClassNames[name] = append([]string(nil), ids...)
		}
	}

	// Global, path, autoload, and preload edges do not depend on graph order.
	// Scope lookup can depend on an enclosing class's inherited scopes, so it
	// is retried monotonically until no additional edge resolves.
	pending := map[string]bool{}
	for _, id := range index.ClassIDs() {
		class := index.Classes[id]
		if class.Extends == "" {
			continue
		}
		if !index.resolve(source, class, false, nil) && class.UnresolvedCause == "" && class.ExternalBase == "" {
			pending[id] = true
		}
	}
	for changed := true; changed && len(pending) > 0; {
		changed = false
		for _, id := range sortedBoolKeys(pending) {
			class := index.Classes[id]
			if index.resolve(source, class, true, pending) || class.UnresolvedCause != "" || class.ExternalBase != "" {
				delete(pending, id)
				changed = true
			}
		}
	}
	for _, id := range sortedBoolKeys(pending) {
		class := index.Classes[id]
		// A dependency deadlock is incomplete project resolution, never proof
		// that a bare name is an external engine class.
		class.unresolved(class.Extends)
	}
	for _, id := range index.ClassIDs() {
		if parent := index.Classes[id].ParentID; parent != "" {
			index.Children[parent] = append(index.Children[parent], id)
		}
	}
	for id := range index.Children {
		sort.Strings(index.Children[id])
	}
	index.findCycles()
	return index
}

func buildClass(source SourceSet, id, filePath string, inner bool, statements []ast.Statement) *ClassDecl {
	class := &ClassDecl{ID: id, Path: filePath, Name: strings.TrimSuffix(path.Base(filePath), ".gd"), Inner: inner, Line: 1, Column: 1}
	for _, statement := range statements {
		switch node := statement.(type) {
		case *ast.Directive:
			switch node.Name {
			case "class_name":
				if identifier, ok := node.Value.(*ast.Identifier); ok {
					class.Name, class.HasClassName = identifier.Name, true
				}
				if node.Extends != nil {
					class.extendsExpr = node.Extends
				}
			case "extends":
				class.extendsExpr = node.Value
			}
		case *ast.VariableDeclaration:
			kind := DeclarationVariable
			if node.Constant {
				kind = DeclarationConstant
			}
			class.addDeclaration(node.Name, kind, node, node.NameSpan.Start.Line, node.NameSpan.Start.Column)
		case *ast.FunctionDeclaration:
			class.addDeclaration(node.Name, DeclarationMethod, node, node.NameSpan.Start.Line, node.NameSpan.Start.Column)
		case *ast.SignalDeclaration:
			class.addDeclaration(node.Name, DeclarationSignal, node, node.NameSpan.Start.Line, node.NameSpan.Start.Column)
		case *ast.EnumDeclaration:
			if node.Name != "" {
				class.addDeclaration(node.Name, DeclarationEnum, node, node.NameSpan.Start.Line, node.NameSpan.Start.Column)
			} else {
				for _, member := range node.Members {
					class.Declarations = append(class.Declarations, Declaration{Name: member.Name, Kind: DeclarationEnumMember, Line: member.NameSpan.Start.Line, Column: member.NameSpan.Start.Column, Node: node})
				}
			}
		case *ast.ClassDeclaration:
			class.addDeclaration(node.Name, DeclarationClass, node, node.NameSpan.Start.Line, node.NameSpan.Start.Column)
		}
	}
	class.Extends = renderExtends(class.extendsExpr)
	class.PreloadAliases = collectPreloads(source, filePath, statements)
	return class
}

func (c *ClassDecl) addDeclaration(name string, kind DeclarationKind, node ast.Statement, line, column int) {
	c.Declarations = append(c.Declarations, Declaration{Name: name, Kind: kind, Line: line, Column: column, Node: node})
}

func (i *Index) buildInnerClasses(source SourceSet, enclosingID, filePath string, statements []ast.Statement) {
	for _, statement := range statements {
		declaration, ok := statement.(*ast.ClassDeclaration)
		if !ok {
			continue
		}
		id := enclosingID + "#" + declaration.Name
		class := buildClass(source, id, filePath, true, declaration.Body)
		class.Name, class.Extends = declaration.Name, strings.TrimSpace(declaration.Extends)
		class.Line, class.Column = declaration.NameSpan.Start.Line, declaration.NameSpan.Start.Column
		i.Classes[id] = class
		i.buildInnerClasses(source, id, filePath, declaration.Body)
	}
}

type extendsTarget struct {
	Path  string
	Name  string
	Chain []string
}

func targetOf(class *ClassDecl) (extendsTarget, bool) {
	if class.extendsExpr != nil {
		return parseExtends(class.extendsExpr)
	}
	return parseExtendsText(class.Extends)
}

func parseExtends(expression ast.Expression) (extendsTarget, bool) {
	target := extendsTarget{}
	for expression != nil {
		switch node := expression.(type) {
		case *ast.MemberExpression:
			target.Chain = append([]string{node.Property}, target.Chain...)
			expression = node.Object
		case *ast.Identifier:
			target.Name = node.Name
			return target, true
		case *ast.Literal:
			text, ok := literalText(node)
			if !ok {
				return extendsTarget{}, false
			}
			target.Path = text
			return target, true
		default:
			return extendsTarget{}, false
		}
	}
	return extendsTarget{}, false
}

func parseExtendsText(text string) (extendsTarget, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return extendsTarget{}, false
	}
	parts := strings.Split(text, ".")
	for _, part := range parts {
		if !isIdentifier(part) {
			return extendsTarget{}, false
		}
	}
	return extendsTarget{Name: parts[0], Chain: parts[1:]}, true
}

func isIdentifier(text string) bool {
	if text == "" {
		return false
	}
	for at, r := range text {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || at > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func renderExtends(expression ast.Expression) string {
	target, ok := parseExtends(expression)
	if !ok {
		return ""
	}
	base := target.Name
	if target.Path != "" {
		base = `"` + target.Path + `"`
	}
	return strings.Join(append([]string{base}, target.Chain...), ".")
}

func (i *Index) resolve(source SourceSet, class *ClassDecl, includeScope bool, pending map[string]bool) bool {
	target, ok := targetOf(class)
	if !ok {
		class.unresolved(class.Extends)
		return false
	}
	var head *ClassDecl
	if target.Path != "" {
		resolved, ok := source.ResolvePath(class.Path, target.Path)
		if !ok || i.TopLevel[resolved] == nil {
			class.unresolved(target.Path)
			return false
		}
		head = i.TopLevel[resolved]
	} else if _, ambiguous := i.DuplicateClassNames[target.Name]; ambiguous {
		class.unresolved(target.Name)
		return false
	} else if global := i.byClassName[target.Name]; global != nil {
		head = global
	} else if autoloadPath, declared := i.autoloads[target.Name]; declared {
		head = i.TopLevel[autoloadPath]
		if head == nil {
			class.unresolved(autoloadPath)
			return false
		}
	} else if alias, declared := i.preloadAlias(class, target.Name); declared {
		if !alias.Resolved || i.TopLevel[alias.ResolvedPath] == nil {
			class.unresolved(alias.Target)
			return false
		}
		head = i.TopLevel[alias.ResolvedPath]
	} else if includeScope {
		var settled bool
		head, settled = i.scopeLookup(class, target.Name, pending)
		if head == nil {
			if settled {
				if len(target.Chain) > 0 {
					class.unresolved(class.Extends)
				} else {
					class.ExternalBase = target.Name
				}
			}
			return false
		}
	} else {
		return false
	}
	for _, segment := range target.Chain {
		next := i.Classes[head.ID+"#"+segment]
		if next == nil {
			class.unresolved(class.Extends)
			return false
		}
		head = next
	}
	class.ParentID = head.ID
	return true
}

func (c *ClassDecl) unresolved(cause string) {
	c.UnresolvedBase, c.UnresolvedCause = true, cause
}

func (i *Index) preloadAlias(class *ClassDecl, name string) (PreloadAlias, bool) {
	for scope := class.ID; scope != ""; {
		if owner := i.Classes[scope]; owner != nil {
			for _, alias := range owner.PreloadAliases {
				if alias.Name == name {
					return alias, true
				}
			}
		}
		cut := strings.LastIndexByte(scope, '#')
		if cut < 0 {
			break
		}
		scope = scope[:cut]
	}
	return PreloadAlias{}, false
}

// scopeLookup returns settled=false when a nearer inherited scope still has an
// unresolved base. A farther enclosing match must not be accepted until that
// scope settles, because its eventual ancestors can contain a nearer match.
func (i *Index) scopeLookup(class *ClassDecl, name string, pending map[string]bool) (*ClassDecl, bool) {
	for scope := class.ID; scope != ""; {
		seen := map[string]bool{}
		for ancestor := scope; ancestor != "" && !seen[ancestor]; {
			seen[ancestor] = true
			if found := i.Classes[ancestor+"#"+name]; found != nil && found.ID != class.ID {
				return found, true
			}
			owner := i.Classes[ancestor]
			if owner == nil {
				break
			}
			if ancestor != class.ID && pending[ancestor] {
				return nil, false
			}
			ancestor = owner.ParentID
		}
		cut := strings.LastIndexByte(scope, '#')
		if cut < 0 {
			break
		}
		scope = scope[:cut]
	}
	return nil, true
}

func collectPreloads(source SourceSet, filePath string, statements []ast.Statement) []PreloadAlias {
	aliases := []PreloadAlias{}
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
		resolved, resolvedOK := source.ResolvePath(filePath, target)
		aliases = append(aliases, PreloadAlias{Name: declaration.Name, Target: target, ResolvedPath: resolved, Resolved: resolvedOK})
	}
	sort.Slice(aliases, func(a, b int) bool {
		if aliases[a].Name != aliases[b].Name {
			return aliases[a].Name < aliases[b].Name
		}
		if aliases[a].Target != aliases[b].Target {
			return aliases[a].Target < aliases[b].Target
		}
		return aliases[a].ResolvedPath < aliases[b].ResolvedPath
	})
	return aliases
}

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

// ClassIDs returns every class ID in deterministic order.
func (i *Index) ClassIDs() []string { return sortedMapKeys(i.Classes) }

// TopLevelPaths returns every indexed top-level script path in order.
func (i *Index) TopLevelPaths() []string { return sortedMapKeys(i.TopLevel) }

// Autoloads returns an owned copy of the normalized autoload table.
func (i *Index) Autoloads() map[string]string { return cloneMap(i.autoloads) }

// ClassByName returns the sole claimant of a global class_name. Ambiguous
// names deliberately return nil.
func (i *Index) ClassByName(name string) *ClassDecl { return i.byClassName[name] }

// Ancestry returns id and its project ancestors, stopping at a repeated node.
func (i *Index) Ancestry(id string) []string {
	result := []string{}
	seen := map[string]bool{}
	for current := id; current != "" && !seen[current]; {
		seen[current] = true
		result = append(result, current)
		class := i.Classes[current]
		if class == nil {
			break
		}
		current = class.ParentID
	}
	return result
}

// Descendants returns every transitive project descendant, sorted.
func (i *Index) Descendants(id string) []string {
	found := []string{}
	seen := map[string]bool{id: true}
	queue := append([]string(nil), i.Children[id]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		found = append(found, current)
		queue = append(queue, i.Children[current]...)
	}
	sort.Strings(found)
	return found
}

func (i *Index) findCycles() {
	const done = 2
	state := map[string]int{}
	for _, id := range i.ClassIDs() {
		if state[id] != 0 {
			continue
		}
		walk, positions := []string{}, map[string]int{}
		for current := id; current != ""; {
			if state[current] == done {
				break
			}
			if at, ok := positions[current]; ok {
				for _, member := range walk[at:] {
					i.InCycle[member] = true
				}
				break
			}
			positions[current] = len(walk)
			walk = append(walk, current)
			class := i.Classes[current]
			if class == nil {
				break
			}
			current = class.ParentID
		}
		for _, member := range walk {
			state[member] = done
		}
	}
}

func sortedUnique(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return slicesCompact(result)
}

func slicesCompact(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolKeys(values map[string]bool) []string { return sortedMapKeys(values) }

func cloneMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
