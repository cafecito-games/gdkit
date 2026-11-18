package semantic

import (
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
)

// gdscriptBuiltinFunctions are Godot 4's @GDScript compiler-intrinsic utility
// functions. Unlike @GlobalScope utilities (such as print and str_to_var), they
// are implemented in the GDScript module rather than ClassDB and therefore do
// not appear in extension_api.json. load and preload are handled separately as
// BindingLanguageSpecial in ScopeIndex.
var gdscriptBuiltinFunctions = map[string]bool{
	"Color8":         true,
	"assert":         true,
	"char":           true,
	"convert":        true,
	"dict_to_inst":   true,
	"get_stack":      true,
	"inst_to_dict":   true,
	"is_instance_of": true,
	"len":            true,
	"ord":            true,
	"print_debug":    true,
	"print_stack":    true,
	"type_exists":    true,
}

// IdentifierSymbols resolves a bare identifier expression at its recorded
// lexical scope to the globally addressable symbol names it denotes. Lexical
// bindings, self, file-private classes, and unresolved or ambiguous bindings
// return nil.
func (a *Analyzer) IdentifierSymbols(identifier *ast.Identifier) []string {
	if a == nil || isNilNode(identifier) || a.scopes == nil || a.interfaces == nil {
		return nil
	}
	resolved := a.scopes.Resolve(identifier)
	if resolved.State() != LookupFound {
		if a.provenGDScriptBuiltin(identifier) {
			return []string{identifier.Name}
		}
		return nil
	}
	binding, ok := resolved.Binding()
	if !ok {
		return nil
	}
	switch binding.Kind() {
	case BindingLocal, BindingParameter, BindingFor, BindingMatch, BindingSetter, BindingSelf:
		return nil
	case BindingSuper:
		member, hasMember := binding.SuperMember()
		if !hasMember {
			return nil
		}
		scope, hasScope := a.scopes.ScopeAt(identifier)
		if !hasScope {
			return nil
		}
		class, hasClass := a.interfaces.Class(scope.ClassID())
		if !hasClass || len(class.owners) <= 1 {
			return a.memberChainSymbols(nil, member, scope.Static())
		}
		return a.memberChainSymbols(class.owners[1:], member, scope.Static())
	case BindingMember:
		member, hasMember := binding.Member()
		if !hasMember {
			return nil
		}
		if member.Kind() == MemberClass {
			return a.classGlobalNames(member.Type().Name())
		}
		scope, hasScope := a.scopes.ScopeAt(identifier)
		if !hasScope {
			return a.memberChainSymbols(nil, member, false)
		}
		class, hasClass := a.interfaces.Class(scope.ClassID())
		if !hasClass {
			return a.memberChainSymbols(nil, member, scope.Static())
		}
		return a.memberChainSymbols(class.owners, member, scope.Static())
	case BindingEnclosingConstant, BindingEnclosingType:
		member, hasMember := binding.Member()
		if !hasMember {
			return nil
		}
		if member.Kind() == MemberClass {
			return a.classGlobalNames(member.Type().Name())
		}
		return a.ownerPrefixedNames(member.DeclaringClassID(), member.EngineOwner(), member.Name())
	case BindingProjectClass:
		return a.classGlobalNames(binding.ClassID())
	case BindingAutoload:
		return appendUnique([]string{binding.Name()}, a.classGlobalNames(binding.ClassID())...)
	case BindingEngineType, BindingEngineSingleton, BindingEngineUtility, BindingLanguageSpecial:
		return []string{binding.Name()}
	default:
		return nil
	}
}

