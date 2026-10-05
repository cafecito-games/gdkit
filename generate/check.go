package generate

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/gdkit/format"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// Generator computes the generated region of every class that opted in.
type Generator struct {
	compiled *compiled
	options  gdformat.Options
	gap      int
	// emit renders one generator's text. It is a field so a test can stand in
	// an emitter that damages its output and prove the damage is refused,
	// which is the only way to reach verify from Check.
	emit func(Emitter, *Class, *Index, *Capabilities) (string, []Diagnostic)
}

// New builds a Generator, rejecting an invalid config.
//
// It takes the project's format config as well, because the region is
// canonicalised with it: without that, gen write and format check would fight
// over the region forever.
func New(config Config, formatting format.Config) (*Generator, error) {
	compiledConfig, err := config.compile()
	if err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := formatting.Validate(); err != nil {
		return nil, err
	}
	options, err := formatting.Options()
	if err != nil {
		return nil, err
	}
	return &Generator{
		compiled: compiledConfig,
		options:  options,
		gap:      formatting.BlankLines.TopLevel,
		emit: func(emitter Emitter, class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
			return emitter.Emit(class, index, capabilities)
		},
	}, nil
}

// Candidate is a file generate can rewrite, and the contents to write.
type Candidate struct {
	Path     string
	Class    string
	Contents []byte
	Region   Span
	Changed  bool
}

// Plan is the outcome of one run. A class with a blocking diagnostic
// contributes no candidate.
type Plan struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
	// Orphans are the paths holding a region whose class no longer opts in.
	// --prune removes these; without it they are reported and left alone.
	Orphans []string
}

// Report renders the plan's public, JSON-shaped result.
func (p Plan) Report() Report {
	report := Report{Results: []Result{}, Diagnostics: append([]Diagnostic{}, p.Diagnostics...)}
	for _, candidate := range p.Candidates {
		report.Results = append(report.Results, Result{Path: candidate.Path, Changed: candidate.Changed})
		if candidate.Changed {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Rule:    ruleStale,
				Message: "generated region is missing or out of date",
				Path:    candidate.Path,
				Line:    1,
				Column:  1,
			})
		}
	}
	report.sort()
	return report
}

// HasBlockers reports a diagnostic that is not mere staleness, which is what
// decides gen write's exit code: fixing staleness is the point of write.
func (p Plan) HasBlockers() bool { return len(p.Diagnostics) > 0 }

// Check computes the plan. It performs no I/O.
//
// Resolve, emit, and verify run as a loop, because a generate.unsafe refusal is
// only discovered after emission and it withdraws a capability a descendant may
// already have composed with. Without the loop, a parent whose region was
// refused leaves a child emitting super.equals against a method that was never
// written. Blockers only accumulate and are bounded by the number of classes,
// so this terminates.
func (g *Generator) Check(snapshot *project.Snapshot) Plan {
	index := BuildIndex(snapshot)
	index.HelpersClass = index.ByClassName[helpersClassName]
	requested, markers := g.resolveOptIn(snapshot, index)
	blockers := g.localBlockers(index, requested, markers)
	// An unsafe refusal is reported from the pass that found it. A later pass
	// only knows the class is blocked, so re-deriving the message there would
	// replace "the result is not formatted" with a generic one and lose the
	// reason.
	refusals := []Diagnostic{}
	refused := map[string]bool{}
	for {
		capabilities := Resolve(index, requested, blockers)
		plan := g.buildPlan(snapshot, index, requested, capabilities, blockers)
		newBlocker := false
		for _, diagnostic := range plan.Diagnostics {
			if diagnostic.Rule != ruleUnsafe || blockers[diagnostic.Path] {
				continue
			}
			blockers[diagnostic.Path] = true
			refused[diagnostic.Path] = true
			refusals = append(refusals, diagnostic)
			newBlocker = true
		}
		if newBlocker {
			continue
		}
		kept := make([]Diagnostic, 0, len(plan.Diagnostics))
		for _, diagnostic := range plan.Diagnostics {
			// The generic "another unresolved problem" message for a class
			// already refused with its real reason adds nothing.
			if refused[diagnostic.Path] && diagnostic.Rule == ruleUnsupported {
				continue
			}
			kept = append(kept, diagnostic)
		}
		plan.Diagnostics = append(append(markers, refusals...), kept...)
		return plan
	}
}

