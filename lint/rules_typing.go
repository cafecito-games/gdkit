package lint

import (
	"fmt"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/internal/versiongate"
	"github.com/cafecito-games/gdkit/project"
)

// Rule names are a public contract. Naming them once means a typo at a site
// fails to compile instead of silently dropping that rule's findings.
//
// Unlike rules_name.go, which renders every message from the nameMessages
// table, each message here is rendered where its site is recorded: every one
// names the declaration's owner — the function, signal, lambda, or property
// that the site belongs to — which a table keyed by rule name cannot reach.
const (
	ruleRequireReturnType   = "require-return-type"
	ruleRequireArgumentType = "require-argument-type"

	ruleRequireVariableType    = "require-variable-type"
	ruleRequireTypedCollection = "require-typed-collection"

	ruleRequireSignalArgumentType = "require-signal-argument-type"
	ruleRequireTypedLoopVariable  = "require-typed-loop-variable"
)

// typingRuleNames is every rule backed by the typing collector. Registering
// from one list keeps the set and the collector in one file, the way
// nameMessages does for the name rules.
var typingRuleNames = []string{
	ruleRequireReturnType,
	ruleRequireArgumentType,
	ruleRequireVariableType,
	ruleRequireTypedCollection,
	ruleRequireSignalArgumentType,
	ruleRequireTypedLoopVariable,
}

func init() {
	for _, rule := range typingRuleNames {
		register(typingRule{rule: rule})
	}
}

// The Godot version that first accepts each typed form. They are values rather
// than strings so a site compares without parsing anything per file.
var (
	// godot40 is the floor for a type annotation at all, and for Array[T].
	godot40 = versiongate.Version{Major: 4}
	// godot42 is when a typed "for" variable arrived.
	godot42 = versiongate.Version{Major: 4, Minor: 2}
	// godot44 is when Dictionary[K, V] arrived; a bare Dictionary has no fix
	// before it, while Array[T] has been available since 4.0.
	godot44 = versiongate.Version{Major: 4, Minor: 4}
)

// typingRule reports a declaration that carries no static type annotation.
// Static typing is Godot's documented correctness and performance win, and an
// untyped declaration is a Variant: the engine cannot check it, cannot
// specialize it, and reports nothing when the wrong thing is assigned to it.
//
// All of the typing rules share this one type and one collector, the way the
// fourteen name rules share nameRule and collectNames. They ship inert, as a
// new rule must, so an upgrade cannot change what an existing project reports
// on unchanged configuration.
type typingRule struct{ rule string }

func (r typingRule) Name() string { return r.rule }

func (r typingRule) PendingSince() string { return "0.5.0" }

// NeedsSemanticAnalysis is true only for the value-sensitive collection rule.
// The other values of this shared type remain syntactic and must not make a
// project select an engine schema or build an analyzer.
func (r typingRule) NeedsSemanticAnalysis() bool { return r.rule == ruleRequireTypedCollection }

func (r typingRule) Check(context *Context, script *project.Script) []Diagnostic {
	var found []Diagnostic
	for _, site := range collectTypingSites(script) {
		if site.rule != r.rule {
			continue
		}
		// A site whose fix needs newer syntax than the project's engine is
		// dropped: telling a project to write a type it cannot parse is worse
		// than saying nothing.
		if !context.supports(site.floor) {
			continue
		}
		if context.exempt(r.rule, site.enclosing) {
			continue
		}
		message := site.message
		if site.literal != nil {
			var reported bool
			message, reported = populatedCollectionMessage(context, site.literal)
			if !reported {
				continue
			}
		}
		start, end := site.span.Start, site.span.End
		found = append(found, Diagnostic{
			Message:   message,
			Line:      start.Line,
			Column:    runeColumn(script, start),
			EndLine:   end.Line,
			EndColumn: runeColumn(script, end),
		})
	}
	return found
}

