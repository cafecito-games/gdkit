package semantic

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
)

// BindingKind identifies the namespace that supplied a resolved identifier.
// The names are intentionally about lookup provenance rather than inferred
// expression behavior: a found binding may still carry a reasoned Unknown
// type for the reducer to refine later.
type BindingKind uint8

const (
	BindingLocal BindingKind = iota
	BindingParameter
	BindingFor
	BindingMatch
	BindingSetter
	BindingMember
	BindingEnclosingConstant
	BindingEnclosingType
	BindingProjectClass
	BindingAutoload
	BindingEngineType
	BindingEngineSingleton
	BindingEngineUtility
	BindingLanguageSpecial
	BindingSelf
	BindingSuper
)

func (k BindingKind) String() string {
	switch k {
	case BindingLocal:
		return "local"
	case BindingParameter:
		return "parameter"
	case BindingFor:
		return "for"
	case BindingMatch:
		return "match"
	case BindingSetter:
		return "setter"
	case BindingMember:
		return "member"
	case BindingEnclosingConstant:
		return "enclosing-constant"
	case BindingEnclosingType:
		return "enclosing-type"
	case BindingProjectClass:
		return "project-class"
	case BindingAutoload:
		return "autoload"
	case BindingEngineType:
		return "engine-type"
	case BindingEngineSingleton:
		return "engine-singleton"
	case BindingEngineUtility:
		return "engine-utility"
	case BindingLanguageSpecial:
		return "language-special"
	case BindingSelf:
		return "self"
	case BindingSuper:
		return "super"
	default:
		return fmt.Sprintf("BindingKind(%d)", k)
	}
}

// BindingID is a comparable identity for one declaration or namespace entry.
// It is stable for one ScopeIndex only: its unexported owner makes identities
// from independently parsed snapshots deliberately distinct even when their
// source text and declaration positions are identical.
type BindingID struct {
	index   *ScopeIndex
	ordinal uint64
	key     string
}

func (id BindingID) String() string {
	if id.key != "" {
		return id.key
	}
	return fmt.Sprintf("binding:%d", id.ordinal)
}

// ScopeID is a comparable identity for one immutable lexical frame revision.
type ScopeID struct {
	index   *ScopeIndex
	ordinal uint64
}

func (id ScopeID) String() string { return fmt.Sprintf("scope:%d", id.ordinal) }

// Binding records one resolved identifier without mutating the parsed AST.
// ID is its only identity. Declaration is retained for lexical bindings and
// user members when the snapshot owns their AST header; namespace origins with
// no retained header leave it nil. Slot is positional metadata for AST-owned
// lexical records such as parameters, loop variables, setter parameters, and
// match cases that do not each have a declaration statement.
type Binding struct {
	id          BindingID
	kind        BindingKind
	name        string
	classID     string
	scopeID     ScopeID
	declaration ast.Node
	slot        int
	line        int
	column      int
	typeValue   Type
	constant    bool
	member      *Member
	superMember *Member
}

// ID returns the stable in-analysis identity of this binding.
func (b Binding) ID() BindingID { return b.id }

// Kind reports where lookup found this binding.
func (b Binding) Kind() BindingKind { return b.kind }

// Name returns the source spelling that resolved to this binding.
func (b Binding) Name() string { return b.name }

// ClassID returns the #45 user class that declares the binding. It is empty
// for retained engine and language globals; Scope.ClassID reports the class
// context where a particular lookup occurred.
func (b Binding) ClassID() string { return b.classID }

// ScopeID returns the immutable lexical frame that installed or resolved this
// binding. Namespace bindings use the frame that performed the lookup.
func (b Binding) ScopeID() ScopeID { return b.scopeID }

// Declaration returns the retained AST owner when one exists. It is nil for
// origins without a retained AST header, including engine/language globals,
// manifest-backed autoloads, and global class-name declarations.
func (b Binding) Declaration() ast.Node { return b.declaration }

// Slot is positional metadata for an AST-owned lexical binding. It is neither
// globally unique nor part of binding identity; use ID to distinguish bindings.
func (b Binding) Slot() int { return b.slot }

// Line and Column locate the declaration or retained producer when available.
func (b Binding) Line() int   { return b.line }
func (b Binding) Column() int { return b.column }

// Type is the shallow type justified by the interface/index snapshot.
func (b Binding) Type() Type { return b.typeValue }

// Member returns the authoritative #46 member for a member or engine-utility
// binding. It returns an owned copy.
func (b Binding) Member() (Member, bool) {
	if b.member == nil {
		return Member{}, false
	}
	return cloneMember(*b.member), true
}

// SuperMember returns the selected base method for a super binding. It is
// separate from Member so consumers cannot mistake base dispatch for ordinary
// member lookup.
func (b Binding) SuperMember() (Member, bool) {
	if b.superMember == nil {
		return Member{}, false
	}
	return cloneMember(*b.superMember), true
}

func cloneBinding(binding Binding) Binding {
	if binding.member != nil {
		member := cloneMember(*binding.member)
		binding.member = &member
	}
	if binding.superMember != nil {
		member := cloneMember(*binding.superMember)
		binding.superMember = &member
	}
	return binding
}

// BindingResult carries the same found/absent/unknown vocabulary as #46
// member lookup. Scope resolution returns unknown rather than absence for a
// final unresolved spelling because retained engine globals are intentionally
// incomplete (notably, global constants are out of the selected schema).
type BindingResult struct {
	state   LookupState
	binding *Binding
	reason  string
}

// State returns whether lookup found a binding, proved an intermediate miss,
// or stopped on incomplete/ambiguous evidence.
func (r BindingResult) State() LookupState { return r.state }

// Reason explains an unknown result. Found and absent results have no reason.
func (r BindingResult) Reason() string { return r.reason }

// Binding returns an owned binding value for a found result.
func (r BindingResult) Binding() (Binding, bool) {
	if r.state != LookupFound || r.binding == nil {
		return Binding{}, false
	}
	return cloneBinding(*r.binding), true
}

func foundBinding(binding Binding) BindingResult {
	copy := cloneBinding(binding)
	return BindingResult{state: LookupFound, binding: &copy}
}