// resolveOptIn decides which classes asked for which generators.
//
// A class-level directive is a direct ast.Comment statement of a class suite,
// and nothing else is a directive at all. Span containment cannot work: a
// class's body span also covers its functions, so comparing offsets would let a
// marker written inside a method opt the class in.
func (g *Generator) resolveOptIn(snapshot *project.Snapshot, index *Index) (map[string][]string, []Diagnostic) {
	requested := map[string][]string{}
	diagnostics := []Diagnostic{}
	selected := map[string]bool{}
	for _, path := range snapshot.Selected {
		selected[path] = true
	}
	for _, id := range sortedKeys(index.Classes) {
		class := index.Classes[id]
		if !selected[class.Path] {
			continue
		}
		script := snapshot.Scripts[class.Path]
		names, ignored, err := directivesOf(class, script)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Rule: ruleMarker, Message: err.Error(),
				Path: class.Path, Line: class.Line, Column: 1,
			})
			continue
		}
		if class.Inner {
			// An inner class can never be a generation target, and a marker on
			// one must be reported rather than quietly doing nothing.
			if names != nil || ignored {
				diagnostics = append(diagnostics, Diagnostic{
					Rule:    ruleUnsupported,
					Message: fmt.Sprintf("the inner class %q cannot be generated for", class.Name),
					Path:    class.Path, Line: class.Line, Column: 1,
				})
			}
			continue
		}
		// Precedence is ignore > directive > config: an opt-out that config can
		// override is not an opt-out.
		if ignored {
			continue
		}
		if names == nil {
			names = g.compiled.generatorsFor(class.Path)
		}
		if len(names) > 0 {
			requested[id] = names
		}
	}
	return requested, diagnostics
}

// directivesOf reads the directive comments a class owns directly.
func directivesOf(class *Class, script *project.Script) (names []string, ignored bool, err error) {
	for _, statement := range class.Statements {
		comment, ok := statement.(*ast.Comment)
		if !ok {
			continue
		}
		line := script.Line(lineAt(script, comment.Span().Start.Offset))
		if !standsAlone(line) {
			continue
		}
		if MatchIgnore(line) {
			ignored = true
			continue
		}
		listed, matchErr := MatchGenerate(line)
		if matchErr != nil {
			return nil, false, matchErr
		}
		if listed != nil {
			names = append(names, listed...)
		}
	}
	return names, ignored, nil
}

// localBlockers marks every requested class carrying a blocking condition that
// is knowable before emission.
//
// They are computed before the first Resolve rather than discovered after it:
// the transfer function lists marker, conflict, and inner-class as demotion
// conditions, so a pass that has not seen them cannot be correct — a conflicted
// parent would look like a provider and its child would compose with a method
// that is never emitted. Only generate.unsafe is genuinely late.
func (g *Generator) localBlockers(index *Index, requested map[string][]string, markers []Diagnostic) map[string]bool {
	blockers := map[string]bool{}
	for _, diagnostic := range markers {
		if diagnostic.Rule == ruleMarker {
			blockers[diagnostic.Path] = true
		}
	}
	for id, names := range requested {
		class := index.Classes[id]
		if class == nil || class.Inner || class.RegionError != nil {
			blockers[id] = true
			continue
		}
		for _, emitter := range inRegistryOrder(names) {
			for _, signature := range emitter.Signatures() {
				declared, ok := class.Methods[signature.Name]
				if !ok || declared.InRegion {
					continue
				}
				if declared.Signature != signature {
					blockers[id] = true
				}
			}
		}
	}
	return blockers
}

