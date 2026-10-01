// Package lint reports style and correctness problems in GDScript sources.
// Its rule names, text output, and inline suppression comments match gdlint
// from godot-gdscript-toolkit so the two are interchangeable in CI.
package lint

import (
	"fmt"
	"sort"
	"strings"
)

// Severity is the importance of a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one rule violation at one source position.
type Diagnostic struct {
	Rule      string   `json:"rule"`
	Severity  Severity `json:"severity"`
	Message   string   `json:"message"`
	Path      string   `json:"path"`
	Line      int      `json:"line"`
	Column    int      `json:"column"`
	EndLine   int      `json:"end_line,omitempty"`
	EndColumn int      `json:"end_column,omitempty"`
}

// String renders the diagnostic in gdlint's text form, so output handling
// written against gdlint keeps working.
func (d Diagnostic) String() string {
	label := string(d.Severity)
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	return fmt.Sprintf("%s:%d: %s: %s (%s)", d.Path, d.Line, label, d.Message, d.Rule)
}

// Report is the deterministic result of one lint run.
type Report struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasFindings reports whether anything was found.
func (r Report) HasFindings() bool { return len(r.Diagnostics) > 0 }

func (r *Report) sort() {
	sort.SliceStable(r.Diagnostics, func(i, j int) bool {
		a, b := r.Diagnostics[i], r.Diagnostics[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
}
