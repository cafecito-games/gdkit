package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/lexer"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/project"
)

func init() {
	register(unnecessaryPassRule{})
	register(expressionNotAssignedRule{})
	register(duplicatedLoadRule{})
	register(unusedArgumentRule{})
	register(comparisonWithItselfRule{})
}

// spanDiagnostic builds a diagnostic covering a parser span.
func spanDiagnostic(script *project.Script, message string, span token.Span) Diagnostic {
	return Diagnostic{
		Message:   message,
		Line:      span.Start.Line,
		Column:    runeColumn(script, span.Start),
		EndLine:   span.End.Line,
		EndColumn: runeColumn(script, span.End),
	}
}

// significantTokens are the tokens of a script that carry syntax. Layout and
// comments are dropped so a run of tokens reads the way gdlint's grammar sees
// it, where whitespace and comments are ignored.
type significantTokens struct {
	source []byte
	tokens []token.Token
}

func lexSignificant(script *project.Script) *significantTokens {
	all, err := lexer.Lex(script.Source)
	if err != nil {
		return nil
	}
	kept := all[:0:0]
	for _, current := range all {
		switch current.Type {
		case token.Newline, token.Indent, token.Dedent, token.Comment, token.EOF:
			continue
		}
		kept = append(kept, current)
	}
	return &significantTokens{source: script.Source, tokens: kept}
}

// firstAtOrAfter returns the index of the first token starting at or after
// offset.
func (s *significantTokens) firstAtOrAfter(offset int) int {
	return sort.Search(len(s.tokens), func(index int) bool {
		return s.tokens[index].Span.Start.Offset >= offset
	})
}

// lastEndingAtOrBefore returns the index of the last token ending at or before
// offset.
func (s *significantTokens) lastEndingAtOrBefore(offset int) int {
	return sort.Search(len(s.tokens), func(index int) bool {
		return s.tokens[index].Span.End.Offset > offset
	}) - 1
}

func (s *significantTokens) text(index int) string {
	span := s.tokens[index].Span
	return string(s.source[span.Start.Offset:span.End.Offset])
}

func (s *significantTokens) is(index int, kind token.Type) bool {
	return index >= 0 && index < len(s.tokens) && s.tokens[index].Type == kind
}

// closerOf returns the index of the bracket closing the one opened at index,
// or -1 when there is none.
func (s *significantTokens) closerOf(index int) int {
	depth := 0
	for current := index; current < len(s.tokens); current++ {
		switch s.tokens[current].Type {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
			if depth == 0 {
				return current
			}
		}
	}
	return -1
}

// depthChange is the net number of brackets opened by tokens[from..to].
func (s *significantTokens) depthChange(from, to int) int {
	depth := 0
	for current := from; current <= to; current++ {
		switch s.tokens[current].Type {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
		}
	}
	return depth
}

// unnecessaryPassRule reports every "pass" that shares its block with another
// statement. gdlint counts statements per syntax-tree node, so comments and
// annotations do not count, and the "extends" clause of a class declaration
// does.
type unnecessaryPassRule struct{}

func (unnecessaryPassRule) Name() string { return "unnecessary-pass" }

func (unnecessaryPassRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	var found []Diagnostic
	block := func(statements []ast.Statement, extra int) {
		var passes []*ast.KeywordStatement
		total := extra
		for _, statement := range statements {
			switch statement := statement.(type) {
			case *ast.Comment, *ast.Annotation, *ast.FunctionDeclaration, *ast.ClassDeclaration:
				continue
			case *ast.KeywordStatement:
				if statement.Keyword == "pass" {
					passes = append(passes, statement)
				}
			}
			total++
		}
		if len(passes) == total {
			return
		}
		for _, pass := range passes {
			found = append(found, spanDiagnostic(script, `"pass" statement not necessary`, pass.Span()))
		}
	}

	block(script.File.Statements, 0)
	ast.Inspect(script.File, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ClassDeclaration:
			extra := 0
			if node.Extends != "" {
				extra = 1
			}
			block(node.Body, extra)
		case *ast.FunctionDeclaration:
			block(node.Body, 0)
		case *ast.LambdaExpression:
			block(node.Body, 0)
		case *ast.IfStatement:
			for _, branch := range node.Branches {
				block(branch.Body, 0)
			}
			block(node.Else, 0)
		case *ast.WhileStatement:
			block(node.Body, 0)
		case *ast.ForStatement:
			block(node.Body, 0)
		case *ast.MatchStatement:
			for _, matchCase := range node.Cases {
				block(matchCase.Body, 0)
			}
		case *ast.VariableDeclaration:
			block(node.Getter, 0)
			if node.Setter != nil {
				block(node.Setter.Body, 0)
			}
		}
		return true
	})
	return found
}

