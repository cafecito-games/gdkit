package semantic

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/cafecito-games/gdparser/ast"
)

// Analyzer reduces expressions against one immutable parsed-source and engine
// snapshot. It owns no parser, schema selection, diagnostics, or mutable AST
// state. Completed reductions are cached only after their complete immutable
// result is available.
type Analyzer struct {
	interfaces *InterfaceSet
	scopes     *ScopeIndex
	engine     *Engine

	unavailable string

	cacheMu sync.RWMutex
	cache   map[reductionKey]reductionResult
}

// reductionContextToken distinguishes the base source view from a future
// immutable flow overlay. Its zero value is the ordinary source view; the
// pointer is deliberately comparable so a later overlay cannot share an
// ordinary cache answer merely because it wraps the same Scope.
type reductionContextToken struct {
	overlay *reductionOverlay
}

// reductionOverlay carries no flow facts until #50 owns them, but it must stay
// non-zero-sized: pointers to separate zero-sized allocations may compare
// equal, which would collapse distinct overlay cache identities.
type reductionOverlay struct {
	_ byte
}

type reductionContext struct {
	scope *Scope
	token reductionContextToken
}

type reductionKey struct {
	expression ast.Expression
	scope      ScopeID
	token      reductionContextToken
}

// reductionResult retains just enough provenance for an enclosing call to use
// a declared callable signature. TypeOf never exposes this extra information.
type reductionResult struct {
	typeValue Type
	member    *Member
	special   string
}

type reductionRequest struct {
	active  map[reductionKey]bool
	tainted map[reductionKey]bool
	local   map[reductionKey]reductionResult
}

// NewAnalyzer constructs the only accepted semantic pipeline for source and
// engine. An unavailable source fails closed before BuildIndex could call a
// method on it; an unavailable engine is retained so the established engine
// and interface boundaries can report their reasoned Unknowns.
func NewAnalyzer(source SourceSet, engine *Engine) *Analyzer {
	analyzer := &Analyzer{
		engine: engine,
		cache:  map[reductionKey]reductionResult{},
	}
	if sourceUnavailable(source) {
		analyzer.unavailable = "source set is unavailable"
		return analyzer
	}
	index := BuildIndex(source)
	analyzer.interfaces = BuildInterfaces(index, engine)
	analyzer.scopes = BuildScopes(analyzer.interfaces)
	return analyzer
}

// TypeOf returns one reduced source expression type. An expression from a
// different parsed snapshot has no recorded scope and therefore cannot reuse
// a result from this analyzer.
func (a *Analyzer) TypeOf(expression ast.Expression) Type {
	if a == nil {
		return Unknown("semantic analyzer is unavailable")
	}
	if isNilNode(expression) {
		return Unknown(unavailableExpressionReason(expression))
	}
	if a.scopes == nil {
		if a.unavailable != "" {
			return Unknown(a.unavailable)
		}
		return Unknown("scope index is unavailable")
	}
	scope, ok := a.scopes.ScopeAt(expression)
	if !ok {
		return Unknown("expression is not indexed by this source snapshot")
	}
	return a.typeOfIn(expression, scope, reductionContextToken{}).typeValue
}

// typeOfIn is the internal context-aware reducer seam for #50. The supplied
// scope must be this expression's exact immutable ScopeAt view; the token is
// part of cache identity even before flow overlays supply additional facts.
func (a *Analyzer) typeOfIn(expression ast.Expression, scope *Scope, token reductionContextToken) reductionResult {
	if a != nil && a.scopes != nil {
		recorded, ok := a.scopes.ScopeAt(expression)
		if !ok || recorded != scope {
			return unknownReduction("expression scope does not match this analyzer snapshot")
		}
	}
	request := &reductionRequest{active: map[reductionKey]bool{}}
	return a.reduce(expression, reductionContext{scope: scope, token: token}, request)
}

