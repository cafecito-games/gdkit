package lint

import (
	"fmt"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/internal/versiongate"
	"github.com/cafecito-games/gdkit/project"
)

// Rule names are a public contract. Naming them once means a typo at a site
// fails to compile instead of silently dropping that rule's findings.
const (
	ruleRequireReturnType   = "require-return-type"
	ruleRequireArgumentType = "require-argument-type"
)

// typingRuleNames is every rule backed by the typing collector. Registering
// from one list keeps the set and the collector in one file, the way
// nameMessages does for the name rules.
var typingRuleNames = []string{
	ruleRequireReturnType,
	ruleRequireArgumentType,
}

func init() {
	for _, rule := range typingRuleNames {
		register(typingRule{rule: rule})
	}
}

// The Godot version that first accepts each typed form. They are values rather
// than strings so a site compares without parsing anything per file.
var godot40 = versiongate.Version{Major: 4}

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
		start, end := site.span.Start, site.span.End
		found = append(found, Diagnostic{
			Message:   site.message,
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
func (c *typingCollector) classVariable(declaration *ast.VariableDeclaration) {
	c.inspect("", declaration.Value)
	c.functionScope(declaration.Name, declaration.Getter)
	if declaration.Setter != nil {
		c.functionScope(declaration.Name, declaration.Setter.Body)
	}
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
	c.parameters(ruleRequireArgumentType, declaration.Parameters, declaration.Name,
		fmt.Sprintf("function %q", declaration.Name))
	for _, parameter := range declaration.Parameters {
		c.inspect(declaration.Name, parameter.Default)
	}
	c.functionScope(declaration.Name, declaration.Body)
}

// parameters records every parameter with no type.
func (c *typingCollector) parameters(rule string, parameters []ast.Parameter, enclosing, owner string) {
	for _, parameter := range parameters {
		// A variadic parameter needs no annotation: it collects the arguments
		// after it into an Array whatever is passed, so "untyped" is not a
		// missing type. It can still carry one, which a rule about the
		// annotation itself must be free to inspect.
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
