// Package architecture analyzes GDScript projects and enforces dependency rules.
package architecture

import (
	"fmt"
	"sort"
)

// Location identifies a position in a project-relative source file.
type Location struct {
	Path   string `json:"path"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// Classification is the architectural identity assigned to a file.
type Classification struct {
	Layer   string `json:"layer"`
	Feature string `json:"feature"`
}

// File describes an indexed GDScript file.
type File struct {
	Path           string         `json:"path"`
	ClassName      string         `json:"class_name,omitempty"`
	Classification Classification `json:"classification"`
}

// EdgeKind explains how a dependency was discovered.
type EdgeKind string

const (
	ClassReference EdgeKind = "class"
	ResourceLoad   EdgeKind = "resource"
)

// Edge is a resolved dependency between project files.
type Edge struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Kind     EdgeKind `json:"kind"`
	Symbol   string   `json:"symbol"`
	Location Location `json:"location"`
}

// Severity is the importance of a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one policy or source-code problem.
type Diagnostic struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
	Target   string   `json:"target,omitempty"`
	Symbol   string   `json:"symbol,omitempty"`
}

func (d Diagnostic) String() string {
	where := d.Location.Path
	if d.Location.Line > 0 {
		where += fmt.Sprintf(":%d:%d", d.Location.Line, d.Location.Column)
	}
	return fmt.Sprintf("%s: %s [%s]", where, d.Message, d.Rule)
}

// Report is the deterministic result of one analysis run.
type Report struct {
	Files       []File       `json:"files"`
	Edges       []Edge       `json:"edges"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasErrors reports whether any unsuppressed error was found.
func (r Report) HasErrors() bool {
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (r *Report) sort() {
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].Path < r.Files[j].Path })
	sort.Slice(r.Edges, func(i, j int) bool {
		a, b := r.Edges[i], r.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		return a.Symbol < b.Symbol
	})
	sort.Slice(r.Diagnostics, func(i, j int) bool {
		a, b := r.Diagnostics[i], r.Diagnostics[j]
		if a.Location.Path != b.Location.Path {
			return a.Location.Path < b.Location.Path
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		if a.Location.Column != b.Location.Column {
			return a.Location.Column < b.Location.Column
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
}
