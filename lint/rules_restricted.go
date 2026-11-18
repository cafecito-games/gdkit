package lint

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/internal/glob"
	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(restrictedAPIRule{})
}

type restrictionUseKind uint8

const (
	restrictionKindReference restrictionUseKind = iota
	restrictionKindCall
	restrictionKindAssignment
)

type compiledRestriction struct {
	exact   string
	pattern *regexp.Regexp
	kind    restrictionUseKind
	allow   []glob.Pattern
	message string
}

func (r compiledRestriction) allowsFile(path string) bool {
	for _, allowPattern := range r.allow {
		if matched, _ := allowPattern.Match(path); matched {
			return true
		}
	}
	return false
}

func (r compiledRestriction) matchesKind(actual restrictionUseKind) bool {
	switch r.kind {
	case restrictionKindReference:
		return true
	case restrictionKindCall:
		return actual == restrictionKindCall
	case restrictionKindAssignment:
		return actual == restrictionKindAssignment
	default:
		return false
	}
}

func (r compiledRestriction) matchesSymbol(candidate string) bool {
	if r.pattern != nil {
		return r.pattern.MatchString(candidate)
	}
	return candidate == r.exact
}

func (r compiledRestriction) formatMessage(matchedSymbol string) string {
	if r.message == "" {
		return fmt.Sprintf("%q is restricted", matchedSymbol)
	}
	return fmt.Sprintf("%q is restricted: %s", matchedSymbol, r.message)
}

func compileRestrictions(restrictions []Restriction) ([]compiledRestriction, error) {
	if len(restrictions) == 0 {
		return nil, nil
	}
	compiled := make([]compiledRestriction, 0, len(restrictions))
	for _, entry := range restrictions {
		exact, pattern, err := compileRestrictionSymbol(entry.Symbol)
		if err != nil {
			return nil, err
		}
		kind, err := parseRestrictionKind(entry.Symbol, entry.Kind)
		if err != nil {
			return nil, err
		}
		allowPatterns := make([]glob.Pattern, 0, len(entry.Allow))
		for _, allow := range entry.Allow {
			if err := validateRestrictionAllowPattern(entry.Symbol, allow); err != nil {
				return nil, err
			}
			parsed, compileErr := glob.Compile(allow)
			if compileErr != nil {
				return nil, fmt.Errorf("restricted-api allow pattern %q: %w", allow, compileErr)
			}
			allowPatterns = append(allowPatterns, parsed)
		}
		compiled = append(compiled, compiledRestriction{
			exact:   exact,
			pattern: pattern,
			kind:    kind,
			allow:   allowPatterns,
			message: strings.TrimSpace(entry.Message),
		})
	}
	return compiled, nil
}

