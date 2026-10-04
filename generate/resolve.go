package generate

import (
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

// extendsTarget is the parsed form of a base class: a base plus the member
// chain reaching an inner class.
type extendsTarget struct {
	// Path is the quoted script path, when the base was a string literal.
	Path string
	// Name is the identifier, when the base was not a string literal.
	Name string
	// Chain is the dotted inner-class names after the base.
	Chain []string
}

// parseExtends reads the base-class form out of the AST rather than the source
// text.
//
// gdparser's parseBaseClassExpression has already done this work: a base class
// is a string Literal or an Identifier, optionally wrapped in a
// MemberExpression chain, which is exactly the structure these rules need.
// Re-deriving it by splitting rendered text on quotes and dots would both
// duplicate the parser and misread valid spellings such as r"res://base.gd",
// and it is the kind of text handling gdkit's "no regex over source text"
// invariant exists to keep out.
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

// parseExtendsText is parseExtends for an inner class, whose base gdparser
// records as a rendered string rather than an expression.
//
// Only a plain dotted identifier chain is understood. Any other spelling a
// project could write — a quoted path, an r-prefixed string — is reported as
// unrecognised so the caller can set UnresolvedBase, because light parsing is
// only acceptable when every form it does not handle refuses rather than
// silently dropping the edge.
func parseExtendsText(text string) (extendsTarget, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return extendsTarget{}, false
	}
	segments := strings.Split(text, ".")
	for _, segment := range segments {
		if segment == "" || !isIdentifier(segment) {
			return extendsTarget{}, false
		}
	}
	return extendsTarget{Name: segments[0], Chain: segments[1:]}, true
}

func isIdentifier(text string) bool {
	for index, letter := range text {
		switch {
		case letter == '_':
		case letter >= 'a' && letter <= 'z', letter >= 'A' && letter <= 'Z':
		case letter >= '0' && letter <= '9' && index > 0:
		default:
			return false
		}
	}
	return true
}

// renderExtends spells a base-class expression for a diagnostic message.
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

// resolveExtends resolves a class's base to another indexed class.
//
// Resolution is tri-state, and treating it as a yes-or-no question is what
// leaves an edge able to vanish silently:
//
//	resolved                     the edge is recorded
//	unresolved project reference  sets UnresolvedBase, demoting every
//	                             inheritance-sensitive pair in the universe
//	not a project reference      an engine type: no edge, no refusal
//
// The middle state covers every form that *had* to name a project script and
// did not: a res://, relative, or uid:// path with no such file; a chain whose
// head resolved but whose inner name did not; a declared preload alias whose
// target is missing, since the alias proves a script was meant; an autoload
// whose script is missing; and an inner class whose base spelling this package
// does not parse.
//
// The third state is what keeps the tool usable. Without a native class
// catalogue, "extends Control" cannot be told from a typo, so an unrecognised
// bare identifier is read as an engine type. That stays sound for the
// descendant rule, which only ever asks whether another class names *this* one
// as its base and matches a name exactly.
func (i *Index) resolveExtends(snapshot *project.Snapshot, class *Class) (*Class, bool) {
	target, ok := i.targetOf(class)
	if !ok {
		if class.Extends != "" {
			// A spelling that exists in the file but that this package cannot
			// read is a recognised project reference it failed to resolve.
			class.UnresolvedBase, class.UnresolvedCause = true, class.Extends
		}
		return nil, false
	}
	head, state := i.resolveBase(snapshot, class, target)
	switch state {
	case baseUnresolved:
		cause := target.Name
		if target.Path != "" {
			cause = target.Path
		}
		class.UnresolvedBase, class.UnresolvedCause = true, cause
		return nil, false
	case baseNotProject:
		return nil, false
	}
	for _, segment := range target.Chain {
		next, ok := i.Classes[head.ID+"#"+segment]
		if !ok {
			class.UnresolvedBase = true
			class.UnresolvedCause = class.Extends
			return nil, false
		}
		head = next
	}
	return head, true
}

// targetOf parses a class's base, from the AST for a top-level class and from
// the rendered string for an inner one.
func (i *Index) targetOf(class *Class) (extendsTarget, bool) {
	if class.ExtendsExpr != nil {
		return parseExtends(class.ExtendsExpr)
	}
	if class.Extends == "" {
		return extendsTarget{}, false
	}
	return parseExtendsText(class.Extends)
}

// baseState is which of the three resolution states a base landed in.
type baseState int

const (
	baseResolved baseState = iota
	baseUnresolved
	baseNotProject
)