// expressionNotAssignedRule reports an expression statement whose value is
// thrown away. Calls, awaits, string literals (docstrings), and lambdas are
// tolerated; everything else, including a lone member access or node path,
// is reported.
type expressionNotAssignedRule struct{}

func (expressionNotAssignedRule) Name() string { return "expression-not-assigned" }

func (expressionNotAssignedRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	tokens := lexSignificant(script)
	var found []Diagnostic
	ast.Inspect(script.File, func(node ast.Node) bool {
		statement, ok := node.(*ast.ExpressionStatement)
		if !ok || statement.Expression == nil || valueIsUsed(statement.Expression) {
			return true
		}
		span := statement.Expression.Span()
		if tokens != nil {
			span = outerExpressionSpan(tokens, span)
		}
		found = append(found, spanDiagnostic(script, "expression is not asigned, and hence it can be removed", span))
		return true
	})
	return found
}

func valueIsUsed(expression ast.Expression) bool {
	switch expression := expression.(type) {
	case *ast.CallExpression, *ast.LambdaExpression:
		return true
	case *ast.UnaryExpression:
		return expression.Operator == "await"
	case *ast.Literal:
		return expression.Kind == ast.StringLiteral && !expression.RawPrefix
	}
	return false
}

// outerExpressionSpan widens the span of a statement's expression back out to
// the first token gdlint would report: the parser drops parentheses, but
// gdlint strips only the parentheses that wrap the whole expression.
func outerExpressionSpan(tokens *significantTokens, span token.Span) token.Span {
	first := tokens.firstAtOrAfter(span.Start.Offset)
	last := tokens.lastEndingAtOrBefore(span.End.Offset)
	if first >= len(tokens.tokens) || last < first {
		return span
	}
	for tokens.is(first-1, token.LParen) {
		first--
	}
	for tokens.is(last+1, token.RParen) {
		last++
	}
	for tokens.is(first, token.LParen) && tokens.closerOf(first) == last {
		first++
		last--
	}
	return token.Span{Start: tokens.tokens[first].Span.Start, End: tokens.tokens[last].Span.End}
}

// duplicatedLoadRule reports the second and later load() or preload() of the
// same string literal within one file. gdlint compares the literal as written,
// quotes included, and looks only at the first argument of a bare call.
type duplicatedLoadRule struct{}

func (duplicatedLoadRule) Name() string { return "duplicated-load" }

func (duplicatedLoadRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	tokens := lexSignificant(script)
	seen := map[string]bool{}
	var found []Diagnostic
	ast.Inspect(script.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpression)
		if !ok || len(call.Arguments) == 0 {
			return true
		}
		callee, ok := call.Callee.(*ast.Identifier)
		if !ok || (callee.Name != "load" && callee.Name != "preload") {
			return true
		}
		literal, ok := call.Arguments[0].(*ast.Literal)
		if !ok || literal.Kind != ast.StringLiteral || literal.RawPrefix || tokens.isParenthesizedArgument(callee, literal) {
			return true
		}
		if seen[literal.Raw] {
			found = append(found, spanDiagnostic(script, "duplicated loading of "+literal.Raw, literal.Span()))
			return true
		}
		seen[literal.Raw] = true
		return true
	})
	return found
}

// isParenthesizedArgument reports whether the literal is wrapped in its own
// parentheses rather than sitting directly inside the call's. The parser drops
// such parentheses, but gdlint sees a different expression and ignores it.
func (s *significantTokens) isParenthesizedArgument(callee *ast.Identifier, literal *ast.Literal) bool {
	if s == nil {
		return false
	}
	callOpen := s.firstAtOrAfter(callee.Span().End.Offset)
	literalIndex := s.firstAtOrAfter(literal.Span().Start.Offset)
	return literalIndex-1 != callOpen
}

// unusedArgumentRule reports a named function's parameter that its function
// never mentions. gdlint counts name tokens rather than resolving scopes, so
// the parameter counts as used when any other identifier-like name in the
// function matches it: a member access such as self.x, a node path, a
// dictionary key written {x = 1}, or the name or a parameter of a lambda. Type
// annotations, strings, and the get() and set() builtins do not count. Lambda
// parameters are never reported, and neither are abstract functions or names
// starting with an underscore.
type unusedArgumentRule struct{}

func (unusedArgumentRule) Name() string { return "unused-argument" }