func sourceUnavailable(source SourceSet) bool {
	if source == nil {
		return true
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (a *Analyzer) reduce(expression ast.Expression, context reductionContext, request *reductionRequest) reductionResult {
	if a == nil {
		return unknownReduction("semantic analyzer is unavailable")
	}
	if isNilNode(expression) {
		return unknownReduction(unavailableExpressionReason(expression))
	}
	if a.scopes == nil {
		if a.unavailable != "" {
			return unknownReduction(a.unavailable)
		}
		return unknownReduction("scope index is unavailable")
	}
	if context.scope == nil || context.scope.index != a.scopes {
		return unknownReduction("expression scope is unavailable from this analyzer snapshot")
	}
	key := reductionKey{expression: expression, scope: context.scope.ID(), token: context.token}
	if request == nil {
		request = &reductionRequest{active: map[reductionKey]bool{}}
	} else if request.active == nil {
		request.active = map[reductionKey]bool{}
	}
	if result, ok := request.cached(key); ok {
		// This completed value was derived by a path that observed a cycle.
		// Any active parent that consumes it must stay request-local too.
		request.markCycle()
		return result
	}
	if result, ok := a.cached(key); ok {
		return result
	}
	if request.active[key] {
		request.markCycle()
		return unknownReduction("expression reduction cycle")
	}
	request.active[key] = true
	result := a.reduceExpression(expression, context, request)
	delete(request.active, key)
	if request.tainted[key] {
		// A child saw this request re-enter an active key. Its result may be
		// locally usable (for example a lambda remains Callable while its
		// ignored default cycles), but caching it would make a later query
		// depend on which traversal won the first write.
		return request.publish(key, result)
	}
	return a.publish(key, result)
}

// markCycle taints every active ancestor in this request. DFS state is request
// local, so no concurrent query can observe either the active set or this
// taint. Results below the re-entry may still be returned to their caller, but
// only a traversal that did not observe the cycle may enter the shared cache.
func (r *reductionRequest) markCycle() {
	if r == nil {
		return
	}
	if r.tainted == nil {
		r.tainted = map[reductionKey]bool{}
	}
	for key := range r.active {
		r.tainted[key] = true
	}
}

// cached returns one completed request-local result. These entries are only
// for paths that observed a cycle, so they never cross a TypeOf request or
// enter the synchronized shared cache.
func (r *reductionRequest) cached(key reductionKey) (reductionResult, bool) {
	if r == nil {
		return reductionResult{}, false
	}
	result, ok := r.local[key]
	if !ok {
		return reductionResult{}, false
	}
	return cloneReductionResult(result), true
}

func (r *reductionRequest) publish(key reductionKey, result reductionResult) reductionResult {
	if r == nil {
		return result
	}
	if r.local == nil {
		r.local = map[reductionKey]reductionResult{}
	}
	if existing, ok := r.local[key]; ok {
		return cloneReductionResult(existing)
	}
	r.local[key] = cloneReductionResult(result)
	return cloneReductionResult(result)
}

func (a *Analyzer) cached(key reductionKey) (reductionResult, bool) {
	a.cacheMu.RLock()
	result, ok := a.cache[key]
	a.cacheMu.RUnlock()
	if !ok {
		return reductionResult{}, false
	}
	return cloneReductionResult(result), true
}

func (a *Analyzer) publish(key reductionKey, result reductionResult) reductionResult {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	if existing, ok := a.cache[key]; ok {
		return cloneReductionResult(existing)
	}
	if a.cache == nil {
		a.cache = map[reductionKey]reductionResult{}
	}
	a.cache[key] = cloneReductionResult(result)
	return cloneReductionResult(result)
}

func cloneReductionResult(result reductionResult) reductionResult {
	if result.member != nil {
		member := cloneMember(*result.member)
		result.member = &member
	}
	return result
}

func knownReduction(typeValue Type) reductionResult {
	return reductionResult{typeValue: typeValue}
}

func memberReduction(member Member) reductionResult {
	if member.Kind() == MemberEnum {
		return unknownReduction(fmt.Sprintf("enum type value %q is deferred to #49", member.Name()))
	}
	copy := cloneMember(member)
	return reductionResult{typeValue: copy.Type(), member: &copy}
}

func unknownReduction(reason string) reductionResult {
	return knownReduction(Unknown(reason))
}

func (a *Analyzer) reduceChild(expression ast.Expression, parent reductionContext, request *reductionRequest) reductionResult {
	if isNilNode(expression) {
		return unknownReduction(unavailableExpressionReason(expression))
	}
	if a == nil || a.scopes == nil {
		return unknownReduction("scope index is unavailable")
	}
	scope, ok := a.scopes.ScopeAt(expression)
	if !ok {
		return unknownReduction("expression is not indexed by this source snapshot")
	}
	return a.reduce(expression, reductionContext{scope: scope, token: parent.token}, request)
}

func (a *Analyzer) reduceExpression(expression ast.Expression, context reductionContext, request *reductionRequest) reductionResult {
	switch node := expression.(type) {
	case *ast.Literal:
		return reduceLiteral(node)
	case *ast.Identifier:
		return a.reduceIdentifier(node, context, request)
	case *ast.UnaryExpression:
		return a.reduceUnary(node, context, request)
	case *ast.BinaryExpression:
		return a.reduceBinary(node, context, request)
	case *ast.TernaryExpression:
		return a.reduceTernary(node, context, request)
	case *ast.CallExpression:
		return a.reduceCall(node, context, request)
	case *ast.MemberExpression:
		return a.reduceMember(node, context, request)
	case *ast.SubscriptExpression:
		return a.reduceSubscript(node, context, request)
	case *ast.ArrayLiteral:
		return a.reduceArray(node, context, request)
	case *ast.DictionaryLiteral:
		return a.reduceDictionary(node, context, request)
	case *ast.NodePathExpression:
		return unknownReduction("node-path shorthand is deferred to #53")
	case *ast.TypeExpression:
		return unknownReduction("type expression is not a value")
	case *ast.BindingPattern:
		return unknownReduction("binding pattern is not a value")
	case *ast.WildcardPattern:
		return unknownReduction("wildcard pattern is not a value")
	case *ast.RestPattern:
		return unknownReduction("rest pattern is not a value")
	case *ast.LambdaExpression:
		return a.reduceLambda(node, context, request)
	default:
		return unknownReduction(fmt.Sprintf("unsupported expression form %T", expression))
	}
}

func reduceLiteral(literal *ast.Literal) reductionResult {
	if literal == nil {
		return unknownReduction("literal is unavailable")
	}
	switch literal.Kind {
	case ast.IntegerLiteral:
		return knownReduction(Builtin("int"))
	case ast.FloatLiteral:
		return knownReduction(Builtin("float"))
	case ast.StringLiteral:
		return knownReduction(Builtin("String"))
	case ast.StringNameLiteral:
		return knownReduction(Builtin("StringName"))
	case ast.NodePathLiteral:
		return knownReduction(Builtin("NodePath"))
	case ast.BoolLiteral:
		return knownReduction(Builtin("bool"))
	case ast.NullLiteral:
		return knownReduction(Variant())
	default:
		return unknownReduction(fmt.Sprintf("literal kind %q is unsupported", literal.Kind))
	}
}

func (a *Analyzer) reduceIdentifier(identifier *ast.Identifier, context reductionContext, request *reductionRequest) reductionResult {
	if identifier == nil {
		return unknownReduction("identifier is unavailable")
	}
	if a.scopes == nil {
		return unknownReduction("scope index is unavailable")
	}
	resolved := a.scopes.Resolve(identifier)
	switch resolved.State() {
	case LookupUnknown:
		return unknownReduction(resolved.Reason())
	case LookupAbsent:
		return unknownReduction(fmt.Sprintf("identifier %q is absent from the lexical snapshot", identifier.Name))
	case LookupFound:
		binding, ok := resolved.Binding()
		if !ok {
			return unknownReduction(fmt.Sprintf("identifier %q resolved without a binding", identifier.Name))
		}
		result := knownReduction(binding.Type())
		if binding.Kind() == BindingSuper {
			if member, found := binding.SuperMember(); found {
				result = memberReduction(member)
				result.special = "super"
			} else {
				return unknownReduction(fmt.Sprintf("super binding for %q has no selected base member", identifier.Name))
			}
		} else if member, found := binding.Member(); found {
			result = memberReduction(member)
		}
		if result.typeValue.Kind() != KindUnknown {
			if binding.Kind() == BindingLanguageSpecial {
				result.special = binding.Name()
			}
			return result
		}
		return a.reduceDeferredBinding(binding, result, context, request)
	default:
		return unknownReduction(fmt.Sprintf("identifier %q resolution returned invalid state", identifier.Name))
	}
}

func (a *Analyzer) reduceDeferredBinding(binding Binding, prior reductionResult, context reductionContext, request *reductionRequest) reductionResult {
	declaration := binding.Declaration()
	switch node := declaration.(type) {
	case *ast.VariableDeclaration:
		return a.reduceDeferredVariable(node, prior, context, request)
	case *ast.FunctionDeclaration:
		return a.reduceInferredParameter(node.Parameters, binding, prior, context, request)
	case *ast.LambdaExpression:
		return a.reduceInferredParameter(node.Parameters, binding, prior, context, request)
	default:
		return prior
	}
}

func (a *Analyzer) reduceDeferredVariable(node *ast.VariableDeclaration, prior reductionResult, context reductionContext, request *reductionRequest) reductionResult {
	if node == nil {
		return prior
	}
	if (node.Constant || node.Inferred) && node.Type == "" {
		if isNilNode(node.Value) {
			return prior
		}
		return knownReduction(a.reduceChild(node.Value, context, request).typeValue)
	}
	if node.Value != nil && node.Type == "" {
		return knownReduction(Variant())
	}
	return prior
}

func (a *Analyzer) reduceInferredParameter(parameters []ast.Parameter, binding Binding, prior reductionResult, context reductionContext, request *reductionRequest) reductionResult {
	if binding.Kind() != BindingParameter {
		return prior
	}
	slot := binding.Slot()
	if slot < 0 || slot >= len(parameters) {
		return prior
	}
	parameter := parameters[slot]
	if !parameter.Inferred || isNilNode(parameter.Default) {
		return prior
	}
	return knownReduction(a.reduceChild(parameter.Default, context, request).typeValue)
}

func (a *Analyzer) reduceUnary(expression *ast.UnaryExpression, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("unary expression is unavailable")
	}
	operand := a.reduceChild(expression.Operand, context, request)
	if operand.typeValue.Kind() == KindUnknown {
		return operand
	}
	if expression.Operator == "await" {
		return knownReduction(operand.typeValue)
	}
	left, ok := a.engineOperandIdentity(operand.typeValue)
	if !ok {
		return unknownReduction(fmt.Sprintf("unary operator %q has no canonical selected-engine operand", expression.Operator))
	}
	if a.engine == nil {
		return unknownReduction("engine schema is unavailable while resolving unary operator")
	}
	operator := engineUnaryOperator(expression.Operator)
	result, found := a.engine.Operator(left, operator, "")
	if !found {
		return unknownReduction(fmt.Sprintf("selected engine has no unary operator %s %q", left, operator))
	}
	return knownReduction(result)
}

// engineUnaryOperator translates gdparser's lexical prefix spellings to the
// exact extension-API operator vocabulary. It does not choose a result or
// broaden lookup: the selected Engine still supplies the only matching row.
func engineUnaryOperator(operator string) string {
	switch operator {
	case "+":
		return "unary+"
	case "-":
		return "unary-"
	case "!":
		return "not"
	default:
		return operator
	}
}

func (a *Analyzer) reduceBinary(expression *ast.BinaryExpression, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("binary expression is unavailable")
	}
	left := a.reduceChild(expression.Left, context, request)
	if left.typeValue.Kind() == KindUnknown {
		return left
	}
	if expression.Operator == "as" || expression.Operator == "is" {
		typeExpression, ok := expression.Right.(*ast.TypeExpression)
		if !ok || typeExpression == nil {
			return unknownReduction(fmt.Sprintf("operator %q requires a type expression", expression.Operator))
		}
		annotationScope, ok := a.scopeAt(typeExpression)
		if !ok {
			return unknownReduction("type expression is not indexed by this source snapshot")
		}
		if a.interfaces == nil {
			return unknownReduction("interface set is unavailable while resolving type expression")
		}
		resolved := a.interfaces.ResolveType(annotationScope.ClassID(), typeExpression.Name)
		if resolved.Kind() == KindUnknown {
			return knownReduction(resolved)
		}
		if expression.Operator == "as" {
			return knownReduction(resolved)
		}
		return knownReduction(Builtin("bool"))
	}
	right := a.reduceChild(expression.Right, context, request)
	if right.typeValue.Kind() == KindUnknown {
		return right
	}
	leftID, leftOK := a.engineOperandIdentity(left.typeValue)
	rightID, rightOK := a.engineOperandIdentity(right.typeValue)
	if !leftOK || !rightOK {
		return unknownReduction(fmt.Sprintf("binary operator %q has no canonical selected-engine operands", expression.Operator))
	}
	if a.engine == nil {
		return unknownReduction("engine schema is unavailable while resolving binary operator")
	}
	operator := engineBinaryOperator(expression.Operator)
	result, found := a.engine.Operator(leftID, operator, rightID)
	if !found {
		return unknownReduction(fmt.Sprintf("selected engine has no binary operator %s %q %s", leftID, operator, rightID))
	}
	return knownReduction(result)
}

