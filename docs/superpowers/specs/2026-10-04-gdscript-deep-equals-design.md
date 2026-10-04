# The deep_equals generator

A third generator for `gdkit gen`, emitting a structural equality method for a
class that holds other value objects. It builds on
[the code generation design](2026-10-04-gdscript-codegen-design.md) and does not
revisit it: the marker grammar, the region and its extent, verification, the
format oracle, the plan model, field selection, configuration, and the command
surface are all unchanged.

It is **not** a drop-in generator. It extends capability resolution to follow
field references, and it adds a builtin type catalogue, a second emitted helper
method carrying recursion state, diagnostic severity, and
`Report.HasErrors()`. This document covers only those additions: structural
recursion needs to know a field's type, which needs the catalogue; it needs the
target type to actually have the method, which widens the capability graph; and
it needs the walk to terminate, which needs cycle detection in the generated
code.

**Status: specified, not scheduled.** The base design ships first.

## Why it is needed at all

`equals` is enough for every field except an object-valued one. Godot 4 compares
`Array` and `Dictionary` **by value**, not by reference, so a field-wise `==`
already handles containers of builtins correctly. Objects still compare by
reference, which means a plain `==` on `var origin: Coordinate` silently
compares identity — the exact bug a value object exists to prevent. gdkit is
Godot 4 only, so this holds for every supported version and nothing here is
version-gated.

## Type resolution needs a builtin catalogue

"Not in the project `class_name` index" does not mean "engine-owned", and it
does not mean "project class" either: engine types are absent from that index
too, so `Vector2` and a misspelled `Coordinat` are indistinguishable by it
alone. Resolution is against two catalogues in order:

1. **Builtin Variant value types** — an embedded list in `generate/builtin.go`.
   A field of one compares with `==`.
2. **The project `class_name` index.**

The builtin set is **not** stable across Godot 4. `PackedVector4Array` is absent
from the Godot 4.0 `Variant.Type` enum and present by 4.6. The list is therefore
the **union** of every builtin introduced anywhere in Godot 4, which needs no
version configuration to be sound: a type that does not exist in the engine
version a project targets cannot appear in that project's source, so a union can
only be over-permissive about names nobody can write. A per-version list would
buy nothing and would need a `godot_version` key to consult.

`Object` and `Variant` are in the enum but are **not** in this list. `Object` is
the very thing whose reference semantics justify refusing a `Node`-typed field,
so classifying it as a comparable builtin would contradict that rule. `Variant`
is handled by the untyped row of the dispatch table, since that is what it
means.

A type in neither catalogue is refused with `generate.unsupported` naming it.
That rejects a field typed as a native engine class, such as `Node` or
`Texture2D`: a native engine object has reference semantics and no value
equality to generate. The field-level opt-out is the escape hatch, and the
diagnostic names it.

This applies to `deep_equals` alone. `_to_string` formats any value and `equals`
compares any value, so neither refuses a field for having an unresolvable type.
A full native-class catalogue — roughly 800 versioned names — would only improve
the *message* for a field that is refused either way, and stays out of scope.

## Realizable, not merely planned

A method a class *requested* is not a method it will *have*. If `B` requests
`deep_equals` and then loses its candidate — a conflict, an unsupported field, a
refused inheritance — then a `deep_equals` in `A` that called into `B` would
compile and crash. "Requested" is an intention; emission may only depend on a
capability that survives every blocker.

The base design already resolves capabilities by monotone demotion to
stability, because its descendant refusal rule makes capability flow both up
and down the inheritance hierarchy. `deep_equals` does not introduce the
iteration — it **widens the dependency graph** the iteration runs over, from
inheritance edges alone to inheritance edges plus every field whose type is a
project class. Those edges can form cycles that inheritance cannot.

The algorithm is unchanged: seed optimistically with every
declared-and-compatible method and every requested generator, repeatedly demote
any **generated** pair whose provider is not realizable or whose class carries a
blocker, and stop when a pass demotes nothing. The set only ever shrinks, so
this terminates. Starting optimistically rather
than pessimistically is what makes a cycle work: `A` and `B` holding each other
and both newly requesting `deep_equals` are realizable together, because neither
is demoted by anything other than the other's absence. A cycle survives exactly
when every member survives on its own merits — the correct answer, and the one a
least fixed point would get wrong by refusing both.

A declared, compatible method remains immutable, as in the base design. Only a
generated realization can be withdrawn, so a blocked generator cannot erase a
hand-written provider that something else was relying on.

## Termination

A recursion can **not terminate**, which is a separate and worse problem than
method availability: a self-referential field, or a live `A → B → A` object
graph, makes a naive structural comparison descend forever. The fixed point
proves the method exists, not that the walk it performs is finite.

So the generator emits **two** methods. `deep_equals` is the public entry point
and holds the visited set; `_gdkit_deep_equals` does the work and is what
recursion calls. Keeping the set out of the public signature is what lets
`a.deep_equals(b)` stay a one-argument call.

Returning `true` on a revisited pair is the coinductive answer — a pair already
under comparison is assumed equal unless something else proves it unequal —
which is the standard treatment for bisimulation on cyclic structures and the
only one that terminates without declaring every cyclic graph unequal.

The visited set is keyed on the pair of instance IDs, so an `Array` is used as a
`Dictionary` key, which Godot 4 hashes by value. The `super` call participates
in the recursion, so the set threads through the ancestry too.