func unknownBinding(reason string) BindingResult {
	if strings.TrimSpace(reason) == "" {
		reason = "identifier resolution is incomplete"
	}
	return BindingResult{state: LookupUnknown, reason: reason}
}

type scopeEntry struct {
	binding *Binding
	reason  string
}

type scopeContext struct {
	classID            string
	static             bool
	methodName         string
	superTarget        string
	constantExpression bool
}

// Scope is one immutable lexical view. Successive declarations create a new
// frame revision rather than changing an earlier frame, so the scope recorded
// for an initializer can never gain the declaration that follows it.
type Scope struct {
	index    *ScopeIndex
	id       ScopeID
	parent   *Scope
	bindings map[string]scopeEntry
	context  scopeContext
	blocked  string
}

// ID returns the stable in-analysis frame identity.
func (s *Scope) ID() ScopeID {
	if s == nil {
		return ScopeID{}
	}
	return s.id
}

// ClassID returns the current user class context.
func (s *Scope) ClassID() string {
	if s == nil {
		return ""
	}
	return s.context.classID
}

// Static reports whether implicit member and self lookup use static context.
func (s *Scope) Static() bool { return s != nil && s.context.static }

// Lookup resolves name from lexical frames outward, then composes the #46
// member lookup and the remaining lexical/global namespaces. It is read-only
// and safe for concurrent callers after BuildScopes returns.
func (s *Scope) Lookup(name string) BindingResult {
	if s == nil || s.index == nil {
		return unknownBinding("scope is unavailable")
	}
	if strings.TrimSpace(name) == "" {
		return unknownBinding("identifier name is empty")
	}
	for current := s; current != nil; current = current.parent {
		if current.blocked != "" {
			return unknownBinding(current.blocked)
		}
		entry, ok := current.bindings[name]
		if !ok {
			continue
		}
		if entry.binding != nil {
			if s.context.constantExpression && !entry.binding.constantExpressionBinding() {
				return unknownBinding(fmt.Sprintf("non-constant lexical binding %q is unavailable in a constant initializer", name))
			}
			return foundBinding(*entry.binding)
		}
		return unknownBinding(entry.reason)
	}
	return s.index.resolveNamespace(s, name)
}

func (b Binding) constantExpressionBinding() bool {
	return b.kind == BindingLocal && b.constant
}

func (s *Scope) lexicalEntry(name string) (scopeEntry, bool) {
	for current := s; current != nil; current = current.parent {
		if entry, ok := current.bindings[name]; ok {
			return entry, true
		}
	}
	return scopeEntry{}, false
}

// ScopeIndex is a deterministic side table for one immutable InterfaceSet
// snapshot. It accepts no separately supplied Index or Engine, preventing
// scope lookup from composing facts from disagreeing snapshots.
type ScopeIndex struct {
	interfaces  *InterfaceSet
	scopes      map[ast.Node]*Scope
	shared      map[ast.Node]bool
	nextScope   uint64
	nextBinding uint64
}

// BuildScopes indexes functions, property accessors, and lambdas retained by
// interfaces. It performs no I/O and leaves the AST, interface set, index, and
// engine unchanged.
func BuildScopes(interfaces *InterfaceSet) *ScopeIndex {
	index := &ScopeIndex{
		interfaces: interfaces,
		scopes:     map[ast.Node]*Scope{},
		shared:     map[ast.Node]bool{},
	}
	if interfaces == nil || interfaces.index == nil {
		return index
	}
	index.findSharedNodes()
	for _, id := range interfaces.ClassIDs() {
		class := interfaces.index.Classes[id]
		if class != nil {
			index.buildClass(class)
		}
	}
	return index
}

// findSharedNodes walks exactly the declaration subtrees ScopeIndex builds.
// It runs before bindings are published so a malformed shared declaration
// cannot leave the first traversal with a found binding before the second
// traversal discovers the ambiguity. Direct inner-class declarations are
// omitted because their body belongs to the separately indexed inner class.
func (i *ScopeIndex) findSharedNodes() {
	if i == nil || i.interfaces == nil || i.interfaces.index == nil {
		return
	}
	if i.shared == nil {
		i.shared = map[ast.Node]bool{}
	}
	seen := map[ast.Node]bool{}
	visit := func(root ast.Node) {
		pending := []ast.Node{root}
		for len(pending) > 0 {
			at := len(pending) - 1
			node := pending[at]
			pending = pending[:at]
			if isNilNode(node) {
				continue
			}
			if seen[node] {
				i.markSharedTree(node)
				continue
			}
			seen[node] = true
			pending = append(pending, ast.Children(node)...)
		}
	}
	for _, id := range i.interfaces.ClassIDs() {
		class := i.interfaces.index.Classes[id]
		if class == nil {
			continue
		}
		roots := map[ast.Node]bool{}
		for _, declaration := range class.Declarations {
			if isNilNode(declaration.Node) || roots[declaration.Node] {
				continue
			}
			switch declaration.Node.(type) {
			case *ast.FunctionDeclaration, *ast.VariableDeclaration, *ast.EnumDeclaration:
				roots[declaration.Node] = true
				visit(declaration.Node)
			}
		}
	}
}

func (i *ScopeIndex) markSharedTree(root ast.Node) {
	if isNilNode(root) || i.nodeShared(root) {
		return
	}
	pending := []ast.Node{root}
	marked := map[ast.Node]bool{}
	for len(pending) > 0 {
		at := len(pending) - 1
		node := pending[at]
		pending = pending[:at]
		if isNilNode(node) || marked[node] {
			continue
		}
		marked[node] = true
		i.shared[node] = true
		pending = append(pending, ast.Children(node)...)
	}
}

// ScopeAt returns the effective lexical view recorded for node. A node from a
// separately parsed snapshot has no entry, even if it has identical text.
func (i *ScopeIndex) ScopeAt(node ast.Node) (*Scope, bool) {
	if i == nil || isNilNode(node) {
		return nil, false
	}
	scope, ok := i.scopes[node]
	return scope, ok
}

