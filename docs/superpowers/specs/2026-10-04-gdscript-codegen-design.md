# GDScript code generation

A fifth gdkit tool, `gdkit gen`, that writes boilerplate value-object methods
into a class the project has opted in. This spec covers two generators,
`to_string` and `equals`, and the machinery they share. A third,
`deep_equals`, is specified separately in
[the deep_equals design](2026-10-04-gdscript-deep-equals-design.md). That one is
**not** a drop-in third generator: it replaces capability resolution with a
version that follows field references, and it adds a builtin type catalogue, a
second emitted helper method carrying recursion state, diagnostic severity, and
`Report.HasErrors()`. v1 should not be built on the assumption that a later
generator is only an `emit_*.go` and a registry entry, because that one is not.

GDScript has no partial classes, no mixins, and no user-defined annotations — an
`@gdkit_value` is a compile error in Godot, not an extension point. Generated
methods therefore have to land inside the user's own script. That single language
fact is what makes this tool shaped like `format` (a verified in-place rewriter)
rather than like a conventional out-of-band generator, and it decides most of
what follows.

It reports only on classes that opted in, with one stated exception for parse
failures, and it never rewrites anything outside the region it owns.

## The marker

A class opts in through a standalone comment directive in its body, with path
globs in configuration as a second route and a per-file override that beats
both.

| Form | Scope | Meaning |
| --- | --- | --- |
| a `generate` entry in `.gdkit/generate.json` | path glob | opts matching files in, with that entry's generator list |
| `# gdkit:generate = to_string, equals` | class body, standalone | opts this class in; replaces any config-supplied list |
| `# gdkit:generate:ignore` | class body, standalone | opts this class out |
| `# gdkit:generate:ignore-field` | on, or directly above, a `var` | excludes that field |

Precedence is `ignore` > directive > config, which is lint's `disable` >
`enable` rule applied to the same problem for the same reason: an opt-out that
can be overridden by configuration is not an opt-out.

The class-level and field-level opt-outs are spelled **differently on purpose**.
One spelling for both is ambiguous and no precedence rule can recover the
author's intent, because a standalone comment directly above the first `var` of
a class satisfies the definition of both at once:

```gdscript
# gdkit:generate = equals

# gdkit:generate:ignore     <- the class? or just q?
var q: int
```

Resolving that by position — "class-level unless a `var` follows" — is
deterministic but surprising, and it makes inserting a field above another field
silently change the meaning of a comment nobody touched. Distinct words cost
four characters and remove the question.

A comment directive rather than configuration alone, because the marker is then
visible in the script a reader is already looking at, travels with the file when
it moves, and appears in the diff that introduces it. A hand-written base class
(`extends GdkitValue`) was rejected because it spends GDScript's single
inheritance slot, forces an addon into the project, and cannot say *which*
helpers the class wants.

### Why not a wholly owned generated file

The `go generate` model — emit a file the tool owns outright and rewrite it
whole — is not available, because GDScript requires a class's methods to live in
that class's one script file. A sibling file cannot add a method to `Hex`.

A **generated base class** does get most of the way there, and was considered
seriously rather than dismissed: `hex.gd` declares
`extends "res://domain/hex.generated.gd"`, and the generated file holds the
methods and extends what `Hex` used to. It would delete the region extent rules,
byte-identity verification, the format oracle, `generate.unsafe`, the orphan
lifecycle and `--prune`, and most of `generate.conflict` — a hand-written
`equals` would simply override the generated one. It would also thread the
inheritance slot through rather than spending it, since each generated base
extends the real one.

It was rejected for what the generated bodies would have to look like. A base
class cannot statically reference a member declared in its subclass — GDScript
rejects the identifier — so every field access would become `get("q")`. That
costs static type checking inside the generated code and replaces direct member
access with a dynamic property lookup in `_to_string` and `equals`, which are
exactly the methods that run on every `print()` and every comparison. It also
requires the generated files to be committed, since a fresh clone would not open
in Godot until `gen write` had run, and each new `.gd` file needs a `.uid`
sidecar, putting `gen` and `uid` in each other's way.

The in-file region keeps `self.q`: type-checked, direct, and adding nothing to
the filesystem or to Godot's global class list.