// engineBinaryOperator translates only gdparser's lexical aliases to the
// extension-API operator names. The selected Engine remains the sole source of
// the exact row and its result type.
func engineBinaryOperator(operator string) string {
	switch operator {
	case "&&":
		return "and"
	case "||":
		return "or"
	default:
		return operator
	}
}

func (a *Analyzer) reduceTernary(expression *ast.TernaryExpression, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("ternary expression is unavailable")
	}
	value := a.reduceChild(expression.Value, context, request)
	if value.typeValue.Kind() == KindUnknown {
		return value
	}
	condition := a.reduceChild(expression.Condition, context, request)
	if condition.typeValue.Kind() == KindUnknown {
		return condition
	}
	alternative := a.reduceChild(expression.Alternative, context, request)
	if alternative.typeValue.Kind() == KindUnknown {
		return alternative
	}
	return knownReduction(a.commonType(value.typeValue, alternative.typeValue))
}

func (a *Analyzer) reduceCall(expression *ast.CallExpression, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("call expression is unavailable")
	}
	callee := a.reduceChild(expression.Callee, context, request)
	if callee.typeValue.Kind() == KindUnknown {
		return callee
	}
	for _, argument := range expression.Arguments {
		resolved := a.reduceChild(argument, context, request)
		if resolved.typeValue.Kind() == KindUnknown {
			return resolved
		}
	}
	if callee.typeValue.Kind() == KindVariant {
		return knownReduction(Variant())
	}
	if callee.typeValue.Meta() {
		return unknownReduction("meta-class construction is deferred to #49")
	}
	if callee.special == "preload" || callee.special == "load" {
		return unknownReduction(fmt.Sprintf("%s call is deferred to #49", callee.special))
	}
	if callee.member != nil {
		returned, ok := callee.member.ReturnType()
		if !ok {
			return unknownReduction(fmt.Sprintf("callable member %q has no declared return type", callee.member.Name()))
		}
		return knownReduction(returned)
	}
	if callee.typeValue.Kind() == KindCallable {
		return unknownReduction("Callable value has no retained signature")
	}
	return unknownReduction(fmt.Sprintf("receiver %s is not callable", reductionTypeLabel(callee.typeValue)))
}