// MemberSymbols resolves a member access expression (Object.Property) to the
// globally addressable qualified symbol names it denotes.
func (a *Analyzer) MemberSymbols(expression *ast.MemberExpression) []string {
	if a == nil || isNilNode(expression) || a.scopes == nil || a.interfaces == nil {
		return nil
	}
	if identifier, ok := expression.Object.(*ast.Identifier); ok && identifier.Name == "super" {
		return a.IdentifierSymbols(identifier)
	}
	if objectResult, ok := a.reductionAt(expression.Object); ok && objectResult.hasNamedEnumObject {
		lookup := a.interfaces.lookupNamedEnumMember(objectResult.namedEnumObject, expression.Property)
		if lookup.state != LookupFound || objectResult.namedEnumObject == nil {
			return nil
		}
		suffix := objectResult.namedEnumObject.enumName + "." + expression.Property
		return a.ownerPrefixedNames(objectResult.namedEnumObject.declaringClassID, "", suffix)
	}
	if enumSymbols := a.scopedNamedEnumMemberSymbols(expression); len(enumSymbols) > 0 {
		return enumSymbols
	}
	receiver := a.symbolReceiverType(expression.Object)
	if receiver.Kind() == KindUnknown {
		if identifier, ok := expression.Object.(*ast.Identifier); ok {
			resolved := a.scopes.Resolve(identifier)
			if binding, found := resolved.Binding(); found && binding.Kind() == BindingEngineType {
				if builtin, isBuiltin := a.interfaces.intrinsicEngineType(identifier.Name); isBuiltin && builtin.Kind() != KindUnknown {
					return []string{identifier.Name + "." + expression.Property}
				}
			}
		}
		return nil
	}
	if receiver.Kind() == KindClass && receiver.Meta() {
		if expression.Property == "new" {
			constructed := a.reduceConstructorMember(receiver)
			if !constructed.hasConstructor {
				return nil
			}
			if _, userClass := a.interfaces.index.Classes[receiver.Name()]; userClass {
				return a.ownerPrefixedNames(receiver.Name(), "", "new")
			}
			return []string{receiver.Name() + ".new"}
		}
		resolved := a.interfaces.LookupMetaMember(receiver, expression.Property)
		switch resolved.State() {
		case LookupFound:
			member, ok := resolved.Member()
			if !ok {
				return nil
			}
			if member.Kind() == MemberClass {
				return a.classGlobalNames(member.Type().Name())
			}
			return a.metaMemberSymbols(receiver.Name(), member)
		case LookupUnknown:
			if _, userClass := a.interfaces.index.Classes[receiver.Name()]; !userClass && a.engine != nil {
				instance := a.engine.Class(receiver.Name())
				if instance.Kind() == KindClass && !instance.Meta() &&
					a.interfaces.LookupMember(instance, expression.Property).State() == LookupAbsent {
					return []string{receiver.Name() + "." + expression.Property}
				}
			}
			return nil
		default:
			return nil
		}
	}
	resolved := a.interfaces.LookupMember(receiver, expression.Property)
	if resolved.State() != LookupFound {
		return nil
	}
	member, ok := resolved.Member()
	if !ok {
		return nil
	}
	return a.instanceMemberSymbols(receiver, member)
}

// ResourceScriptSymbols returns the global class symbols of a script loaded by
// a static preload(...) or load(...) call, or nil when the call is not a
// resolved script resource load.
func (a *Analyzer) ResourceScriptSymbols(call *ast.CallExpression) []string {
	if a == nil || isNilNode(call) || a.scopes == nil || a.interfaces == nil {
		return nil
	}
	callee, ok := a.reductionAt(call.Callee)
	if !ok || (callee.special != "preload" && callee.special != "load") {
		return nil
	}
	resolved := a.TypeOf(call)
	if resolved.Kind() != KindClass || !resolved.Meta() {
		return nil
	}
	return a.classAndEnclosingGlobalNames(resolved.Name())
}

// ExtendsSymbols returns the global class symbols referenced by classID's
// declared base class, including enclosing global class prefixes for an inner
// base class.
func (a *Analyzer) ExtendsSymbols(classID string) []string {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil || classID == "" {
		return nil
	}
	class := a.interfaces.index.Classes[classID]
	if class == nil || class.UnresolvedBase {
		return nil
	}
	if class.ParentID != "" {
		return a.classAndEnclosingGlobalNames(class.ParentID)
	}
	if class.ExternalBase == "" || a.engine == nil {
		return nil
	}
	if a.engine.Class(class.ExternalBase).Kind() != KindClass {
		return nil
	}
	return []string{class.ExternalBase}
}

// AnnotationSymbols parses a GDScript type annotation in classID's scope and
// returns every globally addressable type symbol referenced within it.
func (a *Analyzer) AnnotationSymbols(classID, spelling string, allowVoid bool) []string {
	var out []string
	for _, group := range a.AnnotationSymbolGroups(classID, spelling, allowVoid) {
		out = appendUnique(out, group...)
	}
	return out
}

