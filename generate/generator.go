package generate

import "sort"

// Emitter emits one family of methods for a class. Each registered Emitter is
// one user-facing "generator" — the name a directive or a config entry spells —
// while Generator is the tool itself, matching format.Formatter.
//
// A later generator is not necessarily only an emitter and a registry entry.
// deep_equals, specified separately, widens capability resolution to follow
// field references and adds a builtin type catalogue, a recursion-state helper
// method, and diagnostic severity. This interface is not the whole extension
// point.
type Emitter interface {
	// Name is the directive and configuration spelling: "to_string".
	Name() string
	// Signatures are the methods it emits. A class declaring one of these
	// outside the region satisfies the request; declaring the same name in an
	// incompatible shape is a generate.conflict, because GDScript has no
	// overloading and emitting ours would not compile.
	Signatures() []Signature
	// NeedsInheritanceGraph reports that soundness depends on the inheritance
	// graph, which is what subjects a pair to the cycle, universe, ancestry,
	// and descendant demotion conditions. It belongs on the generator rather
	// than being inferred from a signature, because a future generator may
	// emit several methods of differing sensitivity.
	NeedsInheritanceGraph() bool
	// Emit returns the method text, unformatted and at indent 0.
	Emit(class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic)
}

// internalEmitter marks a generator that exists for gen's own use. Such a
// generator is registered — the capability seed, the conflict check, and
// candidate emission all resolve it by name — but is not a configuration or
// directive spelling, so it never reaches GeneratorNames or isGeneratorName.
// It is an optional interface rather than a field on every emitter for the
// same reason lint.PendingRule is: a test can stand one in through the
// registry without a parallel map to keep in step.
type internalEmitter interface {
	Internal()
}

func isInternal(emitter Emitter) bool {
	_, internal := emitter.(internalEmitter)
	return internal
}

// registry is every generator, in the order they emit. Emission order is fixed
// here rather than taken from the directive, so a region's content depends only
// on the class and not on how the marker was written.
var registry = []Emitter{
	toStringGenerator{},
	equalsGenerator{},
	deepEqualsGenerator{},
	helpersGenerator{},
}

// emitterByName returns the registered emitter for a generator name, or nil.
func emitterByName(name string) Emitter {
	for _, generator := range registry {
		if generator.Name() == name {
			return generator
		}
	}
	return nil
}

// isGeneratorName reports a registered name, which is what makes an unknown
// name in a directive a generate.marker rather than a silent no-op.
func isGeneratorName(name string) bool {
	emitter := emitterByName(name)
	return emitter != nil && !isInternal(emitter)
}

// GeneratorNames lists every registered generator, sorted, for messages and
// for the init template.
func GeneratorNames() []string {
	names := make([]string, 0, len(registry))
	for _, generator := range registry {
		if isInternal(generator) {
			continue
		}
		names = append(names, generator.Name())
	}
	sort.Strings(names)
	return names
}

// inRegistryOrder returns the requested emitters in emission order.
func inRegistryOrder(names []string) []Emitter {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	ordered := []Emitter{}
	for _, generator := range registry {
		if wanted[generator.Name()] {
			ordered = append(ordered, generator)
		}
	}
	return ordered
}