// Lookup resolves name at node's effective lexical view.
func (i *ScopeIndex) Lookup(node ast.Node, name string) BindingResult {
	scope, ok := i.ScopeAt(node)
	if !ok {
		return unknownBinding("node is not indexed by this scope snapshot")
	}
	return scope.Lookup(name)
}

// Resolve resolves one parsed identifier at its recorded lexical view.
func (i *ScopeIndex) Resolve(identifier *ast.Identifier) BindingResult {
	if identifier == nil {
		return unknownBinding("identifier is unavailable")
	}
	return i.Lookup(identifier, identifier.Name)
}

func (i *ScopeIndex) buildClass(class *ClassDecl) {
	seen := map[ast.Node]bool{}
	for _, declaration := range class.Declarations {
		if isNilNode(declaration.Node) || seen[declaration.Node] {
			continue
		}
		seen[declaration.Node] = true
		switch node := declaration.Node.(type) {
		case *ast.FunctionDeclaration:
			i.buildFunction(class.ID, node)
		case *ast.VariableDeclaration:
			// A lambda held in a class property is still an indexed lambda,
			// even though it has no enclosing function frame to capture.
			propertyScope := i.newScope(nil, scopeContext{classID: class.ID, static: node.Static || node.Constant, constantExpression: node.Constant})
			if i.nodeShared(node) {
				i.blockSharedTree(node, propertyScope)
				continue
			}
			i.visitExpression(node.Value, propertyScope)
			i.buildAccessors(class.ID, node)
		case *ast.EnumDeclaration:
			enumScope := i.newScope(nil, scopeContext{classID: class.ID, static: true, constantExpression: true})
			if i.nodeShared(node) {
				i.blockSharedTree(node, enumScope)
				continue
			}
			for _, member := range node.Members {
				i.visitExpression(member.Value, enumScope)
			}
		}
	}
}

func (i *ScopeIndex) buildFunction(classID string, node *ast.FunctionDeclaration) {
	context := scopeContext{classID: classID, static: node.Static, methodName: node.Name}
	parameters := i.newScope(nil, context)
	if i.record(node, parameters) {
		return
	}
	for at, parameter := range node.Parameters {
		i.visitExpression(parameter.Default, parameters)
		binding := i.newBinding(
			BindingParameter,
			parameter.Name,
			classID,
			node,
			at,
			parameter.NameSpan.Start.Line,
			parameter.NameSpan.Start.Column,
			i.parameterType(classID, parameter, node.Name),
		)
		parameters = i.install(parameters, binding, false)
	}
	body := i.newScope(parameters, context)
	i.visitStatements(node.Body, body)
}

func (i *ScopeIndex) buildAccessors(classID string, node *ast.VariableDeclaration) {
	context := scopeContext{classID: classID, static: node.Static}
	if i.nodeShared(node) {
		i.blockSharedTree(node, i.newScope(nil, context))
		return
	}
	if node.Getter != nil {
		i.visitStatements(node.Getter, i.newScope(nil, context))
	}
	if node.Setter == nil {
		return
	}
	setter := i.newScope(nil, context)
	if name := strings.TrimSpace(node.Setter.Parameter); name != "" {
		binding := i.newBinding(
			BindingSetter,
			name,
			classID,
			node,
			0,
			node.Setter.ParameterSpan.Start.Line,
			node.Setter.ParameterSpan.Start.Column,
			i.variableType(classID, node),
		)
		setter = i.install(setter, binding, false)
	}
	i.visitStatements(node.Setter.Body, setter)
}

func (i *ScopeIndex) parameterType(classID string, parameter ast.Parameter, owner string) Type {
	if i.interfaces == nil {
		return Unknown("interface set is unavailable while resolving parameter")
	}
	return i.interfaces.parameter(classID, parameter, owner).Type()
}

func (i *ScopeIndex) variableType(classID string, node *ast.VariableDeclaration) Type {
	if i.interfaces == nil {
		return Unknown("interface set is unavailable while resolving declaration")
	}
	return i.interfaces.variableType(classID, node)
}

func (i *ScopeIndex) newScope(parent *Scope, context scopeContext) *Scope {
	i.nextScope++
	return &Scope{
		index:    i,
		id:       ScopeID{index: i, ordinal: i.nextScope},
		parent:   parent,
		bindings: map[string]scopeEntry{},
		context:  context,
	}
}

func (i *ScopeIndex) copyScope(scope *Scope) *Scope {
	if scope == nil {
		return i.newScope(nil, scopeContext{})
	}
	i.nextScope++
	bindings := make(map[string]scopeEntry, len(scope.bindings)+1)
	for name, entry := range scope.bindings {
		bindings[name] = entry
	}
	return &Scope{
		index:    i,
		id:       ScopeID{index: i, ordinal: i.nextScope},
		parent:   scope.parent,
		bindings: bindings,
		context:  scope.context,
		blocked:  scope.blocked,
	}
}

func (i *ScopeIndex) newBinding(kind BindingKind, name, classID string, declaration ast.Node, slot, line, column int, typeValue Type) Binding {
	i.nextBinding++
	return Binding{
		id:          BindingID{index: i, ordinal: i.nextBinding},
		kind:        kind,
		name:        name,
		classID:     classID,
		declaration: declaration,
		slot:        slot,
		line:        line,
		column:      column,
		typeValue:   typeValue,
	}
}

func (i *ScopeIndex) namespaceBinding(scope *Scope, key string, kind BindingKind, name, classID string, typeValue Type, member, superMember *Member, line, column int) Binding {
	binding := Binding{
		id:        BindingID{index: i, key: key},
		kind:      kind,
		name:      name,
		classID:   classID,
		scopeID:   scope.id,
		line:      line,
		column:    column,
		typeValue: typeValue,
	}
	if member != nil {
		copy := cloneMember(*member)
		binding.member = &copy
	}
	if superMember != nil {
		copy := cloneMember(*superMember)
		binding.superMember = &copy
	}
	return binding
}