// resolveBase resolves the head of a base-class target.
//
// A bare identifier follows Godot's analyzer order: global class_names, then
// autoloads, then preload aliases in scope, then classes in the enclosing and
// inherited scopes. Godot searches globals and natives before current-scope
// classes and rejects an inner class that hides a global rather than
// preferring it.
func (i *Index) resolveBase(snapshot *project.Snapshot, class *Class, target extendsTarget) (*Class, baseState) {
	if target.Path != "" {
		if strings.HasPrefix(target.Path, "uid://") {
			path, ok := snapshot.UIDs[target.Path]
			if !ok {
				return nil, baseUnresolved
			}
			if parent, ok := i.TopLevel[path]; ok {
				return parent, baseResolved
			}
			return nil, baseUnresolved
		}
		// A path names a file rather than an engine type, so failing to
		// resolve one means the graph is genuinely incomplete.
		if parent, ok := i.TopLevel[resolveScriptPath(class.Path, target.Path)]; ok {
			return parent, baseResolved
		}
		return nil, baseUnresolved
	}
	if parent, ok := i.ByClassName[target.Name]; ok {
		return parent, baseResolved
	}
	if script, ok := i.Autoloads[target.Name]; ok {
		// Godot resolves an autoload identifier as a project global while
		// analysing a base class, so this is a real edge. The name proves a
		// project script was meant, so a missing one is unresolved rather
		// than an engine type.
		if parent, ok := i.TopLevel[script]; ok {
			return parent, baseResolved
		}
		return nil, baseUnresolved
	}
	if alias, declared := i.preloadAlias(class, target.Name); declared {
		if alias != nil {
			return alias, baseResolved
		}
		// The alias was declared, so a project script was meant.
		return nil, baseUnresolved
	}
	if scoped, ok := i.scopeLookup(class, target.Name); ok {
		return scoped, baseResolved
	}
	return nil, baseNotProject
}

// preloadAlias resolves a name bound by a preload constant, searching the
// class and then the classes enclosing it.
//
// The second return value reports that an alias of that name was *declared*,
// separately from whether its target resolved. A declared alias whose script
// is missing must not fall through as an engine type: the alias is proof that
// a project script was meant.
func (i *Index) preloadAlias(class *Class, name string) (*Class, bool) {
	for scope := class.ID; scope != ""; {
		if owner, ok := i.Classes[scope]; ok {
			if target, ok := owner.PreloadAliases[name]; ok {
				return i.TopLevel[target], true
			}
		}
		cut := strings.LastIndexByte(scope, '#')
		if cut < 0 {
			break
		}
		scope = scope[:cut]
	}
	return nil, false
}

// scopeLookup finds name as an inner class of this class, of a class enclosing
// it, or of one of their ancestors, innermost first. Godot searches this only
// after the global, autoload, and native names, and it walks enclosing base
// types as well as enclosing classes.
func (i *Index) scopeLookup(class *Class, name string) (*Class, bool) {
	for scope := class.ID; scope != ""; {
		for _, ancestor := range i.Ancestry(scope) {
			if found, ok := i.Classes[ancestor+"#"+name]; ok && found.ID != class.ID {
				return found, true
			}
		}
		cut := strings.LastIndexByte(scope, '#')
		if cut < 0 {
			break
		}
		scope = scope[:cut]
	}
	return nil, false
}

// Ancestry returns id and every ancestor above it, nearest first. It carries a
// visited set, so a cycle truncates the walk rather than hanging it.
func (i *Index) Ancestry(id string) []string {
	ancestry := []string{}
	seen := map[string]bool{}
	for current := id; current != "" && !seen[current]; {
		seen[current] = true
		ancestry = append(ancestry, current)
		class := i.Classes[current]
		if class == nil {
			break
		}
		current = class.ParentID
	}
	return ancestry
}

// ReachesCycle reports whether id or any ancestor sits on an inheritance
// cycle. A class above a cycle is as unresolvable as one inside it, because
// provider() has no answer for its parent.
func (i *Index) ReachesCycle(id string) bool {
	for _, ancestor := range i.Ancestry(id) {
		if i.InCycle[ancestor] {
			return true
		}
	}
	return false
}

// Descendants returns every class below id, transitively, sorted.
func (i *Index) Descendants(id string) []string {
	found := []string{}
	seen := map[string]bool{id: true}
	queue := append([]string{}, i.Children[id]...)
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

// findCycles marks every class on an extends cycle. Each class has at most one
// parent, so a cycle is found by walking up and meeting a class already on the
// current walk; no general SCC algorithm is needed.
func (i *Index) findCycles() {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	for _, id := range sortedKeys(i.Classes) {
		if state[id] != 0 {
			continue
		}
		walk := []string{}
		position := map[string]int{}
		for current := id; current != ""; {
			if state[current] == done {
				break
			}
			if at, ok := position[current]; ok {
				for _, member := range walk[at:] {
					i.InCycle[member] = true
				}
				break
			}
			position[current] = len(walk)
			walk = append(walk, current)
			state[current] = visiting
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