func (a *Analyzer) reduceMember(expression *ast.MemberExpression, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("member expression is unavailable")
	}
	receiver := a.reduceChild(expression.Object, context, request)
	if receiver.typeValue.Kind() == KindUnknown {
		return receiver
	}
	if receiver.typeValue.Kind() == KindVariant {
		return knownReduction(Variant())
	}
	if receiver.member != nil && receiver.special == "super" {
		return memberReduction(*receiver.member)
	}
	if receiver.typeValue.Meta() {
		return unknownReduction("meta-class member access is deferred to #49")
	}
	if a.interfaces == nil {
		return unknownReduction("interface set is unavailable while resolving member")
	}
	resolved := a.interfaces.LookupMember(receiver.typeValue, expression.Property)
	result := reduceMemberLookup(receiver.typeValue, expression.Property, resolved)
	if result.typeValue.Kind() != KindUnknown || result.member == nil {
		return result
	}
	if result.member.Kind() != MemberVariable && result.member.Kind() != MemberConstant {
		return result
	}
	return a.reduceDeferredMember(*result.member, result, context, request)
}

func (a *Analyzer) reduceDeferredMember(member Member, prior reductionResult, context reductionContext, request *reductionRequest) reductionResult {
	if a == nil || a.scopes == nil {
		return prior
	}
	declaration, ok := a.scopes.memberDeclaration(member).(*ast.VariableDeclaration)
	if !ok {
		return prior
	}
	return a.reduceDeferredVariable(declaration, prior, context, request)
}

