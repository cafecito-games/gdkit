# The deep_equals generator

A third generator for `gdkit gen`, emitting a structural equality method for a
class that holds other value objects. It builds on
[the code generation design](2026-10-04-gdscript-codegen-design.md) and does not
revisit it: the marker grammar, the region and its extent, verification, the
format oracle, the plan model, field selection, configuration, and the command
surface are all unchanged.

## Why it is needed

`equals` compares each field with `==`, which is correct for every builtin
Variant type — Godot 4 compares `Array` and `Dictionary` by value, so containers
of primitives behave as expected.

It is not correct for a field holding another object. `==` on an `Object` in
Godot 4 compares identity, so two distinct instances carrying the same values
come out unequal. Avoiding exactly that is the point of a value object.

## Runtime dispatch, not static resolution

The comparison asks the value what it can do rather than proving it in advance:

```gdscript
if self.origin is Object and self.origin.has_method("deep_equals"):
	if not self.origin.deep_equals(p_other.origin):
		return false
```

An earlier draft of this spec resolved each field's type statically against a
catalogue of builtin Variant types, proved the target class would have the
method through a fixed point over field edges, and threaded a visited set
through a generated `_gdkit_deep_equals` helper. Runtime dispatch deletes three
of those four mechanisms:

- **no builtin type catalogue.** The set is not even stable across Godot 4
  (`PackedVector4Array` is absent in 4.0, present by 4.6), and asking the value
  makes the question moot.
- **no capability resolution over field edges.** Nothing has to be proven about
  the target class in advance, so the fixed point stays on inheritance edges
  where the base design already has it.
- **no `generate.untyped` diagnostic, and no diagnostic severity.** A field with
  no static type was the case static resolution had to degrade on; here it is
  the ordinary case, so there is nothing to warn about.

It also handles a case the static design *refused*: a field whose type has only
a hand-written `equals` is now used rather than rejected.

Two guards are not optional. `has_method` is declared on `Object`, so calling it
on an `int` or a `String` is a runtime error rather than `false` — hence
`is Object` first. And `null.has_method(...)` is a runtime error, so a null
mismatch is settled before any dispatch.

## Identity settles a field

The first thing the comparison asks is whether the two values are the same
object, which is both a fast path and the main defence against recursion:

```gdscript
func deep_equals(p_other: Variant) -> bool:
	if self == p_other:
		return true
```

At the top of the method this is a whole-function answer: the same instance is
strictly equal. Per field, `==` being true settles *that field* and the deeper
check is never reached — which is why the dispatch block sits inside an
inequality test rather than replacing one.

This is what makes the realistic recursive shapes terminate:

| Shape | Outcome |
| --- | --- |
| `a.deep_equals(a)` | the top-level identity check answers immediately |
| `a.origin = a`, compared with itself | same, by the same check |
| `a.origin = shared`, `a2.origin = shared` | `shared == shared` settles the field; no recursion |

## Cycles are refused, not survived

The identity check does not fix every shape, and the gap has to be stated
rather than implied. Two *independently constructed* cyclic graphs recurse
forever, because no pair is ever the same instance:

```
a.origin  = b        a2.origin = b2
b.origin  = a        b2.origin = a2      # all four distinct
```

`a.deep_equals(a2)` calls `b.deep_equals(b2)` calls `a.deep_equals(a2)`.

Threading a visited set through a generated helper method would survive this.
It is not worth it: a cyclic *value object* is pathological, and the machinery
is heavy and visible in code people read. Instead the cycle is **detected and
refused**, using the index the base design already builds.

> A requested class is refused with `generate.unsupported` when the graph of
> its project-class-typed fields, followed transitively, can reach the class
> itself. The message names the cycle.

**The residual gap, documented rather than hidden:** a field with no declared
type, an explicit `Variant`, or a `:=`-inferred type (whose type gdparser does
not record) is invisible to that check, so an untyped field participating in a
cycle still recurses until the stack is exhausted. The typed case is both the
common one and the one gdkit's own typing rules push a project toward.

## Dispatch

A field's declared type decides which form is emitted, resolved against the
project's `class_name` index alone — no catalogue of engine or builtin names is
needed:

| Declared type | Emitted |
| --- | --- |
| a project class | the dispatch block below |
| any other declared type | `if self.q != p_other.q: return false` |
| absent, `Variant`, or `:=`-inferred | the dispatch block below |

A declared type that is not a project class is a builtin or an engine class.
Either way `==` is the right comparison: a builtin compares by value, and an
engine object has no value equality to recurse into.

```gdscript
func deep_equals(p_other: Variant) -> bool:
	if self == p_other:
		return true
	if not p_other is Object:
		return false
	if p_other.get_script() != get_script():
		return false
	if not super.deep_equals(p_other):
		return false
	if self.q != p_other.q:
		return false
	if self.origin != p_other.origin:
		if (self.origin == null) != (p_other.origin == null):
			return false
		if self.origin is Object and self.origin.has_method("deep_equals"):
			if not self.origin.deep_equals(p_other.origin):
				return false
		elif self.origin is Object and self.origin.has_method("equals"):
			if not self.origin.equals(p_other.origin):
				return false
		else:
			return false
	return true
```

The final `else: return false` is not a fallback but a conclusion: `==` already
answered unequal, and no deeper answer is available, so unequal stands.

`super.deep_equals(p_other)` is emitted under the base design's composition and
refusal rules, unchanged: a subclass composes with its ancestor's provider, and
is refused when there is none to compose with and the ancestry declares fields.

## Diagnostics

No new rule names. `generate.unsupported` gains one cause — a class whose
project-class field graph is cyclic — and `generate.conflict` already covers a
same-named method with an incompatible signature.

Dispatching on the public `deep_equals` name has one sharp edge worth recording:
a project class declaring `func deep_equals(a, b)` makes `has_method` answer
true, and the one-argument call then fails at runtime. Statically, that class is
caught as `generate.conflict` if it requested the generator; a class that never
opted in is not checked, so this is the one way the emitted code can fail on a
project that passed `gen check`.

## Testing

- a field typed as a project class recurses; one typed `Vector2` or `Node` does
  not;
- an untyped field, a `Variant` field, and a `:=`-inferred field all recurse via
  dispatch;
- a field type with only a hand-written `equals` is used, not refused;
- a `null` field on one side and not the other is unequal, with no dispatch;
- two distinct instances with equal values compare equal, which is the whole
  point and what `equals` gets wrong;
- the same instance on both sides short-circuits at the top;
- a shared sub-object settles its field without recursing;
- a self-referential field type is refused with the cycle named;
- a two-class field cycle is refused;
- a subclass composes with its parent's `deep_equals`.