// buildPlan emits, canonicalises, splices, and verifies each requested class.
func (g *Generator) buildPlan(snapshot *project.Snapshot, index *Index, requested map[string][]string, capabilities *Capabilities, blockers map[string]bool) Plan {
	plan := Plan{Candidates: []Candidate{}, Diagnostics: []Diagnostic{}, Orphans: []string{}}

	// Parse failures are universe-wide when an inheritance-sensitive generator
	// is requested, because inheritance is a reverse dependency: an unreadable
	// file may be a field-adding subclass that no refusal rule can see.
	if g.anySensitive(requested) {
		for _, path := range index.ParseFailures {
			plan.Diagnostics = append(plan.Diagnostics, Diagnostic{
				Rule: ruleSourceParse, Message: "file could not be parsed",
				Path: path, Line: 1, Column: 1,
			})
		}
	} else {
		for _, path := range index.ParseFailures {
			if snapshot.Scripts[path] == nil {
				continue
			}
			for _, selected := range snapshot.Selected {
				if selected == path {
					plan.Diagnostics = append(plan.Diagnostics, Diagnostic{
						Rule: ruleSourceParse, Message: "file could not be parsed",
						Path: path, Line: 1, Column: 1,
					})
				}
			}
		}
	}

	// Orphan detection scans the whole universe, not just targets: otherwise
	// adding a path to exclude would make its region invisible to both the
	// diagnostic and --prune, so the most likely way to orphan a region would
	// also be the way to hide it.
	for _, id := range sortedKeys(index.Classes) {
		class := index.Classes[id]
		if class.Inner || !class.HasRegion || len(requested[id]) > 0 {
			continue
		}
		plan.Orphans = append(plan.Orphans, class.Path)
		plan.Diagnostics = append(plan.Diagnostics, Diagnostic{
			Rule:    ruleOrphaned,
			Message: "generated region's class no longer opts in; gen write --prune removes it",
			Path:    class.Path, Line: class.Line, Column: 1,
		})
	}

	for _, id := range sortedKeys(requested) {
		class := index.Classes[id]
		if class == nil {
			continue
		}
		if diagnostic := g.refusal(index, class, requested[id], capabilities, blockers); diagnostic != nil {
			plan.Diagnostics = append(plan.Diagnostics, *diagnostic)
			continue
		}
		candidate, diagnostic := g.candidate(snapshot, index, class, requested[id], capabilities)
		if diagnostic != nil {
			plan.Diagnostics = append(plan.Diagnostics, *diagnostic)
			continue
		}
		if candidate != nil {
			plan.Candidates = append(plan.Candidates, *candidate)
		}
	}
	return plan
}

// anySensitive reports whether any requested generator depends on the
// inheritance graph.
func (g *Generator) anySensitive(requested map[string][]string) bool {
	for _, names := range requested {
		for _, emitter := range inRegistryOrder(names) {
			if emitter.NeedsInheritanceGraph() {
				return true
			}
		}
	}
	return false
}

// refusal returns the diagnostic that replaces a class's candidate, or nil.
func (g *Generator) refusal(index *Index, class *Class, names []string, capabilities *Capabilities, blockers map[string]bool) *Diagnostic {
	if class.RegionError != nil {
		return &Diagnostic{
			Rule: ruleMarker, Message: class.RegionError.Error(),
			Path: class.Path, Line: class.Line, Column: 1,
		}
	}
	for _, emitter := range inRegistryOrder(names) {
		for _, signature := range emitter.Signatures() {
			declared, ok := class.Methods[signature.Name]
			if ok && !declared.InRegion && declared.Signature != signature {
				// GDScript has no overloading, so emitting ours alongside this
				// would be a duplicate declaration and the file would not load.
				return &Diagnostic{
					Rule: ruleConflict,
					Message: fmt.Sprintf("%q is already declared with a different signature, so %s cannot emit it",
						signature.Name, emitter.Name()),
					Path: class.Path, Line: declared.Line, Column: 1,
				}
			}
			if capabilities.Realizable(class.ID, signature) {
				continue
			}
			return &Diagnostic{
				Rule: ruleUnsupported, Message: g.whyRefused(index, class, signature, capabilities, blockers),
				Path: class.Path, Line: class.Line, Column: 1,
			}
		}
	}
	return nil
}

// whyRefused explains a demotion in the terms the reader can act on.
func (g *Generator) whyRefused(index *Index, class *Class, signature Signature, capabilities *Capabilities, blockers map[string]bool) string {
	switch {
	case class.Inner:
		return fmt.Sprintf("the inner class %q cannot be generated for", class.Name)
	case blockers[class.ID]:
		return fmt.Sprintf("%s cannot be generated while this class has another unresolved problem", signature.Name)
	case index.ReachesCycle(class.ID):
		return fmt.Sprintf("%s needs the inheritance graph, and this class's ancestry reaches a cycle", signature.Name)
	case capabilities.UniverseCause != "":
		return fmt.Sprintf("%s needs the whole inheritance graph, and it is incomplete: %s",
			signature.Name, capabilities.UniverseCause)
	case signature == deepEqualsSignature && index.HelpersClass == nil:
		return fmt.Sprintf(
			"deep_equals calls %s, which this project does not declare; install the gdkit addon "+
				"(gpm add --name gdkit --source git --url https://github.com/cafecito-games/gdkit.git "+
				"--source-path addons/gdkit)", helpersClassName)
	case signature == deepEqualsSignature && !hasHelpersClass(index):
		return fmt.Sprintf(
			"%s is declared at %s but has no static deep_equals(p_lhs, p_rhs), so the installed gdkit addon is too old",
			helpersClassName, index.HelpersClass.Path)
	case signature == deepEqualsSignature && index.FieldTypeCycle(class.ID):
		return fmt.Sprintf(
			"deep_equals cannot be shown to terminate: this class's field types form a cycle through %s",
			strings.Join(index.FieldTypeTargets(class.ID), ", "))
	case ancestryHasFields(index, class.ID):
		nearest := "its base class"
		if parent := index.Classes[class.ParentID]; parent != nil {
			nearest = parent.Name
		}
		return fmt.Sprintf("%s compares inherited fields through super, so %s must generate or declare it too",
			signature.Name, nearest)
	default:
		for _, descendant := range index.Descendants(class.ID) {
			if pathBetweenHasFields(index, class.ID, descendant) {
				return fmt.Sprintf("%s would be inherited by %q, which adds fields it does not compare; opt that class in too",
					signature.Name, index.Classes[descendant].Name)
			}
		}
		return fmt.Sprintf("%s cannot be generated for this class", signature.Name)
	}
}