func validateRestrictionAllowPattern(symbol, allow string) error {
	if allow == "" {
		return fmt.Errorf("restricted-api allow pattern for %q must not be empty", symbol)
	}
	if strings.Contains(allow, `\`) {
		return fmt.Errorf("restricted-api allow pattern %q must be slash-separated", allow)
	}
	if strings.ContainsRune(allow, '\x00') {
		return errors.New("restricted-api allow pattern must not contain a NUL byte")
	}
	if strings.HasPrefix(allow, "res://") || strings.HasPrefix(allow, "uid://") {
		return fmt.Errorf("restricted-api allow pattern %q must be a project-relative path, not a Godot URI", allow)
	}
	clean := path.Clean(allow)
	if path.IsAbs(allow) || filepath.IsAbs(allow) || clean == ".." || strings.HasPrefix(clean, "../") ||
		(len(allow) >= 2 && ((allow[0] >= 'a' && allow[0] <= 'z') || (allow[0] >= 'A' && allow[0] <= 'Z')) && allow[1] == ':') {
		return fmt.Errorf("restricted-api allow pattern %q must be a project-relative path", allow)
	}
	return nil
}

func parseRestrictionKind(symbol, raw string) (restrictionUseKind, error) {
	switch raw {
	case "", "reference":
		return restrictionKindReference, nil
	case "call":
		return restrictionKindCall, nil
	case "assignment":
		return restrictionKindAssignment, nil
	default:
		return 0, fmt.Errorf("restricted-api restriction for %q has unknown kind %q (must be \"reference\", \"call\", or \"assignment\")", symbol, raw)
	}
}

func compileRestrictionSymbol(symbol string) (string, *regexp.Regexp, error) {
	if symbol == "" {
		return "", nil, errors.New("restricted-api symbol must not be empty")
	}
	segments := strings.Split(symbol, ".")
	hasWildcard := false
	for _, segment := range segments {
		if !isSymbolSegmentPattern(segment) {
			return "", nil, fmt.Errorf("restricted-api symbol %q does not name a valid symbol pattern", symbol)
		}
		if strings.ContainsAny(segment, "*?") {
			hasWildcard = true
		}
	}
	if !hasWildcard {
		return symbol, nil, nil
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for segmentIndex, segment := range segments {
		if segmentIndex > 0 {
			expression.WriteString(`\.`)
		}
		for index := 0; index < len(segment); index++ {
			switch segment[index] {
			case '*':
				expression.WriteString(`[A-Za-z0-9_]*`)
			case '?':
				expression.WriteString(`[A-Za-z0-9_]`)
			default:
				expression.WriteString(regexp.QuoteMeta(string(segment[index])))
			}
		}
	}
	expression.WriteByte('$')
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return "", nil, fmt.Errorf("restricted-api symbol %q: %w", symbol, err)
	}
	return "", compiled, nil
}

func isSymbolSegmentPattern(segment string) bool {
	if segment == "" {
		return false
	}
	for index, character := range segment {
		switch {
		case character == '_', character == '*', character == '?':
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9' && index > 0:
		default:
			return false
		}
	}
	return true
}

// restrictedAPIRule reports a reference to a configured symbol outside the
// files allowed to reach it. Receiver and scope resolution run through the
// semantic analyzer so comments, strings, shadowed locals, untyped Variants,
// and unrelated classes declaring a same-named member never produce findings.
//
// The rule ships inert so an upgrade cannot change an existing project's
// verdict on unchanged configuration.
type restrictedAPIRule struct{}

func (restrictedAPIRule) Name() string { return "restricted-api" }

func (restrictedAPIRule) Describe() string {
	return "Reports a reference to a restricted symbol outside its allowed files"
}

func (restrictedAPIRule) PendingSince() string { return "0.12.0" }

func (restrictedAPIRule) RequiresEngineSchema() {}

func (restrictedAPIRule) NeedsSemanticAnalysis() bool { return true }

func (restrictedAPIRule) Check(context *Context, script *project.Script) []Diagnostic {
	if context == nil || context.compiled == nil || context.analyzer == nil || script == nil || script.File == nil {
		return nil
	}
	var active []compiledRestriction
	for _, restriction := range context.compiled.restrictions {
		if !restriction.allowsFile(script.Path) {
			active = append(active, restriction)
		}
	}
	if len(active) == 0 {
		return nil
	}
	classID := context.projectClassID(script.Path)
	walker := restrictedWalker{
		script:       script,
		analyzer:     context.analyzer,
		restrictions: active,
	}
	walker.walkClassBody(classID, script.File.Statements)
	return walker.found
}

type restrictedWalker struct {
	script       *project.Script
	analyzer     *semantic.Analyzer
	restrictions []compiledRestriction
	found        []Diagnostic
}

func (w *restrictedWalker) emit(candidates []string, use restrictionUseKind, span token.Span) {
	if len(candidates) == 0 {
		return
	}
	for _, restriction := range w.restrictions {
		if !restriction.matchesKind(use) {
			continue
		}
		for _, candidate := range candidates {
			if restriction.matchesSymbol(candidate) {
				w.found = append(w.found, spanDiagnostic(w.script, restriction.formatMessage(candidate), span))
				break
			}
		}
	}
}

func (w *restrictedWalker) emitAnnotation(classID, spelling string, allowVoid bool, span token.Span) {
	if strings.TrimSpace(spelling) == "" {
		return
	}
	groups := w.analyzer.AnnotationSymbolGroups(classID, spelling, allowVoid)
	if len(groups) == 0 {
		return
	}
	type emittedKey struct {
		restriction int
		symbol      string
	}
	var seen map[emittedKey]bool
	for _, group := range groups {
		for index, restriction := range w.restrictions {
			if !restriction.matchesKind(restrictionKindReference) {
				continue
			}
			for _, candidate := range group {
				if restriction.matchesSymbol(candidate) {
					key := emittedKey{restriction: index, symbol: candidate}
					if seen[key] {
						break
					}
					if seen == nil {
						seen = make(map[emittedKey]bool)
					}
					seen[key] = true
					w.found = append(w.found, spanDiagnostic(w.script, restriction.formatMessage(candidate), span))
					break
				}
			}
		}
	}
}

func (w *restrictedWalker) walkAnnotations(classID string, annotations []*ast.Annotation) {
	for _, annotation := range annotations {
		if annotation == nil {
			continue
		}
		for _, argument := range annotation.Arguments {
			w.walkExpression(classID, argument, restrictionKindReference)
		}
	}
}

func (w *restrictedWalker) walkClassBody(classID string, statements []ast.Statement) {
	for _, statement := range statements {
		switch node := statement.(type) {
		case *ast.Directive:
			switch node.Name {
			case "extends":
				if node.Value != nil {
					w.emit(w.analyzer.ExtendsSymbols(classID), restrictionKindReference, node.Value.Span())
				}
			case "class_name":
				if node.Extends != nil {
					w.emit(w.analyzer.ExtendsSymbols(classID), restrictionKindReference, node.Extends.Span())
				}
			}
		case *ast.ClassDeclaration:
			w.walkAnnotations(classID, node.Annotations)
			innerID := ""
			if classID != "" {
				innerID = classID + "#" + node.Name
			}
			if strings.TrimSpace(node.Extends) != "" {
				w.emit(w.analyzer.ExtendsSymbols(innerID), restrictionKindReference, node.BaseTypeSpan)
			}
			w.walkClassBody(innerID, node.Body)
		case *ast.SignalDeclaration:
			w.walkAnnotations(classID, node.Annotations)
			w.checkParameters(classID, node.Parameters)
		case *ast.EnumDeclaration:
			for _, member := range node.Members {
				w.walkExpression(classID, member.Value, restrictionKindReference)
			}
		case *ast.VariableDeclaration:
			w.walkVariableDeclaration(classID, node)
		case *ast.FunctionDeclaration:
			w.walkAnnotations(classID, node.Annotations)
			w.checkParameters(classID, node.Parameters)
			w.emitAnnotation(classID, node.ReturnType, true, node.ReturnTypeSpan)
			w.walkStatements(classID, node.Body)
		default:
			w.walkStatement(classID, statement)
		}
	}
}

func (w *restrictedWalker) walkVariableDeclaration(classID string, node *ast.VariableDeclaration) {
	if node == nil {
		return
	}
	w.walkAnnotations(classID, node.Annotations)
	w.emitAnnotation(classID, node.Type, false, node.TypeSpan)
	w.walkExpression(classID, node.Value, restrictionKindReference)
	if node.GetterName != "" {
		w.emit(w.analyzer.ClassMemberSymbols(classID, node.GetterName), restrictionKindReference, node.GetterNameSpan)
	}
	if node.SetterName != "" {
		w.emit(w.analyzer.ClassMemberSymbols(classID, node.SetterName), restrictionKindReference, node.SetterNameSpan)
	}
	w.walkStatements(classID, node.Getter)
	if node.Setter != nil {
		w.walkStatements(classID, node.Setter.Body)
	}
}

func (w *restrictedWalker) checkParameters(classID string, parameters []ast.Parameter) {
	for _, parameter := range parameters {
		w.emitAnnotation(classID, parameter.Type, false, parameter.TypeSpan)
		w.walkExpression(classID, parameter.Default, restrictionKindReference)
	}
}

func (w *restrictedWalker) walkStatements(classID string, statements []ast.Statement) {
	for _, statement := range statements {
		w.walkStatement(classID, statement)
	}
}

func (w *restrictedWalker) walkStatement(classID string, statement ast.Statement) {
	if statement == nil {
		return
	}
	switch node := statement.(type) {
	case *ast.ExpressionStatement:
		w.walkExpression(classID, node.Expression, restrictionKindReference)
	case *ast.VariableDeclaration:
		w.walkVariableDeclaration(classID, node)
	case *ast.Assignment:
		w.walkExpression(classID, node.Target, restrictionKindAssignment)
		w.walkExpression(classID, node.Value, restrictionKindReference)
	case *ast.ReturnStatement:
		w.walkExpression(classID, node.Value, restrictionKindReference)
	case *ast.IfStatement:
		for _, branch := range node.Branches {
			w.walkExpression(classID, branch.Condition, restrictionKindReference)
			w.walkStatements(classID, branch.Body)
		}
		w.walkStatements(classID, node.Else)
	case *ast.WhileStatement:
		w.walkExpression(classID, node.Condition, restrictionKindReference)
		w.walkStatements(classID, node.Body)
	case *ast.ForStatement:
		w.emitAnnotation(classID, node.Type, false, node.TypeSpan)
		w.walkExpression(classID, node.Iterable, restrictionKindReference)
		w.walkStatements(classID, node.Body)
	case *ast.MatchStatement:
		w.walkExpression(classID, node.Value, restrictionKindReference)
		for _, matchCase := range node.Cases {
			w.walkAnnotations(classID, matchCase.Annotations)
			for _, pattern := range matchCase.Patterns {
				w.walkExpression(classID, pattern, restrictionKindReference)
			}
			w.walkExpression(classID, matchCase.Guard, restrictionKindReference)
			w.walkStatements(classID, matchCase.Body)
		}
	case *ast.Annotation:
		for _, argument := range node.Arguments {
			w.walkExpression(classID, argument, restrictionKindReference)
		}
	}
}

func (w *restrictedWalker) walkExpression(classID string, expression ast.Expression, use restrictionUseKind) {
	if expression == nil {
		return
	}
	switch node := expression.(type) {
	case *ast.Identifier:
		w.emit(w.analyzer.IdentifierSymbols(node), use, node.Span())
	case *ast.MemberExpression:
		if identifier, ok := node.Object.(*ast.Identifier); !ok || identifier.Name != "super" {
			w.walkExpression(classID, node.Object, restrictionKindReference)
		}
		w.emit(w.analyzer.MemberSymbols(node), use, node.Span())
	case *ast.CallExpression:
		w.emit(w.analyzer.ResourceScriptSymbols(node), restrictionKindReference, node.Span())
		w.walkExpression(classID, node.Callee, restrictionKindCall)
		for _, argument := range node.Arguments {
			w.walkExpression(classID, argument, restrictionKindReference)
		}
	case *ast.UnaryExpression:
		w.walkExpression(classID, node.Operand, restrictionKindReference)
	case *ast.BinaryExpression:
		w.walkExpression(classID, node.Left, restrictionKindReference)
		w.walkExpression(classID, node.Right, restrictionKindReference)
	case *ast.TernaryExpression:
		w.walkExpression(classID, node.Value, restrictionKindReference)
		w.walkExpression(classID, node.Condition, restrictionKindReference)
		w.walkExpression(classID, node.Alternative, restrictionKindReference)
	case *ast.SubscriptExpression:
		w.walkExpression(classID, node.Object, restrictionKindReference)
		w.walkExpression(classID, node.Index, restrictionKindReference)
	case *ast.ArrayLiteral:
		for _, element := range node.Elements {
			w.walkExpression(classID, element, restrictionKindReference)
		}
	case *ast.DictionaryLiteral:
		for _, entry := range node.Entries {
			if !w.isLuaDictionaryKey(entry) {
				w.walkExpression(classID, entry.Key, restrictionKindReference)
			}
			w.walkExpression(classID, entry.Value, restrictionKindReference)
		}
	case *ast.TypeExpression:
		w.emitAnnotation(classID, node.Name, false, node.Span())
	case *ast.LambdaExpression:
		w.checkParameters(classID, node.Parameters)
		w.emitAnnotation(classID, node.ReturnType, true, node.ReturnTypeSpan)
		w.walkStatements(classID, node.Body)
	}
}

func (w *restrictedWalker) isLuaDictionaryKey(entry ast.DictionaryEntry) bool {
	start := entry.SeparatorSpan.Start.Offset
	end := entry.SeparatorSpan.End.Offset
	if start < 0 || end <= start || end > len(w.script.Source) {
		return false
	}
	return string(w.script.Source[start:end]) == "="
}
