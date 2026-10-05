package generate

import "fmt"

// The shapes the two generators emit. They are package variables so the
// emitters, the conflict check, and the tests agree on one definition.
var (
	// toStringSignature is Godot's virtual, which takes no arguments.
	toStringSignature = Signature{Name: "_to_string", Arity: 0}
	// equalsSignature is what must exist for other.equals(x) to be callable.
	equalsSignature = Signature{Name: "equals", Arity: 1}
	// deepEqualsSignature is the shape the generated code dispatches on at
	// runtime, so a hand-written method of this shape participates too.
	deepEqualsSignature = Signature{Name: "deep_equals", Arity: 1}
	// helpersSignature is the shared static comparison. Its arity is what
	// keeps it distinct from deepEqualsSignature, which is the instance
	// method it dispatches to.
	helpersSignature = Signature{Name: "deep_equals", Static: true, Arity: 2}
)

type pair struct {
	class     string
	signature Signature
}

// Capabilities records which (class, signature) pairs will exist after this
// run, and where a class's implementation comes from.
type Capabilities struct {
	realizable  map[pair]bool
	handwritten map[pair]bool
	orphaned    map[string]bool
	// inheritanceSensitive is the generator's NeedsInheritanceGraph, recorded
	// per generated pair at seed time rather than inferred from the signature:
	// a future generator may emit several methods of differing sensitivity,
	// and deriving it from the shape would silently get that wrong.
	inheritanceSensitive map[pair]bool
	// UniverseCause is why every inheritance-sensitive pair was demoted, when
	// one was: an unparseable file, or a class whose base does not resolve. It
	// goes into the refusal message so the reader learns which file to fix.
	UniverseCause string
}

// Realizable reports that the class will have a callable implementation of
// signature once this run completes.
func (c *Capabilities) Realizable(class string, signature Signature) bool {
	return c.realizable[pair{class, signature}]
}

// Provider walks a class's ancestry and returns the class whose implementation
// of signature a call from it would reach.
//
// The walk stops at the nearest class declaring a method with signature's
// name — not the nearest compatible one — because GDScript's runtime lookup
// does not walk past an incompatible override either. What it finds decides the
// outcome:
//
//	compatible, hand-written outside any region   -> provider
//	incompatible, hand-written                    -> barrier, no provider
//	inside a region, that pair realizable         -> provider
//	inside a region, not realizable or orphaned   -> barrier, no provider
//	no declaration, but the pair is realizable    -> virtual provider
//	no declaration and not realizable             -> keep walking
//
// The virtual row is what makes a first adoption work: a class that requested a
// generator but has no region yet declares nothing, so without it a newly
// requested parent could never provide for its child — the very case the
// optimistic seed exists to serve.
//
// An orphaned region is a barrier rather than invisible. Its method physically
// exists, so falling through to an ancestor would emit a super call that lands
// somewhere else; but --prune may delete it, so nothing may compose with it.
func (c *Capabilities) Provider(index *Index, class string, signature Signature) (string, bool) {
	for _, ancestor := range index.Ancestry(class) {
		indexed := index.Classes[ancestor]
		if indexed == nil {
			return "", false
		}
		method, declared := indexed.Methods[signature.Name]
		if !declared {
			if c.realizable[pair{ancestor, signature}] {
				return ancestor, true
			}
			continue
		}
		if method.InRegion {
			if c.orphaned[ancestor] || !c.realizable[pair{ancestor, signature}] {
				return "", false
			}
			return ancestor, true
		}
		if method.Signature != signature {
			return "", false
		}
		return ancestor, true
	}
	return "", false
}