// AnnotationSymbolGroups parses a GDScript type annotation in classID's scope
// and returns one candidate-symbol slice per referenced type position (for
// example, Dictionary[K, V] yields separate groups for Dictionary, K, and V,
// while an inner class Outer.Inner yields one group ["Outer.Inner", "Outer"]).
func (a *Analyzer) AnnotationSymbolGroups(classID, spelling string, allowVoid bool) [][]string {
	if a == nil || a.interfaces == nil || strings.TrimSpace(spelling) == "" {
		return nil
	}
	parsed, err := parseAnnotation(spelling)
	if err != nil {
		return nil
	}
	var groups [][]string
	a.collectAnnotationSymbolGroups(classID, parsed, allowVoid, &groups)
	return groups
}

// ClassMemberSymbols resolves a member name directly in classID's interface
// (such as a property shorthand getter or setter) to its qualified symbol
// names.
func (a *Analyzer) ClassMemberSymbols(classID, name string) []string {
	if a == nil || a.interfaces == nil || classID == "" || strings.TrimSpace(name) == "" {
		return nil
	}
	class, ok := a.interfaces.Class(classID)
	if !ok {
		return nil
	}
	resolved := class.Lookup(name)
	if resolved.State() != LookupFound {
		return nil
	}
	member, ok := resolved.Member()
	if !ok {
		return nil
	}
	return a.memberChainSymbols(class.owners, member, false)
}

func (a *Analyzer) collectAnnotationSymbolGroups(classID string, node annotationNode, allowVoid bool, groups *[][]string) {
	if node.name == "void" {
		return
	}
	addGroup := func(symbols ...string) {
		group := appendUnique(nil, symbols...)
		if len(group) > 0 {
			*groups = append(*groups, group)
		}
	}
	switch node.name {
	case "Array":
		if a.interfaces.containerType("Array", KindArray).Kind() != KindUnknown {
			addGroup("Array")
		}
		if len(node.arguments) == 1 {
			a.collectAnnotationSymbolGroups(classID, node.arguments[0], false, groups)
		}
		return
	case "Dictionary":
		if a.interfaces.containerType("Dictionary", KindDictionary).Kind() != KindUnknown {
			addGroup("Dictionary")
		}
		if len(node.arguments) == 2 {
			a.collectAnnotationSymbolGroups(classID, node.arguments[0], false, groups)
			a.collectAnnotationSymbolGroups(classID, node.arguments[1], false, groups)
		}
		return
	}
	if len(node.arguments) != 0 || node.name == "Variant" {
		return
	}
	resolved := a.interfaces.resolveNamedType(classID, node.name)
	switch resolved.Kind() {
	case KindClass:
		if _, userClass := a.interfaces.index.Classes[resolved.Name()]; userClass {
			addGroup(a.classAndEnclosingGlobalNames(resolved.Name())...)
			return
		}
		addGroup(resolved.Name())
	case KindBuiltin:
		addGroup(resolved.Name())
	case KindCallable:
		addGroup("Callable")
	case KindSignal:
		addGroup("Signal")
	case KindEnum:
		if cut := strings.LastIndexByte(resolved.Name(), '.'); cut > 0 {
			ownerID := resolved.Name()[:cut]
			enumName := resolved.Name()[cut+1:]
			var group []string
			group = appendUnique(group, a.ownerPrefixedNames(ownerID, "", enumName)...)
			group = appendUnique(group, a.classAndEnclosingGlobalNames(ownerID)...)
			addGroup(group...)
		}
	case KindUnknown:
		if owner, _, qualified := strings.Cut(node.name, "."); qualified {
			ownerResolved := a.interfaces.resolveNamedType(classID, owner)
			if ownerResolved.Kind() == KindClass {
				if _, userClass := a.interfaces.index.Classes[ownerResolved.Name()]; !userClass {
					addGroup(ownerResolved.Name())
				}
			} else if ownerResolved.Kind() == KindBuiltin {
				addGroup(ownerResolved.Name())
			}
		}
	}
}

func (a *Analyzer) scopedNamedEnumMemberSymbols(expression *ast.MemberExpression) []string {
	identifier, ok := expression.Object.(*ast.Identifier)
	if !ok || identifier == nil {
		return nil
	}
	resolved := a.scopes.Resolve(identifier)
	binding, found := resolved.Binding()
	if !found || (binding.Kind() != BindingMember && binding.Kind() != BindingEnclosingType) {
		return nil
	}
	member, hasMember := binding.Member()
	if !hasMember || member.Kind() != MemberEnum || member.DeclaringClassID() == "" {
		return nil
	}
	provenance := a.interfaces.namedEnumObject(Class(member.DeclaringClassID(), nil, true), member)
	if a.interfaces.lookupNamedEnumMember(provenance, expression.Property).state != LookupFound {
		return nil
	}
	return a.ownerPrefixedNames(member.DeclaringClassID(), "", member.Name()+"."+expression.Property)
}

