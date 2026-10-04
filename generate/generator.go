package generate

import "sort"

// Generator emits one family of methods for a class.
//
// A later generator is not necessarily only an emitter and a registry entry.
// deep_equals, specified separately, widens capability resolution to follow
// field references and adds a builtin type catalogue, a recursion-state helper
// method, and diagnostic severity. This interface is not the whole extension
// point.
type Generator interface {
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

// registry is every generator, in the order they emit. Emission order is fixed
// here rather than taken from the directive, so a region's content depends only
// on the class and not on how the marker was written.
var registry = []Generator{
	toStringGenerator{},
	equalsGenerator{},
}

// generatorByName returns the registered generator, or nil.
func generatorByName(name string) Generator {
	for _, generator := range registry {
		if generator.Name() == name {
			return generator
		}
	}
	return nil
}

// isGeneratorName reports a registered name, which is what makes an unknown
// name in a directive a generate.marker rather than a silent no-op.
func isGeneratorName(name string) bool { return generatorByName(name) != nil }

// GeneratorNames lists every registered generator, sorted, for messages and
// for the init template.
func GeneratorNames() []string {
	names := make([]string, 0, len(registry))
	for _, generator := range registry {
		names = append(names, generator.Name())
	}
	sort.Strings(names)
	return names
}

// inRegistryOrder returns the requested generators in emission order.
func inRegistryOrder(names []string) []Generator {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	ordered := []Generator{}
	for _, generator := range registry {
		if wanted[generator.Name()] {
			ordered = append(ordered, generator)
		}
	}
	return ordered
}
