package semantic

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cafecito-games/gdparser/ast"
)

// MemberKind identifies one declaration in a class interface.
type MemberKind uint8

const (
	MemberVariable MemberKind = iota
	MemberConstant
	MemberMethod
	MemberSignal
	MemberEnum
	MemberEnumMember
	MemberClass
	MemberEngineMethod
	MemberEngineProperty
)

func (k MemberKind) String() string {
	switch k {
	case MemberVariable:
		return "variable"
	case MemberConstant:
		return "constant"
	case MemberMethod:
		return "method"
	case MemberSignal:
		return "signal"
	case MemberEnum:
		return "enum"
	case MemberEnumMember:
		return "enum-member"
	case MemberClass:
		return "class"
	case MemberEngineMethod:
		return "engine-method"
	case MemberEngineProperty:
		return "engine-property"
	default:
		return fmt.Sprintf("MemberKind(%d)", k)
	}
}

// Parameter is one resolved method or signal parameter. Type is Array[element]
// for a typed rest parameter and an untyped Array for an untyped rest
// parameter.
type Parameter struct {
	name       string
	typeValue  Type
	hasDefault bool
	variadic   bool
}

// Name returns the parameter name. Engine arguments do not carry names.
func (p Parameter) Name() string { return p.name }

// Type returns the parameter's resolved declaration type.
func (p Parameter) Type() Type { return p.typeValue }

// HasDefault reports whether the parameter declares a default expression.
func (p Parameter) HasDefault() bool { return p.hasDefault }

// Variadic reports whether this is a rest parameter.
func (p Parameter) Variadic() bool { return p.variadic }

// Member is one immutable user or engine declaration. It stores declaration
// metadata, while Type describes the value exposed under the member name. A
// callable member's return type is available through ReturnType.
type Member struct {
	name             string
	kind             MemberKind
	typeValue        Type
	returnType       Type
	hasReturnType    bool
	parameters       []Parameter
	constant         bool
	static           bool
	abstract         bool
	variadic         bool
	declaringClassID string
	engineOwner      string
	line             int
	column           int
}

// Name returns the member name.
func (m Member) Name() string { return m.name }

// Kind returns the member category.
func (m Member) Kind() MemberKind { return m.kind }

// Type returns the type exposed by this member. Methods expose Callable and
// signals expose Signal; inspect ReturnType and Parameters for signatures.
func (m Member) Type() Type { return m.typeValue }

// ReturnType returns a method's declared return type. The Boolean is false for
// non-method members; an omitted method annotation returns an Unknown with the
// Boolean true rather than an implicit void.
func (m Member) ReturnType() (Type, bool) { return m.returnType, m.hasReturnType }

// Parameters returns an owned copy of the member's parameter list.
func (m Member) Parameters() []Parameter { return append([]Parameter(nil), m.parameters...) }

// Const reports whether this declaration is a constant.
func (m Member) Const() bool { return m.constant }

// Static reports whether a method or variable is static.
func (m Member) Static() bool { return m.static }

// Abstract reports whether a method is abstract.
func (m Member) Abstract() bool { return m.abstract }

// Variadic reports whether a callable accepts a rest parameter.
func (m Member) Variadic() bool { return m.variadic }

// DeclaringClassID returns the stable #45 class ID for a user declaration. It
// is empty for an engine declaration.
func (m Member) DeclaringClassID() string { return m.declaringClassID }

// EngineOwner returns the engine class or builtin that directly declares an
// engine member. It is empty for a user declaration.
func (m Member) EngineOwner() string { return m.engineOwner }

// Line returns the user source line, or zero for engine declarations.
func (m Member) Line() int { return m.line }

// Column returns the user source column, or zero for engine declarations.
func (m Member) Column() int { return m.column }

// LookupState distinguishes a found name, a proven absence, and an answer
// blocked by incomplete or ambiguous semantic information.
type LookupState uint8

const (
	LookupUnknown LookupState = iota
	LookupFound
	LookupAbsent
)

func (s LookupState) String() string {
	switch s {
	case LookupFound:
		return "found"
	case LookupAbsent:
		return "absent"
	case LookupUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("LookupState(%d)", s)
	}
}

// LookupResult is an immutable tri-state member lookup result.
type LookupResult struct {
	state  LookupState
	member *Member
	reason string
}

// State returns whether the name was found, proven absent, or remains unknown.
func (r LookupResult) State() LookupState { return r.state }

// Reason explains an unknown lookup. Found and absent lookups have no reason.
func (r LookupResult) Reason() string { return r.reason }

// Member returns an owned member value for a found lookup.
func (r LookupResult) Member() (Member, bool) {
	if r.state != LookupFound || r.member == nil {
		return Member{}, false
	}
	return cloneMember(*r.member), true
}

// InterfaceOwner records one member-lookup owner in nearest-first order.
// Exactly one of ClassID and EngineOwner is non-empty.
type InterfaceOwner struct {
	classID     string
	engineOwner string
}

// ClassID returns the stable user class ID for this owner.
func (o InterfaceOwner) ClassID() string { return o.classID }

