package semantic

import "github.com/cafecito-games/gdparser/ast"

// narrowIndex is the analyzer-owned, immutable source-order flow side table.
// It records only non-base views: an absent entry is deliberately the base
// source view, which prevents an unavailable or malformed traversal from
// inheriting a surrounding fact by accident.
type narrowIndex struct {
	interfaces *InterfaceSet
	scopes     *ScopeIndex
	views      map[ast.Expression]*reductionOverlay
	next       uint64
}

// reductionOverlay is one persistent flow revision. Its pointer is the
// opaque, comparable cache token retained by reductionContextToken. The
// fields make the allocation non-zero-sized, so distinct revisions cannot
// collapse to one pointer identity.
type reductionOverlay struct {
	owner     *narrowIndex
	parent    *reductionOverlay
	binding   BindingID
	typeValue Type
	state     narrowState
	ordinal   uint64
}

type narrowState uint8

const (
	narrowPositive narrowState = iota + 1
	narrowTombstone
)

// newNarrowIndex finishes all traversal before returning. There are no writes
// to the maps or overlay chain after NewAnalyzer publishes the analyzer, so
// concurrent TypeOf callers only perform immutable reads.
func newNarrowIndex(interfaces *InterfaceSet, scopes *ScopeIndex) *narrowIndex {
	if interfaces == nil || interfaces.index == nil || scopes == nil {
		return nil
	}
	index := &narrowIndex{
		interfaces: interfaces,
		scopes:     scopes,
		views:      map[ast.Expression]*reductionOverlay{},
	}
	for _, classID := range interfaces.ClassIDs() {
		class := interfaces.index.Classes[classID]
		if class == nil {
			continue
		}
		seen := map[ast.Node]bool{}
		for _, declaration := range class.Declarations {
			if isNilNode(declaration.Node) || seen[declaration.Node] {
				continue
			}
			seen[declaration.Node] = true
			index.visitDeclaration(declaration.Node)
		}
	}
	return index
}

func (n *narrowIndex) visitDeclaration(declaration ast.Statement) {
	switch node := declaration.(type) {
	case *ast.FunctionDeclaration:
		n.visitFunction(node, nil)
	case *ast.VariableDeclaration:
		n.visitVariable(node, nil)
	case *ast.EnumDeclaration:
		for _, member := range node.Members {
			n.visitExpression(member.Value, nil)
		}
	}
}

func (n *narrowIndex) visitFunction(function *ast.FunctionDeclaration, overlay *reductionOverlay) {
	if function == nil {
		return
	}
	for _, parameter := range function.Parameters {
		n.visitExpression(parameter.Default, overlay)
	}
	n.visitStatements(function.Body, overlay)
}

func (n *narrowIndex) visitVariable(variable *ast.VariableDeclaration, overlay *reductionOverlay) {
	if variable == nil {
		return
	}
	n.visitExpression(variable.Value, overlay)
	n.visitStatements(variable.Getter, overlay)
	if variable.Setter != nil {
		n.visitStatements(variable.Setter.Body, overlay)
	}
}

func (n *narrowIndex) visitStatements(statements []ast.Statement, overlay *reductionOverlay) *reductionOverlay {
	current := overlay
	for _, statement := range statements {
		current = n.visitStatement(statement, current)
	}
	return current
}

func (n *narrowIndex) visitStatement(statement ast.Statement, overlay *reductionOverlay) *reductionOverlay {
	if isNilNode(statement) {
		return overlay
	}
	switch node := statement.(type) {
	case *ast.ExpressionStatement:
		n.visitExpression(node.Expression, overlay)
	case *ast.VariableDeclaration:
		n.visitVariable(node, overlay)
	case *ast.Assignment:
		// Both sides are evaluated before a direct assignment invalidates an
		// existing fact. This applies to = and every compound spelling alike.
		n.visitExpression(node.Target, overlay)
		n.visitExpression(node.Value, overlay)
		return n.invalidateAssignment(overlay, node.Target)
	case *ast.ReturnStatement:
		n.visitExpression(node.Value, overlay)
	case *ast.IfStatement:
		for _, branch := range node.Branches {
			n.visitExpression(branch.Condition, overlay)
			n.visitStatements(branch.Body, n.applyCondition(overlay, branch.Condition))
		}
		n.visitStatements(node.Else, overlay)
	case *ast.WhileStatement:
		// Loop conditions do not add facts, but a surrounding supported region
		// remains in force for their expressions and bodies.
		n.visitExpression(node.Condition, overlay)
		n.visitStatements(node.Body, overlay)
	case *ast.ForStatement:
		n.visitExpression(node.Iterable, overlay)
		n.visitStatements(node.Body, overlay)
	case *ast.MatchStatement:
		n.visitMatch(node, overlay)
	case *ast.KeywordStatement, *ast.Comment, *ast.Annotation:
		n.visitChildren(statement, overlay)
	case *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.SignalDeclaration, *ast.EnumDeclaration:
		// ScopeIndex marks nested declarations unsupported/ambiguous. Following
		// them here could create a flow view where lexical resolution is already
		// deliberately unavailable, so leave them base and fail closed.
	default:
		// Future statement forms receive no control-flow interpretation. Their
		// children still inherit the surrounding view only if ScopeIndex retained
		// a usable lexical scope for them.
		n.visitChildren(statement, overlay)
	}
	return overlay
}

