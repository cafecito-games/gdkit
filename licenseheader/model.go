package licenseheader

import (
	"fmt"
	"sort"
)

const ruleMarker = "license.marker"

// Result is the planned outcome for one selected script.
type Result struct {
	Path     string `json:"path"`
	Changed  bool   `json:"changed"`
	Contents []byte `json:"-"`
}

// Diagnostic explains why gdkit refused to update one script.
type Diagnostic struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
}

// String renders the diagnostic in the text form used by other source tools.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d: Error: %s (%s)", d.Path, d.Line, d.Message, d.Rule)
}

// Report is the deterministic plan for one license run. A malformed block is
// a diagnostic rather than a result, so Apply can never rewrite it.
type Report struct {
	Results     []Result     `json:"results"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasChanges reports whether any selected file needs an insertion or update.
func (r Report) HasChanges() bool {
	for _, result := range r.Results {
		if result.Changed {
			return true
		}
	}
	return false
}

// HasDiagnostics reports whether any file has an ambiguous managed block.
func (r Report) HasDiagnostics() bool { return len(r.Diagnostics) > 0 }

// Changed returns the changed results in project-relative path order.
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