// typingSite is one declaration that is missing a type. The collector emits a
// site only for a violation, so a rule never re-decides what counts as typed.
type typingSite struct {
	// rule is the rule that governs the site.
	rule string
	// message is the rendered diagnostic.
	message string
	// enclosing is the name an exempt pattern matches: the function being
	// declared, the function a site sits inside, or a signal's own name. It is
	// empty for a class-scope declaration, which no list can exempt.
	enclosing string
	// floor is the Godot version that first accepts the annotation the site
	// wants. It belongs to the site rather than the rule because a later rule
	// needs two: Array[T] is 4.0 and Dictionary[K, V] is 4.4.
	floor versiongate.Version
	// span is what to underline.
	span token.Span
	// literal is a direct populated collection initializer. Empty literals and
	// written annotations stay entirely syntactic; this field asks the one
	// value-sensitive consumer to query the run's analyzer exactly once.
	literal ast.Expression
}

// annotated reports whether a declaration carries a static type. ":=" inference
// is static typing, and an explicit Variant is a deliberate opt-out, so both
// satisfy every typing rule.
func annotated(typeName string, inferred bool) bool {
	return inferred || typeName != ""
}

type typingCollector struct {
	found []typingSite
}

// collectTypingSites walks a file once and returns every declaration missing a
// type. Class bodies and function bodies are walked separately, as in
// collectNames: parameters and property accessors are not ast.Nodes, so they
// are reached from their parent declaration rather than through ast.Inspect.
func collectTypingSites(script *project.Script) []typingSite {
	if script.File == nil {
		return nil
	}
	collector := &typingCollector{}
	collector.classBody(script.File.Statements)
	return collector.found
}

func (c *typingCollector) add(site typingSite) {
	c.found = append(c.found, site)
}

func (c *typingCollector) classBody(statements []ast.Statement) {
	for _, statement := range statements {
		switch declaration := statement.(type) {
		case *ast.ClassDeclaration:
			c.classBody(declaration.Body)
		case *ast.FunctionDeclaration:
			c.function(declaration)
		case *ast.VariableDeclaration:
			c.classVariable(declaration)
		case *ast.SignalDeclaration:
			c.signal(declaration)
		case *ast.EnumDeclaration:
			for _, member := range declaration.Members {
				c.inspect("", member.Value)
			}
		}
	}
}

// classVariable walks a class-scope variable's initializer and its property
// accessors. Both hold code a rule must see: "var handler = func(event): ..."
// is the commonest unannotated lambda in a Godot project, and an accessor body
// is function-like code that nothing else in this walk reaches.
//
// The initializer carries no enclosing name, because a class-scope declaration
// is not inside a function and no exempt pattern can name it. An accessor body
// carries the property's name, which is what a reader would write a pattern
// for.
//
// It walks the initializer rather than the declaration, and that is what keeps
// a class-scope variable from being recorded twice: ast.Inspect both visits a
// VariableDeclaration and descends into it, so inspecting the declaration here
// would record the same variable that this function already recorded, and would
// reach accessor bodies a second time with the wrong enclosing name. A local
// variable is recorded by the walk instead, which is the only path that reaches
// one.
func (c *typingCollector) classVariable(declaration *ast.VariableDeclaration) {
	c.variable(declaration, "")
	c.inspect("", declaration.Value)
	c.functionScope(declaration.Name, declaration.Getter)
	if declaration.Setter != nil {
		c.functionScope(declaration.Name, declaration.Setter.Body)
	}
}

// signal records a signal's untyped parameters. A signal's payload is the
// boundary an untyped value travels furthest from: the emitter and every
// receiver are written apart, and nothing checks the type between them.
//
// A signal has no enclosing function, so its own name is what an exempt
// pattern matches.
func (c *typingCollector) signal(declaration *ast.SignalDeclaration) {
	c.parameters(ruleRequireSignalArgumentType, declaration.Parameters, declaration.Name,
		fmt.Sprintf("signal %q", declaration.Name))
}