// EngineOwner returns the engine class name for this owner.
func (o InterfaceOwner) EngineOwner() string { return o.engineOwner }

// IsEngine reports whether this owner originates in the selected engine.
func (o InterfaceOwner) IsEngine() bool { return o.engineOwner != "" }

// ClassInterface is one immutable shallow user-class interface.
type ClassInterface struct {
	set          *InterfaceSet
	id           string
	typeValue    Type
	direct       []Member
	directByName map[string][]Member
	owners       []InterfaceOwner
	complete     bool
	cause        string
}

// ID returns the stable #45 user class ID.
func (c *ClassInterface) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}

// Type returns this class's instance type, including its project and engine
// base chain. An incomplete chain ends in a reasoned Unknown base.
func (c *ClassInterface) Type() Type {
	if c == nil {
		return Unknown("class interface is unavailable")
	}
	return c.typeValue
}

// DirectMembers returns direct declarations in deterministic source order.
func (c *ClassInterface) DirectMembers() []Member {
	if c == nil {
		return nil
	}
	result := make([]Member, len(c.direct))
	for at, member := range c.direct {
		result[at] = cloneMember(member)
	}
	return result
}

// Owners returns the complete nearest-first project-to-engine lookup chain.
// It is retained even when Complete is false so callers can use direct and
// already-proven ancestor declarations without treating a miss as absent.
func (c *ClassInterface) Owners() []InterfaceOwner {
	if c == nil {
		return nil
	}
	return append([]InterfaceOwner(nil), c.owners...)
}

// Complete reports whether every inheritance link terminated at engine Object.
func (c *ClassInterface) Complete() bool { return c != nil && c.complete }

// Cause explains why Complete is false.
func (c *ClassInterface) Cause() string {
	if c == nil {
		return "class interface is unavailable"
	}
	return c.cause
}

// Lookup resolves one member through the unified nearest-name namespace.
func (c *ClassInterface) Lookup(name string) LookupResult {
	if c == nil || c.set == nil {
		return unknownLookup("class interface is unavailable")
	}
	if strings.TrimSpace(name) == "" {
		return unknownLookup("member name is empty")
	}
	for _, owner := range c.owners {
		if owner.classID != "" {
			parent := c.set.classes[owner.classID]
			if parent == nil {
				return unknownLookup(fmt.Sprintf("class interface %q is unavailable", owner.classID))
			}
			members := parent.directByName[name]
			switch len(members) {
			case 0:
				continue
			case 1:
				member := cloneMember(members[0])
				return LookupResult{state: LookupFound, member: &member}
			default:
				return unknownLookup(fmt.Sprintf("member %q is declared more than once in class %q", name, owner.classID))
			}
		}

		method, hasMethod := c.set.engine.Method(owner.engineOwner, name)
		property, hasProperty := c.set.engine.Property(owner.engineOwner, name)
		if hasMethod && hasProperty {
			return unknownLookup(fmt.Sprintf("engine member %s.%s has conflicting direct method and property declarations", owner.engineOwner, name))
		}
		if hasMethod {
			member := engineMethodMember(method)
			return LookupResult{state: LookupFound, member: &member}
		}
		if hasProperty {
			member := enginePropertyMember(property)
			return LookupResult{state: LookupFound, member: &member}
		}
	}
	if c.complete {
		return LookupResult{state: LookupAbsent}
	}
	return unknownLookup(c.cause)
}

// InterfaceSet caches immutable shallow interfaces for one index/engine pair.
// Construction is eager, so concurrent readers observe a fully published set.
type InterfaceSet struct {
	index      *Index
	engine     *Engine
	classes    map[string]*ClassInterface
	classTypes map[string]Type
}

// BuildInterfaces composes a deterministic shallow interface for every class
// in index. It performs no I/O and does not mutate index, engine, or AST nodes.
func BuildInterfaces(index *Index, engine *Engine) *InterfaceSet {
	snapshot := interfaceIndexSnapshot(index)
	set := &InterfaceSet{
		index:      snapshot,
		engine:     engine,
		classes:    map[string]*ClassInterface{},
		classTypes: map[string]Type{},
	}
	if snapshot == nil {
		return set
	}

	states := map[string]interfaceVisit{}
	ids := snapshot.ClassIDs()
	for _, id := range ids {
		set.classType(id, states)
	}
	for _, id := range ids {
		class := snapshot.Classes[id]
		direct := set.directMembers(class)
		byName := make(map[string][]Member)
		for _, member := range direct {
			byName[member.name] = append(byName[member.name], cloneMember(member))
		}
		set.classes[id] = &ClassInterface{
			set:          set,
			id:           id,
			typeValue:    set.classTypes[id],
			direct:       direct,
			directByName: byName,
		}
	}
	for _, id := range ids {
		owners, complete, cause := set.lookupOwners(id)
		set.classes[id].owners = owners
		set.classes[id].complete = complete
		set.classes[id].cause = cause
	}
	return set
}