func (a *Analyzer) reductionAt(expression ast.Expression) (reductionResult, bool) {
	if a == nil || a.scopes == nil || isNilNode(expression) {
		return reductionResult{}, false
	}
	scope, ok := a.scopes.ScopeAt(expression)
	if !ok {
		return reductionResult{}, false
	}
	token := reductionContextToken{}
	if a.narrow != nil {
		token = a.narrow.tokenAt(expression)
	}
	return a.typeOfIn(expression, scope, token), true
}

func (a *Analyzer) symbolReceiverType(expression ast.Expression) Type {
	receiver := a.memberReceiverType(expression)
	if receiver.Kind() != KindUnknown {
		return receiver
	}
	call, ok := expression.(*ast.CallExpression)
	if !ok {
		return receiver
	}
	calleeResult, ok := a.reductionAt(call.Callee)
	if !ok {
		return receiver
	}
	if calleeResult.hasConstructor && calleeResult.builtinConstructor != nil {
		constructed, problem := a.validateBuiltinConstructorCapability(calleeResult.builtinConstructor, calleeResult.constructor)
		if problem == "" {
			return constructed
		}
	}
	if calleeResult.member != nil {
		if returned, hasReturn := calleeResult.member.ReturnType(); hasReturn &&
			returned.Kind() != KindUnknown && returned.Kind() != KindVoid {
			return returned
		}
	}
	return receiver
}

func (a *Analyzer) instanceMemberSymbols(receiver Type, member Member) []string {
	switch receiver.Kind() {
	case KindClass:
		if _, userClass := a.interfaces.index.Classes[receiver.Name()]; userClass {
			class, ok := a.interfaces.Class(receiver.Name())
			if !ok {
				return a.ownerPrefixedNames(member.DeclaringClassID(), member.EngineOwner(), member.Name())
			}
			return a.memberChainSymbols(class.owners, member, false)
		}
		owners, _, _ := a.interfaces.engineOwners(receiver.Name())
		return a.memberChainSymbols(owners, member, false)
	case KindBuiltin, KindArray, KindDictionary, KindCallable, KindSignal:
		if member.EngineOwner() != "" {
			return []string{member.EngineOwner() + "." + member.Name()}
		}
	}
	return nil
}

func (a *Analyzer) metaMemberSymbols(ownerName string, member Member) []string {
	if _, userClass := a.interfaces.index.Classes[ownerName]; userClass {
		class, ok := a.interfaces.Class(ownerName)
		if !ok {
			return a.ownerPrefixedNames(member.DeclaringClassID(), member.EngineOwner(), member.Name())
		}
		return a.memberChainSymbols(class.owners, member, true)
	}
	owners, _, _ := a.interfaces.engineOwners(ownerName)
	return a.memberChainSymbols(owners, member, true)
}

func (a *Analyzer) memberChainSymbols(owners []InterfaceOwner, member Member, static bool) []string {
	out := a.ownerPrefixedNames(member.DeclaringClassID(), member.EngineOwner(), member.Name())
	lastDeclaring := -1
	for index, owner := range owners {
		if static {
			if a.ownerDeclaresMetaMember(owner, member.Name()) {
				lastDeclaring = index
			}
		} else if a.ownerDeclaresMember(owner, member.Name()) {
			lastDeclaring = index
		}
	}
	for index := 0; index <= lastDeclaring; index++ {
		owner := owners[index]
		out = appendUnique(out, a.ownerPrefixedNames(owner.classID, owner.engineOwner, member.Name())...)
	}
	return out
}

func (a *Analyzer) ownerDeclaresMember(owner InterfaceOwner, name string) bool {
	if owner.classID != "" {
		class := a.interfaces.classes[owner.classID]
		return class != nil && len(class.directByName[name]) == 1
	}
	if owner.engineOwner != "" {
		return a.interfaces.lookupDirectEngineMember(owner.engineOwner, name).State() == LookupFound
	}
	return false
}