// Resolve computes realizability by monotone demotion to stability.
//
// The obvious phrasing is circular: optimistic seeding makes a requested pair
// realizable, so Provider finds the class itself, and "demote when the provider
// is not realizable" can never detect a missing *parent* provider. So the three
// demotion conditions are stated explicitly and Provider is recomputed against
// the current set on every pass.
//
// Iteration is required rather than a parents-first walk, because the
// descendant rule points the other way: demoting B can move provider(C) from B
// to A and force A down too, after a parents-first pass had settled A.
//
// requested maps a class ID to the generator names it asked for. blockers marks
// a class carrying a local blocking diagnostic, including a verification
// failure found after emission — Check re-enters Resolve with those.
func Resolve(index *Index, requested map[string][]string, blockers map[string]bool) *Capabilities {
	capabilities := &Capabilities{
		realizable:           map[pair]bool{},
		handwritten:          map[pair]bool{},
		orphaned:             map[string]bool{},
		inheritanceSensitive: map[pair]bool{},
	}
	// Two conditions make the inheritance graph untrustworthy as a whole, and
	// both demote every inheritance-sensitive pair rather than one class's.
	// Inheritance is a reverse dependency: the class that would invalidate a
	// generated equals is a descendant, and nothing in the base names it, so an
	// incomplete graph cannot be narrowed to the pairs it affects.
	//
	// Reporting them as diagnostics is not enough, because gen write applies
	// candidates despite unrelated diagnostics and would write the unsound
	// method anyway.
	if len(index.ParseFailures) > 0 {
		capabilities.UniverseCause = fmt.Sprintf("%s could not be parsed", index.ParseFailures[0])
	}
	if capabilities.UniverseCause == "" {
		for _, id := range sortedKeys(index.Classes) {
			class := index.Classes[id]
			if !class.UnresolvedBase {
				continue
			}
			capabilities.UniverseCause = fmt.Sprintf(
				"%s extends %s, which does not resolve to a project script",
				id, class.UnresolvedCause)
			break
		}
	}
	for id, class := range index.Classes {
		if class.HasRegion && len(requested[id]) == 0 && class != index.HelpersClass {
			capabilities.orphaned[id] = true
		}
	}
	// Seed: realizable = compatibleHandwritten or requested.
	generated := map[pair]bool{}
	for id, class := range index.Classes {
		for _, method := range class.Methods {
			if method.InRegion {
				continue
			}
			key := pair{id, method.Signature}
			capabilities.realizable[key] = true
			capabilities.handwritten[key] = true
		}
	}
	for id, names := range requested {
		for _, name := range names {
			emitter := emitterByName(name)
			if emitter == nil {
				continue
			}
			for _, signature := range emitter.Signatures() {
				key := pair{id, signature}
				capabilities.inheritanceSensitive[key] = emitter.NeedsInheritanceGraph()
				if capabilities.handwritten[key] {
					continue
				}
				capabilities.realizable[key] = true
				generated[key] = true
			}
		}
	}
	// Iterate. The set only shrinks, so this terminates.
	for {
		demoted := false
		for key := range generated {
			if !capabilities.realizable[key] {
				continue
			}
			if capabilities.demote(index, key, blockers) {
				capabilities.realizable[key] = false
				demoted = true
			}
		}
		if !demoted {
			return capabilities
		}
	}
}

// demote reports whether a generated pair fails any demotion condition.
//
// The conditions split on inheritance sensitivity. Only local blockers apply to
// every signature; the cycle, universe, ancestry, and descendant conditions
// apply solely to a pair whose soundness depends on the inheritance graph.
// _to_string neither composes with an ancestor nor walks one, so a cycle
// elsewhere, an unparseable unrelated file, and a fieldful ancestor without a
// _to_string cannot affect it.
func (c *Capabilities) demote(index *Index, key pair, blockers map[string]bool) bool {
	class := index.Classes[key.class]
	if class == nil || class.Inner || blockers[key.class] || class.RegionError != nil {
		return true
	}
	if !c.inheritanceSensitive[key] {
		return false
	}
	if c.UniverseCause != "" || index.ReachesCycle(key.class) {
		return true
	}
	// A cyclic field-type graph cannot be shown to terminate: comparing two
	// independently built cyclic graphs never finds an identical pair, so the
	// identity check that settles every realistic recursive shape never fires.
	if key.signature == deepEqualsSignature && index.FieldTypeCycle(key.class) {
		return true
	}
	// A class_name this pair must resolve to find its provider is claimed by
	// more than one script, so Provider has no answer.
	for _, ancestor := range index.Ancestry(key.class) {
		if len(index.DuplicateClassNames[index.Classes[ancestor].Extends]) > 0 {
			return true
		}
	}
	// Ancestry rule: a strict ancestor declares a selectable field and the
	// parent has no provider to compose with.
	if ancestryHasFields(index, key.class) {
		if _, ok := c.Provider(index, class.ParentID, key.signature); !ok {
			return true
		}
	}
	// Descendant rule: a descendant would inherit this implementation while
	// adding state it does not compare.
	for _, descendant := range index.Descendants(key.class) {
		provider, ok := c.Provider(index, descendant, key.signature)
		if !ok || provider != key.class {
			continue
		}
		if pathBetweenHasFields(index, key.class, descendant) {
			return true
		}
	}
	return false
}

// ancestryHasFields reports whether any strict ancestor declares a selectable
// field.
func ancestryHasFields(index *Index, id string) bool {
	ancestry := index.Ancestry(id)
	for _, ancestor := range ancestry[1:] {
		if len(index.Classes[ancestor].Fields) > 0 {
			return true
		}
	}
	return false
}

// pathBetweenHasFields reports whether any class from base (exclusive) to
// descendant (inclusive) declares a selectable field.
func pathBetweenHasFields(index *Index, base, descendant string) bool {
	for _, ancestor := range index.Ancestry(descendant) {
		if ancestor == base {
			return false
		}
		if len(index.Classes[ancestor].Fields) > 0 {
			return true
		}
	}
	return false
}