func (n *narrowIndex) visitMatch(match *ast.MatchStatement, overlay *reductionOverlay) {
	if match == nil {
		return
	}
	n.visitExpression(match.Value, overlay)
	for _, matchCase := range match.Cases {
		for _, pattern := range matchCase.Patterns {
			n.visitExpression(pattern, overlay)
		}
		n.visitExpression(matchCase.Guard, overlay)
		n.visitStatements(matchCase.Body, n.applyCondition(overlay, matchCase.Guard))
	}
}

func (n *narrowIndex) visitExpression(expression ast.Expression, overlay *reductionOverlay) {
	if isNilNode(expression) || n == nil || n.scopes == nil {
		return
	}
	if n.scopes.nodeShared(expression) {
		return
	}
	scope, indexed := n.scopes.ScopeAt(expression)
	if !indexed || scope == nil || scope.blocked != "" {
		return
	}
	if overlay != nil {
		n.views[expression] = overlay
	}
	switch node := expression.(type) {
	case *ast.LambdaExpression:
		for _, parameter := range node.Parameters {
			n.visitExpression(parameter.Default, overlay)
		}
		n.visitStatements(node.Body, overlay)
		return
	}
	for _, child := range ast.Children(expression) {
		n.visitNode(child, overlay)
	}
}

func (n *narrowIndex) visitNode(node ast.Node, overlay *reductionOverlay) {
	if isNilNode(node) {
		return
	}
	switch child := node.(type) {
	case ast.Expression:
		n.visitExpression(child, overlay)
	case ast.Statement:
		n.visitStatement(child, overlay)
	default:
		n.visitChildren(node, overlay)
	}
}

func (n *narrowIndex) visitChildren(node ast.Node, overlay *reductionOverlay) {
	for _, nested := range ast.Children(node) {
		n.visitNode(nested, overlay)
	}
}

// tokenAt selects the exact source-bound non-base view. A missing entry is
// the ordinary base view, never an inherited ambient flow state.
func (n *narrowIndex) tokenAt(expression ast.Expression) reductionContextToken {
	if n == nil || isNilNode(expression) {
		return reductionContextToken{}
	}
	return reductionContextToken{overlay: n.views[expression]}
}

// childToken lets the immutable source table choose a nested expression's
// view. A foreign test/internal token is intentionally preserved so #48's
// context seam remains usable without letting it inspect #50 facts.
func (n *narrowIndex) childToken(expression ast.Expression, parent reductionContextToken) reductionContextToken {
	if n == nil || parent.overlay == nil || !n.owns(parent.overlay) {
		return parent
	}
	return n.tokenAt(expression)
}

func (n *narrowIndex) owns(overlay *reductionOverlay) bool {
	return n != nil && overlay != nil && overlay.owner == n
}

func (n *narrowIndex) applyCondition(parent *reductionOverlay, condition ast.Expression) *reductionOverlay {
	facts, supported := n.conditionFacts(condition)
	if !supported {
		return parent
	}
	result := parent
	for _, fact := range facts {
		result = n.applyFact(result, fact)
	}
	return result
}

type narrowFact struct {
	binding   BindingID
	typeValue Type
}

