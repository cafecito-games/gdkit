package uid

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/project"
)

// scriptExtension is what scopes the duplicate diagnostic to GDScript; the
// missing one is scoped by Snapshot.Paths, which holds nothing else. Godot
// writes sidecars beside shaders and other text resources too, but gdkit does
// not know which of those files Godot would have given one, so it neither asks
// for a missing one nor reissues a colliding one. A sidecar that exists is
// checked and resolved whatever it sits beside.
const scriptExtension = ".gd"

// Check examines every identity the project declares and every reference to
// one. It performs no I/O and changes nothing; Apply acts on the report it
// returns.
//
// The snapshot should come from a project.Config with Identities set, which is
// what builds the table a reference is resolved against. Without it only the
// sidecars are known, so Check reports what it can see and no reference
// diagnostics at all.
func Check(snapshot *project.Snapshot) Report {
	report := Report{Scripts: len(snapshot.Paths), Diagnostics: []Diagnostic{}}

	claims := identityClaims(snapshot)
	declarations, table := resolveClaims(claims)

	report.reportDeclarations(claims)
	report.reportMissing(snapshot)
	report.reportDuplicates(snapshot)
	report.reportReferences(snapshot, declarations, table)
	report.planAdoptions(snapshot)
	// Sorted before the plan is built, so the order a repair works in — and
	// so the order it reports having written files in — is the report's own.
	report.sort()
	report.planReissues(snapshot, declarations, claims)
	// The rewrites are collected in two passes, so they are ordered here
	// rather than by the pass that found them: the files a run reports having
	// written come out in path order either way.
	sort.SliceStable(report.work.rewrites, func(i, j int) bool {
		a, b := report.work.rewrites[i].reference, report.work.rewrites[j].reference
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	return report
}

// identityClaims returns every declaration in the snapshot. A snapshot loaded
// without project.Config.Identities carries none, so the sidecars stand in for
// them: that gives the three sidecar diagnostics their old behavior rather
// than silently reporting a project as clean.
func identityClaims(snapshot *project.Snapshot) []project.Claim {
	if len(snapshot.Claims) > 0 || len(snapshot.Sidecars) == 0 {
		return snapshot.Claims
	}
	claims := make([]project.Claim, 0, len(snapshot.Sidecars))
	for _, sidecar := range snapshot.Sidecars {
		claims = append(claims, project.Claim{
			UID: sidecar.Text, Owner: sidecar.Owner, Path: sidecar.Path,
			Line: 1, Kind: project.ClaimSidecar,
		})
	}
	return claims
}

// resolveClaims returns the identity each file declares and the file each
// identifier resolves to.
//
// Two claims can name one file and two files can claim one identifier, and
// both are decided the same way: a sidecar wins over a resource header, which
// wins over a .import file, and the lowest path wins a tie. That mirrors how
// project.Snapshot.UIDs prefers a sidecar, and it agrees with the duplicate
// diagnostic, which leaves the identifier with the first claimant in path
// order. A claim .gdkitignore hides takes part in both: Godot has never heard
// of the ignore file, so a hidden file still owns its identity.
func resolveClaims(claims []project.Claim) (map[string]project.Claim, map[uint64]string) {
	declarations := make(map[string]project.Claim, len(claims))
	for _, claim := range claims {
		if existing, found := declarations[claim.Owner]; found && !preferred(claim, existing) {
			continue
		}
		declarations[claim.Owner] = claim
	}
	owners := make([]string, 0, len(declarations))
	for owner := range declarations {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	table := make(map[uint64]string, len(declarations))
	winners := make(map[uint64]project.Claim, len(declarations))
	for _, owner := range owners {
		claim := declarations[owner]
		id, valid := Decode(claim.UID)
		if !valid {
			continue
		}
		if existing, found := winners[id]; found && !preferred(claim, existing) {
			continue
		}
		winners[id], table[id] = claim, owner
	}
	return declarations, table
}

// position identifies one reference by the line it sits on.
type position struct {
	path string
	line int
}

// preferred reports whether claim beats existing: by mechanism first, then by
// the path the declaration lives in.
func preferred(claim, existing project.Claim) bool {
	if rank(claim.Kind) != rank(existing.Kind) {
		return rank(claim.Kind) < rank(existing.Kind)
	}
	return claim.Path < existing.Path
}

func rank(kind project.ClaimKind) int {
	switch kind {
	case project.ClaimSidecar:
		return 0
	case project.ClaimHeader:
		return 1
	default:
		return 2
	}
}

// reportDeclarations reports every declared identity that does not name an
// identifier Godot could have written, wherever it is declared. A declaration
// .gdkitignore hides is resolved but not reported, exactly as a hidden script
// is not reported for having no identity at all.
func (r *Report) reportDeclarations(claims []project.Claim) {
	for _, claim := range claims {
		if claim.Ignored {
			continue
		}
		if _, valid := Decode(claim.UID); valid {
			continue
		}
		r.Diagnostics = append(r.Diagnostics, Diagnostic{
			Rule:    RuleMalformed,
			Message: malformedMessage(claim),
			Path:    claim.Owner,
			UID:     claim.UID,
		})
	}
}

func malformedMessage(claim project.Claim) string {
	switch claim.Kind {
	case project.ClaimHeader:
		return fmt.Sprintf("the header of %s does not declare a uid Godot could have written: %s",
			claim.Path, describe(claim.UID))
	case project.ClaimImport:
		return fmt.Sprintf("%s does not declare a uid Godot could have written: %s",
			claim.Path, describe(claim.UID))
	default:
		return fmt.Sprintf("%s does not hold a uid Godot could have written: %s",
			claim.Path, describe(claim.UID))
	}
}

// reportMissing reports every script with no sidecar beside it.
func (r *Report) reportMissing(snapshot *project.Snapshot) {
	owners := make(map[string]struct{}, len(snapshot.Sidecars))
	for _, sidecar := range snapshot.Sidecars {
		owners[sidecar.Owner] = struct{}{}
	}
	for _, path := range snapshot.Paths {
		if _, found := owners[path]; found {
			continue
		}
		r.Diagnostics = append(r.Diagnostics, Diagnostic{
			Rule:    RuleMissing,
			Message: fmt.Sprintf("no %s.uid sidecar, so the script has no stable identity", path),
			Path:    path,
		})
	}
}

// reportDuplicates reports a script whose sidecar claims an identifier another
// script's sidecar already claims. It is scoped to scripts because that is
// what a reissue can fix without touching anything Godot wrote: a scene and a
// script that collide are reported from the reference side instead, as
// RuleCrossed, which names the reference that loads the wrong file.
func (r *Report) reportDuplicates(snapshot *project.Snapshot) {
	claimed := make(map[uint64][]string)
	for _, sidecar := range snapshot.Sidecars {
		if !strings.HasSuffix(sidecar.Owner, scriptExtension) {
			continue
		}
		if id, valid := Decode(sidecar.Text); valid {
			claimed[id] = append(claimed[id], sidecar.Owner)
		}
	}
	for id, paths := range claimed {
		if len(paths) < 2 {
			continue
		}
		// The first path in sorted order keeps the identifier and the rest
		// are reported, so both this report and any repair of it are the
		// same on every run.
		sort.Strings(paths)
		for _, path := range paths[1:] {
			r.Diagnostics = append(r.Diagnostics, Diagnostic{
				Rule:    RuleDuplicate,
				Message: fmt.Sprintf("%s is already claimed by %s", Encode(id), paths[0]),
				Path:    path,
				UID:     Encode(id),
			})
		}
	}
}

// reportReferences resolves every uid:// reference against the identity table
// and reports the two ways it can be wrong. A reference that resolves to the
// file its path names is clean and says nothing.
func (r *Report) reportReferences(snapshot *project.Snapshot, declarations map[string]project.Claim, table map[uint64]string) {
	for _, reference := range snapshot.References {
		owner, resolved := "", false
		if id, valid := Decode(reference.UID); valid {
			owner, resolved = table[id]
		}
		if resolved && (reference.Target == "" || owner == reference.Target) {
			continue
		}
		replacement, repairable := repairTarget(reference, declarations, table)
		diagnostic := Diagnostic{
			Path:       reference.Path,
			UID:        reference.UID,
			Line:       reference.Line,
			Target:     reference.Target,
			repairable: repairable,
		}
		if resolved {
			diagnostic.Rule = RuleCrossed
			diagnostic.Message = fmt.Sprintf("%s is the identity of %s, not of %s",
				reference.UID, owner, reference.Target)
		} else {
			diagnostic.Rule = RuleDangling
			diagnostic.Message = fmt.Sprintf("%s is no file's identity%s",
				reference.UID, danglingRemedy(reference, repairable, replacement))
		}
		if repairable {
			diagnostic.Message += fmt.Sprintf("; %s declares %s", reference.Target, replacement)
		}
		r.Diagnostics = append(r.Diagnostics, diagnostic)
		if repairable {
			r.work.rewrites = append(r.work.rewrites, rewrite{reference: reference, to: replacement})
		}
	}
}

// planAdoptions repairs the references to a script the same run is about to
// give an identity to.
//
// A scene that names a script by path and by a uid the script does not have
// yet is the common shape of this: the sidecar is missing, so the reference
// resolves to nothing, and creating the sidecar alone would leave the scene
// still pointing at nothing. Writing the new identifier into the reference is
// what the editor would have done, and it is what makes one run enough.
func (r *Report) planAdoptions(snapshot *project.Snapshot) {
	adopting := make(map[string]struct{}, len(r.Diagnostics))
	for _, diagnostic := range r.Missing() {
		adopting[diagnostic.Path] = struct{}{}
	}
	if len(adopting) == 0 {
		return
	}
	for i := range r.Diagnostics {
		diagnostic := &r.Diagnostics[i]
		if diagnostic.Rule != RuleDangling && diagnostic.Rule != RuleCrossed || diagnostic.repairable {
			continue
		}
		if _, found := adopting[diagnostic.Target]; !found {
			continue
		}
		reference, found := referenceAt(snapshot, diagnostic)
		if !found || reference.Kind != project.ReferenceExternal {
			continue
		}
		diagnostic.repairable = true
		diagnostic.Message += fmt.Sprintf("; %s is given one by this run", diagnostic.Target)
		r.work.rewrites = append(r.work.rewrites, rewrite{reference: reference, adopt: diagnostic.Target})
	}
}

// referenceAt finds the reference a diagnostic was built from.
func referenceAt(snapshot *project.Snapshot, diagnostic *Diagnostic) (project.Reference, bool) {
	for _, reference := range snapshot.References {
		if reference.Path == diagnostic.Path && reference.Line == diagnostic.Line && reference.UID == diagnostic.UID {
			return reference, true
		}
	}
	return project.Reference{}, false
}

// danglingRemedy explains why a dangling reference is beyond repair, which is
// the part a reader of the report has to act on themselves.
func danglingRemedy(reference project.Reference, repairable bool, replacement string) string {
	switch {
	case repairable:
		return ""
	case reference.Target == "":
		return ", and the reference names no path to repair it from"
	case replacement != "":
		return fmt.Sprintf(", and %s does not own %s either", reference.Target, replacement)
	default:
		return fmt.Sprintf(", and %s declares none to point at", reference.Target)
	}
}

// repairTarget returns the identity a broken reference should name instead, and
// whether pointing it there is unambiguous.
//
// The path= beside the identifier is the authority: it is what Godot already
// falls back to, so rewriting the identifier to the identity that path declares
// cannot change what the project loads. Four things stop it being a repair: a
// reference that names no path, a path that declares no identity, a path whose
// identity is not one Godot could have written, and a path that does not
// actually own the identity it declares — the last is a duplicate, where the
// value is already what the reference holds and writing it again would fix
// nothing.
func repairTarget(reference project.Reference, declarations map[string]project.Claim, table map[uint64]string) (string, bool) {
	if reference.Target == "" {
		return "", false
	}
	claim, found := declarations[reference.Target]
	if !found {
		return "", false
	}
	id, valid := Decode(claim.UID)
	if !valid {
		return claim.UID, false
	}
	if table[id] != reference.Target || claim.UID == reference.UID {
		return claim.UID, false
	}
	return claim.UID, true
}

// planReissues records, for every declaration a repair would replace, the
// references that have to move with it so the old value is left nowhere in the
// project.
//
// A reference is attributed to a declaration two ways. A malformed value is
// attributed by text, because nothing else in the project can hold it — unless
// something does, in which case no reference is moved rather than the wrong
// one. A duplicated value is shared by construction, so only an [ext_resource]
// whose path= names the file being reissued can be attributed to it; a
// reference that names no path keeps resolving to the first claimant, which is
// what it already did.
func (r *Report) planReissues(snapshot *project.Snapshot, declarations map[string]project.Claim, claims []project.Claim) {
	declaring := make(map[string]int, len(claims))
	for _, claim := range claims {
		declaring[claim.UID]++
	}
	// A reference this run already repoints at an identity on disk is not
	// moved a second time by a reissue.
	taken := make(map[position]struct{}, len(r.work.rewrites))
	for _, rewrite := range r.work.rewrites {
		taken[position{rewrite.reference.Path, rewrite.reference.Line}] = struct{}{}
	}
	for _, diagnostic := range r.Repairs() {
		claim, found := declarations[diagnostic.Path]
		if !found {
			continue
		}
		entry := reissue{claim: claim}
		for _, reference := range snapshot.References {
			key := position{reference.Path, reference.Line}
			if _, used := taken[key]; used {
				continue
			}
			if !attributable(diagnostic, claim, reference, declaring) {
				continue
			}
			taken[key] = struct{}{}
			entry.references = append(entry.references, reference)
		}
		r.work.reissues = append(r.work.reissues, entry)
		r.markAttributed(entry.references)
	}
}

func attributable(diagnostic Diagnostic, claim project.Claim, reference project.Reference, declaring map[string]int) bool {
	if diagnostic.Rule == RuleMalformed {
		return reference.UID == claim.UID && declaring[claim.UID] == 1
	}
	if reference.Kind != project.ReferenceExternal || reference.Target != claim.Owner {
		return false
	}
	id, valid := Decode(reference.UID)
	if !valid {
		return false
	}
	declared, _ := Decode(claim.UID)
	return id == declared
}

// markAttributed records that a repair moves these references, so a run that
// repairs does not also report them as left behind.
func (r *Report) markAttributed(references []project.Reference) {
	for _, reference := range references {
		for i := range r.Diagnostics {
			diagnostic := &r.Diagnostics[i]
			if diagnostic.Path == reference.Path && diagnostic.Line == reference.Line &&
				(diagnostic.Rule == RuleDangling || diagnostic.Rule == RuleCrossed) {
				diagnostic.attributed = true
			}
		}
	}
}

// Remaining returns the diagnostics a write would leave behind, in report
// order: everything no part of the run addresses. Without repair that is every
// untrustworthy declaration as well, because reissuing one is what --repair
// asks for.
func (r Report) Remaining(repair bool) []Diagnostic {
	left := make([]Diagnostic, 0, len(r.Diagnostics))
	for _, diagnostic := range r.Diagnostics {
		switch diagnostic.Rule {
		case RuleMissing:
			continue
		case RuleMalformed, RuleDuplicate:
			if repair {
				continue
			}
		case RuleDangling, RuleCrossed:
			if diagnostic.repairable || repair && diagnostic.attributed {
				continue
			}
		}
		left = append(left, diagnostic)
	}
	return left
}

// describe renders a declaration's text for a message, quoted and shortened, so
// a sidecar holding a whole file cannot flood the output.
func describe(text string) string {
	if text == "" {
		return "the file is empty"
	}
	const limit = 40
	if len(text) > limit {
		return fmt.Sprintf("%q...", text[:limit])
	}
	return fmt.Sprintf("%q", text)
}
