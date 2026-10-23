// Package assets answers one question about a Godot project: is anything
// still using this file?
//
// It is a whole-project reachability analysis, not a per-file check. Every
// file is a node, every resource reference is an edge, and project.godot
// supplies the roots. What the mark never reaches is what the project has
// stopped using.
//
// The analysis cannot be sound, and the package is built around admitting so
// rather than hiding it. A path assembled at runtime, a resource loaded from
// C# or a GDExtension, a scene a plugin discovers by scanning a directory:
// none of these leave evidence a parser can read. So findings come in two
// tiers of confidence, every unreadable load site is reported as a finding of
// its own, and the tool has no write subcommand. Its output is a review list.
package assets

// The rule names, which are a public contract: they appear in JSON output, in
// the severity map of a project's config, and in --disable. Renaming one
// breaks every project that referenced it.
const (
	// RuleUnreferenced is a file nothing in the project names. It is the
	// confident tier: no edge points at it at all.
	RuleUnreferenced = "asset.unreferenced"
	// RuleUnreachable is a file that is referenced, but only by files that
	// are themselves unreachable — a dead cluster that keeps itself alive.
	// Deleting the unreferenced file at its head is what exposes it, so this
	// is reported separately and is a warning by default.
	RuleUnreachable = "asset.unreachable"
	// RuleDynamic is a load whose path is not a literal. It is not a defect;
	// it is the disclosure that reachability is incomplete here, and it names
	// the line so the reader can judge what the analysis could not.
	RuleDynamic = "asset.dynamic"
	// RuleMissing is a reference that resolves to no file.
	RuleMissing = "asset.missing"
)

// Rules is every rule name, in report order.
var Rules = []string{RuleUnreferenced, RuleUnreachable, RuleDynamic, RuleMissing}

// IsRule reports a known rule name.
func IsRule(name string) bool {
	for _, rule := range Rules {
		if rule == name {
			return true
		}
	}
	return false
}

// Severity is how a finding affects the exit code.
type Severity string

const (
	// SeverityError fails the run.
	SeverityError Severity = "error"
	// SeverityWarning prints without failing.
	SeverityWarning Severity = "warning"
)

// Diagnostic is one finding.
type Diagnostic struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	// Path is the file the finding is about for asset.unreferenced and
	// asset.unreachable, and the file holding the reference for asset.missing
	// and asset.dynamic. Those two report where a reader would go to edit.
	Path string `json:"path"`
	// Line is the reference site, and 0 for a finding about a whole file.
	Line int `json:"line,omitempty"`
	// Target is the reference as written, for the two rules that have one.
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

// Report is the result of one check.
type Report struct {
	// Diagnostics is sorted by path, line, rule.
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Inventoried is how many files the project holds, and Reportable how
	// many of those could have appeared in a finding. The difference is what
	// asset_extensions and the exclusions hid, and it is reported because a
	// clean run otherwise cannot be told from one that looked at nothing.
	Inventoried int `json:"inventoried"`
	Reportable  int `json:"reportable"`
}

// Failed reports whether the run should exit nonzero.
func (r Report) Failed() bool {
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == SeverityError {
			return true
		}
	}
	return false
}
