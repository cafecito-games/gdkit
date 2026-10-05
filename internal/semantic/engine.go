package semantic

import (
	"fmt"
	"sort"
	"strings"
)

// Engine is an immutable index of the classes and callable surface exposed by
// one Godot engine schema. Its maps and slices are private so concurrent rule
// execution can only observe the complete index published by Build.
type Engine struct {
	builtins   map[string]bool
	classes    map[string]Type
	methods    map[engineMemberKey]EngineMethod
	properties map[engineMemberKey]EngineProperty
	operators  map[engineOperatorKey]Type
	singletons map[string]Type
	utilities  map[string]EngineMethod
}

type engineMemberKey struct {
	owner string
	name  string
}

type engineOperatorKey struct {
	left     string
	operator string
	right    string
}

// EngineArgumentSpec is the serialized spelling and default-presence bit used
// while constructing an Engine. Build resolves Type through the complete set
// of classes and builtins, after every declaration has been added.
type EngineArgumentSpec struct {
	Type       string
	HasDefault bool
}

// EngineArgument is one immutable resolved callable argument.
type EngineArgument struct {
	typeValue  Type
	hasDefault bool
}

// Type returns the argument's resolved type.
func (a EngineArgument) Type() Type { return a.typeValue }

// HasDefault reports whether the engine declaration supplies a default.
func (a EngineArgument) HasDefault() bool { return a.hasDefault }

// EngineMethod is one resolved engine method or utility function. Utilities
// have an empty Owner; engine class and builtin methods always retain theirs.
type EngineMethod struct {
	owner      string
	name       string
	returnType Type
	arguments  []EngineArgument
	static     bool
	vararg     bool
}

// Owner returns the declaring engine class or builtin. It is empty for a
// utility function.
func (m EngineMethod) Owner() string { return m.owner }

// Name returns the method or utility-function name.
func (m EngineMethod) Name() string { return m.name }

// ReturnType returns the resolved return type.
func (m EngineMethod) ReturnType() Type { return m.returnType }

// Arguments returns a copy so a caller cannot mutate the published index.
func (m EngineMethod) Arguments() []EngineArgument {
	return append([]EngineArgument(nil), m.arguments...)
}

// Static reports whether this is a static engine method.
func (m EngineMethod) Static() bool { return m.static }

// Vararg reports whether the callable accepts additional arguments.
func (m EngineMethod) Vararg() bool { return m.vararg }

// EngineProperty is one resolved engine class or builtin property.
type EngineProperty struct {
	owner     string
	name      string
	typeValue Type
}

// Owner returns the declaring engine class or builtin.
func (p EngineProperty) Owner() string { return p.owner }

// Name returns the property name.
func (p EngineProperty) Name() string { return p.name }

// Type returns the property's resolved type.
func (p EngineProperty) Type() Type { return p.typeValue }

// Class returns a resolved engine class including its complete recorded base
// chain. A miss is reasoned Unknown, never Variant.
func (e *Engine) Class(name string) Type {
	if e == nil {
		return Unknown("engine schema is unavailable")
	}
	if resolved, ok := e.classes[name]; ok {
		return resolved
	}
	return Unknown(fmt.Sprintf("engine class %q is absent from schema", name))
}

// ResolveType resolves the spelling used by extension_api.json through this
// engine's complete class and builtin vocabulary.
func (e *Engine) ResolveType(spelling string) Type {
	if e == nil {
		return Unknown("engine schema is unavailable")
	}
	return resolveEngineType(spelling, e.builtins, e.classes)
}

// Method returns the method declared directly by owner. Inherited lookup is a
// consumer policy because user declarations may shadow engine members.
func (e *Engine) Method(owner, name string) (EngineMethod, bool) {
	if e == nil {
		return EngineMethod{}, false
	}
	method, ok := e.methods[engineMemberKey{owner: owner, name: name}]
	return method, ok
}

// Property returns the property declared directly by owner.
func (e *Engine) Property(owner, name string) (EngineProperty, bool) {
	if e == nil {
		return EngineProperty{}, false
	}
	property, ok := e.properties[engineMemberKey{owner: owner, name: name}]
	return property, ok
}

// Operator returns one exact left/operator/right result. An empty right name
// addresses a unary row; binary operators retain all three identity fields.
func (e *Engine) Operator(left, operator, right string) (Type, bool) {
	if e == nil {
		return Type{}, false
	}
	result, ok := e.operators[engineOperatorKey{left: left, operator: operator, right: right}]
	return result, ok
}

// Singleton returns the resolved type of a named engine singleton.
func (e *Engine) Singleton(name string) (Type, bool) {
	if e == nil {
		return Type{}, false
	}
	resolved, ok := e.singletons[name]
	return resolved, ok
}

