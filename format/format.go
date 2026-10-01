package format

import (
	"bytes"
	"errors"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
	"github.com/cafecito-games/gdparser/parser"
)

// Formatter computes the canonical form of every script in a project.
type Formatter struct {
	options gdformat.Options
	// emit renders a tree as source. It is a field so a test can stand in a
	// formatter that damages its input and prove the damage is refused.
	emit func(*ast.File, gdformat.Options) string
}

// New builds a Formatter, rejecting an invalid config.
func New(config Config) (*Formatter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	options, err := config.options()
	if err != nil {
		return nil, err
	}
	return &Formatter{options: options, emit: gdformat.FileWithOptions}, nil
}

// Format computes the canonical form of every script in the snapshot. It
// performs no I/O and leaves the snapshot untouched; Apply writes the results.
// A file that does not parse, or whose formatted output does not keep its
// syntax tree, gets a diagnostic instead of a result.
func (f *Formatter) Format(snapshot *project.Snapshot) Report {
	report := Report{Results: []Result{}, Diagnostics: []Diagnostic{}}
	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script.ParseError != nil {
			report.Diagnostics = append(report.Diagnostics, parseDiagnostic(path, script.ParseError))
			continue
		}
		formatted := []byte(f.emit(script.File, f.options))
		if bytes.Equal(formatted, script.Source) {
			report.Results = append(report.Results, Result{Path: path})
			continue
		}
		if err := verify(path, script.Source, formatted, f.options); err != nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Rule: ruleUnsafe, Message: err.Error(), Path: path, Line: 1, Column: 1,
			})
			continue
		}
		report.Results = append(report.Results, Result{Path: path, Changed: true, Formatted: formatted})
	}
	report.sort()
	return report
}

// parseDiagnostic reports a parse failure at the position the parser gave, or
// at the start of the file when the error carries none.
func parseDiagnostic(path string, parseError error) Diagnostic {
	diagnostic := Diagnostic{Rule: ruleSourceParse, Message: parseError.Error(), Path: path, Line: 1, Column: 1}
	var located *parser.Error
	if errors.As(parseError, &located) {
		diagnostic.Message = located.Message
		if start := located.Token.Span.Start; start.Line > 0 {
			diagnostic.Line = start.Line
			diagnostic.Column = max(start.Column, 1)
		}
	}
	return diagnostic
}