// interfaceIndexSnapshot detaches the composed interface from caller-owned
// maps and slices. AST header nodes remain shared because this pass only reads
// them while publishing immutable member facts.
func interfaceIndexSnapshot(source *Index) *Index {
	if source == nil {
		return nil
	}
	snapshot := &Index{
		Classes:             make(map[string]*ClassDecl, len(source.Classes)),
		TopLevel:            make(map[string]*ClassDecl, len(source.TopLevel)),
		DuplicateClassNames: make(map[string][]string, len(source.DuplicateClassNames)),
		Children:            make(map[string][]string, len(source.Children)),
		InCycle:             make(map[string]bool, len(source.InCycle)),
		ParseFailures:       append([]string(nil), source.ParseFailures...),
		byClassName:         make(map[string]*ClassDecl, len(source.byClassName)),
		autoloads:           cloneMap(source.autoloads),
	}
	for id, class := range source.Classes {
		snapshot.Classes[id] = cloneInterfaceClass(class)
	}
	for path, class := range source.TopLevel {
		if class == nil {
			continue
		}
		if cloned := snapshot.Classes[class.ID]; cloned != nil {
			snapshot.TopLevel[path] = cloned
			continue
		}
		snapshot.TopLevel[path] = cloneInterfaceClass(class)
	}
	for name, ids := range source.DuplicateClassNames {
		snapshot.DuplicateClassNames[name] = append([]string(nil), ids...)
	}
	for id, children := range source.Children {
		snapshot.Children[id] = append([]string(nil), children...)
	}
	for id, inCycle := range source.InCycle {
		snapshot.InCycle[id] = inCycle
	}
	for name, class := range source.byClassName {
		if class != nil {
			snapshot.byClassName[name] = snapshot.Classes[class.ID]
		}
	}
	return snapshot
}

func cloneInterfaceClass(source *ClassDecl) *ClassDecl {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Declarations = append([]Declaration(nil), source.Declarations...)
	cloned.PreloadAliases = append([]PreloadAlias(nil), source.PreloadAliases...)
	return &cloned
}

// Class returns a cached interface by stable #45 class ID.
func (s *InterfaceSet) Class(id string) (*ClassInterface, bool) {
	if s == nil {
		return nil, false
	}
	class, ok := s.classes[id]
	return class, ok
}

// ClassIDs returns cached class IDs in deterministic order.
func (s *InterfaceSet) ClassIDs() []string {
	if s == nil {
		return nil
	}
	return sortedMapKeys(s.classes)
}

// Lookup resolves name in the specified class interface.
func (s *InterfaceSet) Lookup(classID, name string) LookupResult {
	if s == nil || s.index == nil {
		return unknownLookup("class index is unavailable")
	}
	class, ok := s.Class(classID)
	if !ok {
		return unknownLookup(fmt.Sprintf("class %q is absent from the declaration index", classID))
	}
	return class.Lookup(name)
}

// ResolveType resolves one user-written annotation in classID's declaration
// context. A malformed, unsupported, ambiguous, or unavailable spelling is a
// reasoned Unknown that retains the original spelling.
func (s *InterfaceSet) ResolveType(classID, spelling string) Type {
	return s.resolveAnnotation(classID, spelling, "type")
}

type interfaceVisit uint8

const (
	interfaceResolving interfaceVisit = iota + 1
	interfaceResolved
)

func (s *InterfaceSet) classType(id string, states map[string]interfaceVisit) Type {
	if resolved, ok := s.classTypes[id]; ok {
		return resolved
	}
	if s.index == nil {
		return Unknown("class index is unavailable")
	}
	class := s.index.Classes[id]
	if class == nil {
		resolved := Unknown(fmt.Sprintf("user class %q is absent from the declaration index", id))
		s.classTypes[id] = resolved
		return resolved
	}
	if states[id] == interfaceResolving {
		cycle := Unknown(fmt.Sprintf("project inheritance cycle reaches class %q", id))
		return Class(id, &cycle, false)
	}
	states[id] = interfaceResolving
	base := s.classBaseType(class, states)
	resolved := Class(class.ID, &base, false)
	s.classTypes[id] = resolved
	states[id] = interfaceResolved
	return resolved
}

func (s *InterfaceSet) classBaseType(class *ClassDecl, states map[string]interfaceVisit) Type {
	if class == nil {
		return Unknown("class declaration is unavailable")
	}
	if s.index.InCycle[class.ID] {
		return Unknown(fmt.Sprintf("project inheritance cycle includes class %q", class.ID))
	}
	if class.UnresolvedBase {
		return Unknown(fmt.Sprintf("project base of class %q is unresolved: %s", class.ID, class.UnresolvedCause))
	}
	if class.ParentID != "" {
		return s.classType(class.ParentID, states)
	}
	if len(s.index.ParseFailures) > 0 {
		return Unknown(fmt.Sprintf("source parse failure at %q leaves project ancestry incomplete", s.index.ParseFailures[0]))
	}
	base := class.ExternalBase
	if base == "" {
		base = "RefCounted"
	}
	if s.engine == nil {
		return Unknown(fmt.Sprintf("engine schema is unavailable while resolving base %q of class %q", base, class.ID))
	}
	resolved := s.engine.Class(base)
	if resolved.Kind() == KindUnknown {
		return resolved
	}
	if _, complete, cause := s.engineOwners(base); !complete {
		return Unknown(cause)
	}
	return resolved
}