// function records a function's signature and walks its body. An abstract
// function has no body but still declares a signature its callers rely on.
func (c *typingCollector) function(declaration *ast.FunctionDeclaration) {
	// A return type has no ":=" form, so an empty name is the whole test.
	if declaration.ReturnType == "" {
		c.add(typingSite{
			rule:      ruleRequireReturnType,
			message:   fmt.Sprintf("Function %q has no return type", declaration.Name),
			enclosing: declaration.Name,
			floor:     godot40,
			span:      declaration.NameSpan,
		})
	}
	c.collection(declaration.ReturnType, declaration.ReturnTypeSpan, declaration.Name)
	c.parameters(ruleRequireArgumentType, declaration.Parameters, declaration.Name,
		fmt.Sprintf("function %q", declaration.Name))
	c.functionScope(declaration.Name, declaration.Body)
}

// parameters records every parameter with no type and walks every default
// value. A default is code that runs in the declaration's own scope, so it is
// walked here rather than by each caller: a lambda written as a default is
// reached no other way.
func (c *typingCollector) parameters(rule string, parameters []ast.Parameter, enclosing, owner string) {
	for _, parameter := range parameters {
		c.inspect(enclosing, parameter.Default)
		// The annotation is examined first and for every parameter, because the
		// collection rule is about what is written rather than what is absent.
		c.collection(parameter.Type, parameter.TypeSpan, enclosing)
		// A variadic parameter needs no annotation: it collects the arguments
		// after it into an Array whatever is passed, so "untyped" is not a
		// missing type.
		if parameter.Variadic || annotated(parameter.Type, parameter.Inferred) {
			continue
		}
		c.add(typingSite{
			rule:      rule,
			message:   fmt.Sprintf("Argument %q of %s has no type", parameter.Name, owner),
			enclosing: enclosing,
			floor:     godot40,
			span:      parameter.NameSpan,
		})
	}
}

// variable records a variable with no type. A constant is never recorded:
// GDScript types a const from its value, so it is already statically typed. An
// @export is recorded like any other variable — the editor infers the exported
// type from the assigned value, which is not the same as the variable carrying
// one.
func (c *typingCollector) variable(declaration *ast.VariableDeclaration, enclosing string) {
	if !declaration.Constant && !annotated(declaration.Type, declaration.Inferred) {
		c.add(typingSite{
			rule:      ruleRequireVariableType,
			message:   fmt.Sprintf("Variable %q has no type", declaration.Name),
			enclosing: enclosing,
			floor:     godot40,
			span:      declaration.NameSpan,
		})
	}
	c.collection(declaration.Type, declaration.TypeSpan, enclosing)
	// A written annotation is the whole story when there is one: "var x: Array =
	// []" is one bare collection, reported from the annotation, not two.
	if declaration.Type == "" {
		c.collectionLiteral(declaration.Value, enclosing)
	}
}

// collectionLiteral records a declaration whose collection type comes directly
// from a literal. Empty literals retain the existing syntactic diagnostic.
// Populated literals carry their own span to the rule, which can ask the
// run-local analyzer for exactly the outer literal and stay silent when it is
// not conclusive. Calls, references, and all other initializers stay out of
// scope: their result is not a literal the author can annotate directly.
func (c *typingCollector) collectionLiteral(value ast.Expression, enclosing string) {
	switch literal := value.(type) {
	case *ast.ArrayLiteral:
		if len(literal.Elements) == 0 {
			c.collection("Array", literal.Span(), enclosing)
			return
		}
		c.populatedCollection("Array", literal, literal.Span(), enclosing)
	case *ast.DictionaryLiteral:
		if len(literal.Entries) == 0 {
			c.collection("Dictionary", literal.Span(), enclosing)
			return
		}
		c.populatedCollection("Dictionary", literal, literal.Span(), enclosing)
	}
}