func (unusedArgumentRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	var found []Diagnostic
	ast.Inspect(script.File, func(node ast.Node) bool {
		function, ok := node.(*ast.FunctionDeclaration)
		if !ok || function.Abstract || len(function.Parameters) == 0 {
			return true
		}
		occurrences := countNames(function)
		for _, parameter := range function.Parameters {
			if occurrences[parameter.Name] != 1 || strings.HasPrefix(parameter.Name, "_") {
				continue
			}
			found = append(found, spanDiagnostic(script, fmt.Sprintf("unused function argument '%s'", parameter.Name), parameter.NameSpan))
		}
		return true
	})
	return found
}

// countNames tallies every name gdlint's grammar lexes as a plain name inside
// a function declaration: its own name, its parameters, and the names in its
// defaults and body. Names declared in the body are left out, because a local,
// a loop variable, or a match bind can never share a parameter's name.
func countNames(function *ast.FunctionDeclaration) map[string]int {
	counts := map[string]int{function.Name: 1}
	notNames := map[ast.Node]bool{}
	count := func(name string) {
		if name != "get" && name != "set" {
			counts[name]++
		}
	}
	visit := func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Identifier:
			if !notNames[node] {
				counts[node.Name]++
			}
		case *ast.CallExpression:
			if callee, ok := node.Callee.(*ast.Identifier); ok && (callee.Name == "get" || callee.Name == "set") {
				notNames[callee] = true
			}
		case *ast.MemberExpression:
			count(node.Property)
		case *ast.LambdaExpression:
			if node.Name != "" {
				counts[node.Name]++
			}
			for _, parameter := range node.Parameters {
				counts[parameter.Name]++
			}
		case *ast.NodePathExpression:
			for _, name := range bareNodePathNames(node.Path) {
				counts[name]++
			}
		case *ast.Annotation:
			counts[node.Name]++
		}
		return true
	}
	for _, parameter := range function.Parameters {
		counts[parameter.Name]++
		if parameter.Default != nil {
			ast.Inspect(parameter.Default, visit)
		}
	}
	for _, statement := range function.Body {
		ast.Inspect(statement, visit)
	}
	return counts
}