func (s *InterfaceSet) lookupOwners(id string) ([]InterfaceOwner, bool, string) {
	if s.index == nil {
		return nil, false, "class index is unavailable"
	}
	owners := []InterfaceOwner{}
	seen := map[string]bool{}
	for current := id; current != ""; {
		if seen[current] {
			return owners, false, fmt.Sprintf("project inheritance cycle reaches class %q", current)
		}
		seen[current] = true
		class := s.index.Classes[current]
		if class == nil {
			return owners, false, fmt.Sprintf("project class %q is absent from the declaration index", current)
		}
		owners = append(owners, InterfaceOwner{classID: class.ID})
		if s.index.InCycle[class.ID] {
			return owners, false, fmt.Sprintf("project inheritance cycle includes class %q", class.ID)
		}
		if class.UnresolvedBase {
			return owners, false, fmt.Sprintf("project base of class %q is unresolved: %s", class.ID, class.UnresolvedCause)
		}
		if class.ParentID != "" {
			current = class.ParentID
			continue
		}

		base := class.ExternalBase
		if base == "" {
			base = "RefCounted"
		}
		if len(s.index.ParseFailures) > 0 {
			return owners, false, fmt.Sprintf("source parse failure at %q leaves project ancestry incomplete", s.index.ParseFailures[0])
		}
		engineOwners, complete, cause := s.engineOwners(base)
		owners = append(owners, engineOwners...)
		if !complete {
			return owners, false, cause
		}
		return owners, true, ""
	}
	return owners, false, "class inheritance ended before an engine base"
}

func (s *InterfaceSet) engineOwners(name string) ([]InterfaceOwner, bool, string) {
	if s.engine == nil {
		return nil, false, fmt.Sprintf("engine schema is unavailable while resolving class %q", name)
	}
	owners := []InterfaceOwner{}
	seen := map[string]bool{}
	current := s.engine.Class(name)
	for {
		if current.Kind() == KindUnknown {
			return owners, false, current.Reason()
		}
		if current.Kind() != KindClass || current.Name() == "" {
			return owners, false, fmt.Sprintf("engine ancestry for %q contains non-class type %q", name, current.String())
		}
		owner := current.Name()
		if seen[owner] {
			return owners, false, fmt.Sprintf("engine inheritance cycle reaches class %q", owner)
		}
		seen[owner] = true
		owners = append(owners, InterfaceOwner{engineOwner: owner})
		base, hasBase := current.Base()
		if !hasBase {
			if owner == "Object" {
				return owners, true, ""
			}
			return owners, false, fmt.Sprintf("engine ancestry for %q terminates at %q instead of Object", name, owner)
		}
		current = base
	}
}

func (s *InterfaceSet) directMembers(class *ClassDecl) []Member {
	if class == nil {
		return nil
	}
	members := make([]Member, 0, len(class.Declarations))
	for _, declaration := range class.Declarations {
		members = append(members, s.member(class, declaration))
	}
	return members
}

func (s *InterfaceSet) member(class *ClassDecl, declaration Declaration) Member {
	member := Member{
		name:             declaration.Name,
		kind:             memberKind(declaration.Kind),
		declaringClassID: class.ID,
		line:             declaration.Line,
		column:           declaration.Column,
		typeValue:        Unknown(fmt.Sprintf("declaration %q has an unsupported header", declaration.Name)),
	}
	switch declaration.Kind {
	case DeclarationVariable, DeclarationConstant:
		node, ok := declaration.Node.(*ast.VariableDeclaration)
		if declaration.Kind == DeclarationConstant {
			member.constant = true
		}
		if !ok {
			return member
		}
		if node.Constant || declaration.Kind == DeclarationConstant {
			member.kind = MemberConstant
			member.constant = true
		}
		member.static = node.Static
		member.typeValue = s.variableType(class.ID, node)
		return member
	case DeclarationMethod:
		node, ok := declaration.Node.(*ast.FunctionDeclaration)
		if !ok {
			return member
		}
		member.typeValue = Callable()
		member.returnType, member.hasReturnType = s.methodReturnType(class.ID, node), true
		member.parameters = s.parameters(class.ID, node.Parameters, declaration.Name)
		member.static = node.Static
		member.abstract = node.Abstract
		member.variadic = containsVariadic(member.parameters)
		return member
	case DeclarationSignal:
		node, ok := declaration.Node.(*ast.SignalDeclaration)
		if !ok {
			return member
		}
		member.typeValue = Signal()
		member.parameters = s.parameters(class.ID, node.Parameters, declaration.Name)
		member.variadic = containsVariadic(member.parameters)
		return member
	case DeclarationEnum:
		member.typeValue = Enum(class.ID + "." + declaration.Name)
		return member
	case DeclarationEnumMember:
		member.typeValue = Builtin("int")
		member.constant = true
		return member
	case DeclarationClass:
		childID := class.ID + "#" + declaration.Name
		if s.index == nil || s.index.Classes[childID] == nil {
			member.typeValue = Unknown(fmt.Sprintf("inner class %q is absent from the declaration index", childID))
			return member
		}
		member.typeValue = Class(childID, nil, true)
		return member
	default:
		return member
	}
}