func (c *typingCollector) populatedCollection(typeName string, literal ast.Expression, span token.Span, enclosing string) {
	floor, _, ok := collectionSuggestion(typeName)
	if !ok {
		return
	}
	c.add(typingSite{
		rule:      ruleRequireTypedCollection,
		enclosing: enclosing,
		floor:     floor,
		span:      span,
		literal:   literal,
	})
}

// collection records a bare Array or Dictionary, whether it was written as an
// annotation — on a variable, a parameter, a return type, or a loop variable —
// or inferred from an empty literal by collectionLiteral.
//
// The span is the caller's, so a finding underlines whichever of the two the
// reader has to change: the annotation when one is written, the literal when the
// type came from it.
func (c *typingCollector) collection(typeName string, span token.Span, enclosing string) {
	floor, form, ok := collectionSuggestion(typeName)
	if !ok {
		return
	}
	c.add(typingSite{
		rule:      ruleRequireTypedCollection,
		message:   fmt.Sprintf("%s has no element type; write %s", typeName, form),
		enclosing: enclosing,
		floor:     floor,
		span:      span,
	})
}

// collectionSuggestion keeps each collection's syntax floor coupled to its
// generic fallback spelling. A populated literal and a written bare
// annotation therefore cannot drift onto different version policies.
func collectionSuggestion(typeName string) (versiongate.Version, string, bool) {
	switch typeName {
	case "Array":
		return godot40, "Array[T]", true
	case "Dictionary":
		return godot44, "Dictionary[K, V]", true
	default:
		return versiongate.Version{}, "", false
	}
}

// populatedCollectionMessage classifies only a direct literal's one reduced
// result. Unknown is deliberately silent: a generic suggestion would conceal
// an unavailable semantic fact as though the container were confidently known.
func populatedCollectionMessage(context *Context, literal ast.Expression) (string, bool) {
	if context == nil || context.analyzer == nil {
		return "", false
	}
	return collectionTypeMessage(context.Engine(), context.analyzer.TypeOf(literal))
}

func collectionTypeMessage(engine *semantic.Engine, typeValue semantic.Type) (string, bool) {
	switch typeValue.Kind() {
	case semantic.KindUnknown:
		return "", false
	case semantic.KindArray:
		element, typed := typeValue.Element()
		if !typed {
			return "Array has no element type; write Array[T]", true
		}
		spelling, status := sourceWritableType(engine, element)
		switch status {
		case sourceTypeWritable:
			return fmt.Sprintf("Array has no element type; write Array[%s]", spelling), true
		case sourceTypeUnknown:
			return "", false
		default:
			return "Array has no element type; write Array[T]", true
		}
	case semantic.KindDictionary:
		key, typedKey := typeValue.Key()
		value, typedValue := typeValue.Value()
		if !typedKey || !typedValue {
			return "Dictionary has no element type; write Dictionary[K, V]", true
		}
		keySpelling, keyStatus := sourceWritableType(engine, key)
		valueSpelling, valueStatus := sourceWritableType(engine, value)
		if keyStatus == sourceTypeUnknown || valueStatus == sourceTypeUnknown {
			return "", false
		}
		if keyStatus != sourceTypeWritable || valueStatus != sourceTypeWritable {
			return "Dictionary has no element type; write Dictionary[K, V]", true
		}
		return fmt.Sprintf("Dictionary has no element type; write Dictionary[%s, %s]", keySpelling, valueSpelling), true
	default:
		return "", false
	}
}

type sourceTypeStatus uint8

const (
	sourceTypeUnwritable sourceTypeStatus = iota
	sourceTypeWritable
	sourceTypeUnknown
)