A free-function helper (`HexEquality.equals(a, b)`) would keep full static
typing in a wholly owned file, but `_to_string` must be a method on the class
for `str()` and `print()` to use it, so that model cannot deliver one of the two
generators at all.

### Grammar

The directive reuses the shape of the three suppression directives in
`internal/suppression` — `#`, `gdkit` or `gdlint`, `:`, the directive word, `=`,
a comma-separated list — but it is parsed by `generate/marker.go`, not by that
package, and it is strict where that package is lax.

`internal/suppression` lets a rule list run to the end of the line because
gdlint does, which is why a trailing remark silently breaks `# gdkit:ignore`. A
gdkit-original directive owes gdlint nothing, so each item in a `generate` list
must be a known generator name and anything else is a `generate.marker`
diagnostic naming it.

No change to `internal/suppression` is needed, and none is wanted.
`directivePattern` requires the literal word `ignore`, `disable`, or `enable`
followed by `=`, so none of the three `generate` forms matches any of its
patterns. In particular `lint`'s `unknown-ignore`, which reports only names
found *inside* a recognised list, passes over them silently. A test pins this,
because a future change to that regex could quietly turn every marker in every
adopting project into an `unknown-ignore` finding.

## The region

Generated methods live between sentinels:

```gdscript
# gdkit:generated:begin
func _to_string() -> String:
	return "Hex(q=%s, r=%s)" % [self.q, self.r]
# gdkit:generated:end
```

The region stays exactly where it already is in the file. `gen check` does not
care about its position, so a placement the author chose is never churned. When
no region exists, it is inserted at the end of the enclosing class body. A file
holds at most one region per class; a second is a `generate.marker` diagnostic.

The sentinels carry no checksum. A region whose content is not what the
generator would emit is simply out of date — `gen check` reports it,
`gen check --diff` shows it, `gen write` rewrites it — exactly as `format`
treats a file that is not canonical. Git is the safety net, and `gen write` is
documented the way `format write` is: run it on a clean tree. A checksummed
region that refused to be clobbered was considered and rejected: it puts a hash
comment in every generated region, churning the diff on metadata, to protect
against editing code whose own first line says it is generated.

### Region extent

The region is a byte span, and it must be defined exactly, because whether a
canonical file stays canonical turns on it. The span runs from the first byte of
the line holding `# gdkit:generated:begin` to the newline ending the line
holding `# gdkit:generated:end`, **extended backwards over every immediately
preceding blank line**. Trailing blank lines after the end sentinel are *not*
owned: they belong to the gap before whatever follows, which the following
statement's own formatting governs.

The leading gap the region emits depends on its neighbour, and these are the
rules gdparser's `blankLineGaps` actually applies rather than an approximation
of them:

| Position | Leading gap emitted |
| --- | --- |
| region is the first statement in the class body | none |
| preceded by a major declaration (`func`, `class`) | `blank_lines.top_level` |
| preceded by any other statement (`var`, `const`, `signal`) | `blank_lines.top_level` |
| region is at end of file | no trailing blank lines; file ends with one newline after the end sentinel |

The second and third rows agree, and that is not an accident worth glossing
over: gdparser surrounds a *major declaration* with `top_level`, and
`commentRunStart` attributes the gap to the top of a comment run that documents
a declaration. The begin sentinel is such a run, sitting above a `func`, so the
gap gdparser wants before the sentinel is `top_level` regardless of what
precedes it. The clamp to `blank_lines.nested` applies only between statements
that are not major declarations, which the region never is.

This is why owning the *leading* gap and not the trailing one is correct rather
than arbitrary: the leading gap is attributed to the region by the formatter,
and the trailing gap is attributed to the statement after it.

### Orphaned regions

A region whose class no longer opts in — it gained an `ignore`, stopped matching
configuration, or dropped its last generator — is **retained**, and reported as
`generate.orphaned`. It is removed only by `gen write --prune`.

Neither alternative is acceptable as a default. Silently keeping it leaves
methods in the file that the class has explicitly opted out of, with nothing
saying so. Silently deleting it makes `gen write` destroy code as a side effect
of an unrelated edit to a glob, which is beyond what "the tool owns this region"
licenses.

**Orphan detection scans the whole semantic universe, not just targets**, and is
the one lifecycle question target filtering does not get to answer. Otherwise
adding a path to `exclude` would make its region invisible to both the
diagnostic and `--prune`, so the most likely way to orphan a region would also
be the way to hide it. `--prune` will not write to a file outside the selection,
and says so: the diagnostic names the file and states that it must re-enter the
selection to be pruned.