// reduceMemberLookup is the reducer's one tri-state boundary for ordinary
// receiver-member composition. Unknown remains the owner's exact cause; only
// a proven absence becomes this reducer's receiver/member-specific Unknown.
func reduceMemberLookup(receiver Type, name string, resolved LookupResult) reductionResult {
	switch resolved.State() {
	case LookupFound:
		member, ok := resolved.Member()
		if !ok {
			return unknownReduction(fmt.Sprintf("member lookup for %q returned no member", name))
		}
		return memberReduction(member)
	case LookupUnknown:
		return unknownReduction(resolved.Reason())
	case LookupAbsent:
		return unknownReduction(fmt.Sprintf("member %q is absent from %s", name, reductionTypeLabel(receiver)))
	default:
		return unknownReduction(fmt.Sprintf("member lookup for %q returned invalid state", name))
	}
}

func (a *Analyzer) reduceSubscript(expression *ast.SubscriptExpression, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("subscript expression is unavailable")
	}
	receiver := a.reduceChild(expression.Object, context, request)
	if receiver.typeValue.Kind() == KindUnknown {
		return receiver
	}
	index := a.reduceChild(expression.Index, context, request)
	if index.typeValue.Kind() == KindUnknown {
		return index
	}
	switch receiver.typeValue.Kind() {
	case KindVariant:
		return knownReduction(Variant())
	case KindArray:
		if element, typed := receiver.typeValue.Element(); typed {
			return knownReduction(element)
		}
		return knownReduction(Variant())
	case KindDictionary:
		if value, typed := receiver.typeValue.Value(); typed {
			return knownReduction(value)
		}
		return knownReduction(Variant())
	default:
		return unknownReduction(fmt.Sprintf("%s receiver is not subscriptable", reductionTypeLabel(receiver.typeValue)))
	}
}

