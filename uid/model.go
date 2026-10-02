package uid

import (
	"fmt"
	"slices"
	"sort"
)

// The rules a uid run reports. They are a public contract: they appear in JSON
// output, so renaming one breaks anything downstream that matches on them.
const (
	// RuleMissing marks a script with no .uid sidecar beside it.
	RuleMissing = "uid.missing"
	// RuleMalformed marks a sidecar whose contents do not name an
	// identifier Godot could have written. See Decode.
	RuleMalformed = "uid.malformed"
	// RuleDuplicate marks a script whose sidecar claims an identifier
	// another script's sidecar already claims.
	RuleDuplicate = "uid.duplicate"
)

// Diagnostic is one problem found with a script's identity. It carries no line
// or column: a sidecar holds a single identifier, so the file is the position.
type Diagnostic struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	// Path is the script the problem concerns, project-relative and
	// slash-separated. Its sidecar is this path plus ".uid".
	Path string `json:"path"`
	// UID is the identifier involved: the contents found in a malformed or
	// duplicated sidecar. It is empty for RuleMissing.
	UID string `json:"uid,omitempty"`
}

// String renders the diagnostic in the text form the other gdkit tools use,
// minus the line number they carry.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: Error: %s (%s)", d.Path, d.Message, d.Rule)
}

// Report is the deterministic result of one uid run.
type Report struct {
	// Scripts is how many .gd files were examined.
	Scripts int `json:"scripts"`
	// Diagnostics are sorted by path, then by rule.
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasDiagnostics reports whether anything is wrong with the project's
// identities.
func (r Report) HasDiagnostics() bool { return len(r.Diagnostics) > 0 }

// Missing returns the diagnostics for scripts that have no sidecar, in path
// order. Apply always writes these.
func (r Report) Missing() []Diagnostic { return r.selected(RuleMissing) }

// Repairs returns the diagnostics for sidecars that exist but cannot be
// trusted, in path order. Apply only rewrites these when asked to repair,
// because issuing a new identifier changes what every existing reference to
// the script resolves to.
func (r Report) Repairs() []Diagnostic {
	return r.selected(RuleMalformed, RuleDuplicate)
}

func (r Report) selected(rules ...string) []Diagnostic {
	selected := make([]Diagnostic, 0, len(r.Diagnostics))
	for _, diagnostic := range r.Diagnostics {
		if slices.Contains(rules, diagnostic.Rule) {
			selected = append(selected, diagnostic)
		}
	}
	return selected
}

func (r *Report) sort() {
	sort.SliceStable(r.Diagnostics, func(i, j int) bool {
		a, b := r.Diagnostics[i], r.Diagnostics[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Rule < b.Rule
	})
}