// install publishes a new revision of the current lexical frame. Except for a
// lambda parameter shadowing a captured outer name, Godot rejects ordinary
// local-over-visible-name redeclarations; malformed hand-built ASTs are marked
// ambiguous rather than being assigned an invented shadowing rule.
func (i *ScopeIndex) install(scope *Scope, binding Binding, allowCapturedShadow bool) *Scope {
	// Every lexical producer retains its owning AST node as Declaration, so the
	// preflight applies uniformly to locals, loop and match bindings,
	// parameters, and setter parameters before any one traversal publishes it.
	if i.nodeShared(binding.declaration) {
		return i.ambiguous(scope, binding.name, sharedASTNodeReason)
	}
	next := i.copyScope(scope)
	binding.scopeID = next.id
	name := binding.name
	if strings.TrimSpace(name) == "" {
		next.bindings[name] = scopeEntry{reason: "lexical binding has an empty name"}
		return next
	}
	if name == "self" || name == "super" {
		next.bindings[name] = scopeEntry{reason: fmt.Sprintf("reserved identifier %q cannot be declared lexically", name)}
		return next
	}
	if _, direct := scope.bindings[name]; direct {
		next.bindings[name] = scopeEntry{reason: fmt.Sprintf("binding %q is declared more than once in one lexical frame", name)}
		return next
	}
	if !allowCapturedShadow {
		if _, visible := scope.lexicalEntry(name); visible {
			next.bindings[name] = scopeEntry{reason: fmt.Sprintf("binding %q conflicts with a visible lexical binding", name)}
			return next
		}
	}
	copy := cloneBinding(binding)
	next.bindings[name] = scopeEntry{binding: &copy}
	return next
}

func (i *ScopeIndex) ambiguous(scope *Scope, name, reason string) *Scope {
	next := i.copyScope(scope)
	next.bindings[name] = scopeEntry{reason: reason}
	return next
}

func (i *ScopeIndex) record(node ast.Node, scope *Scope) bool {
	if isNilNode(node) || scope == nil {
		return false
	}
	if i.nodeShared(node) {
		i.blockSharedTree(node, scope)
		return true
	}
	if prior, recorded := i.scopes[node]; recorded && prior != scope {
		i.blockSharedTree(node, scope)
		return true
	}
	i.scopes[node] = scope
	return false
}

const sharedASTNodeReason = "AST node is shared between lexical positions"

func (i *ScopeIndex) nodeShared(node ast.Node) bool {
	return i != nil && !isNilNode(node) && i.shared != nil && i.shared[node]
}

func (i *ScopeIndex) blockSharedTree(root ast.Node, scope *Scope) {
	if isNilNode(root) {
		return
	}
	if prior, recorded := i.scopes[root]; recorded && prior != nil && prior.blocked == sharedASTNodeReason {
		return
	}
	if i.shared == nil {
		i.shared = map[ast.Node]bool{}
	}
	context := scopeContext{}
	if scope != nil {
		context = scope.context
	}
	blocked := i.newScope(nil, context)
	blocked.blocked = sharedASTNodeReason
	pending := []ast.Node{root}
	visited := map[ast.Node]bool{}
	for len(pending) > 0 {
		at := len(pending) - 1
		node := pending[at]
		pending = pending[:at]
		if isNilNode(node) || visited[node] {
			continue
		}
		visited[node] = true
		i.shared[node] = true
		i.scopes[node] = blocked
		pending = append(pending, ast.Children(node)...)
	}
}