func (a *Analyzer) reduceArray(expression *ast.ArrayLiteral, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("array literal is unavailable")
	}
	if len(expression.Elements) == 0 {
		return knownReduction(Array(nil))
	}
	var element Type
	homogeneous := true
	for at, item := range expression.Elements {
		resolved := a.reduceChild(item, context, request)
		if resolved.typeValue.Kind() == KindUnknown {
			return resolved
		}
		if problem := collectionComponentProblem(resolved.typeValue); problem != "" {
			return unknownReduction(problem)
		}
		if at == 0 {
			element = resolved.typeValue
			continue
		}
		if !element.Equal(resolved.typeValue) {
			homogeneous = false
		}
	}
	if !homogeneous {
		return knownReduction(Array(nil))
	}
	return knownReduction(Array(&element))
}

func (a *Analyzer) reduceDictionary(expression *ast.DictionaryLiteral, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("dictionary literal is unavailable")
	}
	if len(expression.Entries) == 0 {
		return knownReduction(Dictionary(nil, nil))
	}
	var key, value Type
	homogeneous := true
	for at, entry := range expression.Entries {
		resolvedKey := a.reduceChild(entry.Key, context, request)
		if resolvedKey.typeValue.Kind() == KindUnknown {
			return resolvedKey
		}
		if problem := collectionComponentProblem(resolvedKey.typeValue); problem != "" {
			return unknownReduction(problem)
		}
		resolvedValue := a.reduceChild(entry.Value, context, request)
		if resolvedValue.typeValue.Kind() == KindUnknown {
			return resolvedValue
		}
		if problem := collectionComponentProblem(resolvedValue.typeValue); problem != "" {
			return unknownReduction(problem)
		}
		if at == 0 {
			key, value = resolvedKey.typeValue, resolvedValue.typeValue
			continue
		}
		if !key.Equal(resolvedKey.typeValue) || !value.Equal(resolvedValue.typeValue) {
			homogeneous = false
		}
	}
	if !homogeneous {
		return knownReduction(Dictionary(nil, nil))
	}
	return knownReduction(Dictionary(&key, &value))
}

