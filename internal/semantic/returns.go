package semantic

import (
	"fmt"

	"github.com/cafecito-games/gdparser/ast"
)

// inferredFunctionReturn reduces an omitted result annotation only when the
// final callable provenance is one retained direct user method. It deliberately
// receives Member provenance rather than a callee spelling, so engine methods,
// Callable values, aliases, and unproven lookups remain on reduceCall's
// established path.
func (a *Analyzer) inferredFunctionReturn(member Member, request *reductionRequest) (Type, bool) {
	function, ok := a.directFunctionDeclaration(member)
	if !ok {
		return Type{}, false
	}
	return a.functionReturn(function, request), true
}

// directFunctionDeclaration recovers only the exact direct declaration that
// produced member in this analyzer's immutable interface snapshot. It never
// performs name, receiver, base, or super lookup: the final #49 result has
// already selected member using those policies.
func (a *Analyzer) directFunctionDeclaration(member Member) (*ast.FunctionDeclaration, bool) {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil || a.scopes == nil || member.Kind() != MemberMethod || member.DeclaringClassID() == "" || member.EngineOwner() != "" {
		return nil, false
	}
	class := a.interfaces.index.Classes[member.DeclaringClassID()]
	if class == nil {
		return nil, false
	}
	var function *ast.FunctionDeclaration
	for _, declaration := range class.Declarations {
		if declaration.Kind != DeclarationMethod || declaration.Name != member.Name() || declaration.Line != member.Line() || declaration.Column != member.Column() {
			continue
		}
		candidate, ok := declaration.Node.(*ast.FunctionDeclaration)
		if !ok || candidate == nil || function != nil {
			return nil, false
		}
		function = candidate
	}
	if function == nil || function.ReturnType != "" {
		return nil, false
	}
	if a.scopes.memberDeclaration(member) != function {
		return nil, false
	}
	return function, true
}

// functionReturn owns the separate completed-result cache for named function
// bodies. It never shares in-progress state: recursion lives only in the
// reduction request that is already reducing the owned return expressions.
func (a *Analyzer) functionReturn(function *ast.FunctionDeclaration, request *reductionRequest) Type {
	if a == nil {
		return Unknown("semantic analyzer is unavailable")
	}
	if function == nil {
		return Unknown("function declaration is unavailable for return inference")
	}
	if a.scopes == nil {
		return Unknown("scope index is unavailable while inferring function return")
	}
	if a.interfaces == nil || a.interfaces.index == nil {
		return Unknown("interface set is unavailable while inferring function return")
	}
	if a.engine == nil {
		return Unknown("engine schema is unavailable while inferring function return")
	}
	if a.narrow == nil {
		return Unknown("narrowing index is unavailable while inferring function return")
	}
	if function.Abstract || function.Body == nil {
		return Unknown("function body is unavailable for return inference")
	}
	if a.scopes.nodeShared(function) {
		return Unknown(sharedASTNodeReason)
	}
	if scope, ok := a.scopes.ScopeAt(function); !ok || scope == nil || scope.index != a.scopes || scope.blocked != "" {
		if scope != nil && scope.blocked != "" {
			return Unknown(scope.blocked)
		}
		return Unknown("function declaration is not indexed by this source snapshot")
	}
	if request == nil {
		request = &reductionRequest{}
	}
	if request.functionActive == nil {
		request.functionActive = map[*ast.FunctionDeclaration]bool{}
	}
	if request.functionActive[function] {
		request.markFunctionCycle()
		return Unknown("function return inference cycle")
	}
	if result, ok := a.cachedFunctionReturn(function); ok {
		return result
	}

	request.functionActive[function] = true
	walker := functionReturnWalker{
		analyzer: a,
		request:  request,
		seen:     map[ast.Statement]bool{},
	}
	terminal := walker.walkStatements(function.Body)
	delete(request.functionActive, function)
	result := walker.result(terminal)
	if (request.functionTainted != nil && request.functionTainted[function]) || len(request.tainted) != 0 {
		return result
	}
	return a.publishFunctionReturn(function, result)
}

