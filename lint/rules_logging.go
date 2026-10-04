package lint

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cafecito-games/gdparser/ast"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(noEngineLoggingRule{})
}

// defaultEngineLoggingFunctions are Godot's global diagnostic-output calls:
// everything that writes to the engine's output or error stream from anywhere
// in a project, with no level, no category, and no way to redirect it.
//
// OS.alert is qualified because it is a method on a singleton rather than a
// global utility function. The rest are globals, so a bare call is the only
// way to reach them.
var defaultEngineLoggingFunctions = []string{
	"OS.alert",
	"print",
	"print_debug",
	"print_rich",
	"print_stack",
	"printerr",
	"printraw",
	"prints",
	"printt",
	"push_error",
	"push_warning",
}

// noEngineLoggingRule reports a call that writes a diagnostic message straight
// to Godot's output, so a project can require its own logger instead. A logger
// abstraction is what gives a message a level, a category, and somewhere other
// than the editor's output panel to go; a direct push_error cannot be filtered,
// captured in a build, or silenced in a test.
//
// Only a bare call is reported for an unqualified name, and only a call on the
// named object for a qualified one. A logger whose own method happens to be
// called push_error or print is therefore never reported: the rule is about
// reaching the engine's function, not about the word.
//
// The rule ships inert, as a new rule must, so an upgrade cannot change what an
// existing project reports on unchanged configuration.
type noEngineLoggingRule struct{}

func (noEngineLoggingRule) Name() string { return "no-engine-logging" }

func (noEngineLoggingRule) PendingSince() string { return "0.4.0" }

func (noEngineLoggingRule) Check(context *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	rejected := make(map[string]bool, len(context.Config.NoEngineLogging.Functions))
	for _, function := range context.Config.NoEngineLogging.Functions {
		rejected[function] = true
	}
	if len(rejected) == 0 {
		return nil
	}
	replacement := "a logger abstraction"
	if logger := context.Config.NoEngineLogging.Logger; logger != "" {
		replacement = strconv.Quote(logger)
	}

	var found []Diagnostic
	ast.Inspect(script.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpression)
		if !ok {
			return true
		}
		name, ok := engineLoggingCallee(call.Callee)
		if !ok || !rejected[name] {
			return true
		}
		message := fmt.Sprintf("%q writes to the engine output directly; use %s instead", name, replacement)
		found = append(found, spanDiagnostic(script, message, call.Callee.Span()))
		return true
	})
	return found
}

// engineLoggingCallee names the function a callee reaches, in the form the
// configuration uses: a bare identifier, or one object and its property. Any
// deeper expression — a call's result, an index, a callable held in a field —
// names nothing the configuration can list, so it is not a candidate.
func engineLoggingCallee(callee ast.Expression) (string, bool) {
	switch callee := callee.(type) {
	case *ast.Identifier:
		return callee.Name, true
	case *ast.MemberExpression:
		if object, ok := callee.Object.(*ast.Identifier); ok {
			return object.Name + "." + callee.Property, true
		}
	}
	return "", false
}

// validateEngineLoggingFunction checks one configured name. A name is an
// identifier, optionally qualified by one object, so a typo like "OS .alert" or
// "print()" is a configuration error rather than a rule that quietly matches
// nothing.
func validateEngineLoggingFunction(name string) error {
	if name == "" {
		return fmt.Errorf("no-engine-logging names an empty function")
	}
	object, function, qualified := strings.Cut(name, ".")
	if qualified && !isIdentifier(object) {
		return fmt.Errorf("no-engine-logging function %q does not name a function", name)
	}
	if !qualified {
		function = name
	}
	if !isIdentifier(function) {
		return fmt.Errorf("no-engine-logging function %q does not name a function", name)
	}
	return nil
}

func isIdentifier(text string) bool {
	if text == "" {
		return false
	}
	for index, character := range text {
		switch {
		case character == '_':
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9' && index > 0:
		default:
			return false
		}
	}
	return true
}