func memberKind(kind DeclarationKind) MemberKind {
	switch kind {
	case DeclarationVariable:
		return MemberVariable
	case DeclarationConstant:
		return MemberConstant
	case DeclarationMethod:
		return MemberMethod
	case DeclarationSignal:
		return MemberSignal
	case DeclarationEnum:
		return MemberEnum
	case DeclarationEnumMember:
		return MemberEnumMember
	case DeclarationClass:
		return MemberClass
	default:
		return MemberVariable
	}
}

func (s *InterfaceSet) variableType(classID string, node *ast.VariableDeclaration) Type {
	if node.Type != "" {
		return s.resolveAnnotation(classID, node.Type, fmt.Sprintf("declaration %q", node.Name))
	}
	if node.Constant {
		if resolved, ok := scalarLiteralType(node.Value); ok {
			return resolved
		}
		return Unknown(fmt.Sprintf("constant %q initializer requires expression reduction", node.Name))
	}
	if node.Value == nil {
		return Variant()
	}
	if node.Inferred {
		if resolved, ok := scalarLiteralType(node.Value); ok {
			return resolved
		}
		return Unknown(fmt.Sprintf("inferred declaration %q initializer requires expression reduction", node.Name))
	}
	if _, ok := scalarLiteralType(node.Value); ok {
		return Variant()
	}
	return Unknown(fmt.Sprintf("declaration %q initializer requires expression reduction", node.Name))
}

func (s *InterfaceSet) methodReturnType(classID string, node *ast.FunctionDeclaration) Type {
	if node.ReturnType == "" {
		return Unknown(fmt.Sprintf("return type not declared for method %q", node.Name))
	}
	return s.resolveReturnAnnotation(classID, node.ReturnType, fmt.Sprintf("return of method %q", node.Name))
}

func (s *InterfaceSet) parameters(classID string, parameters []ast.Parameter, owner string) []Parameter {
	result := make([]Parameter, len(parameters))
	for at, parameter := range parameters {
		result[at] = s.parameter(classID, parameter, owner)
	}
	return result
}

func (s *InterfaceSet) parameter(classID string, parameter ast.Parameter, owner string) Parameter {
	resolved := Variant()
	typed := false
	if parameter.Type != "" {
		resolved = s.resolveAnnotation(classID, parameter.Type, fmt.Sprintf("parameter %q of %q", parameter.Name, owner))
		typed = resolved.Kind() != KindUnknown
	} else if parameter.Inferred {
		if literal, ok := scalarLiteralType(parameter.Default); ok {
			resolved, typed = literal, true
		} else {
			resolved = Unknown(fmt.Sprintf("inferred parameter %q of %q default requires expression reduction", parameter.Name, owner))
		}
	}
	if parameter.Variadic && resolved.Kind() != KindUnknown {
		if typed {
			resolved = Array(&resolved)
		} else {
			resolved = Array(nil)
		}
	}
	return Parameter{name: parameter.Name, typeValue: resolved, hasDefault: parameter.Default != nil, variadic: parameter.Variadic}
}

func scalarLiteralType(expression ast.Expression) (Type, bool) {
	literal, ok := expression.(*ast.Literal)
	if !ok {
		return Type{}, false
	}
	switch literal.Kind {
	case ast.IntegerLiteral:
		return Builtin("int"), true
	case ast.FloatLiteral:
		return Builtin("float"), true
	case ast.StringLiteral:
		return Builtin("String"), true
	case ast.StringNameLiteral:
		return Builtin("StringName"), true
	case ast.NodePathLiteral:
		return Builtin("NodePath"), true
	case ast.BoolLiteral:
		return Builtin("bool"), true
	default:
		return Type{}, false
	}
}

func engineMethodMember(method EngineMethod) Member {
	arguments := method.Arguments()
	parameters := make([]Parameter, len(arguments))
	for at, argument := range arguments {
		parameters[at] = Parameter{typeValue: argument.Type(), hasDefault: argument.HasDefault()}
	}
	return Member{
		name:          method.Name(),
		kind:          MemberEngineMethod,
		typeValue:     Callable(),
		returnType:    method.ReturnType(),
		hasReturnType: true,
		parameters:    parameters,
		static:        method.Static(),
		variadic:      method.Vararg(),
		engineOwner:   method.Owner(),
	}
}

func enginePropertyMember(property EngineProperty) Member {
	return Member{
		name:        property.Name(),
		kind:        MemberEngineProperty,
		typeValue:   property.Type(),
		engineOwner: property.Owner(),
	}
}

func cloneMember(member Member) Member {
	member.parameters = append([]Parameter(nil), member.parameters...)
	return member
}

func containsVariadic(parameters []Parameter) bool {
	for _, parameter := range parameters {
		if parameter.variadic {
			return true
		}
	}
	return false
}

func unknownLookup(reason string) LookupResult {
	if strings.TrimSpace(reason) == "" {
		reason = "member lookup is incomplete"
	}
	return LookupResult{state: LookupUnknown, reason: reason}
}