## Universe and selection

`generate` is a whole-project analysis, closer to `architecture` than to `lint`,
because `equals` is only sound if the whole inheritance graph is visible. The
semantic index must span the whole project while generation targets are
filtered, and `architecture` already carries the comment explaining why
(`architecture/analyzer.go:61`): a file hidden from the walk takes its
`class_name` out of the index, and references to it then produce false results.

`generate` must not implement this filtering itself. `project` is documented as
the only package that reads a project from disk, and `.gdkitignore` is read from
disk, so a `generate/target.go` opening it would break that invariant, while a
`Check` that does not read it cannot identify its own targets. The capability
belongs in `project`:

```go
// Selection narrows which parsed scripts a tool acts on. Scripts outside it
// are still walked, parsed, and present in the snapshot.
type Selection struct {
	SourceRoots     []string
	Exclude         []string
	HonorIgnoreFile bool
}

type Config struct {
	Root string
	// SourceRoots, Exclude, and HonorIgnoreFile describe the universe: what
	// is walked and parsed at all.
	SourceRoots     []string
	Exclude         []string
	HonorIgnoreFile bool
	// Selection, when non-nil, narrows what the caller acts on without
	// narrowing the universe.
	Selection *Selection
}

type Snapshot struct {
	// ...
	// Selected is the subset of Paths that Config.Selection admits, sorted.
	// It equals Paths when Selection is nil.
	Selected []string
}
```

`generate` loads the universe with no exclusions and puts its `source_roots`,
`exclude`, and `.gdkitignore` in `Selection`. One load, one parse per file, and
no second package reading the project. The existing four tools pass
`Selection: nil` and are unaffected.

Only a selected script may opt in. Only a selected script may carry a
diagnostic, with **two** exceptions, both universe-wide and both argued for
where they are specified: a parse failure when an inheritance-sensitive
generator is requested, and an orphaned region.

## Pipeline

`Check` runs a fixed sequence, and everything downstream depends on the
ordering:

1. **Load and partition** — the universe, and the selection within it.
2. **Resolve opt-in** — configuration globs, then the in-file directive, then
   `ignore`, over selected scripts only. Yields the *requested* set of
   (script, class, generators).
3. **Index** — over the whole universe: `class_name` to script, the inheritance
   edge each class declares, each class's declared methods with their staticness
   and arity, and each class's selectable fields.
4. **Resolve capabilities** — decide which (class, signature) pairs are
   *realizable*, by monotone demotion to stability.
5. **Emit and canonicalise** — each requested generator produces its method
   text. The region, including the leading gap it owns, is formatted in
   isolation with the project's `format` configuration and spliced into the
   file.
6. **Verify** — the candidate must reparse, and every byte outside the region
   must be identical to the source.
7. **Re-resolve if verification blocked anything** — a refused candidate is a
   new blocker, so return to step 4 and repeat 4–6. Blockers only ever
   accumulate, and they are bounded by the number of (class, signature) pairs,
   so this terminates.
8. **Sort** — by path, then line, column, rule.

Step 7 exists because a `generate.unsafe` refusal is only discovered after
emission, and it withdraws a capability that a descendant may already have
composed with. Without the loop, a parent whose region was refused leaves a
child emitting `super.equals` against a method that was never written.

**Field selection is part of indexing, not a later step for requested classes
only.** Both inheritance rules ask whether some *other* class — an ancestor, or
an unrequested descendant — declares a selectable field, so that question has to
be answerable for every parsed class before step 4 runs.
`# gdkit:generate:ignore-field` is therefore read for every class in the
universe, including classes that never opt in.

### Capabilities

`provider(C, S)` is found by walking `C`'s ancestry, including `C` itself, and
**stopping at the nearest class that declares a method with `S`'s name** — not
at the nearest *compatible* one. If that declaration is compatible, or is a
realizable generated one, it is the provider. If it is incompatible, it is a
**barrier**: there is no provider, and a class trying to compose through it is
refused.