func (a *Analyzer) ownerDeclaresMetaMember(owner InterfaceOwner, name string) bool {
	if owner.classID != "" {
		class := a.interfaces.classes[owner.classID]
		if class == nil || len(class.directByName[name]) != 1 {
			return false
		}
		return metaAccessibleMember(class.directByName[name][0])
	}
	if owner.engineOwner != "" {
		result := a.interfaces.lookupDirectEngineMember(owner.engineOwner, name)
		member, ok := result.Member()
		return ok && member.Kind() == MemberEngineMethod && member.Static()
	}
	return false
}

func (a *Analyzer) ownerPrefixedNames(classID, engineOwner, suffix string) []string {
	var out []string
	if classID != "" {
		for _, prefix := range a.classGlobalNames(classID) {
			out = appendUnique(out, prefix+"."+suffix)
		}
	}
	if engineOwner != "" {
		out = appendUnique(out, engineOwner+"."+suffix)
	}
	return out
}

func (a *Analyzer) classAndEnclosingGlobalNames(classID string) []string {
	var out []string
	for current := classID; current != ""; {
		out = appendUnique(out, a.classGlobalNames(current)...)
		cut := strings.LastIndexByte(current, '#')
		if cut < 0 {
			break
		}
		current = current[:cut]
	}
	return out
}

func (a *Analyzer) classGlobalNames(classID string) []string {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil || classID == "" {
		return nil
	}
	index := a.interfaces.index
	if index.Classes[classID] == nil {
		return nil
	}
	parts := strings.Split(classID, "#")
	topID := parts[0]
	top := index.Classes[topID]
	if top == nil || top.Inner {
		return nil
	}
	suffix := ""
	if len(parts) > 1 {
		suffix = "." + strings.Join(parts[1:], ".")
	}
	var out []string
	if top.HasClassName && index.byClassName[top.Name] == top {
		if _, collidesWithAutoload := index.autoloads[top.Name]; !collidesWithAutoload {
			out = appendUnique(out, top.Name+suffix)
		}
	}
	var autoloadNames []string
	for name, path := range index.autoloads {
		if path == topID && index.ClassByName(name) == nil && len(index.DuplicateClassNames[name]) == 0 {
			autoloadNames = append(autoloadNames, name)
		}
	}
	sort.Strings(autoloadNames)
	for _, name := range autoloadNames {
		out = appendUnique(out, name+suffix)
	}
	return out
}

func (a *Analyzer) provenGDScriptBuiltin(identifier *ast.Identifier) bool {
	if identifier == nil || !gdscriptBuiltinFunctions[identifier.Name] {
		return false
	}
	if a == nil || a.scopes == nil || a.interfaces == nil || a.interfaces.index == nil || a.engine == nil {
		return false
	}
	scope, ok := a.scopes.ScopeAt(identifier)
	if !ok || scope == nil {
		return false
	}
	for current := scope; current != nil; current = current.parent {
		if current.blocked != "" {
			return false
		}
		if _, exists := current.bindings[identifier.Name]; exists {
			return false
		}
	}
	if a.interfaces.Lookup(scope.ClassID(), identifier.Name).State() != LookupAbsent {
		return false
	}
	outers, problem := a.scopes.enclosingClassIDs(scope.ClassID())
	if problem != "" {
		return false
	}
	for _, outer := range outers {
		if a.interfaces.Lookup(outer, identifier.Name).State() != LookupAbsent {
			return false
		}
	}
	index := a.interfaces.index
	if len(index.ParseFailures) > 0 || len(index.DuplicateClassNames[identifier.Name]) > 0 {
		return false
	}
	if index.ClassByName(identifier.Name) != nil {
		return false
	}
	if _, hasAutoload := index.autoloads[identifier.Name]; hasAutoload {
		return false
	}
	if a.engine.ResolveType(identifier.Name).Kind() != KindUnknown {
		return false
	}
	if _, hasSingleton := a.engine.Singleton(identifier.Name); hasSingleton {
		return false
	}
	if _, hasUtility := a.engine.Utility(identifier.Name); hasUtility {
		return false
	}
	return true
}

func appendUnique(list []string, values ...string) []string {
	for _, value := range values {
		if value == "" {
			continue
		}
		exists := false
		for _, existing := range list {
			if existing == value {
				exists = true
				break
			}
		}
		if !exists {
			list = append(list, value)
		}
	}
	return list
}