func (s *InterfaceSet) resolveAnnotation(classID, spelling, label string) Type {
	return s.resolveAnnotationAt(classID, spelling, label, false)
}

func (s *InterfaceSet) resolveReturnAnnotation(classID, spelling, label string) Type {
	return s.resolveAnnotationAt(classID, spelling, label, true)
}

func (s *InterfaceSet) resolveAnnotationAt(classID, spelling, label string, allowVoid bool) Type {
	original := spelling
	parsed, err := parseAnnotation(spelling)
	if err != nil {
		return Unknown(fmt.Sprintf("%s annotation %q is malformed: %v", label, original, err))
	}
	resolved := s.resolveAnnotationNode(classID, parsed, allowVoid)
	if resolved.Kind() == KindUnknown {
		return Unknown(fmt.Sprintf("%s annotation %q is unknown: %s", label, original, resolved.Reason()))
	}
	return resolved
}

func (s *InterfaceSet) resolveAnnotationNode(classID string, annotation annotationNode, allowVoid bool) Type {
	if annotation.name == "void" {
		if allowVoid && len(annotation.arguments) == 0 {
			return Void()
		}
		return Unknown("void is only valid as a method return type")
	}
	switch annotation.name {
	case "Array":
		if available := s.containerType("Array", KindArray); available.Kind() == KindUnknown {
			return available
		}
		switch len(annotation.arguments) {
		case 0:
			return Array(nil)
		case 1:
			element := s.resolveAnnotationNode(classID, annotation.arguments[0], false)
			if element.Kind() == KindUnknown {
				return element
			}
			return Array(&element)
		default:
			return Unknown("Array requires exactly one element type")
		}
	case "Dictionary":
		if available := s.containerType("Dictionary", KindDictionary); available.Kind() == KindUnknown {
			return available
		}
		switch len(annotation.arguments) {
		case 0:
			return Dictionary(nil, nil)
		case 2:
			key := s.resolveAnnotationNode(classID, annotation.arguments[0], false)
			value := s.resolveAnnotationNode(classID, annotation.arguments[1], false)
			if key.Kind() == KindUnknown {
				return key
			}
			if value.Kind() == KindUnknown {
				return value
			}
			return Dictionary(&key, &value)
		default:
			return Unknown("Dictionary requires exactly two component types")
		}
	default:
		if len(annotation.arguments) != 0 {
			return Unknown(fmt.Sprintf("generic type %q is unsupported", annotation.name))
		}
		return s.resolveNamedType(classID, annotation.name)
	}
}

func (s *InterfaceSet) resolveNamedType(classID, name string) Type {
	switch name {
	case "Variant":
		return Variant()
	case "void":
		return Unknown("void is only valid as a method return type")
	}
	if resolved, state, reason := s.resolveUserType(classID, strings.Split(name, ".")); state != typeNotFound {
		switch state {
		case typeFound:
			return resolved
		case typeBlocked:
			if builtin, ok := s.intrinsicEngineType(name); ok {
				return builtin
			}
			return Unknown(reason)
		default:
			return Unknown(reason)
		}
	}
	if builtin, ok := s.intrinsicEngineType(name); ok {
		return builtin
	}
	if s.index != nil && len(s.index.ParseFailures) > 0 {
		return Unknown(fmt.Sprintf("source parse failure at %q prevents resolving type %q as an engine type", s.index.ParseFailures[0], name))
	}
	if s.engine == nil {
		return Unknown(fmt.Sprintf("engine schema is unavailable while resolving type %q", name))
	}
	if resolved := s.engine.ResolveType(name); resolved.Kind() != KindUnknown {
		return resolved
	}
	if owner, _, qualified := strings.Cut(name, "."); qualified {
		ownerType := s.engine.ResolveType(owner)
		if ownerType.Kind() == KindClass || ownerType.Kind() == KindBuiltin {
			// Engine intentionally retains only class ancestry, direct methods,
			// and direct properties. It has no enum-name table, so a dotted
			// engine spelling cannot be proved at this boundary. In particular,
			// do not turn a typo into a confident enum merely because its owner
			// happens to exist.
			return Unknown(fmt.Sprintf("engine enum annotation %q cannot be proven from selected engine schema", name))
		}
	}
	return s.engine.ResolveType(name)
}

func (s *InterfaceSet) intrinsicEngineType(name string) (Type, bool) {
	if s.engine == nil {
		return Type{}, false
	}
	resolved := s.engine.ResolveType(name)
	switch resolved.Kind() {
	case KindBuiltin, KindArray, KindDictionary, KindCallable, KindSignal:
		return resolved, true
	default:
		return Type{}, false
	}
}

func (s *InterfaceSet) containerType(name string, want Kind) Type {
	if s.engine == nil {
		return Unknown(fmt.Sprintf("engine schema is unavailable while resolving container %q", name))
	}
	resolved := s.engine.ResolveType(name)
	if resolved.Kind() == want {
		return resolved
	}
	if resolved.Kind() == KindUnknown {
		return resolved
	}
	return Unknown(fmt.Sprintf("engine type %q is not a %v container", name, want))
}