Walking past an incompatible method would be wrong, because GDScript's runtime
lookup does not walk past it. Given `A` with a good `equals`, `B extends A`
declaring `equals(a, b)`, and `C extends B` emitting `super.equals(p_other)`,
the call resolves to `B.equals` and fails. Nearest-compatible-ancestor would
have reported `A` as the provider and emitted that call anyway.

What the walk finds at that nearest declaration decides the outcome, and the
four cases are not interchangeable:

| Nearest declaration of `S`'s name | Outcome |
| --- | --- |
| compatible, hand-written outside any region | **provider** |
| incompatible, hand-written | **barrier** — no provider |
| inside a generated region, that pair realizable | **provider** |
| inside a generated region, that pair not realizable, or the region orphaned | **barrier** — no provider |

The last row is the one that needs the argument. An orphaned region's method
physically exists, so runtime dispatch reaches it and the walk must not fall
through to an ancestor as though it were absent — that would emit a `super`
call that lands somewhere else. But it is also scheduled for deletion by
`gen write --prune`, so nothing may compose with it either. Barrier is the only
answer that is safe both before and after pruning, and it makes the ancestry
refusal rule fire, which is the correct outcome: the child is refused until its
parent opts back in.

A hand-written method outside any region *is* immutable: it already exists and
nothing this run can do takes it away. Only a generated realization can be
withdrawn, so the set tracks provenance and a blocked generator can never erase
a hand-written provider something else relies on.

### Resolution is a fixed point, not a single pass

The transfer function has to be stated literally, because the obvious phrasing
is circular: optimistic seeding makes a requested `(C, S)` realizable, so
`provider(C, S)` is `C` itself, and "demote when the provider is not
realizable" can never detect a *missing parent* provider.

Seed:

```
realizable₀(C, S) = compatibleHandwritten(C, S) ∨ requested(C, S)
```

Then iterate, recomputing `provider` against the current set on every pass. A
**generated** pair is demoted when any of three conditions holds:

- **local blocker** — `C` has a `generate.marker`, `generate.conflict`, or
  `generate.unsafe` diagnostic, is an inner class, or its ancestry reaches an
  inheritance cycle;
- **ancestry rule fails** — `C`'s strict ancestry declares a selectable field
  and `providerₙ(parent(C), S)` does not exist;
- **descendant rule fails** — some descendant `D` has `providerₙ(D, S) == C`
  and at least one class on the path from `C` (exclusive) to `D` (inclusive)
  declares a selectable field.

A pair satisfying `compatibleHandwritten` is never demoted. The set only
shrinks, so iteration stops when a pass demotes nothing.

An earlier draft of this spec claimed a parents-first walk in one pass would do,
on the grounds that a generated `equals` depends only on its parent's capability
and that inheritance is acyclic. That was wrong, because the descendant refusal
rule points the other way. Take requested `A` providing `equals`, requested
`B extends A` providing it, and unrequested `C extends B` that adds a field:
the descendant rule demotes `B`, which moves `provider(C, equals)` from `B` to
`A`, which must then demote `A` — a class a parents-first pass had already
settled. Capability therefore flows both up and down the hierarchy, and only
iteration closes it.

### The inheritance graph may not be a forest

`project.Load` parses syntactically; it does not check that `extends` edges are
acyclic, and cycle detection lives in `architecture`, not in `project`. Two
parseable scripts can therefore name each other, and an unguarded parent walk
would not terminate.

Indexing detects strongly connected components over the `extends` edges, and
two things follow. **Every ancestry traversal carries a visited set**, so no
walk can loop regardless of what the graph turns out to be. And a requested
class is refused with `generate.unsupported` when its ancestry **reaches** a
cycle, not merely when it sits in one: an unrequested `A ↔ B` pair with a
requested `C extends A` is just as unresolvable, because `provider(parent(C), S)`
has no answer. The diagnostic names the cycle in a deterministic order.

Godot would reject such a project too, but `gen` must not hang on it before
Godot gets the chance.

### Composing with the ancestor

A generated comparison that looked only at the fields its own class declares
would be unsound in both directions, and script identity does not save it. Two
`SubHex` instances answer the same `get_script()`, so a `SubHex` inheriting
`Hex.equals` passes the guard and ignores every field `SubHex` added; and a
`SubHex` that generates its *own* `equals` would compare only `r` and ignore the
`q` it inherited.

A generated method therefore **composes with its ancestor's provider** before
comparing its own fields:

```gdscript
func equals(p_other: Variant) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	if not super.equals(p_other):
		return false
	return self.r == p_other.r
```

`super.equals` is emitted only when `provider(parent, S)` exists. The ancestor's
own guard passes, because both operands really are the same script. This is also
what makes inherited fields work: every field in the ancestry is compared, each
by the class that declares it.

What remains is the case with no ancestor to compose with:

> A requested class `R` generating signature `S` is refused with
> `generate.unsupported` when `R`'s ancestry declares a selectable field and
> `provider(parent(R), S)` does not exist. The message names the nearest
> ancestor that would need to opt in.

And the mirror of it, for a descendant that does not opt in at all:

> `R` is refused when some descendant `D` has `provider(D, S) == R` and at least
> one class on the path from `R` (exclusive) to `D` (inclusive) declares a
> selectable field.

Three consequences of the descendant rule are deliberate:

- **Descendants are transitive, not direct.** A grandchild that adds fields and
  inherits `R`'s method is as unsound as a child that does.
- **An override is a barrier.** If an intermediate `B` provides `S` itself then
  `provider(D, S)` is `B`, not `R`, so `R` is not refused for `D`'s sake — and
  `B`, which now composes with `R`, is checked against its own descendants by
  the same rule.
- **A field-free descendant is not a problem**, because it adds no state an
  inherited comparison could miss.

Both diagnostics land on `R`, the class whose generated method would be unsound,
rather than on an ancestor or descendant that may not be selected at all.

### Parse failures are universe-wide, and this is the one scope exception

Diagnostics are otherwise confined to selected scripts. Parse failures are not,
and the reason is that inheritance is a **reverse** dependency. An excluded,
unparseable script may extend a requested base class and add fields. Nothing in
the base names that subclass, so there is no referrer to hang a
`generate.unsupported` on, and the missing edge does not look like missing
information — it looks like a clean project. The refusal rule above would
silently fail to fire.

So: when any generator whose soundness depends on the inheritance graph is
requested, **every** parse failure in the universe is a blocking `source-parse`
diagnostic for the run, selected or not. `equals` is such a generator;
`to_string` is not, and a project generating only `_to_string` is unaffected.

The consequence is real and worth stating plainly: a project with one
unparseable script anywhere cannot generate `equals` until it parses. The
alternative is generating a method that is quietly wrong whenever the file that
would have proved it wrong is also the file that cannot be read.

### Formatting and the full-file invariant

Step 5 formats the region alone rather than the spliced file. Formatting the
whole file would let gdparser reformat code *outside* the region whenever the
file was not already canonical, which breaks step 6 and silently turns
`gen write` into a partial `format write`. Formatting the region alone is only
sound because the region sits at indent 0, which is the honest reason v1 is
limited to top-level classes: an inner class's region would need re-indentation
after formatting.

The gap that isolated formatting cannot see — spacing that depends on
neighbours — is closed by the extent rules above, which reproduce
`blankLineGaps`'s actual behaviour rather than assuming it.

Because that is an argument about another package's code, it is checked rather
than trusted. The full-file formatter runs as a **read-only oracle** on every
candidate whose source file was already format-clean, in `gen check` and
`gen write` alike, not only over test fixtures. A candidate the oracle would
change is refused with `generate.unsafe`. That turns the invariant from a claim
in a document into a precondition of writing, which is the standard `format`
already sets by refusing rather than rewriting badly.

The claim, stated precisely: `gen write` introduces no new `format check`
finding. It does not make a file canonical, and a file that was not canonical
stays exactly as uncanonical as it was.

### Verification

`format`'s safety net cannot be reused. Its value is the promise that a rewrite
changes nothing but layout, enforced by comparing the token stream — and a
generator changes the token stream by definition. Extending `format.unsafe` to
carve out an exception would weaken the one invariant that makes `format write`
trustworthy.

The replacement is tighter and cheaper. Given source `S` with its region at
`[a, b)` and candidate `C` with its region at `[a', b')`, both spans as defined
under Region extent:

- `S[:a]` equals `C[:a']`, and `S[b:]` equals `C[b':]`;
- `C` reparses;
- the region in `C` is byte-for-byte what step 5 produced;
- if `S` was format-clean, the oracle reports no change on `C`.

A candidate failing any of these is refused with `generate.unsafe` and the file
is left untouched.

