package semantic

import (
	"fmt"
	"reflect"
	"strings"
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
	narrow     *narrowIndex
	engine     *Engine
	resources  ResourceResolver

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
// a declared callable signature or a direct meta-class constructor. TypeOf
// never exposes this extra information.
type reductionResult struct {
	typeValue      Type
	member         *Member
	special        string
	constructor    Type
	hasConstructor bool
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
	if resources, ok := source.(ResourceResolver); ok && !sourceUnavailable(resources) {
		analyzer.resources = resources
	}
	index := BuildIndex(source)
	analyzer.interfaces = BuildInterfaces(index, engine)
	analyzer.scopes = BuildScopes(analyzer.interfaces)
	analyzer.narrow = newNarrowIndex(analyzer.interfaces, analyzer.scopes)
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
	token := reductionContextToken{}
	if a.narrow != nil {
		token = a.narrow.tokenAt(expression)
	}
	return a.typeOfIn(expression, scope, token).typeValue
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

func sourceUnavailable(source any) bool {
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
		return unknownReduction(fmt.Sprintf("enum type value %q is unavailable through ordinary instance member lookup", member.Name()))
	}
	copy := cloneMember(member)
	return reductionResult{typeValue: copy.Type(), member: &copy}
}

// metaMemberReduction preserves only values that are usable through a class
// object. A named enum declaration is itself a Dictionary object; its Enum
// type describes an enum member value, so publishing it here would make
// Actor.Mode look like an int-compatible enum value rather than its runtime
// Dictionary and contaminate collection inference.
func metaMemberReduction(member Member) reductionResult {
	if member.Kind() == MemberEnum {
		return knownReduction(Dictionary(nil, nil))
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
	token := parent.token
	if a.narrow != nil {
		token = a.narrow.childToken(expression, parent.token)
	}
	return a.reduce(expression, reductionContext{scope: scope, token: token}, request)
}

// reduceDeferredChild evaluates a retained declaration/default under its own
// recorded lexical scope and the immutable base view. A future flow overlay is
// attached to a use, never to a declaration written elsewhere.
func (a *Analyzer) reduceDeferredChild(expression ast.Expression, request *reductionRequest) reductionResult {
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
	return a.reduce(expression, reductionContext{scope: scope}, request)
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
		return a.reduceLambda(node, request)
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
		} else {
			result = a.reduceDeferredBinding(binding, result, request)
		}
		if a.narrow != nil && narrowableBinding(binding) {
			if typeValue, state, found := a.narrow.lookup(context.token.overlay, binding.ID()); found && state == narrowPositive {
				// A known base type may already be more specific than a wider
				// runtime guard. Preserve its precision, but retain only Type so a
				// flow fact cannot carry #49 resource/meta callable provenance.
				if narrowableType(result.typeValue) && result.typeValue.AssignableTo(typeValue) == AssignabilityYes {
					return knownReduction(result.typeValue)
				}
				return knownReduction(typeValue)
			}
		}
		return result
	default:
		return unknownReduction(fmt.Sprintf("identifier %q resolution returned invalid state", identifier.Name))
	}
}

func (a *Analyzer) reduceDeferredBinding(binding Binding, prior reductionResult, request *reductionRequest) reductionResult {
	declaration := binding.Declaration()
	switch node := declaration.(type) {
	case *ast.VariableDeclaration:
		return a.reduceDeferredVariable(node, prior, request)
	case *ast.FunctionDeclaration:
		return a.reduceInferredParameter(node.Parameters, binding, prior, request)
	case *ast.LambdaExpression:
		return a.reduceInferredParameter(node.Parameters, binding, prior, request)
	default:
		return prior
	}
}

func (a *Analyzer) reduceDeferredVariable(node *ast.VariableDeclaration, prior reductionResult, request *reductionRequest) reductionResult {
	if node == nil {
		return prior
	}
	if (node.Constant || node.Inferred) && node.Type == "" {
		if isNilNode(node.Value) {
			return prior
		}
		return knownReduction(a.reduceDeferredChild(node.Value, request).typeValue)
	}
	if node.Value != nil && node.Type == "" {
		return knownReduction(Variant())
	}
	return prior
}