func collectionComponentProblem(value Type) string {
	switch value.Kind() {
	case KindVoid:
		return "void value cannot be a collection component"
	case KindClass:
		if value.Meta() {
			return fmt.Sprintf("meta-class %q cannot be a collection component", value.Name())
		}
	}
	return ""
}

func (a *Analyzer) reduceLambda(expression *ast.LambdaExpression, context reductionContext, request *reductionRequest) reductionResult {
	if expression == nil {
		return unknownReduction("lambda expression is unavailable")
	}
	for _, parameter := range expression.Parameters {
		if isNilNode(parameter.Default) {
			continue
		}
		// Defaults have their own #47-recorded lexical scopes. Their result
		// does not change the representable Callable answer, but evaluating it
		// populates the same immutable cache a future signature pass can reuse.
		_ = a.reduceChild(parameter.Default, context, request)
	}
	return knownReduction(Callable())
}

func (a *Analyzer) scopeAt(expression ast.Expression) (*Scope, bool) {
	if a == nil || a.scopes == nil || isNilNode(expression) {
		return nil, false
	}
	return a.scopes.ScopeAt(expression)
}

func unavailableExpressionReason(expression ast.Expression) string {
	if expression == nil {
		return "expression is unavailable"
	}
	return fmt.Sprintf("expression form %T is unavailable", expression)
}