## The plan model

`Check` returns a plan, and the two commands read it differently. Without this,
`generate.stale` being an error is incoherent: a stale region is both the thing
`check` must fail on and the thing `write` exists to fix.

```go
func New(config Config, formatting format.Config) (*Generator, error)

// Check is pure: it performs no I/O.
func (g *Generator) Check(snapshot *project.Snapshot) Plan

type Candidate struct {
	Path     string
	Class    string
	Contents []byte // the whole file, region spliced
	Region   Span   // in Contents, including the owned leading gap
	Changed  bool
}

type Plan struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
}
```

`Plan` is internal. The public, JSON-shaped result mirrors `format.Report`:

```go
type Result struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

type Report struct {
	Results     []Result     `json:"results"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (r Report) HasChanges() bool
func (r Report) HasDiagnostics() bool
```

v1 diagnostics carry **no severity field**, as `format` and `uid` carry none.
`deep_equals` introduces the first warning and with it both the field and
`HasErrors()`; adding them now would make `HasErrors()` and `HasDiagnostics()`
the same predicate.

- **`gen check`** reports every `Changed` candidate as `generate.stale`, plus
  every diagnostic. Exit 1 when there is any diagnostic or any changed
  candidate.
- **`gen write`** applies every `Changed` candidate. Staleness is not a failure
  for `write` — fixing it is the point. It exits 1 only when a blocking
  diagnostic remains, 2 on an I/O failure, and prints what it changed either
  way.

A blocking diagnostic suppresses its own class's candidate, and — through step 4
— the candidate of any descendant that was composing with the capability it
cost. It does not stop unrelated classes from being generated.

## Package shape

```
generate/
  config.go      Config, DefaultConfig, LoadConfig, Validate
  marker.go      the directive grammar and the region sentinels
  region.go      region extent and the owned leading gap
  fields.go      member var selection
  index.go       class_name, inheritance, declared signatures, selectable fields
  capability.go  provider() and parents-first capability resolution
  generator.go   the registry and the Generator interface
  emit_to_string.go
  emit_equals.go
  check.go       New, Check — pure, no I/O
  verify.go      the outside-the-region invariant and the format oracle
  apply.go       Apply(snapshot, plan) ([]string, error) — the only writer
  model.go       Diagnostic, Plan, Report, rule name constants
```

A generation stage inside `format` was rejected for the verification reason
above. A `lint` rule reporting staleness alongside a separate writer was also
rejected: it splits one concept across two packages, and `lint` would need the
whole field model and emitter in order to know what the correct text is — all of
the work, in the package that is not allowed to write.

### The registry

```go
type Generator interface {
	// Name is the directive and configuration spelling: "to_string".
	Name() string
	// Signatures are the methods it emits.
	Signatures() []Signature
	// NeedsInheritanceGraph reports that soundness depends on the descendant
	// set, which is what makes a universe-wide parse failure blocking.
	NeedsInheritanceGraph() bool
	Emit(class Class, index *Index, capabilities *Capabilities) (string, []Diagnostic)
}
```

Generators emit in a fixed registry order, not in the order the directive lists
them, so the region's content depends only on the class and not on how the
marker was written.

## What is generated

### Field selection

Every member `var` the class declares, in declaration order. Never a `const`
(GDScript types it from its value and it is not instance state), never a
`static var`, and never an `@onready var` — that is node wiring, not state, and
reading one before `_ready` is a null dereference. A field carrying
`# gdkit:generate:ignore-field` is excluded.

A property with accessors is included by name. The generated code reads
`self.q`, which runs the getter, as hand-written code would.

Neither generator resolves a field's declared type. `_to_string` formats any
value with `%s` and `equals` compares any value with `==`, so a field typed as a
native engine class such as `Node` is included and compared by reference, which
is the correct semantic for a node. Type resolution exists only to decide
whether to recurse, and only `deep_equals` recurses.

### Identifiers in emitted code

Every field reference is written `self.<name>`, and every generated local is
prefixed `__gdkit_`. Both are collision avoidance, and neither is optional:

- a field named `p_other` is shadowed by the parameter, so an unqualified
  `p_other` in the body would read the argument rather than the field;
- a field named `i` would collide with a loop variable.

