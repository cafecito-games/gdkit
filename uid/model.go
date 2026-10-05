package uid

import (
	"fmt"
	"slices"
	"sort"

	"github.com/cafecito-games/gdkit/project"
)

// The rules a uid run reports. They are a public contract: they appear in JSON
// output, so renaming one breaks anything downstream that matches on them.
const (
	// RuleMissing marks a script with no .uid sidecar beside it.
	RuleMissing = "uid.missing"
	// RuleMalformed marks a declared identity whose text does not name an
	// identifier Godot could have written, wherever it is declared: a
	// sidecar, the header of a .tscn or .tres file, or a .import file. See
	// Decode.
	RuleMalformed = "uid.malformed"
	// RuleDuplicate marks a script whose sidecar claims an identifier
	// another script's sidecar already claims.
	RuleDuplicate = "uid.duplicate"
	// RuleDangling marks a uid:// reference that is no file's declared
	// identity. Godot falls back to the path= beside it and warns; a
	// reference that names no path, which is every one in a script, does not
	// resolve at all.
	RuleDangling = "uid.dangling"
	// RuleCrossed marks a reference whose uid:// is the identity of a file
	// other than the one its path= names. It resolves, so Godot loads the
	// wrong resource without warning, which makes it the more dangerous of
	// the two.
	RuleCrossed = "uid.crossed"
)

// Diagnostic is one problem found with an identity or a reference to one.
type Diagnostic struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	// Path is the file the problem concerns, project-relative and
	// slash-separated: the script whose identity is missing or untrustworthy,
	// the file that declares the identity, or the file holding the reference.
	Path string `json:"path"`
	// UID is the identifier involved: the text found in a malformed or
	// duplicated declaration, or the text a broken reference names. It is
	// empty for RuleMissing.
	UID string `json:"uid,omitempty"`
	// Line is the one-based line of Path that holds a broken reference. It is
	// zero for the rules whose position is the file itself.
	Line int `json:"line,omitempty"`
	// Target is the path a broken reference names alongside the identifier.
	// It is empty for a reference that names none, which is every reference
	// in a script, and for the rules that concern a declaration.
	Target string `json:"target,omitempty"`
	// repairable marks a reference Apply can point at the identity its
	// Target declares. It is not part of the JSON contract: a report decoded
	// from JSON is something to display, not something to write from.
	repairable bool
	// attributed marks a reference a repair moves because the declaration it
	// names is being reissued, rather than because the reference itself could
	// be resolved. It is not part of the JSON contract either.
	attributed bool
}

// String renders the diagnostic in the text form the other gdkit tools use. A
// declaration carries no line, because the file is the position; a reference
// carries the line it sits on.
func (d Diagnostic) String() string {
	if d.Line > 0 {
		return fmt.Sprintf("%s:%d: Error: %s (%s)", d.Path, d.Line, d.Message, d.Rule)
	}
	return fmt.Sprintf("%s: Error: %s (%s)", d.Path, d.Message, d.Rule)
}

// Report is the deterministic result of one uid run.
type Report struct {
	// Scripts is how many .gd files were examined.
	Scripts int `json:"scripts"`
	// Diagnostics are sorted by path, then line, then rule.
	Diagnostics []Diagnostic `json:"diagnostics"`
	// work is what Apply would do about them. It is computed by Check, which
	// is where the identity table lives, and is deliberately unexported: a
	// report that crossed a process boundary as JSON cannot be written from.
	work plan
}

// plan is the set of changes Apply makes to answer a report.
type plan struct {
	// reissues replace a declared identity that cannot be trusted. Apply only
	// performs them when asked to repair, because a new identifier changes
	// what every reference to the file resolves to.
	reissues []reissue
	// rewrites point a broken reference at the identity its path declares.
	// Apply always performs them: the path is the authority, and it is what
	// Godot already falls back to.
	rewrites []rewrite
}

// reissue is one declaration to replace, with the references that named the
// old value and so have to move with it.
type reissue struct {
	claim project.Claim
	// references are rewritten to the new value in the same run, so the old
	// one is left nowhere in the project. It is empty when no reference could
	// be attributed to this declaration alone.
	references []project.Reference
}

// rewrite is one reference to repoint at the identity of the file its path=
// names.
type rewrite struct {
	reference project.Reference
	// to is the identity already on disk to point at, and adopt names the
	// script whose freshly created sidecar to point at instead. Exactly one is
	// set: a reference to a script that has no identity yet is only repairable
	// because the same run is about to give it one, and Apply does not know
	// which identifier that will be until it mints it.
	to    string
	adopt string
}

// Moved reports how many uid:// references a write would rewrite: the ones it
// can resolve from the path beside them, plus — when repair is set — the ones
// that have to move with a reissued declaration. Those two are disjoint by
// construction, so no line is counted twice.
func (r Report) Moved(repair bool) int {
	moved := len(r.work.rewrites)
	if repair {
		for _, entry := range r.work.reissues {
			moved += len(entry.references)
		}
	}
	return moved
}

// HasDiagnostics reports whether anything is wrong with the project's
// identities.
func (r Report) HasDiagnostics() bool { return len(r.Diagnostics) > 0 }

// Missing returns the diagnostics for scripts that have no sidecar, in path
// order. Apply always writes these.
func (r Report) Missing() []Diagnostic { return r.selected(RuleMissing) }

// Repairs returns the diagnostics for declared identities that exist but
// cannot be trusted, in report order. Apply only reissues these when asked to
// repair, because a new identifier changes what every existing uid://
// reference to the file resolves to — including any this run cannot see.
func (r Report) Repairs() []Diagnostic {
	return r.selected(RuleMalformed, RuleDuplicate)
}

// Rewritable returns the broken references Apply repoints without being asked,
// in report order: the ones whose path= names a file with a trustworthy
// identity of its own.
func (r Report) Rewritable() []Diagnostic {
	return r.references(true)
}

// Reported returns the broken references no write can repair, in report order:
// a reference that names no path, and one whose path names a file with no
// identity of its own to point at. Giving a script's preload a value would
// mean guessing what was meant.
func (r Report) Reported() []Diagnostic {
	return r.references(false)
}

func (r Report) references(repairable bool) []Diagnostic {
	selected := make([]Diagnostic, 0, len(r.Diagnostics))
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Rule != RuleDangling && diagnostic.Rule != RuleCrossed {
			continue
		}
		if diagnostic.repairable == repairable {
			selected = append(selected, diagnostic)
		}
	}
	return selected
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
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Rule < b.Rule
	})
}