type typeResolution uint8

const (
	typeNotFound typeResolution = iota
	typeFound
	typeUnknown
	typeBlocked
)

func (s *InterfaceSet) resolveUserType(classID string, parts []string) (Type, typeResolution, string) {
	if s.index == nil || len(parts) == 0 {
		return Type{}, typeNotFound, ""
	}
	if classID != "" {
		if resolved, state, reason := s.resolveScopedUserType(classID, parts); state != typeNotFound {
			return resolved, state, reason
		}
	}
	first := parts[0]
	if duplicates := s.index.DuplicateClassNames[first]; len(duplicates) > 0 {
		return Type{}, typeUnknown, fmt.Sprintf("user type %q is ambiguous between %s", first, strings.Join(duplicates, ", "))
	}
	class := s.index.ClassByName(first)
	if class == nil {
		return Type{}, typeNotFound, ""
	}
	return s.followUserType(class, parts[1:])
}

func (s *InterfaceSet) resolveScopedUserType(classID string, parts []string) (Type, typeResolution, string) {
	for scope := classID; scope != ""; {
		seen := map[string]bool{}
		for current := scope; current != "" && !seen[current]; {
			seen[current] = true
			class := s.index.Classes[current]
			if class == nil {
				return Type{}, typeUnknown, fmt.Sprintf("user scope %q is absent from the declaration index", current)
			}
			if matched, resolved, state, reason := s.directUserType(class, parts); matched {
				return resolved, state, reason
			}
			if class.UnresolvedBase {
				cause := class.UnresolvedCause
				if cause == "" {
					cause = "unresolved project base"
				}
				return Type{}, typeBlocked, fmt.Sprintf("project base of class %q is unresolved: %s", class.ID, cause)
			}
			if s.index.InCycle[class.ID] {
				return Type{}, typeBlocked, fmt.Sprintf("project inheritance cycle includes class %q", class.ID)
			}
			if class.ParentID == "" {
				if cause := s.scopedTerminalCause(class); cause != "" {
					return Type{}, typeBlocked, cause
				}
			}
			current = class.ParentID
		}
		cut := strings.LastIndexByte(scope, '#')
		if cut < 0 {
			break
		}
		scope = scope[:cut]
	}
	return Type{}, typeNotFound, ""
}

// scopedTerminalCause reports whether terminal ancestry is incomplete. A
// scoped miss may reach global class names only after its user chain and
// terminal engine segment are complete.
func (s *InterfaceSet) scopedTerminalCause(class *ClassDecl) string {
	if s.index == nil {
		return "class index is unavailable"
	}
	if len(s.index.ParseFailures) > 0 {
		return fmt.Sprintf("source parse failure at %q leaves project ancestry incomplete", s.index.ParseFailures[0])
	}
	base := class.ExternalBase
	if base == "" {
		base = "RefCounted"
	}
	if _, complete, cause := s.engineOwners(base); !complete {
		return cause
	}
	return ""
}