// Utility returns one global utility function.
func (e *Engine) Utility(name string) (EngineMethod, bool) {
	if e == nil {
		return EngineMethod{}, false
	}
	utility, ok := e.utilities[name]
	return utility, ok
}

type engineMethodSpec struct {
	owner      string
	name       string
	returnType string
	arguments  []EngineArgumentSpec
	static     bool
	vararg     bool
}

type enginePropertySpec struct {
	owner     string
	name      string
	typeValue string
}

type engineOperatorSpec struct {
	left       string
	operator   string
	right      string
	returnType string
}

type engineSingletonSpec struct {
	name      string
	typeValue string
}

// EngineBuilder accumulates a fully validated schema before atomically
// publishing an immutable Engine. Add methods reject duplicate identities
// immediately; Build resolves every retained type only after all names exist.
type EngineBuilder struct {
	builtins   map[string]bool
	classes    map[string]string
	methods    map[engineMemberKey]engineMethodSpec
	properties map[engineMemberKey]enginePropertySpec
	operators  map[engineOperatorKey]engineOperatorSpec
	singletons map[string]engineSingletonSpec
	utilities  map[string]engineMethodSpec
}

// NewEngineBuilder creates an empty engine schema builder.
func NewEngineBuilder() *EngineBuilder {
	return &EngineBuilder{
		builtins:   map[string]bool{},
		classes:    map[string]string{},
		methods:    map[engineMemberKey]engineMethodSpec{},
		properties: map[engineMemberKey]enginePropertySpec{},
		operators:  map[engineOperatorKey]engineOperatorSpec{},
		singletons: map[string]engineSingletonSpec{},
		utilities:  map[string]engineMethodSpec{},
	}
}

// AddBuiltin adds one builtin type name.
func (b *EngineBuilder) AddBuiltin(name string) error {
	if err := engineName("builtin", name); err != nil {
		return err
	}
	if b.builtins[name] || b.classes[name] != "" || hasClass(b.classes, name) {
		return fmt.Errorf("duplicate engine type %q", name)
	}
	b.builtins[name] = true
	return nil
}

// AddClass adds one engine class and its direct parent spelling. An empty
// parent records a root class.
func (b *EngineBuilder) AddClass(name, inherits string) error {
	if err := engineName("class", name); err != nil {
		return err
	}
	if b.builtins[name] || hasClass(b.classes, name) {
		return fmt.Errorf("duplicate engine type %q", name)
	}
	if strings.TrimSpace(inherits) != inherits {
		return fmt.Errorf("engine class %q has malformed parent %q", name, inherits)
	}
	b.classes[name] = inherits
	return nil
}

// AddMethod adds one direct method identity.
func (b *EngineBuilder) AddMethod(owner, name, returnType string, arguments []EngineArgumentSpec, static, vararg bool) error {
	if err := engineMember("method", owner, name); err != nil {
		return err
	}
	if err := engineTypeSpelling("method return", returnType); err != nil {
		return err
	}
	for index, argument := range arguments {
		if err := engineTypeSpelling(fmt.Sprintf("method argument %d", index), argument.Type); err != nil {
			return err
		}
	}
	key := engineMemberKey{owner: owner, name: name}
	if _, exists := b.methods[key]; exists {
		return fmt.Errorf("duplicate engine method %s.%s", owner, name)
	}
	b.methods[key] = engineMethodSpec{
		owner: owner, name: name, returnType: returnType,
		arguments: append([]EngineArgumentSpec(nil), arguments...), static: static, vararg: vararg,
	}
	return nil
}

// AddProperty adds one direct property identity.
func (b *EngineBuilder) AddProperty(owner, name, typeValue string) error {
	if err := engineMember("property", owner, name); err != nil {
		return err
	}
	if err := engineTypeSpelling("property", typeValue); err != nil {
		return err
	}
	key := engineMemberKey{owner: owner, name: name}
	if _, exists := b.properties[key]; exists {
		return fmt.Errorf("duplicate engine property %s.%s", owner, name)
	}
	b.properties[key] = enginePropertySpec{owner: owner, name: name, typeValue: typeValue}
	return nil
}

// AddOperator adds one exact operator identity.
func (b *EngineBuilder) AddOperator(left, operator, right, returnType string) error {
	if err := engineName("operator left type", left); err != nil {
		return err
	}
	if err := engineName("operator", operator); err != nil {
		return err
	}
	if strings.TrimSpace(right) != right {
		return fmt.Errorf("engine operator right type %q is malformed", right)
	}
	if err := engineTypeSpelling("operator return", returnType); err != nil {
		return err
	}
	key := engineOperatorKey{left: left, operator: operator, right: right}
	if _, exists := b.operators[key]; exists {
		return fmt.Errorf("duplicate engine operator %s %s %s", left, operator, right)
	}
	b.operators[key] = engineOperatorSpec{left: left, operator: operator, right: right, returnType: returnType}
	return nil
}