func (a *Analyzer) engineOperandIdentity(value Type) (string, bool) {
	if a == nil || a.engine == nil || !fullyKnownOperand(value) {
		return "", false
	}
	switch value.Kind() {
	case KindBuiltin:
		name := value.Name()
		resolved := a.engine.ResolveType(name)
		return name, resolved.Kind() == KindBuiltin && resolved.Equal(value)
	case KindClass:
		if value.Meta() {
			return "", false
		}
		name := value.Name()
		resolved := a.engine.Class(name)
		return name, resolved.Kind() == KindClass && resolved.Equal(value)
	case KindArray:
		resolved := a.engine.ResolveType("Array")
		return "Array", resolved.Kind() == KindArray
	case KindDictionary:
		resolved := a.engine.ResolveType("Dictionary")
		return "Dictionary", resolved.Kind() == KindDictionary
	case KindCallable:
		resolved := a.engine.ResolveType("Callable")
		return "Callable", resolved.Kind() == KindCallable
	case KindSignal:
		resolved := a.engine.ResolveType("Signal")
		return "Signal", resolved.Kind() == KindSignal
	case KindVariant:
		return "Variant", a.engine.ResolveType("Variant").Kind() == KindVariant
	default:
		return "", false
	}
}

func fullyKnownOperand(value Type) bool {
	if value.Kind() == KindUnknown || validate(value) != "" {
		return false
	}
	switch value.Kind() {
	case KindClass:
		if value.Meta() {
			return false
		}
		if base, hasBase := value.Base(); hasBase {
			return fullyKnownOperand(base)
		}
		return true
	case KindArray:
		if element, typed := value.Element(); typed {
			return fullyKnownOperand(element)
		}
		return true
	case KindDictionary:
		if key, typed := value.Key(); typed {
			value, hasValue := value.Value()
			return hasValue && fullyKnownOperand(key) && fullyKnownOperand(value)
		}
		return true
	case KindBuiltin, KindVariant, KindCallable, KindSignal:
		return true
	default:
		return false
	}
}

// commonType returns the narrowest answer justified by both known branches.
// Incomplete class ancestry is analysis failure, rather than a license to
// guess Object or lower the conflict to Variant.
func (a *Analyzer) commonType(left, right Type) Type {
	if left.Kind() == KindUnknown {
		return left
	}
	if right.Kind() == KindUnknown {
		return right
	}
	if left.Equal(right) {
		return left
	}
	if isIntFloatPair(left, right) {
		return Builtin("float")
	}
	if left.Kind() == KindClass && right.Kind() == KindClass && !left.Meta() && !right.Meta() {
		return commonClassType(left, right)
	}
	return Variant()
}

func isIntFloatPair(left, right Type) bool {
	return left.Kind() == KindBuiltin && right.Kind() == KindBuiltin &&
		((left.Name() == "int" && right.Name() == "float") || (left.Name() == "float" && right.Name() == "int"))
}

func commonClassType(left, right Type) Type {
	leftChain, leftOK := completeClassChain(left)
	rightChain, rightOK := completeClassChain(right)
	if !leftOK || !rightOK {
		return Unknown("class ancestry is incomplete while finding a common type")
	}
	for _, leftCandidate := range leftChain {
		for _, rightCandidate := range rightChain {
			if leftCandidate.Name() == rightCandidate.Name() && leftCandidate.Equal(rightCandidate) {
				return leftCandidate
			}
		}
	}
	return Variant()
}

func completeClassChain(start Type) ([]Type, bool) {
	chain := []Type{}
	seen := map[*typeNode]bool{}
	current := start
	for {
		if current.Kind() != KindClass || current.Meta() || current.node == nil || seen[current.node] || validate(current) != "" {
			return nil, false
		}
		seen[current.node] = true
		chain = append(chain, current)
		base, hasBase := current.Base()
		if !hasBase {
			return chain, current.Name() == "Object"
		}
		if base.Kind() == KindUnknown {
			return nil, false
		}
		current = base
	}
}

func reductionTypeLabel(value Type) string {
	switch value.Kind() {
	case KindBuiltin, KindClass, KindEnum:
		return value.Name()
	case KindVariant:
		return "Variant"
	case KindVoid:
		return "void"
	case KindArray:
		return "Array"
	case KindDictionary:
		return "Dictionary"
	case KindCallable:
		return "Callable"
	case KindSignal:
		return "Signal"
	default:
		return "unknown receiver"
	}
}