// sourceWritableType accepts only grammar spellings independently proven by
// the selected engine vocabulary. Nested typed collections are deliberately
// excluded because GDScript cannot parse their spelling. Type.String cannot be
// used: it also renders user classes, enums, meta types, and diagnostic
// Unknowns.
func sourceWritableType(engine *semantic.Engine, typeValue semantic.Type) (string, sourceTypeStatus) {
	switch typeValue.Kind() {
	case semantic.KindUnknown:
		return "", sourceTypeUnknown
	case semantic.KindVariant:
		return "Variant", sourceTypeWritable
	case semantic.KindCallable:
		return "Callable", sourceTypeWritable
	case semantic.KindSignal:
		return "Signal", sourceTypeWritable
	case semantic.KindBuiltin:
		resolved := engine.ResolveType(typeValue.Name())
		if resolved.Kind() == semantic.KindBuiltin && resolved.Equal(typeValue) {
			return typeValue.Name(), sourceTypeWritable
		}
		return "", sourceTypeUnwritable
	case semantic.KindClass:
		if typeValue.Meta() {
			return "", sourceTypeUnwritable
		}
		resolved := engine.Class(typeValue.Name())
		if resolved.Kind() == semantic.KindClass && !resolved.Meta() && resolved.Equal(typeValue) {
			return typeValue.Name(), sourceTypeWritable
		}
		return "", sourceTypeUnwritable
	case semantic.KindArray, semantic.KindDictionary:
		// GDScript does not accept a nested typed collection spelling. Keep a
		// known outer collection actionable with its established generic form
		// rather than suggesting Array[Array[T]] or Dictionary[K, Array[V]].
		if nestedUnknownType(typeValue) {
			return "", sourceTypeUnknown
		}
		return "", sourceTypeUnwritable
	default:
		return "", sourceTypeUnwritable
	}
}

// nestedUnknownType preserves the rule's silence contract even when the
// unknown component lives below an unspellable nested collection. It only
// classifies Type's immutable container vocabulary; it does not invent a
// nested spelling.
func nestedUnknownType(typeValue semantic.Type) bool {
	switch typeValue.Kind() {
	case semantic.KindUnknown:
		return true
	case semantic.KindArray:
		element, typed := typeValue.Element()
		return typed && nestedUnknownType(element)
	case semantic.KindDictionary:
		key, typedKey := typeValue.Key()
		value, typedValue := typeValue.Value()
		return typedKey && typedValue && (nestedUnknownType(key) || nestedUnknownType(value))
	default:
		return false
	}
}

func (c *typingCollector) functionScope(enclosing string, statements []ast.Statement) {
	for _, statement := range statements {
		c.inspect(enclosing, statement)
	}
}

// inspect walks a subtree that executes inside a function. The enclosing
// function's name travels with it, because that is what an exempt pattern
// matches for a site inside a function.
func (c *typingCollector) inspect(enclosing string, node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.VariableDeclaration:
			c.variable(declaration, enclosing)
		case *ast.ForStatement:
			// A "for" header has no ":=" form, so an empty name is the whole
			// test.
			if declaration.Type == "" {
				c.add(typingSite{
					rule:      ruleRequireTypedLoopVariable,
					message:   fmt.Sprintf("Loop variable %q has no type", declaration.Variable),
					enclosing: enclosing,
					floor:     godot42,
					span:      declaration.VariableSpan,
				})
			}
			c.collection(declaration.Type, declaration.TypeSpan, enclosing)
		case *ast.LambdaExpression:
			// A lambda's parameters are a contract its caller satisfies, so
			// they are checked. Its return value is consumed where the lambda
			// is written, where an annotation is noise, so ReturnType is
			// deliberately not checked even though the parser exposes it.
			c.parameters(ruleRequireArgumentType, declaration.Parameters, enclosing, lambdaOwner(declaration))
		}
		return true
	})
}

// lambdaOwner names a lambda in a diagnostic. A lambda may carry a name, which
// Godot reports in a stack trace and a reader will recognize.
func lambdaOwner(declaration *ast.LambdaExpression) string {
	if declaration.Name == "" {
		return "lambda"
	}
	return fmt.Sprintf("lambda %q", declaration.Name)
}