// AddSingleton adds one global singleton identity.
func (b *EngineBuilder) AddSingleton(name, typeValue string) error {
	if err := engineName("singleton", name); err != nil {
		return err
	}
	if err := engineTypeSpelling("singleton", typeValue); err != nil {
		return err
	}
	if _, exists := b.singletons[name]; exists {
		return fmt.Errorf("duplicate engine singleton %s", name)
	}
	b.singletons[name] = engineSingletonSpec{name: name, typeValue: typeValue}
	return nil
}

// AddUtility adds one global utility-function identity.
func (b *EngineBuilder) AddUtility(name, returnType string, arguments []EngineArgumentSpec, vararg bool) error {
	if err := engineName("utility", name); err != nil {
		return err
	}
	if err := engineTypeSpelling("utility return", returnType); err != nil {
		return err
	}
	for index, argument := range arguments {
		if err := engineTypeSpelling(fmt.Sprintf("utility argument %d", index), argument.Type); err != nil {
			return err
		}
	}
	if _, exists := b.utilities[name]; exists {
		return fmt.Errorf("duplicate engine utility %s", name)
	}
	b.utilities[name] = engineMethodSpec{
		name: name, returnType: returnType,
		arguments: append([]EngineArgumentSpec(nil), arguments...), vararg: vararg,
	}
	return nil
}

// Build validates cross-record ownership, resolves every retained type, and
// returns a fully initialized index. No partial Engine is returned on error.
func (b *EngineBuilder) Build() (*Engine, error) {
	if b == nil {
		return nil, fmt.Errorf("engine builder is nil")
	}
	if err := validateEngineInheritance(b.classes); err != nil {
		return nil, err
	}
	for key := range b.methods {
		if !b.ownerExists(key.owner) {
			return nil, fmt.Errorf("engine method %s.%s names unknown owner %q", key.owner, key.name, key.owner)
		}
	}
	for key := range b.properties {
		if !b.ownerExists(key.owner) {
			return nil, fmt.Errorf("engine property %s.%s names unknown owner %q", key.owner, key.name, key.owner)
		}
	}
	for key := range b.operators {
		if !b.ownerExists(key.left) {
			return nil, fmt.Errorf("engine operator %s %s %s names unknown left type %q", key.left, key.operator, key.right, key.left)
		}
	}

	resolver := engineTypeResolver{
		builtins:  cloneBoolMap(b.builtins),
		parents:   cloneStringMap(b.classes),
		classes:   make(map[string]Type, len(b.classes)),
		resolving: make(map[string]bool, len(b.classes)),
	}
	for name := range b.classes {
		resolver.class(name)
	}
	engine := &Engine{
		builtins:   resolver.builtins,
		classes:    resolver.classes,
		methods:    make(map[engineMemberKey]EngineMethod, len(b.methods)),
		properties: make(map[engineMemberKey]EngineProperty, len(b.properties)),
		operators:  make(map[engineOperatorKey]Type, len(b.operators)),
		singletons: make(map[string]Type, len(b.singletons)),
		utilities:  make(map[string]EngineMethod, len(b.utilities)),
	}
	for key, spec := range b.methods {
		engine.methods[key] = resolveEngineMethod(spec, engine.builtins, engine.classes)
	}
	for key, spec := range b.properties {
		engine.properties[key] = EngineProperty{
			owner: spec.owner, name: spec.name,
			typeValue: resolveEngineType(spec.typeValue, engine.builtins, engine.classes),
		}
	}
	for key, spec := range b.operators {
		engine.operators[key] = resolveEngineType(spec.returnType, engine.builtins, engine.classes)
	}
	for name, spec := range b.singletons {
		engine.singletons[name] = resolveEngineType(spec.typeValue, engine.builtins, engine.classes)
	}
	for name, spec := range b.utilities {
		engine.utilities[name] = resolveEngineMethod(spec, engine.builtins, engine.classes)
	}
	return engine, nil
}

func validateEngineInheritance(parents map[string]string) error {
	const (
		inheritanceVisiting = 1
		inheritanceDone     = 2
	)
	state := make(map[string]uint8, len(parents))
	names := make([]string, 0, len(parents))
	for name := range parents {
		names = append(names, name)
	}
	sort.Strings(names)
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case inheritanceVisiting:
			return fmt.Errorf("engine class inheritance cycle at %q", name)
		case inheritanceDone:
			return nil
		}
		parent, exists := parents[name]
		if !exists {
			return nil
		}
		state[name] = inheritanceVisiting
		if parent != "" {
			if _, internal := parents[parent]; internal {
				if err := visit(parent); err != nil {
					return err
				}
			}
		}
		state[name] = inheritanceDone
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func (b *EngineBuilder) ownerExists(name string) bool {
	return b.builtins[name] || hasClass(b.classes, name)
}