`self.p_other` and `p_other.p_other` are unambiguous, so the parameter keeps the
name the public signature wants. The repository already resolves shadowing
before drawing conclusions about an identifier
(`architecture/collectShadows`); this is the same hazard from the emitting side.

### `_to_string`

```gdscript
func _to_string() -> String:
	return "Hex(q=%s, r=%s)" % [self.q, self.r]
```

The name is the `class_name` when the script declares one, otherwise the file's
base name without `.gd`. A class with no selected fields emits `"Hex()"`.

`to_string` does not depend on the inheritance graph, so
`NeedsInheritanceGraph` returns false for it and it does not compose with an
ancestor. An inherited `_to_string` that names too few fields prints an
incomplete value, which is wrong output rather than a wrong answer, and Godot's
own `str()` on a subclass is no better.

There is no configuration for this format. A project that wants a different one
hand-writes `_to_string` and does not opt into the `to_string` generator, which
is better than a style option nobody can change later without rewriting every
region in every adopting project.

### `equals`

```gdscript
func equals(p_other: Variant) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	return self.q == p_other.q and self.r == p_other.r
```

Field-wise `==` behind a script-identity guard, with the `super.equals`
composition above inserted when the parent provides one. `is <ClassName>` was
rejected on two counts: it cannot name a class that declares no `class_name`,
nor an inner class, so those classes could not opt in at all; and it makes
equality asymmetric across a subclass — `Hex.new().equals(SubHex.new())` is true
while the reverse is false. Script identity is symmetric, which is the property
a value object needs. It is not sufficient on its own, which is what the
composition and refusal rules exist for.

The long `and` chain is emitted on one line and wrapped by step 5 to the
project's `line_width`.

`equals` compares object-valued fields **by reference**, because that is what
`==` does to an `Object` in Godot 4. `Array` and `Dictionary` fields are
compared by value, because that is what `==` does to those. A project that needs
structural comparison of a nested value object wants `deep_equals`.

## Diagnostics

Rule names are a public contract: they appear in JSON output and in
configuration. Two are reused rather than coined: `source-parse`, as `lint` and
`format` spell it — `architecture` spells its own `source.parse`, and `generate`
belongs to the former family — and `class_name.duplicate`, from `architecture`.

| Rule | Reports | Severity |
| --- | --- | --- |
| `source-parse` | a file that could not be parsed; universe-wide when an inheritance-sensitive generator is requested, otherwise selected files only | error |
| `class_name.duplicate` | two scripts claiming one `class_name` that a requested class needs to resolve | error |
| `generate.stale` | a region that is missing or out of date (`check` only) | error |
| `generate.marker` | a malformed directive, an unknown generator name, or a second region in one class | error |
| `generate.conflict` | the class declares, outside the region, a method with the same name as one a requested generator emits, in a shape that is not compatible with it | error |
| `generate.unsupported` | an inner class; a requested class whose ancestry has fields but no provider; or a class whose descendant would inherit an unsound method | error |
| `generate.orphaned` | a region whose class no longer opts in, anywhere in the universe | error |
| `generate.unsafe` | verification or the format oracle refused the splice | error |

Every rule in this spec is an error. `deep_equals` introduces the first warning,
and with it the severity field on `Diagnostic`; until then `Report.HasErrors()`
and `HasDiagnostics()` would be the same predicate, so only the latter exists.

**`generate.conflict` fires on the name, and compatibility only decides
satisfaction.** GDScript has no overloading: a `static func equals(a, b)` in a
class that requests `equals` cannot coexist with a generated
`func equals(p_other: Variant) -> bool`, because the second declaration is a
duplicate and the file will not compile. So an existing method whose signature
is compatible *satisfies* the request and nothing is emitted; an existing method
with the same name and an incompatible signature is a conflict.

## Configuration

`.gdkit/generate.json`, written by `gdkit gen init`:

```json
{
  "version": 1,
  "source_roots": ["."],
  "exclude": [".git/**", ".godot/**", ".gdkit/**", "addons/**"],
  "generate": []
}
```

The `exclude` default matches `format.DefaultConfig()` exactly rather than being
empty. An empty default would make `addons/**` a generation target, so a glob
someone wrote for their own code could start rewriting a third-party addon.

A `generate` entry names path globs and the generators they opt in:

```json
{
  "generate": [
    { "paths": ["**/domain/value/*.gd"], "generators": ["to_string", "equals"] }
  ]
}
```