func (r *reductionRequest) markFunctionCycle() {
	if r == nil {
		return
	}
	// Function re-entry occurs while return expressions are reducing. Mark
	// those active expression keys too, so a locally usable result (such as a
	// Callable lambda whose default recursed) never enters the shared cache.
	r.markCycle()
	if r.functionTainted == nil {
		r.functionTainted = map[*ast.FunctionDeclaration]bool{}
	}
	for function := range r.functionActive {
		r.functionTainted[function] = true
	}
}

func (a *Analyzer) cachedFunctionReturn(function *ast.FunctionDeclaration) (Type, bool) {
	if a == nil || function == nil {
		return Type{}, false
	}
	a.functionMu.RLock()
	result, ok := a.functionReturns[function]
	a.functionMu.RUnlock()
	return result, ok
}

func (a *Analyzer) publishFunctionReturn(function *ast.FunctionDeclaration, result Type) Type {
	if a == nil || function == nil {
		return result
	}
	a.functionMu.Lock()
	defer a.functionMu.Unlock()
	if existing, ok := a.functionReturns[function]; ok {
		return existing
	}
	if a.functionReturns == nil {
		a.functionReturns = map[*ast.FunctionDeclaration]Type{}
	}
	a.functionReturns[function] = result
	return result
}

type functionReturnWalker struct {
	analyzer *Analyzer
	request  *reductionRequest
	seen     map[ast.Statement]bool
	values   []Type
	bare     bool
	problem  Type
	failed   bool
}

// walkStatements visits only statement bodies owned by the named function. A
// terminal child ends its own sequence, while branch bodies are still all
// visited in source order so their return values participate in unification.
func (w *functionReturnWalker) walkStatements(statements []ast.Statement) bool {
	for _, statement := range statements {
		terminal := w.walkStatement(statement)
		if w.failed || terminal {
			return terminal
		}
	}
	return false
}

func (w *functionReturnWalker) walkRequiredBody(statements []ast.Statement, owner string) bool {
	if statements == nil {
		w.fail(fmt.Sprintf("%s body is unavailable while inferring function return", owner))
		return false
	}
	return w.walkStatements(statements)
}

func (w *functionReturnWalker) walkStatement(statement ast.Statement) bool {
	if isNilNode(statement) {
		w.fail(fmt.Sprintf("function return walker encountered unavailable statement %T", statement))
		return false
	}
	if w.seen[statement] {
		w.fail(sharedASTNodeReason)
		return false
	}
	w.seen[statement] = true
	if w.analyzer == nil || w.analyzer.scopes == nil {
		w.fail("scope index is unavailable while inferring function return")
		return false
	}
	if w.analyzer.scopes.nodeShared(statement) {
		w.fail(sharedASTNodeReason)
		return false
	}

	switch node := statement.(type) {
	case *ast.ReturnStatement:
		if node.Value == nil {
			w.bare = true
			return true
		}
		if isNilNode(node.Value) {
			w.fail(unavailableExpressionReason(node.Value))
			return true
		}
		w.addValue(node.Value)
		return true
	case *ast.IfStatement:
		if len(node.Branches) == 0 {
			w.fail("if statement has no branches while inferring function return")
			return false
		}
		terminal := hasReturnElse(node)
		for _, branch := range node.Branches {
			if isNilNode(branch.Condition) {
				w.fail("if branch condition is unavailable while inferring function return")
				return false
			}
			terminal = w.walkRequiredBody(branch.Body, "if branch") && terminal
			if w.failed {
				return false
			}
		}
		if hasReturnElse(node) {
			terminal = w.walkRequiredBody(node.Else, "else") && terminal
		}
		return terminal
	case *ast.WhileStatement:
		if isNilNode(node.Condition) {
			w.fail("while condition is unavailable while inferring function return")
			return false
		}
		_ = w.walkRequiredBody(node.Body, "while")
		return false
	case *ast.ForStatement:
		if node.Variable == "" || isNilNode(node.Iterable) {
			w.fail("for statement is unavailable while inferring function return")
			return false
		}
		_ = w.walkRequiredBody(node.Body, "for")
		return false
	case *ast.MatchStatement:
		return w.walkMatch(node)
	case *ast.VariableDeclaration:
		if node.Getter != nil || node.Setter != nil || node.GetterName != "" || node.SetterName != "" || node.AccessorBlock {
			w.fail("local property accessor is unavailable for function return inference")
			return false
		}
		if node.Value != nil && isNilNode(node.Value) {
			w.fail(unavailableExpressionReason(node.Value))
		}
		return false
	case *ast.ExpressionStatement:
		if isNilNode(node.Expression) {
			w.fail(unavailableExpressionReason(node.Expression))
		}
		return false
	case *ast.Assignment:
		if isNilNode(node.Target) || isNilNode(node.Value) {
			w.fail("assignment is unavailable while inferring function return")
		}
		return false
	case *ast.KeywordStatement:
		switch node.Keyword {
		case "pass", "break", "continue":
			return false
		default:
			w.fail(fmt.Sprintf("unsupported keyword statement %q while inferring function return", node.Keyword))
			return false
		}
	case *ast.Comment, *ast.Annotation:
		return false
	case *ast.FunctionDeclaration, *ast.ClassDeclaration:
		w.fail(fmt.Sprintf("unsupported nested %T while inferring function return", statement))
		return false
	default:
		w.fail(fmt.Sprintf("unsupported statement %T while inferring function return", statement))
		return false
	}
}