// candidate emits the region and splices it, or returns the diagnostic that
// refused it. It returns (nil, nil) when a compatible hand-written method
// already satisfies every requested signature.
func (g *Generator) candidate(snapshot *project.Snapshot, index *Index, class *Class, names []string, capabilities *Capabilities) (*Candidate, *Diagnostic) {
	script := snapshot.Scripts[class.Path]
	bodies := []string{}
	for _, emitter := range inRegistryOrder(names) {
		if g.satisfied(class, emitter) {
			continue
		}
		text, diagnostics := g.emit(emitter, class, index, capabilities)
		if len(diagnostics) > 0 {
			diagnostic := diagnostics[0]
			return nil, &diagnostic
		}
		bodies = append(bodies, text)
	}
	if len(bodies) == 0 {
		if !class.HasRegion {
			return nil, nil
		}
		// Everything is satisfied by hand, so the region holds nothing we
		// would emit; it is an orphan rather than a candidate.
		return nil, nil
	}
	region, err := g.canonicalise(bodies)
	if err != nil {
		return nil, &Diagnostic{
			Rule: ruleUnsafe, Message: err.Error(),
			Path: class.Path, Line: class.Line, Column: 1,
		}
	}
	contents, span, err := Splice(script.Source, region, g.gap)
	if err != nil {
		return nil, &Diagnostic{
			Rule: ruleMarker, Message: err.Error(),
			Path: class.Path, Line: class.Line, Column: 1,
		}
	}
	candidate := &Candidate{
		Path:     class.Path,
		Class:    class.ID,
		Contents: contents,
		Region:   span,
		Changed:  string(contents) != string(script.Source),
	}
	if err := g.verify(script, class, *candidate); err != nil {
		return nil, &Diagnostic{
			Rule: ruleUnsafe, Message: err.Error(),
			Path: class.Path, Line: class.Line, Column: 1,
		}
	}
	return candidate, nil
}

// satisfied reports that a compatible hand-written method already provides
// everything the generator would emit, so nothing is emitted for it.
func (g *Generator) satisfied(class *Class, emitter Emitter) bool {
	for _, signature := range emitter.Signatures() {
		declared, ok := class.Methods[signature.Name]
		if !ok || declared.InRegion || declared.Signature != signature {
			return false
		}
	}
	return true
}

// canonicalise formats the region, sentinels included, in isolation.
//
// Formatting the spliced file instead would let gdparser reformat code outside
// the region whenever the file was not already canonical, which is a partial
// format write wearing gen's name. Formatting the region alone is sound only
// because it sits at indent 0, which is the honest reason v1 is limited to
// top-level classes.
//
// The sentinels go through the formatter rather than being wrapped around its
// output, because gdparser decides the spacing around them and it is not the
// spacing one would guess. Both sentinels are comment runs, and blankLineGaps
// attributes a top_level gap to a comment run, so the canonical form carries
// blank lines before the *end* sentinel as well as before the begin one.
// Predicting that here and wrapping afterwards produced a region the full-file
// oracle rejected; letting the formatter produce the whole region makes it
// canonical by construction.
func (g *Generator) canonicalise(bodies []string) (string, error) {
	separator := strings.Repeat("\n", g.gap)
	region := beginSentinel + "\n" + strings.Join(bodies, separator) + endSentinel + "\n"
	file, err := gdparser.Parse([]byte(region))
	if err != nil {
		return "", fmt.Errorf("the generated code does not parse: %w", err)
	}
	formatted := gdformat.FileWithOptions(file, g.options)
	if !strings.HasSuffix(formatted, "\n") {
		formatted += "\n"
	}
	return formatted, nil
}