func (s *InterfaceSet) directUserType(class *ClassDecl, parts []string) (bool, Type, typeResolution, string) {
	if class == nil || len(parts) == 0 {
		return false, Type{}, typeNotFound, ""
	}
	matches := declarationsNamed(class.Declarations, parts[0])
	aliases := []PreloadAlias{}
	for _, alias := range class.PreloadAliases {
		if alias.Name == parts[0] {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) > 1 {
		return true, Type{}, typeUnknown, fmt.Sprintf("preload alias %q is declared more than once in class %q", parts[0], class.ID)
	}
	if len(aliases) == 1 {
		if len(matches) > 1 || len(matches) == 1 && matches[0].Kind != DeclarationConstant {
			return true, Type{}, typeUnknown, fmt.Sprintf("preload alias %q conflicts with a direct declaration in class %q", parts[0], class.ID)
		}
		alias := aliases[0]
		if !alias.Resolved {
			return true, Type{}, typeUnknown, fmt.Sprintf("preload alias %q has unresolved target %q", alias.Name, alias.Target)
		}
		target := s.index.TopLevel[alias.ResolvedPath]
		if target == nil {
			return true, Type{}, typeUnknown, fmt.Sprintf("preload alias %q target %q is absent from the declaration index", alias.Name, alias.ResolvedPath)
		}
		resolved, state, reason := s.followUserType(target, parts[1:])
		return true, resolved, state, reason
	}
	if len(matches) > 1 {
		return true, Type{}, typeUnknown, fmt.Sprintf("type name %q is declared more than once in class %q", parts[0], class.ID)
	}
	if len(matches) == 1 {
		switch matches[0].Kind {
		case DeclarationClass:
			child := s.index.Classes[class.ID+"#"+parts[0]]
			if child == nil {
				return true, Type{}, typeUnknown, fmt.Sprintf("inner class %q is absent from the declaration index", class.ID+"#"+parts[0])
			}
			resolved, state, reason := s.followUserType(child, parts[1:])
			return true, resolved, state, reason
		case DeclarationEnum:
			if len(parts) == 1 {
				return true, Enum(class.ID + "." + parts[0]), typeFound, ""
			}
			return true, Type{}, typeUnknown, fmt.Sprintf("named enum %q has no nested type %q", parts[0], strings.Join(parts[1:], "."))
		default:
			return true, Type{}, typeUnknown, fmt.Sprintf("declaration %q in class %q is not a type", parts[0], class.ID)
		}
	}
	return false, Type{}, typeNotFound, ""
}

func (s *InterfaceSet) followUserType(class *ClassDecl, rest []string) (Type, typeResolution, string) {
	if class == nil {
		return Type{}, typeUnknown, "user class declaration is unavailable"
	}
	if len(rest) == 0 {
		resolved, ok := s.classTypes[class.ID]
		if !ok {
			return Type{}, typeUnknown, fmt.Sprintf("type for user class %q is unavailable", class.ID)
		}
		return resolved, typeFound, ""
	}
	current := class
	for at, segment := range rest {
		matches := declarationsNamed(current.Declarations, segment)
		if len(matches) != 1 {
			if len(matches) > 1 {
				return Type{}, typeUnknown, fmt.Sprintf("nested type %q is declared more than once in class %q", segment, current.ID)
			}
			return Type{}, typeUnknown, fmt.Sprintf("nested type %q is absent from class %q", segment, current.ID)
		}
		switch matches[0].Kind {
		case DeclarationClass:
			child := s.index.Classes[current.ID+"#"+segment]
			if child == nil {
				return Type{}, typeUnknown, fmt.Sprintf("inner class %q is absent from the declaration index", current.ID+"#"+segment)
			}
			current = child
		case DeclarationEnum:
			if at == len(rest)-1 {
				return Enum(current.ID + "." + segment), typeFound, ""
			}
			return Type{}, typeUnknown, fmt.Sprintf("named enum %q has no nested type %q", segment, rest[at+1])
		default:
			return Type{}, typeUnknown, fmt.Sprintf("declaration %q in class %q is not a type", segment, current.ID)
		}
	}
	resolved, ok := s.classTypes[current.ID]
	if !ok {
		return Type{}, typeUnknown, fmt.Sprintf("type for user class %q is unavailable", current.ID)
	}
	return resolved, typeFound, ""
}

func declarationsNamed(declarations []Declaration, name string) []Declaration {
	var result []Declaration
	for _, declaration := range declarations {
		if declaration.Name == name {
			result = append(result, declaration)
		}
	}
	return result
}

type annotationNode struct {
	name      string
	arguments []annotationNode
}

func parseAnnotation(source string) (annotationNode, error) {
	parser := annotationParser{source: source}
	parser.skipSpace()
	parsed, err := parser.node()
	if err != nil {
		return annotationNode{}, err
	}
	parser.skipSpace()
	if parser.pos != len(parser.source) {
		return annotationNode{}, fmt.Errorf("unexpected %q", parser.source[parser.pos:])
	}
	return parsed, nil
}

type annotationParser struct {
	source string
	pos    int
}

func (p *annotationParser) node() (annotationNode, error) {
	name, err := p.qualifiedName()
	if err != nil {
		return annotationNode{}, err
	}
	result := annotationNode{name: name}
	p.skipSpace()
	if !p.take('[') {
		return result, nil
	}
	p.skipSpace()
	if p.take(']') {
		return annotationNode{}, fmt.Errorf("empty type argument list for %q", name)
	}
	for {
		argument, err := p.node()
		if err != nil {
			return annotationNode{}, err
		}
		result.arguments = append(result.arguments, argument)
		p.skipSpace()
		if p.take(']') {
			return result, nil
		}
		if !p.take(',') {
			return annotationNode{}, fmt.Errorf("expected ',' or ']' after type argument to %q", name)
		}
		p.skipSpace()
	}
}

func (p *annotationParser) qualifiedName() (string, error) {
	part, err := p.identifier()
	if err != nil {
		return "", err
	}
	parts := []string{part}
	for {
		p.skipSpace()
		if !p.take('.') {
			break
		}
		p.skipSpace()
		part, err = p.identifier()
		if err != nil {
			return "", fmt.Errorf("qualified type name after '.': %w", err)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "."), nil
}

func (p *annotationParser) identifier() (string, error) {
	if p.pos >= len(p.source) {
		return "", fmt.Errorf("expected identifier")
	}
	start := p.pos
	r, size := utf8.DecodeRuneInString(p.source[p.pos:])
	if r == utf8.RuneError && size == 1 || !(r == '_' || unicode.IsLetter(r)) {
		return "", fmt.Errorf("expected identifier")
	}
	p.pos += size
	for p.pos < len(p.source) {
		r, size = utf8.DecodeRuneInString(p.source[p.pos:])
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			p.pos += size
			continue
		}
		break
	}
	return p.source[start:p.pos], nil
}

func (p *annotationParser) skipSpace() {
	for p.pos < len(p.source) {
		r, size := utf8.DecodeRuneInString(p.source[p.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		p.pos += size
	}
}

func (p *annotationParser) take(want byte) bool {
	if p.pos >= len(p.source) || p.source[p.pos] != want {
		return false
	}
	p.pos++
	return true
}