One consequence for the provider rules: recursion requires
`_gdkit_deep_equals`, not `deep_equals`. A hand-written `deep_equals` therefore
satisfies a top-level call but **cannot** be a provider for a field's recursion,
and a class holding a field of that type is refused with
`generate.unsupported`. The signature a provider must offer is named in the
diagnostic, because the fix is to let gdkit generate the pair.

## Dispatch

| Declared type | Emitted | Diagnostic |
| --- | --- | --- |
| a builtin Variant value type | `self.q != p_other.q` | — |
| project class with `provider(C, _gdkit_deep_equals)` | null-aware recursion | — |
| project class with no provider | nothing; the class is refused | `generate.unsupported` |
| `Array[T]`, `T` a project class with a provider | size check, then null-aware element-wise recursion | — |
| `Dictionary`, or an untyped container | `==` | `generate.untyped` |
| a type in neither catalogue | nothing; the class is refused | `generate.unsupported` |
| absent, `Variant`, or `:=` inferred | runtime `has_method` fallback | `generate.untyped` |

An object-valued field can be `null`, and `null._gdkit_deep_equals(…)` is a
runtime error, so every recursion is null-aware:

```gdscript
func deep_equals(p_other: Variant) -> bool:
	return _gdkit_deep_equals(p_other, {})


func _gdkit_deep_equals(p_other: Variant, __gdkit_seen: Dictionary) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	var __gdkit_key := [get_instance_id(), p_other.get_instance_id()]
	if __gdkit_seen.has(__gdkit_key):
		return true
	__gdkit_seen[__gdkit_key] = true
	if not super._gdkit_deep_equals(p_other, __gdkit_seen):
		return false
	if self.q != p_other.q:
		return false
	if (self.origin == null) != (p_other.origin == null):
		return false
	if self.origin != null and not self.origin._gdkit_deep_equals(p_other.origin, __gdkit_seen):
		return false
	if self.tags.size() != p_other.tags.size():
		return false
	for __gdkit_index in self.tags.size():
		var __gdkit_left: Variant = self.tags[__gdkit_index]
		var __gdkit_right: Variant = p_other.tags[__gdkit_index]
		if (__gdkit_left == null) != (__gdkit_right == null):
			return false
		if __gdkit_left != null and not __gdkit_left._gdkit_deep_equals(__gdkit_right, __gdkit_seen):
			return false
	return true
```

One `if` per field rather than an `and` chain, because the comparison differs
per field and a chain of mixed call and operator forms is unreadable at any
width. The `super` call is emitted only when the parent provides
`_gdkit_deep_equals`, under the same composition and refusal rules the base
design defines for `equals`.

The last row of the table is the one to expect in practice. `var origin :=
Coordinate.new()` is statically typed as far as Godot is concerned, but
`ast.VariableDeclaration.Type` is empty and the node carries only
`Inferred: true` — the inferred type is not in the tree. So the ergonomic `:=`,
a natural way to write exactly these classes, lands in the runtime fallback.

## Diagnostics this adds

| Rule | Reports | Severity |
| --- | --- | --- |
| `generate.untyped` | `deep_equals` fell back to a runtime dispatch | warning |

`generate.untyped` is the first warning in `generate`, so it is what introduces
the severity field on `Diagnostic` and `Report.HasErrors()` alongside
`HasDiagnostics()`: a warning prints but does not take the run to exit 1. It is
hardcoded rather than configurable, because a project able to downgrade
`generate.unsupported` to a warning would be asking for code that does not
compile.

The simpler alternative, considered and not taken: drop the diagnostic and emit
a comment naming the untyped field *inside* the region, where the reader who
needs it is already looking. That needs no severity concept at all. It was
rejected because a fallback is something CI should be able to see, and a comment
inside a generated region is invisible to `--format json`.

`provider` keeps the base design's barrier rule: the walk stops at the nearest
declaration of the method *name*, and an incompatible one blocks rather than
being skipped. For recursion the name is `_gdkit_deep_equals`, so a class whose
nearest declaration of it is hand-written and wrongly shaped is a barrier, not
a provider.

`generate.unsupported` gains three new causes: a field type in neither
catalogue, a field type with no provider for the signature, and a field type
whose only provider is a hand-written `deep_equals` with no
`_gdkit_deep_equals`.

## Testing this adds

- two classes holding each other, both newly requesting `deep_equals`, which
  must generate rather than deadlock;
- the same pair where one is separately blocked, where **both** must be refused
  rather than one emitting a call to a method that will not exist;
- a self-referential field, and a live `A` to `B` to `A` graph, which must
  terminate and compare equal;
- an `Array` field named `i`, which must not collide with the loop variable;
- a `null` object field, and a typed array holding a `null`;
- a field typed as a class `.gdkitignore` hides, which must still resolve;
- a field typed `Vector2`, which must compare; one typed `PackedVector4Array`,
  which must compare although it postdates Godot 4.0; one typed `Object`, which
  must be refused; one typed `Coordinat`, which must be refused;
- a class with a hand-written `deep_equals` used as another class's field type,
  which must be refused for lacking `_gdkit_deep_equals`;
- a zero-field subclass used as a field type, which must recurse via its
  ancestor's provider rather than being refused.

## Out of scope

- **A full native-class catalogue.** Roughly 800 versioned names, to improve a
  message for a field that is refused either way.
- **Comparing two instances of different scripts.** The script-identity guard
  the base design establishes makes that false by construction.
