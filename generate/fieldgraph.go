package generate

import "sort"

// FieldTypeTargets are the project classes a class's fields name by declared
// type, sorted. Only a type that resolves in the class_name index counts: a
// type that does not is a builtin or an engine class, and neither is something
// deep comparison recurses into.
func (i *Index) FieldTypeTargets(id string) []string {
	class := i.Classes[id]
	if class == nil {
		return nil
	}
	seen := map[string]bool{}
	targets := []string{}
	for _, field := range class.Fields {
		target, ok := i.fieldTypeTarget(field)
		if !ok || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// fieldTypeTarget resolves a field's declared type to a project class.
//
// An element type inside Array[T] or Dictionary[K, V] is not followed. The
// generated comparison hands a container to "==", which Godot 4 evaluates by
// value, and so never recurses into its elements; following the element type
// here would refuse cycles that the emitted code cannot reach.
func (i *Index) fieldTypeTarget(field Field) (string, bool) {
	if field.Type == "" {
		return "", false
	}
	target, ok := i.ByClassName[field.Type]
	if !ok {
		return "", false
	}
	return target.ID, true
}

// FieldTypeCycle reports whether the graph of project-class-typed fields,
// followed transitively from id, can reach id itself.
//
// A cyclic value object is pathological, and deep comparison of two
// independently built cyclic graphs cannot be shown to terminate: no pair of
// instances is ever identical, so the identity check that settles every
// realistic recursive shape never fires. Refusing the class is the honest
// answer; the alternative is threading a visited set through a generated helper
// method, which is heavy machinery in code people read.
//
// A field with no declared type, an explicit Variant, or a ":="-inferred type
// is invisible here, so an untyped field in a cycle still recurses until the
// stack is exhausted. That gap is documented rather than hidden.
func (i *Index) FieldTypeCycle(id string) bool {
	seen := map[string]bool{}
	queue := i.FieldTypeTargets(id)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == id {
			return true
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, i.FieldTypeTargets(current)...)
	}
	return false
}
