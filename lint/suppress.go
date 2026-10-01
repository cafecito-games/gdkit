package lint

import (
	"fmt"
	"unicode/utf8"

	"github.com/cafecito-games/gdkit/internal/suppression"
	"github.com/cafecito-games/gdkit/project"
)

// Suppression comments follow gdlint exactly. Three directives name rules in a
// comma-separated list:
//
//	# gdlint:ignore  = a, b   the comment's own line and the line below it
//	# gdlint:disable = a, b   from the comment to the matching enable or end of file
//	# gdlint:enable  = a, b   ends disabling, for every disable of that rule
//
// The grammar itself lives in internal/suppression, which the formatter shares.

// directive is one rule name appearing in a suppression comment.
type directive struct {
	rule   string
	line   int
	column int
}

type lineRange struct{ first, last int }

// suppressions answers which rules are silent on which lines of one script.
type suppressions struct {
	ignored  map[string]map[int]bool
	disabled map[string][]lineRange
	// named lists every rule name written in a suppression comment, so names
	// that match no rule can be reported.
	named []directive
}

// parseSuppressions reads the suppression comments of a script.
func parseSuppressions(script *project.Script) *suppressions {
	found := &suppressions{
		ignored:  map[string]map[int]bool{},
		disabled: map[string][]lineRange{},
	}
	lastLine := script.LineCount()
	disableStarts := map[string][]int{}
	earliestEnable := map[string]int{}

	for number := 1; number <= lastLine; number++ {
		text := script.Line(number)
		for _, name := range found.names(suppression.Ignore, text, number) {
			if found.ignored[name] == nil {
				found.ignored[name] = map[int]bool{}
			}
			found.ignored[name][number] = true
			found.ignored[name][number+1] = true
		}
		for _, name := range found.names(suppression.Disable, text, number) {
			start := number
			if !suppression.StandsAlone(text) {
				start++
			}
			disableStarts[name] = append(disableStarts[name], start)
		}
		for _, name := range found.names(suppression.Enable, text, number) {
			if earliest, ok := earliestEnable[name]; !ok || number < earliest {
				earliestEnable[name] = number
			}
		}
	}

	// gdlint clips every disable of a rule at that rule's earliest enable, even
	// an enable that precedes the disable, which leaves an empty range.
	for name, starts := range disableStarts {
		last := lastLine
		if earliest, ok := earliestEnable[name]; ok && earliest < last {
			last = earliest
		}
		for _, start := range starts {
			found.disabled[name] = append(found.disabled[name], lineRange{start, last})
		}
	}
	return found
}

// names returns the rule names a directive on one line lists and records them
// for unknown-name reporting.
func (s *suppressions) names(kind suppression.Kind, text string, number int) []string {
	offset, names, ok := suppression.Match(kind, text)
	if !ok {
		return nil
	}
	column := utf8.RuneCountInString(text[:offset]) + 1
	for _, name := range names {
		s.named = append(s.named, directive{rule: name, line: number, column: column})
	}
	return names
}

// silences reports whether the rule is suppressed on the line.
func (s *suppressions) silences(rule string, line int) bool {
	if s.ignored[rule][line] {
		return true
	}
	for _, span := range s.disabled[rule] {
		if line >= span.first && line <= span.last {
			return true
		}
	}
	return false
}

// unknownNames reports every suppression comment name that is not a rule.
func (s *suppressions) unknownNames() []Diagnostic {
	var diagnostics []Diagnostic
	for _, named := range s.named {
		if IsRule(named.rule) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Message: fmt.Sprintf("Suppression comment names unknown rule %q", named.rule),
			Line:    named.line,
			Column:  named.column,
		})
	}
	return diagnostics
}