// bareNodePathNames splits an unquoted $Path or %Name into the names gdlint
// lexes inside it. A quoted path is a string and contributes none.
func bareNodePathNames(path string) []string {
	if strings.HasPrefix(path, `"`) || strings.HasPrefix(path, `'`) {
		return nil
	}
	var names []string
	for _, segment := range strings.Split(path, "/") {
		if name := strings.TrimPrefix(segment, "%"); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// comparisonWithItselfRule reports a comparison whose two operands are the
// same sequence of tokens. Tokens stand in for gdlint's syntax subtrees, which
// keep the parentheses that the parser's tree drops, so (a) == a is not a
// match. gdlint's grammar also names an arithmetic or bitwise subtree
// differently on the right of a comparison than on the left, so an
// unparenthesized operand such as a + 1 never matches its twin; parentheses
// restore the match. The same grammar names the comparison itself differently
// wherever it does not lead its expression, and gdlint looks only at the
// leading name, so b and a == a is never reported while a == a and b is.
type comparisonWithItselfRule struct{}

func (comparisonWithItselfRule) Name() string { return "comparison-with-itself" }

var comparisonOperators = map[string]bool{"==": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true}

// asymmetricOperators build subtrees that gdlint's grammar names differently
// on the two sides of a comparison.
var asymmetricOperators = map[string]bool{
	"|": true, "^": true, "&": true, "<<": true, ">>": true, "+": true, "-": true,
	"*": true, "/": true, "%": true, "**": true, "is": true, "is not": true, "as": true,
}

func (comparisonWithItselfRule) Check(_ *Context, script *project.Script) []Diagnostic {
	if script.File == nil {
		return nil
	}
	tokens := lexSignificant(script)
	if tokens == nil {
		return nil
	}
	var found []Diagnostic
	parents := map[ast.Node]ast.Node{}
	var ancestors []ast.Node
	ast.Inspect(script.File, func(node ast.Node) bool {
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return true
		}
		if len(ancestors) > 0 {
			parents[node] = ancestors[len(ancestors)-1]
		}
		ancestors = append(ancestors, node)
		comparison, ok := node.(*ast.BinaryExpression)
		if !ok || !comparisonOperators[comparison.Operator] {
			return true
		}
		operator := tokens.firstAtOrAfter(comparison.OperatorSpan.Start.Offset)
		if !tokens.operandsMatch(comparison, operator) || !tokens.leadsItsExpression(comparison, parents) {
			return true
		}
		leftStart := tokens.operandStart(comparison.Left, operator)
		rightEnd := tokens.operandEnd(comparison.Right, operator)
		found = append(found, spanDiagnostic(script, "Redundant comparison", token.Span{
			Start: tokens.tokens[leftStart].Span.Start,
			End:   tokens.tokens[rightEnd].Span.End,
		}))
		return true
	})
	return found
}

// trailingOperandOperators take a right operand that gdlint's grammar builds
// from differently named rules than the left one.
var trailingOperandOperators = map[string]bool{
	"and": true, "&&": true, "or": true, "||": true, "in": true, "not in": true,
}

// leadsItsExpression reports whether gdlint's grammar names the comparison
// "comparison", the only name its check looks for. The grammar switches to a
// parallel family of rules, in which the node is an "asless_comparison", for
// the right operand of "and", "or", and "in", for the operand of "not", and
// for the condition and alternative of a ternary. Everything below such a
// position stays in that family until a bracket starts a fresh expression.
func (s *significantTokens) leadsItsExpression(comparison ast.Expression, parents map[ast.Node]ast.Node) bool {
	current := comparison
	for {
		if s.isParenthesized(current) {
			return true
		}
		switch parent := parents[current].(type) {
		case *ast.BinaryExpression:
			switch {
			case trailingOperandOperators[parent.Operator]:
				if parent.Right == current {
					return false
				}
			case parent.Operator != "as":
				return true
			}
			current = parent
		case *ast.UnaryExpression:
			return parent.Operator != "not" && parent.Operator != "!"
		case *ast.TernaryExpression:
			if parent.Value != current {
				return false
			}
			current = parent
		default:
			return true
		}
	}
}

// isParenthesized reports whether the expression sits directly inside its own
// pair of parentheses. The parser leaves parentheses out of a span, so the
// span is first widened over the ones that belong to its outermost operands.
func (s *significantTokens) isParenthesized(expression ast.Expression) bool {
	span := expression.Span()
	first := s.firstAtOrAfter(span.Start.Offset)
	last := s.lastEndingAtOrBefore(span.End.Offset)
	if first >= len(s.tokens) || last < first {
		return false
	}
	depth, lowest := 0, 0
	for current := first; current <= last; current++ {
		switch s.tokens[current].Type {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
			if depth < lowest {
				lowest = depth
			}
		}
	}
	for unmatched := -lowest; unmatched > 0 && s.is(first-1, token.LParen); unmatched-- {
		first--
	}
	for unmatched := depth - lowest; unmatched > 0 && s.is(last+1, token.RParen); unmatched-- {
		last++
	}
	return s.is(first-1, token.LParen) && s.closerOf(first-1) == last+1
}

// operandStart is the index of the first token of the left operand of the
// comparison whose operator is at index operator, parentheses included.
func (s *significantTokens) operandStart(left ast.Expression, operator int) int {
	first := s.firstAtOrAfter(left.Span().Start.Offset)
	for unmatched := -s.depthChange(first, operator-1); unmatched > 0 && s.is(first-1, token.LParen); unmatched-- {
		first--
	}
	return first
}

// operandEnd is the index of the last token of the right operand, parentheses
// included.
func (s *significantTokens) operandEnd(right ast.Expression, operator int) int {
	last := s.lastEndingAtOrBefore(right.Span().End.Offset)
	for unmatched := s.depthChange(operator+1, last); unmatched > 0 && s.is(last+1, token.RParen); unmatched-- {
		last++
	}
	return last
}

// operandsMatch reports whether both operands lex identically and would build
// identical syntax subtrees in gdlint's grammar.
func (s *significantTokens) operandsMatch(comparison *ast.BinaryExpression, operator int) bool {
	if operator >= len(s.tokens) || comparison.Left == nil || comparison.Right == nil {
		return false
	}
	leftStart := s.operandStart(comparison.Left, operator)
	rightEnd := s.operandEnd(comparison.Right, operator)
	left := s.tokens[leftStart:operator]
	right := s.tokens[operator+1 : rightEnd+1]
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Type != right[index].Type || s.text(leftStart+index) != s.text(operator+1+index) {
			return false
		}
	}
	wrapped := s.is(leftStart, token.LParen) && s.closerOf(leftStart) == operator-1
	if binary, isBinary := comparison.Left.(*ast.BinaryExpression); isBinary && !wrapped {
		return !asymmetricOperators[binary.Operator]
	}
	return true
}