// conditionFacts accepts exactly a positive is test or an all-and tree of
// them. Any unsupported leaf, including a nested or, invalidates the complete
// condition so a side effect or alternate path cannot lend partial evidence.
func (n *narrowIndex) conditionFacts(condition ast.Expression) ([]narrowFact, bool) {
	if isNilNode(condition) {
		return nil, false
	}
	binary, isBinary := condition.(*ast.BinaryExpression)
	if !isBinary || binary == nil {
		return nil, false
	}
	switch binary.Operator {
	case "and", "&&":
		left, leftOK := n.conditionFacts(binary.Left)
		right, rightOK := n.conditionFacts(binary.Right)
		if !leftOK || !rightOK {
			return nil, false
		}
		return append(left, right...), true
	case "is":
		fact, ok := n.directFact(binary)
		if !ok {
			return nil, false
		}
		return []narrowFact{fact}, true
	default:
		return nil, false
	}
}

func (n *narrowIndex) directFact(guard *ast.BinaryExpression) (narrowFact, bool) {
	if n == nil || n.scopes == nil || n.interfaces == nil || guard == nil {
		return narrowFact{}, false
	}
	guardScope, guardIndexed := n.scopes.ScopeAt(guard)
	if !guardIndexed || guardScope == nil || guardScope.blocked != "" {
		return narrowFact{}, false
	}
	identifier, ok := guard.Left.(*ast.Identifier)
	if !ok || identifier == nil {
		return narrowFact{}, false
	}
	target, ok := guard.Right.(*ast.TypeExpression)
	if !ok || target == nil {
		return narrowFact{}, false
	}
	targetScope, targetIndexed := n.scopes.ScopeAt(target)
	if !targetIndexed || targetScope == nil || targetScope.blocked != "" {
		return narrowFact{}, false
	}
	resolved := n.scopes.Resolve(identifier)
	if resolved.State() != LookupFound {
		return narrowFact{}, false
	}
	binding, found := resolved.Binding()
	if !found || !narrowableBinding(binding) {
		return narrowFact{}, false
	}
	scope, indexed := n.scopes.ScopeAt(identifier)
	if !indexed || scope == nil || scope.blocked != "" {
		return narrowFact{}, false
	}
	typeValue := n.interfaces.ResolveType(scope.ClassID(), target.Name)
	if !narrowableType(typeValue) {
		return narrowFact{}, false
	}
	return narrowFact{binding: binding.ID(), typeValue: typeValue}, true
}

func narrowableBinding(binding Binding) bool {
	switch binding.Kind() {
	case BindingLocal, BindingParameter, BindingMatch:
		return true
	default:
		return false
	}
}

func narrowableType(typeValue Type) bool {
	if validate(typeValue) != "" || typeValue.Kind() == KindUnknown || typeValue.Kind() == KindVariant || typeValue.Kind() == KindVoid {
		return false
	}
	return !typeValue.Meta()
}

func (n *narrowIndex) applyFact(parent *reductionOverlay, fact narrowFact) *reductionOverlay {
	if prior, state, found := n.lookup(parent, fact.binding); found && state == narrowPositive && prior.AssignableTo(fact.typeValue) == AssignabilityYes {
		fact.typeValue = prior
	}
	return n.extend(parent, fact.binding, fact.typeValue, narrowPositive)
}

func (n *narrowIndex) invalidateAssignment(parent *reductionOverlay, target ast.Expression) *reductionOverlay {
	identifier, ok := target.(*ast.Identifier)
	if !ok || identifier == nil || n == nil || n.scopes == nil {
		return parent
	}
	resolved := n.scopes.Resolve(identifier)
	if resolved.State() != LookupFound {
		return parent
	}
	binding, found := resolved.Binding()
	if !found || !narrowableBinding(binding) {
		return parent
	}
	_, state, active := n.lookup(parent, binding.ID())
	if !active || state != narrowPositive {
		return parent
	}
	return n.extend(parent, binding.ID(), Type{}, narrowTombstone)
}

func (n *narrowIndex) extend(parent *reductionOverlay, binding BindingID, typeValue Type, state narrowState) *reductionOverlay {
	if n == nil {
		return parent
	}
	n.next++
	return &reductionOverlay{
		owner:     n,
		parent:    parent,
		binding:   binding,
		typeValue: typeValue,
		state:     state,
		ordinal:   n.next,
	}
}

// lookup stops at a tombstone instead of continuing to an outer positive
// entry. That is what makes direct assignment restore ordinary reduction,
// rather than accidentally resurrecting a parent guard.
func (n *narrowIndex) lookup(overlay *reductionOverlay, binding BindingID) (Type, narrowState, bool) {
	if !n.owns(overlay) {
		return Type{}, 0, false
	}
	for current := overlay; current != nil; current = current.parent {
		if current.binding == binding {
			return current.typeValue, current.state, true
		}
	}
	return Type{}, 0, false
}