func (w *functionReturnWalker) walkMatch(statement *ast.MatchStatement) bool {
	if statement == nil || isNilNode(statement.Value) {
		w.fail("match statement is unavailable while inferring function return")
		return false
	}
	if len(statement.Cases) == 0 {
		w.fail("match statement has no cases while inferring function return")
		return false
	}
	wildcards := 0
	terminal := true
	for _, matchCase := range statement.Cases {
		if matchCase.Guard != nil && isNilNode(matchCase.Guard) {
			w.fail("match guard is unavailable while inferring function return")
			return false
		}
		for _, pattern := range matchCase.Patterns {
			if isNilNode(pattern) {
				w.fail("match pattern is unavailable while inferring function return")
				return false
			}
		}
		if len(matchCase.Patterns) == 1 && matchCase.Guard == nil {
			if wildcard, ok := matchCase.Patterns[0].(*ast.WildcardPattern); ok && wildcard != nil {
				wildcards++
			}
		}
		terminal = w.walkRequiredBody(matchCase.Body, "match case") && terminal
		if w.failed {
			return false
		}
	}
	return terminal && wildcards == 1
}

func hasReturnElse(statement *ast.IfStatement) bool {
	return statement != nil && (statement.Else != nil || statement.ElseKeywordSpan.End.Offset > statement.ElseKeywordSpan.Start.Offset)
}

func (w *functionReturnWalker) addValue(expression ast.Expression) {
	if w.failed {
		return
	}
	if isNilNode(expression) {
		w.fail(unavailableExpressionReason(expression))
		return
	}
	if w.analyzer == nil || w.analyzer.scopes == nil || w.analyzer.narrow == nil {
		w.fail("return-expression reduction seam is unavailable")
		return
	}
	if w.analyzer.scopes.nodeShared(expression) {
		w.fail(sharedASTNodeReason)
		return
	}
	scope, ok := w.analyzer.scopes.ScopeAt(expression)
	if !ok || scope == nil || scope.index != w.analyzer.scopes {
		w.fail("return expression is not indexed by this source snapshot")
		return
	}
	if scope.blocked != "" {
		w.fail(scope.blocked)
		return
	}
	// Return expressions own only their final #50 source-bound token. The
	// caller's context is intentionally not accepted here and this token is
	// never included in the function-result cache identity.
	token := w.analyzer.narrow.tokenAt(expression)
	value := w.analyzer.reduce(expression, reductionContext{scope: scope, token: token}, w.request).typeValue
	if value.Kind() == KindUnknown {
		w.problem = value
		w.failed = true
		return
	}
	w.values = append(w.values, value)
}

func (w *functionReturnWalker) fail(reason string) {
	if w.failed {
		return
	}
	w.problem = Unknown(reason)
	w.failed = true
}

func (w *functionReturnWalker) result(terminal bool) Type {
	if w.failed {
		return w.problem
	}
	if len(w.values) == 0 {
		return Void()
	}
	if w.bare || !terminal {
		return Variant()
	}
	result := w.values[0]
	for _, value := range w.values[1:] {
		result = w.analyzer.commonType(result, value)
		if result.Kind() == KindUnknown {
			return result
		}
	}
	return result
}