func (a *Analyzer) reduceInferredParameter(parameters []ast.Parameter, binding Binding, prior reductionResult, request *reductionRequest) reductionResult {
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
	return knownReduction(a.reduceDeferredChild(parameter.Default, request).typeValue)
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
	if expression.Operator == "as" || expression.Operator == "is" || expression.Operator == "is not" {
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
	case "not in":
		return "in"
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
	if callee.hasConstructor {
		return knownReduction(callee.constructor)
	}
	if callee.special == "preload" || callee.special == "load" {
		return a.reduceResourceCall(expression, context, callee.special)
	}
	if callee.typeValue.Kind() == KindVariant {
		return knownReduction(Variant())
	}
	if callee.typeValue.Meta() {
		return unknownReduction("direct meta-class call is not a supported constructor")
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

// reduceResourceCall consumes the dedicated language-special provenance from
// Scope. No spelling-shaped ordinary call reaches this path: a local binding,
// member (including ResourceLoader.load), alias, or unknown callee is reduced
// through the normal callable rules above.
func (a *Analyzer) reduceResourceCall(expression *ast.CallExpression, context reductionContext, special string) reductionResult {
	if expression == nil {
		return unknownReduction("resource call expression is unavailable")
	}
	if len(expression.Arguments) != 1 {
		return unknownReduction(fmt.Sprintf("%s requires exactly one string literal argument", special))
	}
	target, err := decodeResourceLiteral(expression.Arguments[0])
	if err != nil {
		return unknownReduction(fmt.Sprintf("%s resource target is unavailable: %v", special, err))
	}
	if a == nil || a.resources == nil || sourceUnavailable(a.resources) {
		return unknownReduction(fmt.Sprintf("%s resource resolver is unavailable", special))
	}
	from, problem := a.resourceSourcePath(context)
	if problem != "" {
		return unknownReduction(problem)
	}
	var resolution ResourceResolution
	switch special {
	case "preload":
		resolution = a.resources.ResolvePreloadResource(from, target)
	case "load":
		resolution = a.resources.ResolveLoadResource(from, target)
	default:
		return unknownReduction(fmt.Sprintf("unsupported resource language special %q", special))
	}
	if resolution.Requested() != target {
		return unknownReduction("resource resolver returned a result for a different requested spelling")
	}
	if want := resourceTargetProvenance(target); resolution.Provenance() != want {
		return unknownReduction(fmt.Sprintf("resource resolver returned provenance %s for %s target", resolution.Provenance(), want))
	}
	if problem := resourceResolutionProblem(resolution); problem != "" {
		return unknownReduction("resource resolver returned an invalid result: " + problem)
	}
	if resolution.State() != ResourceFound {
		return unknownReduction(resolution.Reason())
	}
	return a.reduceFoundResource(resolution)
}

func resourceTargetProvenance(target string) ResourceProvenance {
	if strings.HasPrefix(target, "uid://") {
		return ResourceUIDClaim
	}
	return ResourceLiteralPath
}

// resourceSourcePath derives the loader's canonical source path solely from
// the immutable interface index. It never asks a SourceSet to re-resolve or
// read a path after NewAnalyzer has published its snapshot.
func (a *Analyzer) resourceSourcePath(context reductionContext) (string, string) {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil {
		return "", "interface set is unavailable while resolving a resource source"
	}
	if context.scope == nil {
		return "", "resource call has no lexical source scope"
	}
	classID := context.scope.ClassID()
	class := a.interfaces.index.Classes[classID]
	if class == nil || !validResourcePath(class.Path) {
		return "", "resource call source is absent from the immutable script index"
	}
	return class.Path, ""
}

func (a *Analyzer) reduceFoundResource(resolution ResourceResolution) reductionResult {
	if a == nil {
		return unknownReduction("semantic analyzer is unavailable")
	}
	switch resolution.Kind() {
	case ResourceScript:
		if a.interfaces == nil || a.interfaces.index == nil {
			return unknownReduction("interface set is unavailable while resolving a script resource")
		}
		class := a.interfaces.index.TopLevel[resolution.Path()]
		if class == nil {
			return unknownReduction(fmt.Sprintf("script resource %q has no parsed top-level declaration", resolution.Path()))
		}
		if _, ok := a.interfaces.Class(class.ID); !ok {
			return unknownReduction(fmt.Sprintf("script resource %q has no published class interface", resolution.Path()))
		}
		return knownReduction(Class(class.ID, nil, true))
	case ResourceScene:
		return a.engineResourceClass("PackedScene")
	case ResourceText:
		return a.engineResourceClass("Resource")
	default:
		return unknownReduction(fmt.Sprintf("resource resolver found unsupported resource kind %s", resolution.Kind()))
	}
}

func (a *Analyzer) engineResourceClass(name string) reductionResult {
	if a == nil || a.engine == nil {
		return unknownReduction(fmt.Sprintf("engine schema is unavailable while resolving resource class %q", name))
	}
	resolved := a.engine.Class(name)
	if resolved.Kind() == KindUnknown {
		return knownReduction(resolved)
	}
	if resolved.Kind() != KindClass || resolved.Meta() {
		return unknownReduction(fmt.Sprintf("engine resource class %q is not an instance class", name))
	}
	return knownReduction(resolved)
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
		if expression.Property == "new" {
			return a.reduceConstructorMember(receiver.typeValue)
		}
		if a.interfaces == nil {
			return unknownReduction("interface set is unavailable while resolving meta member")
		}
		resolved := a.interfaces.LookupMetaMember(receiver.typeValue, expression.Property)
		result := reduceMetaMemberLookup(receiver.typeValue, expression.Property, resolved)
		if result.typeValue.Kind() != KindUnknown || result.member == nil {
			return result
		}
		if result.member.Kind() != MemberVariable && result.member.Kind() != MemberConstant {
			return result
		}
		return a.reduceDeferredMember(*result.member, result, request)
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
	return a.reduceDeferredMember(*result.member, result, request)
}

// reduceConstructorMember recognizes the engine-private .new member only on
// an already-resolved class object. It never treats a direct ClassMeta() call
// as construction, and an incomplete user inheritance chain remains a valid
// instance answer because the class interface owns that exact Type.
func (a *Analyzer) reduceConstructorMember(receiver Type) reductionResult {
	if receiver.Kind() != KindClass || !receiver.Meta() {
		return unknownReduction("constructor receiver is not a meta class")
	}
	if a == nil || a.interfaces == nil {
		return unknownReduction("interface set is unavailable while resolving a constructor")
	}
	if class, ok := a.interfaces.Class(receiver.Name()); ok {
		instance := class.Type()
		if instance.Kind() != KindClass || instance.Meta() {
			return unknownReduction(fmt.Sprintf("user class %q has no instance constructor type", receiver.Name()))
		}
		return reductionResult{typeValue: Callable(), constructor: instance, hasConstructor: true}
	}
	if a.engine == nil {
		return unknownReduction(fmt.Sprintf("engine schema is unavailable while resolving constructor %q", receiver.Name()))
	}
	instance := a.engine.Class(receiver.Name())
	if instance.Kind() == KindUnknown {
		return knownReduction(instance)
	}
	if instance.Kind() != KindClass || instance.Meta() {
		return unknownReduction(fmt.Sprintf("engine class %q has no instance constructor type", receiver.Name()))
	}
	return reductionResult{typeValue: Callable(), constructor: instance, hasConstructor: true}
}

func (a *Analyzer) reduceDeferredMember(member Member, prior reductionResult, request *reductionRequest) reductionResult {
	if a == nil || a.scopes == nil {
		return prior
	}
	declaration, ok := a.scopes.memberDeclaration(member).(*ast.VariableDeclaration)
	if !ok {
		return prior
	}
	return a.reduceDeferredVariable(declaration, prior, request)
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

// reduceMetaMemberLookup is the corresponding tri-state boundary for static
// meta-class selection. A selected named enum is reduced as its Dictionary
// object while ordinary receiver lookup remains conservative.
func reduceMetaMemberLookup(receiver Type, name string, resolved LookupResult) reductionResult {
	switch resolved.State() {
	case LookupFound:
		member, ok := resolved.Member()
		if !ok {
			return unknownReduction(fmt.Sprintf("meta member lookup for %q returned no member", name))
		}
		return metaMemberReduction(member)
	case LookupUnknown:
		return unknownReduction(resolved.Reason())
	case LookupAbsent:
		return unknownReduction(fmt.Sprintf("meta member %q is absent from %s", name, reductionTypeLabel(receiver)))
	default:
		return unknownReduction(fmt.Sprintf("meta member lookup for %q returned invalid state", name))
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

func (a *Analyzer) reduceLambda(expression *ast.LambdaExpression, request *reductionRequest) reductionResult {
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
		_ = a.reduceDeferredChild(parameter.Default, request)
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
