package format

import (
	"fmt"
	"sort"
)

// The rules a format run reports. They are a public contract: they appear in
// JSON output.
const (
	// ruleSourceParse marks a file that could not be parsed, and so was left
	// alone.
	ruleSourceParse = "source-parse"
	// ruleUnsafe marks a file whose formatted output did not keep the syntax
	// tree or the tokens of its source, or would move a lint suppression
	// comment, and so was refused.
	ruleUnsafe = "format.unsafe"
)

// Result is the outcome of formatting one file that parsed and verified.
type Result struct {
	// Path is project-relative and slash-separated.
	Path string `json:"path"`
	// Changed reports that the canonical form differs from the file on disk.
	Changed bool `json:"changed"`
	// Formatted is the canonical source, set only when Changed is true.
	Formatted []byte `json:"-"`
}

// Diagnostic explains why one file was not formatted. Line and Column are
// 1-based.
type Diagnostic struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
}

// String renders the diagnostic in the same text form gdkit lint uses, so one
// output handler serves both tools.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d: Error: %s (%s)", d.Path, d.Line, d.Message, d.Rule)
}

// Report is the deterministic result of one format run. Every discovered file
// appears exactly once, as a result or as a diagnostic.
type Report struct {
	Results     []Result     `json:"results"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasChanges reports whether any file's canonical form differs from its source.
func (r Report) HasChanges() bool {
	for _, result := range r.Results {
		if result.Changed {
			return true
		}
	}
	return false
}

// HasDiagnostics reports whether any file could not be formatted.
func (r Report) HasDiagnostics() bool { return len(r.Diagnostics) > 0 }

// Changed returns the results whose canonical form differs from their source,
// in path order.
func (r Report) Changed() []Result {
	changed := make([]Result, 0, len(r.Results))
	for _, result := range r.Results {
		if result.Changed {
			changed = append(changed, result)
		}
	}
	return changed
}

func (r *Report) sort() {
	sort.SliceStable(r.Results, func(i, j int) bool { return r.Results[i].Path < r.Results[j].Path })
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
		return a.Rule < b.Rule
	})
}