func isNilNode(node ast.Node) bool {
	if node == nil {
		return true
	}
	value := reflect.ValueOf(node)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (i *ScopeIndex) visitStatements(statements []ast.Statement, scope *Scope) *Scope {
	current := scope
	for _, statement := range statements {
		current = i.visitStatement(statement, current)
	}
	return current
}

func (i *ScopeIndex) visitStatement(statement ast.Statement, scope *Scope) *Scope {
	if isNilNode(statement) {
		return scope
	}
	if i.record(statement, scope) {
		return i.ambiguousSharedStatement(scope, statement)
	}
	switch node := statement.(type) {
	case *ast.ExpressionStatement:
		i.visitExpression(node.Expression, scope)
	case *ast.VariableDeclaration:
		if node.Static {
			return i.ambiguousUnsupported(scope, node, "unsupported local static declaration has no established lexical scope boundary", node.Name)
		}
		if node.Getter != nil || node.Setter != nil || node.GetterName != "" || node.SetterName != "" || node.AccessorBlock {
			return i.ambiguousUnsupported(scope, node, "unsupported local property accessor has no established lexical scope boundary", node.Name)
		}
		initializerScope := scope
		if node.Constant {
			context := scope.context
			context.static = true
			context.methodName = ""
			context.constantExpression = true
			initializerScope = i.newScope(scope, context)
		}
		i.visitExpression(node.Value, initializerScope)
		binding := i.newBinding(
			BindingLocal,
			node.Name,
			scope.context.classID,
			node,
			0,
			node.NameSpan.Start.Line,
			node.NameSpan.Start.Column,
			i.variableType(scope.context.classID, node),
		)
		binding.constant = node.Constant
		return i.install(scope, binding, false)
	case *ast.Assignment:
		i.visitExpression(node.Target, scope)
		i.visitExpression(node.Value, scope)
	case *ast.ReturnStatement:
		i.visitExpression(node.Value, scope)
	case *ast.IfStatement:
		for _, branch := range node.Branches {
			i.visitExpression(branch.Condition, scope)
			i.visitStatements(branch.Body, i.newScope(scope, scope.context))
		}
		i.visitStatements(node.Else, i.newScope(scope, scope.context))
	case *ast.WhileStatement:
		i.visitExpression(node.Condition, scope)
		i.visitStatements(node.Body, i.newScope(scope, scope.context))
	case *ast.ForStatement:
		i.visitExpression(node.Iterable, scope)
		body := i.newScope(scope, scope.context)
		binding := i.newBinding(
			BindingFor,
			node.Variable,
			scope.context.classID,
			node,
			0,
			node.VariableSpan.Start.Line,
			node.VariableSpan.Start.Column,
			i.forType(scope.context.classID, node),
		)
		body = i.install(body, binding, false)
		i.visitStatements(node.Body, body)
	case *ast.MatchStatement:
		i.visitMatch(node, scope)
	case *ast.FunctionDeclaration:
		return i.ambiguousUnsupported(scope, node, fmt.Sprintf("unsupported nested %T has no established lexical scope boundary", statement), node.Name)
	case *ast.ClassDeclaration:
		return i.ambiguousUnsupported(scope, node, fmt.Sprintf("unsupported nested %T has no established lexical scope boundary", statement), node.Name)
	case *ast.SignalDeclaration:
		return i.ambiguousUnsupported(scope, node, fmt.Sprintf("unsupported nested %T has no established lexical scope boundary", statement), node.Name)
	case *ast.EnumDeclaration:
		names := []string{node.Name}
		if node.Name == "" {
			for _, member := range node.Members {
				names = append(names, member.Name)
			}
		}
		return i.ambiguousUnsupported(scope, node, fmt.Sprintf("unsupported nested %T has no established lexical scope boundary", statement), names...)
	case *ast.KeywordStatement, *ast.Comment, *ast.Annotation:
		i.visitChildren(statement, scope)
	default:
		return i.unsupportedStatementContinuation(scope, statement)
	}
	return scope
}

func (i *ScopeIndex) unsupportedStatementContinuation(scope *Scope, statement ast.Statement) *Scope {
	reason := fmt.Sprintf("unsupported %T has no established lexical scope boundary", statement)
	i.visitUnsupported(statement, scope, reason)
	continuation := i.newScope(scope, scope.context)
	continuation.blocked = reason
	return continuation
}

func (i *ScopeIndex) ambiguousSharedStatement(scope *Scope, statement ast.Statement) *Scope {
	names := []string{}
	// Keep the modeled cases aligned with visitStatement. A shared unmodeled
	// statement could introduce an unknown binding, so its continuation cannot
	// safely retain the incoming scope.
	switch node := statement.(type) {
	case *ast.ExpressionStatement, *ast.Assignment, *ast.ReturnStatement,
		*ast.IfStatement, *ast.WhileStatement, *ast.ForStatement, *ast.MatchStatement,
		*ast.KeywordStatement, *ast.Comment, *ast.Annotation:
		return scope
	case *ast.VariableDeclaration:
		names = append(names, node.Name)
	case *ast.FunctionDeclaration:
		names = append(names, node.Name)
	case *ast.ClassDeclaration:
		names = append(names, node.Name)
	case *ast.SignalDeclaration:
		names = append(names, node.Name)
	case *ast.EnumDeclaration:
		if node.Name != "" {
			names = append(names, node.Name)
		} else {
			for _, member := range node.Members {
				names = append(names, member.Name)
			}
		}
	default:
		return i.unsupportedStatementContinuation(scope, statement)
	}
	current := scope
	seen := map[string]bool{}
	for _, name := range names {
		if strings.TrimSpace(name) == "" || seen[name] {
			continue
		}
		seen[name] = true
		current = i.ambiguous(current, name, sharedASTNodeReason)
	}
	return current
}

func (i *ScopeIndex) forType(classID string, node *ast.ForStatement) Type {
	if strings.TrimSpace(node.Type) != "" {
		if i.interfaces == nil {
			return Unknown("interface set is unavailable while resolving for variable")
		}
		return i.interfaces.ResolveType(classID, node.Type)
	}
	return Unknown(fmt.Sprintf("for variable %q requires expression reduction", node.Variable))
}

func (i *ScopeIndex) visitMatch(node *ast.MatchStatement, scope *Scope) {
	if i.nodeShared(node) {
		i.blockSharedTree(node, scope)
		return
	}
	i.visitExpression(node.Value, scope)
	for caseIndex, matchCase := range node.Cases {
		body := i.newScope(scope, scope.context)
		patterns := make([]map[string][]*ast.BindingPattern, len(matchCase.Patterns))
		patternShared := false
		for at, pattern := range matchCase.Patterns {
			var shared bool
			patterns[at], shared = i.collectPatternBindings(pattern, scope)
			patternShared = patternShared || shared
		}
		if patternShared {
			body.blocked = sharedASTNodeReason
			i.visitExpression(matchCase.Guard, body)
			i.visitStatements(matchCase.Body, body)
			continue
		}
		names, compatible := compatibleMatchBindings(patterns)
		if !compatible {
			for _, name := range names {
				body = i.ambiguous(body, name, fmt.Sprintf("match case %d has incompatible or duplicate binding %q", caseIndex, name))
			}
		} else if len(patterns) > 0 {
			for slot, name := range names {
				binding := i.newBinding(
					BindingMatch,
					name,
					scope.context.classID,
					patterns[0][name][0],
					caseIndex*1024+slot,
					patterns[0][name][0].NameSpan.Start.Line,
					patterns[0][name][0].NameSpan.Start.Column,
					Unknown(fmt.Sprintf("match binding %q requires expression reduction", name)),
				)
				body = i.install(body, binding, false)
			}
		}
		i.visitExpression(matchCase.Guard, body)
		i.visitStatements(matchCase.Body, body)
	}
}

func compatibleMatchBindings(patterns []map[string][]*ast.BindingPattern) ([]string, bool) {
	if len(patterns) == 0 {
		return nil, true
	}
	names := map[string]bool{}
	for _, pattern := range patterns {
		for name := range pattern {
			names[name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	if len(patterns) > 1 && len(ordered) > 0 {
		return ordered, false
	}
	for _, pattern := range patterns {
		if len(pattern) != len(names) {
			return ordered, false
		}
		for _, name := range ordered {
			if len(pattern[name]) != 1 {
				return ordered, false
			}
		}
	}
	return ordered, true
}

func (i *ScopeIndex) collectPatternBindings(expression ast.Expression, scope *Scope) (map[string][]*ast.BindingPattern, bool) {
	result := map[string][]*ast.BindingPattern{}
	shared := false
	var collect func(ast.Expression)
	collect = func(current ast.Expression) {
		if isNilNode(current) {
			return
		}
		if binding, ok := current.(*ast.BindingPattern); ok {
			shared = i.record(current, scope) || shared
			result[binding.Name] = append(result[binding.Name], binding)
			return
		}
		if i.record(current, scope) {
			shared = true
			return
		}
		for _, child := range ast.Children(current) {
			if expression, ok := child.(ast.Expression); ok {
				collect(expression)
			}
		}
	}
	collect(expression)
	return result, shared
}

func (i *ScopeIndex) visitExpression(expression ast.Expression, scope *Scope) {
	if isNilNode(expression) {
		return
	}
	if i.record(expression, scope) {
		return
	}
	switch node := expression.(type) {
	case *ast.LambdaExpression:
		i.visitLambda(node, scope)
		return
	case *ast.CallExpression:
		if identifier, ok := node.Callee.(*ast.Identifier); ok && identifier.Name == "super" {
			i.record(identifier, i.superScope(scope, scope.context.methodName))
		} else {
			i.visitExpression(node.Callee, scope)
		}
		for _, argument := range node.Arguments {
			i.visitExpression(argument, scope)
		}
		return
	case *ast.MemberExpression:
		if identifier, ok := node.Object.(*ast.Identifier); ok && identifier.Name == "super" {
			i.record(identifier, i.superScope(scope, node.Property))
			return
		}
		i.visitExpression(node.Object, scope)
		return
	}
	for _, child := range ast.Children(expression) {
		if nested, ok := child.(ast.Expression); ok {
			i.visitExpression(nested, scope)
		}
	}
}

func (i *ScopeIndex) superScope(scope *Scope, target string) *Scope {
	context := scope.context
	context.superTarget = target
	return i.newScope(scope, context)
}

func (i *ScopeIndex) visitLambda(lambda *ast.LambdaExpression, creation *Scope) {
	if isNilNode(lambda) {
		return
	}
	if i.nodeShared(lambda) {
		i.blockSharedTree(lambda, creation)
		return
	}
	// A lambda captures lexical values and static context, but Godot rejects a
	// bare super() from its anonymous method body. Keep the enclosing method
	// name out of its context rather than accidentally dispatching it as though
	// the lambda were that method.
	context := creation.context
	context.methodName = ""
	parameters := i.newScope(creation, context)
	for at, parameter := range lambda.Parameters {
		i.visitExpression(parameter.Default, parameters)
		binding := i.newBinding(
			BindingParameter,
			parameter.Name,
			creation.context.classID,
			lambda,
			at,
			parameter.NameSpan.Start.Line,
			parameter.NameSpan.Start.Column,
			i.parameterType(creation.context.classID, parameter, "lambda"),
		)
		parameters = i.install(parameters, binding, true)
	}
	body := i.newScope(parameters, context)
	i.visitStatements(lambda.Body, body)
}

func (i *ScopeIndex) visitChildren(node ast.Node, scope *Scope) {
	if isNilNode(node) {
		return
	}
	if i.nodeShared(node) {
		i.blockSharedTree(node, scope)
		return
	}
	for _, child := range ast.Children(node) {
		switch child := child.(type) {
		case ast.Expression:
			i.visitExpression(child, scope)
		case ast.Statement:
			i.visitStatement(child, scope)
		}
	}
}

func (i *ScopeIndex) visitUnsupported(node ast.Node, scope *Scope, reason string) {
	if isNilNode(node) {
		return
	}
	if i.record(node, scope) {
		return
	}
	i.blockUnsupportedTree(node, scope, reason)
}

func (i *ScopeIndex) blockUnsupportedTree(root ast.Node, scope *Scope, reason string) {
	blocked := i.newScope(scope, scope.context)
	blocked.blocked = reason
	pending := []ast.Node{root}
	visited := map[ast.Node]bool{}
	for len(pending) > 0 {
		at := len(pending) - 1
		node := pending[at]
		pending = pending[:at]
		if isNilNode(node) || visited[node] {
			continue
		}
		visited[node] = true
		if i.nodeShared(node) {
			i.blockSharedTree(node, scope)
			continue
		}
		i.scopes[node] = blocked
		pending = append(pending, ast.Children(node)...)
	}
}

func (i *ScopeIndex) ambiguousUnsupported(scope *Scope, node ast.Node, reason string, names ...string) *Scope {
	i.visitUnsupported(node, scope, reason)
	current := scope
	seen := map[string]bool{}
	for _, name := range names {
		if strings.TrimSpace(name) == "" || seen[name] {
			continue
		}
		seen[name] = true
		current = i.ambiguous(current, name, reason)
	}
	return current
}

func (i *ScopeIndex) resolveNamespace(scope *Scope, name string) BindingResult {
	if name == "self" {
		return i.resolveSelf(scope)
	}
	if name == "super" {
		return i.resolveSuper(scope)
	}
	if i.interfaces == nil {
		return unknownBinding("interface set is unavailable")
	}
	member := i.interfaces.Lookup(scope.context.classID, name)
	switch member.State() {
	case LookupFound:
		resolved, ok := member.Member()
		if !ok {
			return unknownBinding(fmt.Sprintf("member lookup for %q returned no member", name))
		}
		if scope.context.constantExpression && !constantMemberAllowed(resolved) {
			return unknownBinding(fmt.Sprintf("non-constant member %q is unavailable in a constant initializer", name))
		}
		if scope.context.static && !staticMemberAllowed(resolved) {
			return unknownBinding(fmt.Sprintf("non-static member %q is unavailable from static context", name))
		}
		return foundBinding(i.memberBinding(scope, BindingMember, name, resolved))
	case LookupUnknown:
		return unknownBinding(member.Reason())
	case LookupAbsent:
		// Only a proven #46 miss may reach enclosing and global namespaces.
	default:
		return unknownBinding(fmt.Sprintf("member lookup for %q returned invalid state", name))
	}
	if resolved, found := i.resolveEnclosing(scope, name); found {
		return resolved
	}
	if resolved, found := i.resolveProjectGlobal(scope, name); found {
		return resolved
	}
	return i.resolveEngineGlobal(scope, name)
}

func staticMemberAllowed(member Member) bool {
	switch member.Kind() {
	case MemberConstant, MemberEnum, MemberEnumMember, MemberClass:
		return true
	case MemberVariable, MemberMethod, MemberEngineMethod:
		return member.Static()
	default:
		return false
	}
}

func constantMemberAllowed(member Member) bool {
	switch member.Kind() {
	case MemberConstant, MemberEnum, MemberEnumMember, MemberClass:
		return true
	default:
		return false
	}
}

func (i *ScopeIndex) memberBinding(scope *Scope, kind BindingKind, name string, member Member) Binding {
	key := fmt.Sprintf(
		"member:%s:%s:%d:%s:%d:%d",
		member.DeclaringClassID(),
		member.EngineOwner(),
		member.Kind(),
		member.Name(),
		member.Line(),
		member.Column(),
	)
	binding := i.namespaceBinding(scope, key, kind, name, member.DeclaringClassID(), member.Type(), &member, nil, member.Line(), member.Column())
	binding.declaration = i.memberDeclaration(member)
	return binding
}

func (i *ScopeIndex) memberDeclaration(member Member) ast.Node {
	if i == nil || i.interfaces == nil || i.interfaces.index == nil || member.DeclaringClassID() == "" {
		return nil
	}
	class := i.interfaces.index.Classes[member.DeclaringClassID()]
	if class == nil {
		return nil
	}
	for _, declaration := range class.Declarations {
		if declaration.Name == member.Name() && declaration.Line == member.Line() && declaration.Column == member.Column() {
			return declaration.Node
		}
	}
	return nil
}

func (i *ScopeIndex) resolveSelf(scope *Scope) BindingResult {
	if scope.context.static {
		return unknownBinding("self is unavailable from static context")
	}
	if i.interfaces == nil {
		return unknownBinding("interface set is unavailable while resolving self")
	}
	class, ok := i.interfaces.Class(scope.context.classID)
	if !ok {
		return unknownBinding(fmt.Sprintf("class %q is absent from the declaration index", scope.context.classID))
	}
	return foundBinding(i.namespaceBinding(
		scope,
		"self:"+scope.context.classID,
		BindingSelf,
		"self",
		scope.context.classID,
		class.Type(),
		nil,
		nil,
		0,
		0,
	))
}

func (i *ScopeIndex) resolveSuper(scope *Scope) BindingResult {
	if scope.context.constantExpression {
		return unknownBinding("super is unavailable in a constant initializer")
	}
	if scope.context.superTarget == "" {
		return unknownBinding("super is unavailable outside a direct base-dispatch context")
	}
	if i.interfaces == nil {
		return unknownBinding("interface set is unavailable while resolving super")
	}
	base := i.interfaces.LookupBase(scope.context.classID, scope.context.superTarget)
	switch base.State() {
	case LookupUnknown:
		return unknownBinding(base.Reason())
	case LookupAbsent:
		return unknownBinding(fmt.Sprintf("base implementation of method %q is absent", scope.context.superTarget))
	case LookupFound:
		member, ok := base.Member()
		if !ok {
			return unknownBinding(fmt.Sprintf("base lookup for method %q returned no member", scope.context.superTarget))
		}
		if member.Kind() != MemberMethod && member.Kind() != MemberEngineMethod {
			return unknownBinding(fmt.Sprintf("base declaration %q is %s rather than a method", scope.context.superTarget, member.Kind()))
		}
		if member.Static() != scope.context.static {
			return unknownBinding(fmt.Sprintf("base method %q has static/instance mismatch", scope.context.superTarget))
		}
		key := fmt.Sprintf("super:%s:%s:%s:%s:%d:%d", scope.context.classID, scope.context.superTarget, member.DeclaringClassID(), member.EngineOwner(), member.Line(), member.Column())
		binding := i.namespaceBinding(scope, key, BindingSuper, "super", member.DeclaringClassID(), member.Type(), nil, &member, member.Line(), member.Column())
		binding.declaration = i.memberDeclaration(member)
		return foundBinding(binding)
	default:
		return unknownBinding(fmt.Sprintf("base lookup for method %q returned invalid state", scope.context.superTarget))
	}
}

func (i *ScopeIndex) resolveEnclosing(scope *Scope, name string) (BindingResult, bool) {
	outers, reason := i.enclosingClassIDs(scope.context.classID)
	if reason != "" {
		return unknownBinding(reason), true
	}
	for _, outer := range outers {
		result := i.interfaces.Lookup(outer, name)
		switch result.State() {
		case LookupAbsent:
			continue
		case LookupUnknown:
			return unknownBinding(result.Reason()), true
		case LookupFound:
			member, ok := result.Member()
			if !ok {
				return unknownBinding(fmt.Sprintf("enclosing lookup for %q returned no member", name)), true
			}
			switch member.Kind() {
			case MemberConstant, MemberEnumMember:
				return foundBinding(i.memberBinding(scope, BindingEnclosingConstant, name, member)), true
			case MemberEnum, MemberClass:
				return foundBinding(i.memberBinding(scope, BindingEnclosingType, name, member)), true
			default:
				return unknownBinding(fmt.Sprintf("enclosing declaration %q is a %s and is not lexically visible", name, member.Kind())), true
			}
		default:
			return unknownBinding(fmt.Sprintf("enclosing lookup for %q returned invalid state", name)), true
		}
	}
	return BindingResult{}, false
}

// enclosingClassIDs follows lexical class scopes without reconstructing #46
// member lookup. A class's project bases contribute their lexical enclosures
// before its own enclosure, matching Godot's class-scope order. Each returned
// ID is still resolved exclusively through InterfaceSet.Lookup.
func (i *ScopeIndex) enclosingClassIDs(classID string) ([]string, string) {
	if i.interfaces == nil || i.interfaces.index == nil {
		return nil, "class index is unavailable while resolving enclosing class scope"
	}
	index := i.interfaces.index
	outers := []string{}
	emitted := map[string]bool{}
	visited := map[string]bool{}
	visiting := map[string]bool{}
	var problem string
	var walk func(string)
	walk = func(current string) {
		if problem != "" || current == "" || visited[current] {
			return
		}
		if visiting[current] {
			problem = fmt.Sprintf("class scope topology cycles at %q", current)
			return
		}
		class := index.Classes[current]
		if class == nil {
			problem = fmt.Sprintf("class %q is absent from the declaration index while resolving enclosing class scope", current)
			return
		}
		visiting[current] = true
		if class.ParentID != "" {
			walk(class.ParentID)
		}
		if problem == "" && class.Inner {
			cut := strings.LastIndexByte(class.ID, '#')
			if cut < 0 {
				problem = fmt.Sprintf("inner class %q has no enclosing class identity", class.ID)
			} else {
				outer := class.ID[:cut]
				if !emitted[outer] {
					emitted[outer] = true
					outers = append(outers, outer)
				}
				walk(outer)
			}
		}
		delete(visiting, current)
		visited[current] = true
	}
	walk(classID)
	return outers, problem
}

func (i *ScopeIndex) resolveProjectGlobal(scope *Scope, name string) (BindingResult, bool) {
	if i.interfaces == nil || i.interfaces.index == nil {
		return unknownBinding("class index is unavailable while resolving project global"), true
	}
	index := i.interfaces.index
	if len(index.ParseFailures) > 0 {
		return unknownBinding(fmt.Sprintf("source parse failure at %q leaves project globals incomplete", index.ParseFailures[0])), true
	}
	if duplicates := index.DuplicateClassNames[name]; len(duplicates) > 0 {
		return unknownBinding(fmt.Sprintf("project class_name %q is ambiguous between %s", name, strings.Join(duplicates, ", "))), true
	}
	type candidate struct {
		kind  BindingKind
		class *ClassDecl
		key   string
	}
	candidates := []candidate{}
	if class := index.ClassByName(name); class != nil {
		candidates = append(candidates, candidate{kind: BindingProjectClass, class: class, key: "project-class:" + class.ID})
	}
	if path, declared := index.autoloads[name]; declared {
		class := index.TopLevel[path]
		if class == nil {
			return unknownBinding(fmt.Sprintf("autoload %q targets unindexed path %q", name, path)), true
		}
		candidates = append(candidates, candidate{kind: BindingAutoload, class: class, key: "autoload:" + name + ":" + path})
	}
	switch len(candidates) {
	case 0:
		return BindingResult{}, false
	case 1:
		if scope.context.constantExpression && candidates[0].kind != BindingProjectClass {
			return unknownBinding(fmt.Sprintf("non-constant project global %q is unavailable in a constant initializer", name)), true
		}
		class, ok := i.interfaces.Class(candidates[0].class.ID)
		if !ok {
			return unknownBinding(fmt.Sprintf("project global %q has no published class interface", name)), true
		}
		typeValue := class.Type()
		if candidates[0].kind == BindingProjectClass {
			typeValue = classObjectType(typeValue)
		}
		return foundBinding(i.namespaceBinding(scope, candidates[0].key, candidates[0].kind, name, candidates[0].class.ID, typeValue, nil, nil, candidates[0].class.Line, candidates[0].class.Column)), true
	default:
		return unknownBinding(fmt.Sprintf("project global %q is claimed by more than one retained category", name)), true
	}
}

func (i *ScopeIndex) resolveEngineGlobal(scope *Scope, name string) BindingResult {
	if i.interfaces == nil || i.interfaces.engine == nil {
		return unknownBinding(fmt.Sprintf("engine schema is unavailable while resolving global %q", name))
	}
	engine := i.interfaces.engine
	candidates := []Binding{}
	engineType := engine.ResolveType(name)
	singleton, hasSingleton := engine.Singleton(name)
	// Godot exposes globals such as Input and OS as singletons whose names and
	// instance types match engine classes. Those two schema records describe the
	// same global value, so preserve the singleton binding rather than treating
	// the class object and singleton as competing categories. Other collisions
	// remain unknown below.
	sameNamedSingleton := hasSingleton && sameNamedEngineSingletonClass(name, engineType, singleton)
	if engineType.Kind() != KindUnknown && !sameNamedSingleton {
		candidates = append(candidates, i.namespaceBinding(scope, "engine-type:"+name, BindingEngineType, name, "", engineTypeObjectType(name, engineType), nil, nil, 0, 0))
	}
	if hasSingleton {
		candidates = append(candidates, i.namespaceBinding(scope, "engine-singleton:"+name, BindingEngineSingleton, name, "", singleton, nil, nil, 0, 0))
	}
	if utility, ok := engine.Utility(name); ok {
		member := engineMethodMember(utility)
		candidates = append(candidates, i.namespaceBinding(scope, "engine-utility:"+name, BindingEngineUtility, name, "", member.Type(), &member, nil, 0, 0))
	}
	if name == "preload" || name == "load" {
		candidates = append(candidates, i.namespaceBinding(scope, "language-special:"+name, BindingLanguageSpecial, name, "", Callable(), nil, nil, 0, 0))
	}
	switch len(candidates) {
	case 0:
		return unknownBinding(fmt.Sprintf("global identifier %q is not retained by the selected engine schema", name))
	case 1:
		if scope.context.constantExpression && !constantEngineGlobalAllowed(candidates[0]) {
			return unknownBinding(fmt.Sprintf("non-constant engine global %q is unavailable in a constant initializer", name))
		}
		return foundBinding(candidates[0])
	default:
		return unknownBinding(fmt.Sprintf("retained engine global %q is claimed by multiple categories", name))
	}
}

func sameNamedEngineSingletonClass(name string, engineType, singleton Type) bool {
	return engineType.Kind() == KindClass && engineType.Name() == name && !engineType.Meta() &&
		singleton.Kind() == KindClass && singleton.Name() == name && !singleton.Meta()
}

func classObjectType(resolved Type) Type {
	if resolved.Kind() != KindClass {
		return resolved
	}
	return Class(resolved.Name(), nil, true)
}

func engineTypeObjectType(name string, resolved Type) Type {
	if resolved.Kind() == KindClass {
		return classObjectType(resolved)
	}
	return Unknown(fmt.Sprintf("engine type %q has no class-object representation", name))
}

func constantEngineGlobalAllowed(binding Binding) bool {
	switch binding.Kind() {
	case BindingEngineType:
		return true
	case BindingLanguageSpecial:
		return binding.Name() == "preload"
	case BindingEngineUtility:
		// Engine utilities are intentionally unknown here. Godot only accepts
		// the math utility category in a constant expression, but the immutable
		// Engine surface deliberately does not retain utility categories. A
		// future engine publication that carries that evidence can narrow this
		// answer without guessing from a function name.
		return false
	default:
		return false
	}
}
