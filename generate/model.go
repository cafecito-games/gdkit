// Package generate writes boilerplate value-object methods into a GDScript
// class that opted in, inside a sentinel-delimited region it owns.
//
// It is a whole-project analysis rather than a per-file one: equals composes
// with an ancestor's implementation and is refused when a descendant would
// inherit an unsound one, so both answers need the entire inheritance graph.
// Check is pure; Apply is the only writer.
package generate

import (
	"fmt"
	"sort"
)

// The rules a generate run reports. They are a public contract: they appear in
// JSON output and in configuration, so renaming one breaks user projects.
const (
	// ruleSourceParse marks a file that could not be parsed. It is reported
	// across the whole universe, not only the selection, when an
	// inheritance-sensitive generator is requested, because inheritance is a
	// reverse dependency: an unreadable file may be a field-adding subclass
	// that no refusal rule can see.
	ruleSourceParse = "source-parse"
	// ruleClassNameDuplicate marks two scripts claiming one class_name that a
	// requested class needs to resolve.
	ruleClassNameDuplicate = "class_name.duplicate"
	// ruleStale marks a region that is missing or out of date. Only check
	// reports it; for write, fixing it is the point.
	ruleStale = "generate.stale"
	// ruleMarker marks a malformed directive, an unknown generator name, or a
	// second region in one class.
	ruleMarker = "generate.marker"
	// ruleConflict marks a method declared outside the region with the same
	// name as one a requested generator emits, in an incompatible shape.
	// GDScript has no overloading, so emitting ours would not compile.
	ruleConflict = "generate.conflict"
	// ruleUnsupported marks a class generate refuses: an inner class, one
	// whose ancestry has fields but no provider, or one whose descendant would
	// inherit an unsound implementation.
	ruleUnsupported = "generate.unsupported"
	// ruleOrphaned marks a region whose class no longer opts in.
	ruleOrphaned = "generate.orphaned"
	// ruleUnsafe marks a candidate that verification or the format oracle
	// refused.
	ruleUnsafe = "generate.unsafe"
)

// Diagnostic explains why one class was not generated for. Line and Column are
// 1-based.
//
// There is no severity field: every rule here is an error. The deep_equals
// generator introduces the first warning, and with it both the field and
// Report.HasErrors; adding them now would make HasErrors and HasDiagnostics
// the same predicate.
type Diagnostic struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
}

// String renders the diagnostic in the text form gdkit lint and format use, so
// one output handler serves all three.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d: Error: %s (%s)", d.Path, d.Line, d.Message, d.Rule)
}

// Result is the outcome for one file that produced a candidate.
type Result struct {
	// Path is project-relative and slash-separated.
	Path string `json:"path"`
	// Changed reports that the candidate differs from the file on disk.
	Changed bool `json:"changed"`
}

// Report is the deterministic public result of one run. Every list is sorted,
// so JSON output is diffable and usable for CI annotations.
type Report struct {
	Results     []Result     `json:"results"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasChanges reports whether any file would be rewritten.
func (r Report) HasChanges() bool {
	for _, result := range r.Results {
		if result.Changed {
			return true
		}
	}
	return false
}

// HasDiagnostics reports whether any class was refused.
func (r Report) HasDiagnostics() bool { return len(r.Diagnostics) > 0 }

func (r *Report) sort() {
	sort.SliceStable(r.Results, func(i, j int) bool { return r.Results[i].Path < r.Results[j].Path })
	sort.SliceStable(r.Diagnostics, func(i, j int) bool {
		a, b := r.Diagnostics[i], r.Diagnostics[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Column != b.Column:
			return a.Column < b.Column
		default:
			return a.Rule < b.Rule
		}
	})
}