**A file matching several entries gets the union of their generators**, which is
the grain of the codebase: `architecture`'s `dependencies` are alternative allow
rules, where capability is added by adding a rule. Union is also
order-independent, so reordering the list cannot change the output.

`source_roots`, `exclude`, and `.gdkitignore` populate `project.Selection`. None
narrows the universe. There is no `godot_version` key: every semantic this spec
relies on is constant across Godot 4.

Unknown keys are rejected at every depth, naming the offending key's full JSON
path, as `architecture` does. `minimum_gdkit_version` is supported and checked
before the unknown-key walk, for the reason recorded in `CLAUDE.md`.

Globs use `internal/glob`.

## Command surface

```sh
gdkit gen check [--config path] [--format text|json] [--diff] [project-root]
gdkit gen write [--config path] [--format text|json] [--prune] [project-root]
gdkit gen init  [--force] [project-root]
```

Bare `gdkit gen` runs `check`. Both `check` and `write` take
`--minimum-version`. `--diff` cannot be combined with `--format json`, as in
`format check`.

Exit `0` clean, `1` findings, `2` configuration, usage, or I/O failure. Under
`--format json` an exit-2 failure is an envelope on stderr with stdout empty.
`gen write` prints the files it changed on stdout even when a write fails
part-way, because that list is what tells the caller the state the project is
in.

The subcommand is `gen` rather than `generate`, following `arch`.

## Testing

Tests build throwaway Godot projects with `writeProject(t, t.TempDir(), …)` and
assert on diagnostic rule names, per the repository conventions. One deliberate
departure: they must also assert on emitted text, because the text is the unit
under test. Golden fixtures per generator.

Invariants, each over the full fixture corpus:

- `gen write` is idempotent;
- the full-file formatter, as a read-only oracle, reports no change on any
  candidate whose source was format-clean;
- `gen write` followed by `gen check` is clean;
- removing a region and regenerating returns the same bytes.

Cases that exist because an earlier draft of this design got them wrong, each
with a named test:

- a field named `p_other`;
- a subclass that generates `equals` while its base owns fields, which must
  compose via `super` and compare both;
- a requested subclass whose base has no provider, which must be refused;
- a base class with a field-adding **grandchild** that does not opt in, refused;
- the same graph with an intermediate class that overrides, **not** refused;
- a zero-field subclass, which must not cause a refusal;
- a `static func equals(a, b)` in a requested class, which must be a conflict;
- a blocked generator alongside a hand-written provider of the same signature,
  where the hand-written one must survive and its dependents must still
  generate;
- a class that stops opting in while its region remains;
- an orphaned parent region whose `equals` a requested child would compose
  with, where the child must be refused rather than `--prune` breaking it;
- `A` providing `equals`, requested `B extends A` providing it, and unrequested
  field-adding `C extends B`, where demoting `B` must cascade to demote `A`;
- `A` with a compatible `equals`, `B extends A` declaring `equals(a, b)`, and
  requested `C extends B`, where `B` must be a barrier rather than `A` a
  provider;
- two scripts that `extend` each other, which must be refused deterministically
  rather than hang;
- a parent whose candidate is refused at verification, where the child that
  composed with it must also be refused;
- an orphaned region in an excluded file, which must still be reported;
- an unparseable file outside the selection, which must block `equals` but not
  `to_string`;
- a field typed `Node`, which `equals` must compare by reference rather than
  refuse.

A test pins that `lint` reports nothing on a file carrying any of the three
marker forms.

## Explicitly out of scope

- **Inner classes.** A marker on an inner `class` is `generate.unsupported`.
- **`deep_equals`.** Its own spec, which adds structural recursion, a builtin
  type catalogue, fixed-point capability resolution, and cycle detection in the
  generated code. Every critical finding across three review passes of the
  combined design was in that tier, which is why it ships separately.
- **A `hash` generator.** The obvious next member of this family and the one
  with real subtlety: Godot's `hash()` on an object is identity-based, so a
  value-object hash must be composed by hand and must agree with `equals` or a
  `Dictionary` keyed on these objects breaks. Its own spec.
- **Rewriting a hand-written `_to_string`.** `generate.conflict` reports it;
  nothing migrates it.
- **A `_to_string` format option.** One documented format.