func resolveEngineMethod(spec engineMethodSpec, builtins map[string]bool, classes map[string]Type) EngineMethod {
	arguments := make([]EngineArgument, len(spec.arguments))
	for index, argument := range spec.arguments {
		arguments[index] = EngineArgument{
			typeValue:  resolveEngineType(argument.Type, builtins, classes),
			hasDefault: argument.HasDefault,
		}
	}
	return EngineMethod{
		owner: spec.owner, name: spec.name,
		returnType: resolveEngineType(spec.returnType, builtins, classes),
		arguments:  arguments, static: spec.static, vararg: spec.vararg,
	}
}

type engineTypeResolver struct {
	builtins  map[string]bool
	parents   map[string]string
	classes   map[string]Type
	resolving map[string]bool
}

func (r *engineTypeResolver) class(name string) Type {
	if resolved, ok := r.classes[name]; ok {
		return resolved
	}
	if r.resolving[name] {
		return Unknown(fmt.Sprintf("engine class inheritance cycle at %q", name))
	}
	parent, ok := r.parents[name]
	if !ok {
		return Unknown(fmt.Sprintf("engine class %q is absent from schema", name))
	}
	r.resolving[name] = true
	var base *Type
	if parent != "" {
		resolved := r.class(parent)
		base = &resolved
	}
	resolved := Class(name, base, false)
	delete(r.resolving, name)
	r.classes[name] = resolved
	return resolved
}

func resolveEngineType(spelling string, builtins map[string]bool, classes map[string]Type) Type {
	switch spelling {
	case "Variant":
		return Variant()
	case "void":
		return Void()
	}
	if name, ok := strings.CutPrefix(spelling, "enum::"); ok && name != "" {
		return resolveEngineEnum(spelling, name, builtins, classes)
	}
	if name, ok := strings.CutPrefix(spelling, "bitfield::"); ok && name != "" {
		return resolveEngineEnum(spelling, name, builtins, classes)
	}
	if element, ok := strings.CutPrefix(spelling, "typedarray::"); ok {
		if !builtins["Array"] {
			return Unknown(`engine type "Array" is absent from schema`)
		}
		if _, encoded, found := strings.Cut(element, ":"); found {
			element = encoded
		}
		resolved := Unknown(fmt.Sprintf("typed array element is absent from engine type %q", spelling))
		if element != "" {
			resolved = resolveEngineType(element, builtins, classes)
		}
		return Array(&resolved)
	}
	if components, ok := strings.CutPrefix(spelling, "typeddictionary::"); ok {
		if !builtins["Dictionary"] {
			return Unknown(`engine type "Dictionary" is absent from schema`)
		}
		keyName, valueName, found := strings.Cut(components, ";")
		if !found || keyName == "" || valueName == "" || strings.Contains(valueName, ";") {
			return Unknown(fmt.Sprintf("engine dictionary type %q is malformed", spelling))
		}
		key := resolveEngineType(keyName, builtins, classes)
		value := resolveEngineType(valueName, builtins, classes)
		return Dictionary(&key, &value)
	}
	if resolved, ok := classes[spelling]; ok {
		return resolved
	}
	if builtins[spelling] {
		switch spelling {
		case "Array":
			return Array(nil)
		case "Dictionary":
			return Dictionary(nil, nil)
		case "Callable":
			return Callable()
		case "Signal":
			return Signal()
		default:
			return Builtin(spelling)
		}
	}
	return Unknown(fmt.Sprintf("engine type %q is absent from schema", spelling))
}

func resolveEngineEnum(spelling, name string, builtins map[string]bool, classes map[string]Type) Type {
	owner, _, qualified := strings.Cut(name, ".")
	if qualified && owner != "Variant" && !builtins[owner] {
		if _, exists := classes[owner]; !exists {
			return Unknown(fmt.Sprintf("engine enum owner %q in type %q is absent from schema", owner, spelling))
		}
	}
	return Enum(name)
}

func engineName(kind, name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("engine %s name %q is malformed", kind, name)
	}
	return nil
}

func engineMember(kind, owner, name string) error {
	if err := engineName(kind+" owner", owner); err != nil {
		return err
	}
	return engineName(kind, name)
}

func engineTypeSpelling(kind, spelling string) error {
	if spelling == "" || strings.TrimSpace(spelling) != spelling {
		return fmt.Errorf("engine %s type %q is malformed", kind, spelling)
	}
	return nil
}

func hasClass(classes map[string]string, name string) bool {
	_, ok := classes[name]
	return ok
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
